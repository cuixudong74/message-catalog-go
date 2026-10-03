package catalog

// Entry is a single message entry in one locale.
type Entry struct {
	Key string
	Raw string
	AST []Node
}

// LocaleCatalog holds all entries of one locale.
type LocaleCatalog struct {
	Locale  string
	Entries map[string]*Entry
}

// ChainStep is one step of a fallback resolution walk.
type ChainStep struct {
	Locale string
	Hit    bool
	Reason string
}

// Resolution is the result of resolving a key for a locale.
type Resolution struct {
	Locale       string
	Key          string
	SourceLocale string // locale the entry was ultimately taken from
	Entry        *Entry
	Chain        []ChainStep
}

// SourceFile describes one loaded source file.
type SourceFile struct {
	Path    string
	Locale  string
	Size    int64
	ModTime int64 // unix seconds
	SHA256  string
}

// Version is an immutable, fully-resolved catalog snapshot.
type Version struct {
	ID           int64
	Config       Config
	Locales      map[string]*LocaleCatalog
	Resolved     map[string]map[string]*Resolution // locale -> key -> resolution
	Sources      map[string]SourceFile              // path -> source meta
	FileEntries  map[string]map[string]*Entry       // path -> key -> entry (incremental rebuild index)
	PseudoLocale string                             // pseudo locale code, "" if disabled
}

// Resolve returns the resolution of key for locale in this version, or nil if
// the key is not present anywhere in the fallback chain.
func (v *Version) Resolve(locale, key string) *Resolution {
	if v == nil || v.Resolved == nil {
		return nil
	}
	if m := v.Resolved[locale]; m != nil {
		return m[key]
	}
	return nil
}

// Query resolves key for locale and renders it with values. The returned
// resolution is always from a single committed version.
func (v *Version) Query(locale, key string, values map[string]any) (string, *Resolution, error) {
	r := v.Resolve(locale, key)
	if r == nil || r.Entry == nil || r.Entry.AST == nil {
		return "", r, &ValidationError{
			Locale: locale, Key: key, Code: "MISSING_TRANSLATION",
			Message: "no resolved entry",
		}
	}
	out, err := Render(r.Entry.AST, r.SourceLocale, values)
	if err != nil {
		return "", r, err
	}
	return out, r, nil
}

// LocaleNames returns the sorted list of locale codes in this version.
func (v *Version) LocaleNames() []string {
	if v == nil {
		return nil
	}
	names := make([]string, 0, len(v.Locales))
	for l := range v.Locales {
		names = append(names, l)
	}
	sortStrings(names)
	return names
}

func sortStrings(xs []string) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j-1] > xs[j]; j-- {
			xs[j-1], xs[j] = xs[j], xs[j-1]
		}
	}
}
