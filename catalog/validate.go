package catalog

import (
	"fmt"
	"sort"
	"strings"
)

// ValidationError describes a single problem located at a specific catalog entry.
type ValidationError struct {
	Locale  string
	Key     string
	Code    string
	Message string
	Pos     *Pos
}

func (e *ValidationError) Error() string {
	loc := e.Locale
	if loc == "" {
		loc = "?"
	}
	if e.Pos != nil {
		return fmt.Sprintf("[%s] %s/%s: %s (line %d, col %d)", e.Code, loc, e.Key, e.Message, e.Pos.Line, e.Pos.Col)
	}
	return fmt.Sprintf("[%s] %s/%s: %s", e.Code, loc, e.Key, e.Message)
}

// ValidateEntry parses and validates a single raw message. It returns the
// parsed AST (nil when the message cannot be parsed) and every problem found;
// a single bad entry never aborts the caller's batch.
func ValidateEntry(locale, key, raw string) ([]Node, []ValidationError) {
	nodes, err := Parse(raw)
	if err != nil {
		pe := err.(*ParseError)
		return nil, []ValidationError{{
			Locale: locale, Key: key, Code: "PARSE",
			Message: pe.Message, Pos: &pe.Pos,
		}}
	}
	var errs []ValidationError
	validateNodes(locale, key, nodes, &errs)
	return nodes, errs
}

var pluralCategories = map[string]bool{
	"zero": true, "one": true, "two": true, "few": true, "many": true, "other": true,
}

var genderCategories = map[string]bool{
	"male": true, "female": true, "other": true,
}

func validateNodes(locale, key string, nodes []Node, errs *[]ValidationError) {
	for _, n := range nodes {
		switch t := n.(type) {
		case *Plural:
			validateBranchNode(locale, key, "plural", t.Name, t.Branches, errs)
		case *Select:
			validateBranchNode(locale, key, "select", t.Name, t.Branches, errs)
		case *Gender:
			validateBranchNode(locale, key, "gender", t.Name, t.Branches, errs)
		}
	}
}

func validateBranchNode(locale, key, kind, name string, branches []Branch, errs *[]ValidationError) {
	seen := map[string]bool{}
	hasOther := false
	for _, b := range branches {
		if seen[b.Key] {
			*errs = append(*errs, ValidationError{
				Locale: locale, Key: key, Code: "DUPLICATE_BRANCH",
				Message: fmt.Sprintf("%s argument %q has duplicate branch %q", kind, name, b.Key),
			})
		}
		seen[b.Key] = true
		if b.Key == "other" {
			hasOther = true
		}
		switch kind {
		case "plural":
			if b.Exact {
				if !validExact(b.Key) {
					*errs = append(*errs, ValidationError{
						Locale: locale, Key: key, Code: "INVALID_PLURAL_CATEGORY",
						Message: fmt.Sprintf("plural argument %q has invalid exact selector %q", name, b.Key),
					})
				}
			} else if !pluralCategories[b.Key] {
				*errs = append(*errs, ValidationError{
					Locale: locale, Key: key, Code: "INVALID_PLURAL_CATEGORY",
					Message: fmt.Sprintf("plural argument %q has invalid category %q (expected zero/one/two/few/many/other)", name, b.Key),
				})
			}
		case "gender":
			if !genderCategories[b.Key] {
				*errs = append(*errs, ValidationError{
					Locale: locale, Key: key, Code: "INVALID_GENDER_CATEGORY",
					Message: fmt.Sprintf("gender argument %q has invalid category %q (expected male/female/other)", name, b.Key),
				})
			}
		}
		// Recurse into branch messages.
		validateNodes(locale, key, b.Value, errs)
	}
	if !hasOther {
		*errs = append(*errs, ValidationError{
			Locale: locale, Key: key, Code: "MISSING_OTHER",
			Message: fmt.Sprintf("%s argument %q is missing the required \"other\" branch", kind, name),
		})
	}
}

func validExact(sel string) bool {
	if len(sel) < 2 || sel[0] != '=' {
		return false
	}
	for i := 1; i < len(sel); i++ {
		if sel[i] < '0' || sel[i] > '9' {
			return false
		}
	}
	return true
}

// branchSig identifies a branch node by kind and argument name.
type branchSig struct {
	kind string
	name string
}

