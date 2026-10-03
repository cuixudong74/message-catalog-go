package catalog

// PseudoConfig controls pseudo-localization generation.
type PseudoConfig struct {
	// Enabled turns pseudo-localization on for builds.
	Enabled bool
	// Locale is the pseudo locale code added to the catalog (default "en_XA").
	Locale string
	// SourceLocale is the locale pseudo messages are derived from (default "en").
	SourceLocale string
}

// Config controls catalog loading, merging and generation.
type Config struct {
	// LocalesDir is the directory containing <locale>.json message files.
	LocalesDir string
	// Fallback maps a locale code to its ordered fallback chain (without itself).
	Fallback map[string][]string
	// DefaultChain is used for locales not present in Fallback.
	DefaultChain []string
	// Pseudo controls pseudo-localization.
	Pseudo PseudoConfig
	// MaxEntries caps the total number of entries across all locales.
	MaxEntries int
	// MaxFileSize caps the size of a single source file in bytes.
	MaxFileSize int64
}

// DefaultConfig returns a usable default configuration.
func DefaultConfig() Config {
	return Config{
		Fallback:     map[string][]string{},
		DefaultChain: []string{"en"},
		Pseudo:       PseudoConfig{Enabled: false, Locale: "en_XA", SourceLocale: "en"},
		MaxEntries:   1_000_000,
		MaxFileSize:  64 << 20,
	}
}
