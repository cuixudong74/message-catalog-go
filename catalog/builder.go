package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
)

// ErrCatalogTooLarge is returned when a build exceeds the configured entry cap.
var ErrCatalogTooLarge = fmt.Errorf("catalog exceeds configured size limit")

var versionCounter atomic.Int64

// BuildReport describes the outcome of a build.
type BuildReport struct {
	VersionID     int64
	FullRebuild   bool
	FilesAdded    []string
	FilesModified []string
	FilesDeleted  []string
	Affected      map[string][]string // locale -> affected keys
	Errors        []ValidationError
}

// Build performs a full rebuild of the catalog from the configured directory.
// A single bad entry never fails the build: its errors are reported while the
// rest of the catalog is still published.
func Build(ctx context.Context, cfg Config) (*Version, BuildReport, error) {
	files, err := scanFiles(cfg)
	if err != nil {
		return nil, BuildReport{}, err
	}
	parsed, shas, err := parseFiles(ctx, files)
	if err != nil {
		return nil, BuildReport{}, err
	}
	for i := range files {
		files[i].SHA256 = shas[files[i].Path]
	}
	locales, fileEntries := mergeLocales(files, parsed)
	if err := checkSize(locales, cfg); err != nil {
		return nil, BuildReport{}, err
	}
	errs := validateAll(locales)
	resolved, missErrs := resolveAll(locales, cfg)
	errs = append(errs, missErrs...)
	pseudoLocale, pseudoEntries := buildPseudo(locales, cfg)
	if pseudoLocale != "" {
		locales[pseudoLocale] = &LocaleCatalog{Locale: pseudoLocale, Entries: pseudoEntries}
		// Resolve the pseudo locale (it serves every key itself).
		for key, e := range pseudoEntries {
			if resolved[pseudoLocale] == nil {
				resolved[pseudoLocale] = map[string]*Resolution{}
			}
			resolved[pseudoLocale][key] = &Resolution{
				Locale: pseudoLocale, Key: key, SourceLocale: pseudoLocale, Entry: e,
				Chain: []ChainStep{{Locale: pseudoLocale, Hit: true, Reason: "hit"}},
			}
		}
	}
	sources := map[string]SourceFile{}
	for _, f := range files {
		sources[f.Path] = f.SourceFile
	}
	v := &Version{
		ID:           versionCounter.Add(1),
		Config:       cfg,
		Locales:      locales,
		Resolved:     resolved,
		Sources:      sources,
		FileEntries:  fileEntries,
		PseudoLocale: pseudoLocale,
	}
	return v, BuildReport{
		VersionID:   v.ID,
		FullRebuild: true,
		Errors:      errs,
	}, nil
}