// collectSigs walks an AST and returns sorted placeholder names and branch
// signatures.
func collectSigs(nodes []Node) (placeholders []string, branches []branchSig) {
	ph := map[string]bool{}
	br := map[branchSig]bool{}
	var walk func(ns []Node)
	walk = func(ns []Node) {
		for _, n := range ns {
			switch t := n.(type) {
			case *Placeholder:
				ph[t.Name] = true
			case *Plural:
				ph[t.Name] = true
				br[branchSig{"plural", t.Name}] = true
				for _, b := range t.Branches {
					walk(b.Value)
				}
			case *Select:
				ph[t.Name] = true
				br[branchSig{"select", t.Name}] = true
				for _, b := range t.Branches {
					walk(b.Value)
				}
			case *Gender:
				ph[t.Name] = true
				br[branchSig{"gender", t.Name}] = true
				for _, b := range t.Branches {
					walk(b.Value)
				}
			}
		}
	}
	walk(nodes)
	for k := range ph {
		placeholders = append(placeholders, k)
	}
	for k := range br {
		branches = append(branches, k)
	}
	sort.Strings(placeholders)
	sort.Slice(branches, func(i, j int) bool {
		if branches[i].kind != branches[j].kind {
			return branches[i].kind < branches[j].kind
		}
		return branches[i].name < branches[j].name
	})
	return placeholders, branches
}

// ValidateConsistency compares an entry against the other locale entries of
// the same key. Placeholder name sets and branch node kinds must match so the
// message renders identically regardless of which fallback locale is used.
func ValidateConsistency(locale, key string, nodes []Node, refs map[string][]Node) []ValidationError {
	myPH, myBR := collectSigs(nodes)
	var errs []ValidationError
	refLocales := make([]string, 0, len(refs))
	for l := range refs {
		if l != locale {
			refLocales = append(refLocales, l)
		}
	}
	sort.Strings(refLocales)
	for _, refLoc := range refLocales {
		refNodes := refs[refLoc]
		refPH, refBR := collectSigs(refNodes)
		if diff := setDiff(myPH, refPH); len(diff) > 0 {
			errs = append(errs, ValidationError{
				Locale: locale, Key: key, Code: "PLACEHOLDER_MISMATCH",
				Message: fmt.Sprintf("placeholders %s are not present in %s (which has %s)",
					joinNames(diff), refLoc, joinNames(refPH)),
			})
		}
		if diff := setDiff(refPH, myPH); len(diff) > 0 {
			errs = append(errs, ValidationError{
				Locale: locale, Key: key, Code: "PLACEHOLDER_MISMATCH",
				Message: fmt.Sprintf("placeholders %s from %s are missing here (here: %s)",
					joinNames(diff), refLoc, joinNames(myPH)),
			})
		}
		if diff := brDiff(myBR, refBR); len(diff) > 0 {
			errs = append(errs, ValidationError{
				Locale: locale, Key: key, Code: "BRANCH_KIND_MISMATCH",
				Message: fmt.Sprintf("branch nodes %s do not match %s", joinBranches(diff), refLoc),
			})
		}
		if diff := brDiff(refBR, myBR); len(diff) > 0 {
			errs = append(errs, ValidationError{
				Locale: locale, Key: key, Code: "BRANCH_KIND_MISMATCH",
				Message: fmt.Sprintf("branch nodes %s from %s are missing here", joinBranches(diff), refLoc),
			})
		}
	}
	return errs
}

func setDiff(a, b []string) []string {
	have := map[string]bool{}
	for _, x := range b {
		have[x] = true
	}
	var diff []string
	for _, x := range a {
		if !have[x] {
			diff = append(diff, x)
		}
	}
	return diff
}

func brDiff(a, b []branchSig) []branchSig {
	have := map[branchSig]bool{}
	for _, x := range b {
		have[x] = true
	}
	var diff []branchSig
	for _, x := range a {
		if !have[x] {
			diff = append(diff, x)
		}
	}
	return diff
}

func joinNames(xs []string) string {
	return "{" + strings.Join(xs, ", ") + "}"
}

func joinBranches(xs []branchSig) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = x.kind + " on {" + x.name + "}"
	}
	return strings.Join(parts, ", ")
}
