// Command msgcat validates, builds and queries localized message catalogs.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/cuixudong74/message-catalog-go/catalog"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "validate":
		err = cmdValidate(os.Args[2:])
	case "build":
		err = cmdBuild(os.Args[2:])
	case "query":
		err = cmdQuery(os.Args[2:])
	case "pseudo":
		err = cmdPseudo(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: msgcat <validate|build|query|pseudo> [flags]")
	os.Exit(2)
}

func parseConfig(name string, args []string) (catalog.Config, *flag.FlagSet, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	cfg := catalog.DefaultConfig()
	var fallback flagList
	fs.Var(&fallback, "fallback", "fallback chain, e.g. zh_TW=zh,en (repeatable)")
	fs.StringVar(&cfg.Pseudo.Locale, "pseudo-locale", "en_XA", "pseudo locale code")
	fs.StringVar(&cfg.Pseudo.SourceLocale, "pseudo-source", "en", "pseudo source locale")
	fs.BoolVar(&cfg.Pseudo.Enabled, "pseudo", false, "enable pseudo-localization")
	fs.IntVar(&cfg.MaxEntries, "max-entries", cfg.MaxEntries, "maximum total entries")
	if err := fs.Parse(args); err != nil {
		return cfg, fs, err
	}
	cfg.Fallback = map[string][]string{}
	for _, f := range fallback {
		parts := strings.SplitN(f, "=", 2)
		if len(parts) == 2 {
			cfg.Fallback[parts[0]] = strings.Split(parts[1], ",")
		}
	}
	return cfg, fs, nil
}

type flagList []string

func (f *flagList) String() string { return strings.Join(*f, ",") }
func (f *flagList) Set(v string) error {
	*f = append(*f, v)
	return nil
}

func cmdValidate(args []string) error {
	cfg, fs, err := parseConfig("validate", args)
	if err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: msgcat validate [flags] <locales-dir>")
	}
	cfg.LocalesDir = fs.Arg(0)
	store := catalog.NewStore()
	v, report, err := store.Update(context.Background(), cfg)
	if err != nil {
		return err
	}
	fmt.Printf("version %d: %d locales, %d entries, %d validation errors\n",
		v.ID, len(v.Locales), countEntries(v), len(report.Errors))
	for _, e := range report.Errors {
		fmt.Println(" ", e.Error())
	}
	if len(report.Errors) > 0 {
		os.Exit(1)
	}
	return nil
}

func cmdBuild(args []string) error {
	cfg, fs, err := parseConfig("build", args)
	if err != nil {
		return err
	}
	if fs.NArg() < 2 {
		return fmt.Errorf("usage: msgcat build [flags] <locales-dir> <snapshot-dir>")
	}
	cfg.LocalesDir = fs.Arg(0)
	store := catalog.NewStore()
	v, _, err := store.Update(context.Background(), cfg)
	if err != nil {
		return err
	}
	if err := store.SaveSnapshot(fs.Arg(1)); err != nil {
		return err
	}
	fmt.Printf("built version %d with %d entries\n", v.ID, countEntries(v))
	return nil
}

func cmdQuery(args []string) error {
	// Manual parse so --set may appear after positional arguments (Go's flag
	// package stops at the first non-flag argument).
	var sets []string
	var positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--set":
			if i+1 >= len(args) {
				return fmt.Errorf("--set requires a value")
			}
			sets = append(sets, args[i+1])
			i++
		case strings.HasPrefix(a, "--set="):
			sets = append(sets, strings.TrimPrefix(a, "--set="))
		default:
			positional = append(positional, a)
		}
	}
	if len(positional) < 3 {
		return fmt.Errorf("usage: msgcat query <snapshot-dir> <locale> <key> [--set k=v]")
	}
	store := catalog.NewStore()
	v, _, err := store.LoadSnapshot(positional[0])
	if err != nil {
		return err
	}
	values := map[string]any{}
	for _, s := range sets {
		parts := strings.SplitN(s, "=", 2)
		if len(parts) == 2 {
			values[parts[0]] = parts[1]
		}
	}
	out, r, err := v.Query(positional[1], positional[2], values)
	if err != nil {
		return err
	}
	fmt.Println(out)
	fmt.Printf("resolved from: %s\n", r.SourceLocale)
	var chain []string
	for _, s := range r.Chain {
		mark := "miss"
		if s.Hit {
			mark = "HIT"
		}
		chain = append(chain, fmt.Sprintf("%s(%s)", s.Locale, mark))
	}
	fmt.Println("chain:", strings.Join(chain, " -> "))
	return nil
}

func cmdPseudo(args []string) error {
	cfg, fs, err := parseConfig("pseudo", args)
	if err != nil {
		return err
	}
	if fs.NArg() < 2 {
		return fmt.Errorf("usage: msgcat pseudo [flags] <locales-dir> <out-dir>")
	}
	cfg.LocalesDir = fs.Arg(0)
	cfg.Pseudo.Enabled = true
	store := catalog.NewStore()
	v, _, err := store.Update(context.Background(), cfg)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(fs.Arg(1), 0o755); err != nil {
		return err
	}
	loc := v.Locales[v.PseudoLocale]
	keys := make([]string, 0, len(loc.Entries))
	for k := range loc.Entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := map[string]string{}
	for _, k := range keys {
		out[k] = catalog.Unparse(loc.Entries[k].AST)
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	path := fs.Arg(1) + "/" + v.PseudoLocale + ".json"
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %d pseudo messages to %s\n", len(keys), path)
	return nil
}

func countEntries(v *catalog.Version) int {
	n := 0
	for _, lc := range v.Locales {
		n += len(lc.Entries)
	}
	return n
}