// Rebuild performs an incremental rebuild based on the previous version. Only
// added/modified/deleted files are re-parsed; resolution is recomputed only
// for keys that could be affected. The resulting version is identical to a
// full rebuild of the same directory.
func Rebuild(ctx context.Context, prev *Version, cfg Config) (*Version, BuildReport, error) {
	if prev == nil {
		return Build(ctx, cfg)
	}
	files, err := scanFiles(cfg)
	if err != nil {
		return nil, BuildReport{}, err
	}
	oldPaths := prev.Sources
	newPaths := map[string]SourceFile{}
	for _, f := range files {
		newPaths[f.Path] = f.SourceFile
	}
	added, modified, deleted := diffPaths(oldPaths, newPaths)

	cfgChanged := !reflect.DeepEqual(prev.Config, cfg)
	if len(added)+len(modified)+len(deleted) == 0 && !cfgChanged {
		// Nothing to do: keep the committed version.
		return prev, BuildReport{VersionID: prev.ID}, nil
	}

	// Re-parse only changed files.
	changedFiles := make([]scannedFile, 0, len(added)+len(modified))
	for _, f := range files {
		if contains(added, f.Path) || contains(modified, f.Path) {
			changedFiles = append(changedFiles, f)
		}
	}
	parsed, shas, err := parseFiles(ctx, changedFiles)
	if err != nil {
		return nil, BuildReport{}, err
	}
	for i := range files {
		if sh, ok := shas[files[i].Path]; ok {
			files[i].SHA256 = sh
		}
	}

	// Copy-on-write: start from the previous version's immutable maps.
	locales := cloneLocales(prev.Locales)
	fileEntries := cloneFileEntries(prev.FileEntries)
	resolved := cloneResolved(prev.Resolved)

	// Update file entries and per-locale merged entries.
	affectedKeys := map[string]map[string]bool{} // locale -> keys
	markAffected := func(locale, key string) {
		if affectedKeys[locale] == nil {
			affectedKeys[locale] = map[string]bool{}
		}
		affectedKeys[locale][key] = true
	}
	for _, path := range added {
		fe := findFile(files, path)
		fileEntries[path] = parsed[path]
		locale := fe.Locale
		if locales[locale] == nil {
			locales[locale] = &LocaleCatalog{Locale: locale, Entries: map[string]*Entry{}}
		}
		for key, e := range parsed[path] {
			locales[locale].Entries[key] = e
			markAffected(locale, key)
		}
	}
	for _, path := range modified {
		fe := findFile(files, path)
		locale := fe.Locale
		old := fileEntries[path]
		fileEntries[path] = parsed[path]
		if locales[locale] == nil {
			locales[locale] = &LocaleCatalog{Locale: locale, Entries: map[string]*Entry{}}
		}
		for _, key := range unionKeys(old, parsed[path]) {
			// Re-merge this key across all files of the locale.
			if e := mergeKey(fileEntries, locale, key); e != nil {
				locales[locale].Entries[key] = e
			} else {
				delete(locales[locale].Entries, key)
			}
			markAffected(locale, key)
		}
	}
	for _, path := range deleted {
		locale := oldPaths[path].Locale
		old := fileEntries[path]
		delete(fileEntries, path)
		if locales[locale] != nil {
			for key := range old {
				if e := mergeKey(fileEntries, locale, key); e != nil {
					locales[locale].Entries[key] = e
				} else {
					delete(locales[locale].Entries, key)
				}
				markAffected(locale, key)
			}
			if len(locales[locale].Entries) == 0 {
				delete(locales, locale)
			}
		}
	}

	// When configuration changes, every resolution may change.
	if cfgChanged {
		for locale, keys := range prev.Resolved {
			for key := range keys {
				markAffected(locale, key)
			}
		}
	}

	// Keys whose resolution may change: keys in changed locales plus keys that
	// resolve from a changed locale (reverse dependency index).
	changedLocales := map[string]bool{}
	for _, p := range added {
		changedLocales[newPaths[p].Locale] = true
	}
	for _, p := range modified {
		changedLocales[newPaths[p].Locale] = true
	}
	for _, p := range deleted {
		changedLocales[oldPaths[p].Locale] = true
	}
	for locale := range changedLocales {
		// All keys that existed before or exist now in the changed locale are
		// affected, both for the locale itself and for every locale whose
		// fallback chain passes through it (this covers newly added keys).
		allKeys := map[string]bool{}
		if lc := prev.Locales[locale]; lc != nil {
			for key := range lc.Entries {
				allKeys[key] = true
			}
		}
		if lc := locales[locale]; lc != nil {
			for key := range lc.Entries {
				allKeys[key] = true
			}
		}
		deps := localesDependentOn(locales, cfg, locale)
		for key := range allKeys {
			markAffected(locale, key)
			for _, dep := range deps {
				markAffected(dep, key)
			}
		}
		// Existing dependents cover keys deleted from the changed locale.
		for dep := range dependentsOf(prev.Resolved, locale) {
			markAffected(dep.locale, dep.key)
		}
	}

	// Re-validate affected entries (and consistency peers).
	var errs []ValidationError
	validated := map[string]map[string]bool{}
	for locale, keys := range affectedKeys {
		for key := range keys {
			markValidated(validated, locale, key)
			if lc := locales[locale]; lc != nil {
				if e := lc.Entries[key]; e != nil {
					// Validate on a fresh Entry: entries are shared with the
					// previous committed version, so we must never mutate them
					// in place (concurrent queries may still be reading them).
					nodes, verrs := ValidateEntry(locale, key, e.Raw)
					lc.Entries[key] = &Entry{Key: e.Key, Raw: e.Raw, AST: nodes}
					errs = append(errs, verrs...)
				}
			}
			// Consistency peers: other locales' entries for the same key.
			for other := range locales {
				if other != locale {
					markValidated(validated, other, key)
				}
			}
		}
	}
	for locale, keys := range validated {
		for key := range keys {
			if lc := locales[locale]; lc != nil {
				if e := lc.Entries[key]; e != nil && e.AST != nil {
					errs = append(errs, validateConsistency(locales, locale, key, e)...)
				}
			}
		}
	}

	// Re-resolve affected pairs.
	for locale, keys := range affectedKeys {
		if resolved[locale] == nil {
			resolved[locale] = map[string]*Resolution{}
		}
		for key := range keys {
			r := resolveOne(locale, key, locales, cfg)
			if r == nil {
				delete(resolved[locale], key)
				errs = append(errs, ValidationError{
					Locale: locale, Key: key, Code: "MISSING_TRANSLATION",
					Message: fmt.Sprintf("no translation found in fallback chain %s", chainStr(locale, cfg)),
				})
			} else {
				resolved[locale][key] = r
			}
		}
	}
	// Drop resolutions for keys that no longer exist in any locale.
	unionKeys := map[string]bool{}
	for _, lc := range locales {
		for key := range lc.Entries {
			unionKeys[key] = true
		}
	}
	for locale, keys := range resolved {
		for key := range keys {
			if !unionKeys[key] {
				delete(keys, key)
			}
		}
		_ = locale
	}

	// Pseudo-localization: regenerate affected pseudo entries.
	pseudoLocale := prev.PseudoLocale
	pseudoConfigChanged := !reflect.DeepEqual(prev.Config.Pseudo, cfg.Pseudo)
	if cfg.Pseudo.Enabled {
		pseudoLocale = cfg.Pseudo.Locale
		if pseudoLocale == "" {
			pseudoLocale = "en_XA"
		}
		src := cfg.Pseudo.SourceLocale
		if src == "" {
			src = "en"
		}
		if locales[pseudoLocale] == nil {
			locales[pseudoLocale] = &LocaleCatalog{Locale: pseudoLocale, Entries: map[string]*Entry{}}
		}
		if resolved[pseudoLocale] == nil {
			resolved[pseudoLocale] = map[string]*Resolution{}
		}
		srcChanged := changedLocales[src]
		if pseudoConfigChanged {
			// Pseudo config changed: fully regenerate from the source locale.
			locales[pseudoLocale].Entries = map[string]*Entry{}
			resolved[pseudoLocale] = map[string]*Resolution{}
			if se := locales[src]; se != nil {
				for key, e := range se.Entries {
					if e == nil || e.AST == nil {
						continue
					}
					ps := Pseudo(e.AST)
					pe := &Entry{Key: key, Raw: Unparse(ps), AST: ps}
					locales[pseudoLocale].Entries[key] = pe
					resolved[pseudoLocale][key] = &Resolution{
						Locale: pseudoLocale, Key: key, SourceLocale: pseudoLocale, Entry: pe,
						Chain: []ChainStep{{Locale: pseudoLocale, Hit: true, Reason: "hit"}},
					}
				}
			}
		} else if srcChanged {
			// Source locale changed: regenerate affected keys and drop deleted ones.
			for key := range affectedKeys[src] {
				if se := locales[src]; se != nil {
					if e := se.Entries[key]; e != nil && e.AST != nil {
						ps := Pseudo(e.AST)
						pe := &Entry{Key: key, Raw: Unparse(ps), AST: ps}
						locales[pseudoLocale].Entries[key] = pe
						resolved[pseudoLocale][key] = &Resolution{
							Locale: pseudoLocale, Key: key, SourceLocale: pseudoLocale, Entry: pe,
							Chain: []ChainStep{{Locale: pseudoLocale, Hit: true, Reason: "hit"}},
						}
						continue
					}
				}
				delete(locales[pseudoLocale].Entries, key)
				delete(resolved[pseudoLocale], key)
			}
		}
	} else if prev.PseudoLocale != "" {
		delete(locales, prev.PseudoLocale)
		delete(resolved, prev.PseudoLocale)
		pseudoLocale = ""
	}
	// If the pseudo locale code changed, drop the old pseudo locale.
	if prev.PseudoLocale != "" && prev.PseudoLocale != pseudoLocale {
		delete(locales, prev.PseudoLocale)
		delete(resolved, prev.PseudoLocale)
	}

	if err := checkSize(locales, cfg); err != nil {
		return nil, BuildReport{}, err
	}

	sources := map[string]SourceFile{}
	for _, f := range files {
		sources[f.Path] = f.SourceFile
	}
	v := &Version{
		ID:           versionCounter.Add(1),
		Config:       cfg,
		Locales:      locales,
		Resolved:     resolved,
		Sources:      sources,
		FileEntries:  fileEntries,
		PseudoLocale: pseudoLocale,
	}
	affectedReport := map[string][]string{}
	for locale, keys := range affectedKeys {
		ks := make([]string, 0, len(keys))
		for k := range keys {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		affectedReport[locale] = ks
	}
	return v, BuildReport{
		VersionID:     v.ID,
		FilesAdded:    added,
		FilesModified: modified,
		FilesDeleted:  deleted,
		Affected:      affectedReport,
		Errors:        errs,
	}, nil
}

// ---- scanning & parsing ----

type scannedFile struct {
	SourceFile
}

func scanFiles(cfg Config) ([]scannedFile, error) {
	entries, err := os.ReadDir(cfg.LocalesDir)
	if err != nil {
		return nil, fmt.Errorf("read locales dir %q: %w", cfg.LocalesDir, err)
	}
	var files []scannedFile
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".json") {
			continue
		}
		path := filepath.Join(cfg.LocalesDir, ent.Name())
		info, err := ent.Info()
		if err != nil {
			return nil, err
		}
		if cfg.MaxFileSize > 0 && info.Size() > cfg.MaxFileSize {
			return nil, fmt.Errorf("%s: %w (%d bytes > %d)", path, ErrCatalogTooLarge, info.Size(), cfg.MaxFileSize)
		}
		locale := localeFromFilename(ent.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		sum := sha256.Sum256(data)
		files = append(files, scannedFile{SourceFile{
			Path:    path,
			Locale:  locale,
			Size:    info.Size(),
			ModTime: info.ModTime().Unix(),
			SHA256:  hex.EncodeToString(sum[:]),
		}})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

func parseFiles(ctx context.Context, files []scannedFile) (map[string]map[string]*Entry, map[string]string, error) {
	out := map[string]map[string]*Entry{}
	shas := map[string]string{}
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		data, err := os.ReadFile(f.Path)
		if err != nil {
			return nil, nil, fmt.Errorf("read %s: %w", f.Path, err)
		}
		sum := sha256.Sum256(data)
		raw := map[string]string{}
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, nil, fmt.Errorf("parse %s: %w", f.Path, err)
		}
		entries := map[string]*Entry{}
		for key, val := range raw {
			entries[key] = &Entry{Key: key, Raw: val}
		}
		out[f.Path] = entries
		shas[f.Path] = hex.EncodeToString(sum[:])
	}
	return out, shas, nil
}

