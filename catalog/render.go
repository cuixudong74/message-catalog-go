package catalog

import (
	"fmt"
	"strconv"
	"strings"
)

// Render renders a parsed message with the given argument values.
//
// Placeholders are substituted from values. Plural nodes use CLDR-like
// plural category rules (English and Chinese rules are built in; unknown
// locales fall back to "other"), explicit "=n" selectors match the raw
// argument value, and '#' inside a plural branch renders the value minus the
// plural offset. Select/gender nodes match the stringified argument value and
// fall back to the "other" branch.
func Render(nodes []Node, locale string, values map[string]any) (string, error) {
	ctx := &renderCtx{locale: baseLocale(locale), values: values}
	var sb strings.Builder
	if err := renderNodes(&sb, nodes, ctx); err != nil {
		return "", err
	}
	return sb.String(), nil
}

type renderCtx struct {
	locale string
	values map[string]any
	plural *pluralFrame
}

type pluralFrame struct {
	value int64 // raw argument value
	adj   int64 // value minus offset (rendered by '#')
}

func renderNodes(sb *strings.Builder, nodes []Node, ctx *renderCtx) error {
	for _, n := range nodes {
		switch t := n.(type) {
		case *Text:
			if ctx.plural != nil && !t.Quoted {
				sb.WriteString(strings.ReplaceAll(t.Value, "#", strconv.FormatInt(ctx.plural.adj, 10)))
			} else {
				sb.WriteString(t.Value)
			}
		case *Placeholder:
			v, ok := ctx.values[t.Name]
			if !ok {
				return fmt.Errorf("missing value for placeholder %q", t.Name)
			}
			sb.WriteString(formatValue(v))
		case *Plural:
			if err := renderPlural(sb, t, ctx); err != nil {
				return err
			}
		case *Select:
			if err := renderSelect(sb, t.Name, t.Branches, ctx); err != nil {
				return err
			}
		case *Gender:
			if err := renderSelect(sb, t.Name, t.Branches, ctx); err != nil {
				return err
			}
		}
	}
	return nil
}

func renderPlural(sb *strings.Builder, p *Plural, parent *renderCtx) error {
	v, ok := parent.values[p.Name]
	if !ok {
		return fmt.Errorf("missing value for plural argument %q", p.Name)
	}
	num, err := asInt(v)
	if err != nil {
		return fmt.Errorf("plural argument %q: %w", p.Name, err)
	}
	adj := num - int64(p.Offset)
	// Explicit "=n" selectors match the raw value.
	for _, b := range p.Branches {
		if b.Exact && b.Key == "="+strconv.FormatInt(num, 10) {
			return renderBranch(sb, b, parent, num, adj)
		}
	}
	cat := pluralCategory(parent.locale, adj)
	for _, b := range p.Branches {
		if !b.Exact && b.Key == cat {
			return renderBranch(sb, b, parent, num, adj)
		}
	}
	// Fall back to "other".
	for _, b := range p.Branches {
		if !b.Exact && b.Key == "other" {
			return renderBranch(sb, b, parent, num, adj)
		}
	}
	return fmt.Errorf("plural %q has no %q or %q branch", p.Name, cat, "other")
}

func renderBranch(sb *strings.Builder, b Branch, parent *renderCtx, num, adj int64) error {
	child := &renderCtx{
		locale: parent.locale,
		values: parent.values,
		plural: &pluralFrame{value: num, adj: adj},
	}
	return renderNodes(sb, b.Value, child)
}

func renderSelect(sb *strings.Builder, name string, branches []Branch, parent *renderCtx) error {
	v, ok := parent.values[name]
	if !ok {
		return fmt.Errorf("missing value for select argument %q", name)
	}
	key := formatValue(v)
	for _, b := range branches {
		if b.Key == key {
			return renderNodes(sb, b.Value, parent)
		}
	}
	for _, b := range branches {
		if b.Key == "other" {
			return renderNodes(sb, b.Value, parent)
		}
	}
	return fmt.Errorf("select %q has no branch for %q and no %q branch", name, key, "other")
}

func baseLocale(locale string) string {
	if i := strings.IndexAny(locale, "_-"); i >= 0 {
		return locale[:i]
	}
	return locale
}

// pluralCategory returns the CLDR plural category for the given locale.
// English and French rules are implemented; everything else falls back to
// "other" (which is correct for Chinese, Japanese, Korean, Vietnamese, ...).
func pluralCategory(locale string, n int64) string {
	switch locale {
	case "en":
		if n == 1 {
			return "one"
		}
		return "other"
	case "fr":
		if n == 0 || n == 1 {
			return "one"
		}
		return "other"
	default:
		return "other"
	}
}

func asInt(v any) (int64, error) {
	switch n := v.(type) {
	case int:
		return int64(n), nil
	case int64:
		return n, nil
	case float64:
		return int64(n), nil
	case string:
		i, err := strconv.ParseInt(n, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("value %q is not a number", n)
		}
		return i, nil
	default:
		return 0, fmt.Errorf("value %v is not a number", v)
	}
}

func formatValue(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	default:
		return fmt.Sprint(t)
	}
}