func mergeLocales(files []scannedFile, parsed map[string]map[string]*Entry) (map[string]*LocaleCatalog, map[string]map[string]*Entry) {
	fileEntries := map[string]map[string]*Entry{}
	locales := map[string]*LocaleCatalog{}
	for _, f := range files {
		fileEntries[f.Path] = parsed[f.Path]
		lc := locales[f.Locale]
		if lc == nil {
			lc = &LocaleCatalog{Locale: f.Locale, Entries: map[string]*Entry{}}
			locales[f.Locale] = lc
		}
		for key, e := range parsed[f.Path] {
			lc.Entries[key] = e // files are path-sorted; later file wins deterministically
		}
	}
	return locales, fileEntries
}

// mergeKey recomputes one key's merged entry across all files of a locale.
func mergeKey(fileEntries map[string]map[string]*Entry, locale, key string) *Entry {
	var paths []string
	for path, entries := range fileEntries {
		if entries[key] != nil {
			if loc := localeOfPath(path); loc == locale {
				paths = append(paths, path)
			}
		}
	}
	if len(paths) == 0 {
		return nil
	}
	sort.Strings(paths)
	return fileEntries[paths[len(paths)-1]][key]
}

// localeFromFilename derives the locale code from a catalog file name. Both
// "en.json" and "en.legal.json" belong to the "en" locale.
func localeFromFilename(name string) string {
	base := strings.TrimSuffix(name, ".json")
	if i := strings.Index(base, "."); i >= 0 {
		return base[:i]
	}
	return base
}

func localeOfPath(path string) string {
	return localeFromFilename(filepath.Base(path))
}

// ---- validation ----

func validateAll(locales map[string]*LocaleCatalog) []ValidationError {
	var errs []ValidationError
	names := make([]string, 0, len(locales))
	for l := range locales {
		names = append(names, l)
	}
	sort.Strings(names)
	for _, l := range names {
		keys := make([]string, 0, len(locales[l].Entries))
		for k := range locales[l].Entries {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			e := locales[l].Entries[k]
			errs = append(errs, validateEntry(l, k, e)...)
		}
	}
	for _, l := range names {
		keys := make([]string, 0, len(locales[l].Entries))
		for k := range locales[l].Entries {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if e := locales[l].Entries[k]; e != nil && e.AST != nil {
				errs = append(errs, validateConsistency(locales, l, k, e)...)
			}
		}
	}
	return errs
}

func validateEntry(locale, key string, e *Entry) []ValidationError {
	nodes, errs := ValidateEntry(locale, key, e.Raw)
	e.AST = nodes
	return errs
}

func validateConsistency(locales map[string]*LocaleCatalog, locale, key string, e *Entry) []ValidationError {
	refs := map[string][]Node{}
	for other, lc := range locales {
		if other == locale {
			continue
		}
		if oe := lc.Entries[key]; oe != nil && oe.AST != nil {
			refs[other] = oe.AST
		}
	}
	return ValidateConsistency(locale, key, e.AST, refs)
}

// ---- resolution ----

func resolveAll(locales map[string]*LocaleCatalog, cfg Config) (map[string]map[string]*Resolution, []ValidationError) {
	resolved := map[string]map[string]*Resolution{}
	var errs []ValidationError
	localeNames := make([]string, 0, len(locales))
	for l := range locales {
		localeNames = append(localeNames, l)
	}
	sort.Strings(localeNames)
	for _, locale := range localeNames {
		resolved[locale] = map[string]*Resolution{}
		keys := map[string]bool{}
		for _, lc := range locales {
			for k := range lc.Entries {
				keys[k] = true
			}
		}
		ks := make([]string, 0, len(keys))
		for k := range keys {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		for _, key := range ks {
			r := resolveOne(locale, key, locales, cfg)
			if r == nil {
				errs = append(errs, ValidationError{
					Locale: locale, Key: key, Code: "MISSING_TRANSLATION",
					Message: fmt.Sprintf("no translation found in fallback chain %s", chainStr(locale, cfg)),
				})
				continue
			}
			resolved[locale][key] = r
		}
	}
	return resolved, errs
}

func resolveOne(locale, key string, locales map[string]*LocaleCatalog, cfg Config) *Resolution {
	chain := buildChain(locale, cfg)
	steps := make([]ChainStep, 0, len(chain))
	seen := map[string]bool{}
	for _, l := range chain {
		if seen[l] {
			continue
		}
		seen[l] = true
		lc := locales[l]
		if lc == nil {
			steps = append(steps, ChainStep{Locale: l, Hit: false, Reason: "locale not loaded"})
			continue
		}
		e := lc.Entries[key]
		if e == nil {
			steps = append(steps, ChainStep{Locale: l, Hit: false, Reason: "key missing"})
			continue
		}
		steps = append(steps, ChainStep{Locale: l, Hit: true, Reason: "hit"})
		return &Resolution{
			Locale:       locale,
			Key:          key,
			SourceLocale: l,
			Entry:        e,
			Chain:        steps,
		}
	}
	return nil
}

func buildChain(locale string, cfg Config) []string {
	chain := []string{locale}
	chain = append(chain, cfg.Fallback[locale]...)
	chain = append(chain, cfg.DefaultChain...)
	return chain
}

func chainStr(locale string, cfg Config) string {
	return strings.Join(buildChain(locale, cfg), " -> ")
}

// ---- pseudo ----

func buildPseudo(locales map[string]*LocaleCatalog, cfg Config) (string, map[string]*Entry) {
	if !cfg.Pseudo.Enabled {
		return "", nil
	}
	code := cfg.Pseudo.Locale
	if code == "" {
		code = "en_XA"
	}
	src := cfg.Pseudo.SourceLocale
	if src == "" {
		src = "en"
	}
	srcLC := locales[src]
	if srcLC == nil {
		return code, map[string]*Entry{}
	}
	entries := map[string]*Entry{}
	for key, e := range srcLC.Entries {
		if e.AST == nil {
			continue
		}
		ps := Pseudo(e.AST)
		entries[key] = &Entry{Key: key, Raw: Unparse(ps), AST: ps}
	}
	return code, entries
}

// ---- size ----

func checkSize(locales map[string]*LocaleCatalog, cfg Config) error {
	if cfg.MaxEntries <= 0 {
		return nil
	}
	total := 0
	for _, lc := range locales {
		total += len(lc.Entries)
	}
	if total > cfg.MaxEntries {
		return fmt.Errorf("%w: %d entries > %d", ErrCatalogTooLarge, total, cfg.MaxEntries)
	}
	return nil
}

// ---- diff / clone helpers ----

type pair struct{ locale, key string }

// localesDependentOn returns the locales whose fallback chain includes locale.
func localesDependentOn(locales map[string]*LocaleCatalog, cfg Config, locale string) []string {
	var out []string
	for loc := range locales {
		if loc == locale {
			continue
		}
		for _, c := range buildChain(loc, cfg) {
			if c == locale {
				out = append(out, loc)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

func dependentsOf(resolved map[string]map[string]*Resolution, locale string) map[pair]bool {
	out := map[pair]bool{}
	for loc, keys := range resolved {
		for key, r := range keys {
			if r.SourceLocale == locale && loc != locale {
				out[pair{loc, key}] = true
			}
		}
	}
	return out
}

func diffPaths(old, new map[string]SourceFile) (added, modified, deleted []string) {
	for p, nf := range new {
		of, ok := old[p]
		if !ok {
			added = append(added, p)
		} else if filesDiffer(of, nf) {
			modified = append(modified, p)
		}
	}
	for p := range old {
		if _, ok := new[p]; !ok {
			deleted = append(deleted, p)
		}
	}
	sort.Strings(added)
	sort.Strings(modified)
	sort.Strings(deleted)
	return added, modified, deleted
}

func cloneLocales(in map[string]*LocaleCatalog) map[string]*LocaleCatalog {
	out := map[string]*LocaleCatalog{}
	for l, lc := range in {
		entries := map[string]*Entry{}
		for k, e := range lc.Entries {
			entries[k] = e
		}
		out[l] = &LocaleCatalog{Locale: l, Entries: entries}
	}
	return out
}

func cloneFileEntries(in map[string]map[string]*Entry) map[string]map[string]*Entry {
	out := map[string]map[string]*Entry{}
	for p, m := range in {
		cp := map[string]*Entry{}
		for k, e := range m {
			cp[k] = e
		}
		out[p] = cp
	}
	return out
}

func cloneResolved(in map[string]map[string]*Resolution) map[string]map[string]*Resolution {
	out := map[string]map[string]*Resolution{}
	for l, m := range in {
		cp := map[string]*Resolution{}
		for k, r := range m {
			cp[k] = r
		}
		out[l] = cp
	}
	return out
}

func unionKeys(a, b map[string]*Entry) []string {
	seen := map[string]bool{}
	for k := range a {
		seen[k] = true
	}
	for k := range b {
		seen[k] = true
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func markValidated(m map[string]map[string]bool, locale, key string) {
	if m[locale] == nil {
		m[locale] = map[string]bool{}
	}
	m[locale][key] = true
}

func findFile(files []scannedFile, path string) scannedFile {
	for _, f := range files {
		if f.Path == path {
			return f
		}
	}
	return scannedFile{}
}

// filesDiffer compares by content hash when available, else size+mtime.
func filesDiffer(a, b SourceFile) bool {
	if a.SHA256 != "" && b.SHA256 != "" {
		return a.SHA256 != b.SHA256
	}
	return a.Size != b.Size || a.ModTime != b.ModTime
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
