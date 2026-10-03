package catalog

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---- helpers ----

func writeLocaleAtomic(t *testing.T, dir, locale string, entries map[string]string) {
	t.Helper()
	var sb strings.Builder
	sb.WriteString("{\n")
	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for i, k := range keys {
		fmt.Fprintf(&sb, "  %q: %q", k, entries[k])
		if i < len(keys)-1 {
			sb.WriteString(",")
		}
		sb.WriteString("\n")
	}
	sb.WriteString("}\n")
	tmp := filepath.Join(dir, fmt.Sprintf("%s.json.%d.tmp", locale, time.Now().UnixNano()))
	if err := os.WriteFile(tmp, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, filepath.Join(dir, locale+".json")); err != nil {
		t.Fatal(err)
	}
}

func writeLocale(t *testing.T, dir, locale string, entries map[string]string) {
	t.Helper()
	var sb strings.Builder
	sb.WriteString("{\n")
	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for i, k := range keys {
		fmt.Fprintf(&sb, "  %q: %q", k, entries[k])
		if i < len(keys)-1 {
			sb.WriteString(",")
		}
		sb.WriteString("\n")
	}
	sb.WriteString("}\n")
	if err := os.WriteFile(filepath.Join(dir, locale+".json"), []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func testConfig(dir string) Config {
	cfg := DefaultConfig()
	cfg.LocalesDir = dir
	cfg.Fallback = map[string][]string{
		"zh_TW": {"zh", "en"},
		"zh":    {"en"},
	}
	return cfg
}

// resolvedRaw returns a deterministic map of locale/key -> source locale raw.
func resolvedRaw(v *Version) map[string]string {
	out := map[string]string{}
	for locale, keys := range v.Resolved {
		for key, r := range keys {
			if r.Entry != nil {
				out[locale+"/"+key] = r.SourceLocale + ":" + r.Entry.Raw
			}
		}
	}
	return out
}

func buildTestCatalog(t *testing.T, dir string) *Version {
	t.Helper()
	writeLocale(t, dir, "en", map[string]string{
		"greeting": "Hello {name}!",
		"items":    "{count, plural, one {# item} other {# items}}",
		"only_en":  "English only {x}",
	})
	writeLocale(t, dir, "zh", map[string]string{
		"greeting": "你好 {name}！",
		"items":    "{count, plural, one {# 个} other {# 个}}",
		"zh_only":  "仅中文 {y}",
	})
	writeLocale(t, dir, "zh_TW", map[string]string{
		"greeting": "你好 {name}！",
	})
	cfg := testConfig(dir)
	v, report, err := Build(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, e := range report.Errors {
		if e.Code != "MISSING_TRANSLATION" {
			t.Fatalf("unexpected build errors: %v", report.Errors)
		}
	}
	return v
}

// ---- fallback tests ----

func TestFallbackResolution(t *testing.T) {
	dir := t.TempDir()
	v := buildTestCatalog(t, dir)

	cases := []struct {
		locale, key, wantSource string
	}{
		{"zh_TW", "greeting", "zh_TW"},
		{"zh_TW", "items", "zh"},
		{"zh_TW", "only_en", "en"},
		{"zh_TW", "zh_only", "zh"},
		{"zh", "greeting", "zh"},
		{"zh", "only_en", "en"},
		{"en", "greeting", "en"},
	}
	for _, c := range cases {
		r := v.Resolve(c.locale, c.key)
		if r == nil {
			t.Errorf("%s/%s: no resolution", c.locale, c.key)
			continue
		}
		if r.SourceLocale != c.wantSource {
			t.Errorf("%s/%s: got source %s, want %s", c.locale, c.key, r.SourceLocale, c.wantSource)
		}
		if !r.Chain[len(r.Chain)-1].Hit {
			t.Errorf("%s/%s: chain does not end in a hit: %v", c.locale, c.key, r.Chain)
		}
	}
}

func TestFallbackChainExplainable(t *testing.T) {
	dir := t.TempDir()
	v := buildTestCatalog(t, dir)
	r := v.Resolve("zh_TW", "items")
	if r == nil {
		t.Fatal("no resolution")
	}
	got := []string{}
	for _, s := range r.Chain {
		mark := "miss"
		if s.Hit {
			mark = "hit"
		}
		got = append(got, s.Locale+":"+mark)
	}
	want := []string{"zh_TW:miss", "zh:hit"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("chain = %v, want %v", got, want)
	}
}

func TestFallbackMissingEverywhere(t *testing.T) {
	dir := t.TempDir()
	v := buildTestCatalog(t, dir)
	if r := v.Resolve("zh_TW", "nope"); r != nil {
		t.Errorf("expected nil resolution for missing key, got %v", r)
	}
}

// ---- validation tests ----

func TestValidationPlaceholderMismatch(t *testing.T) {
	dir := t.TempDir()
	writeLocale(t, dir, "en", map[string]string{"k": "Hello {name}"})
	writeLocale(t, dir, "zh", map[string]string{"k": "你好 {mingzi}"})
	cfg := testConfig(dir)
	_, report, err := Build(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	codes := codesOf(report.Errors)
	if !containsCode(codes, "PLACEHOLDER_MISMATCH") {
		t.Errorf("expected PLACEHOLDER_MISMATCH, got %v", report.Errors)
	}
}

func TestValidationBranchKindMismatch(t *testing.T) {
	dir := t.TempDir()
	writeLocale(t, dir, "en", map[string]string{"k": "{count, plural, one {#} other {#}}"})
	writeLocale(t, dir, "zh", map[string]string{"k": "{count}"})
	cfg := testConfig(dir)
	_, report, err := Build(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !containsCode(codesOf(report.Errors), "BRANCH_KIND_MISMATCH") {
		t.Errorf("expected BRANCH_KIND_MISMATCH, got %v", report.Errors)
	}
}

func TestValidationMissingOther(t *testing.T) {
	dir := t.TempDir()
	writeLocale(t, dir, "en", map[string]string{"k": "{n, plural, one {#}}"})
	cfg := testConfig(dir)
	_, report, err := Build(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !containsCode(codesOf(report.Errors), "MISSING_OTHER") {
		t.Errorf("expected MISSING_OTHER, got %v", report.Errors)
	}
}

func TestValidationBadCategory(t *testing.T) {
	dir := t.TempDir()
	writeLocale(t, dir, "en", map[string]string{"k": "{n, plural, weird {#} other {#}}"})
	writeLocale(t, dir, "zh", map[string]string{"k": "{g, gender, male {他} female {她} other {他们}}"})
	cfg := testConfig(dir)
	_, report, err := Build(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	codes := codesOf(report.Errors)
	if !containsCode(codes, "INVALID_PLURAL_CATEGORY") {
		t.Errorf("expected INVALID_PLURAL_CATEGORY, got %v", report.Errors)
	}
}

func TestValidationBatchContinues(t *testing.T) {
	dir := t.TempDir()
	writeLocale(t, dir, "en", map[string]string{
		"good": "Hello {name}",
		"bad":  "Hello {name",
	})
	cfg := testConfig(dir)
	v, report, err := Build(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !containsCode(codesOf(report.Errors), "PARSE") {
		t.Errorf("expected PARSE error, got %v", report.Errors)
	}
	// The good entry must still be published.
	if r := v.Resolve("en", "good"); r == nil {
		t.Error("good entry missing after batch error")
	}
	if r := v.Resolve("en", "bad"); r == nil {
		t.Error("bad entry still present in catalog (with AST nil)")
	} else if r.Entry.AST != nil {
		t.Error("bad entry should have nil AST")
	}
}

func codesOf(errs []ValidationError) []string {
	out := make([]string, 0, len(errs))
	for _, e := range errs {
		out = append(out, e.Code)
	}
	return out
}

func containsCode(codes []string, code string) bool {
	for _, c := range codes {
		if c == code {
			return true
		}
	}
	return false
}

// ---- incremental tests ----

func TestIncrementalModify(t *testing.T) {
	dir := t.TempDir()
	v1 := buildTestCatalog(t, dir)
	before := resolvedRaw(v1)

	// Modify zh/items (zh_TW has no items of its own, so it falls back).
	writeLocale(t, dir, "zh", map[string]string{
		"greeting": "你好 {name}！",
		"items":    "{count, plural, one {# 个} other {# 个物品}",
		"zh_only":  "仅中文 {y}",
	})
	cfg := testConfig(dir)
	v2, report, err := Rebuild(context.Background(), v1, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.FilesModified) != 1 || report.FilesModified[0] != filepath.Join(dir, "zh.json") {
		t.Errorf("FilesModified = %v", report.FilesModified)
	}
	if !contains(report.Affected["zh"], "items") {
		t.Errorf("affected zh keys = %v", report.Affected["zh"])
	}
	// zh_TW/items falls back to zh -> must also be affected.
	if !contains(report.Affected["zh_TW"], "items") {
		t.Errorf("zh_TW should be affected via fallback, got %v", report.Affected["zh_TW"])
	}
	after := resolvedRaw(v2)
	if before["zh_TW/items"] == after["zh_TW/items"] {
		t.Error("zh_TW/items should have changed via fallback")
	}
	if after["zh/items"] != "zh:{count, plural, one {# 个} other {# 个物品}" {
		t.Errorf("zh/items = %q", after["zh/items"])
	}
}

func TestIncrementalAddDelete(t *testing.T) {
	dir := t.TempDir()
	v1 := buildTestCatalog(t, dir)

	// Add a new key to en (modified), add a fr locale file, and delete zh_TW.
	writeLocale(t, dir, "en", map[string]string{
		"greeting": "Hello {name}!",
		"items":    "{count, plural, one {# item} other {# items}}",
		"only_en":  "English only {x}",
		"new_key":  "Brand new {z}",
	})
	writeLocale(t, dir, "fr", map[string]string{
		"greeting": "Bonjour {name} !",
	})
	if err := os.Remove(filepath.Join(dir, "zh_TW.json")); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig(dir)
	v2, report, err := Rebuild(context.Background(), v1, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.FilesModified) != 1 || report.FilesModified[0] != filepath.Join(dir, "en.json") {
		t.Errorf("FilesModified = %v", report.FilesModified)
	}
	if len(report.FilesAdded) != 1 || report.FilesAdded[0] != filepath.Join(dir, "fr.json") {
		t.Errorf("FilesAdded = %v", report.FilesAdded)
	}
	if len(report.FilesDeleted) != 1 || report.FilesDeleted[0] != filepath.Join(dir, "zh_TW.json") {
		t.Errorf("FilesDeleted = %v", report.FilesDeleted)
	}
	if r := v2.Resolve("en", "new_key"); r == nil || r.SourceLocale != "en" {
		t.Error("new_key not resolved from en")
	}
	// zh_TW locale gone; its keys now resolve from zh/en.
	if v2.Locales["zh_TW"] != nil {
		t.Error("zh_TW locale should be removed")
	}
	if r := v2.Resolve("zh_TW", "greeting"); r == nil || r.SourceLocale != "zh" {
		t.Errorf("zh_TW/greeting should fall back to zh, got %v", r)
	}
}

func TestIncrementalEqualsFull(t *testing.T) {
	dir := t.TempDir()
	v1 := buildTestCatalog(t, dir)

	// Several changes.
	writeLocale(t, dir, "en", map[string]string{
		"greeting": "Hello {name}!",
		"items":    "{count, plural, one {# item} other {# items}}",
		"only_en":  "English only {x}",
		"new_key":  "New {z}",
	})
	writeLocale(t, dir, "zh", map[string]string{
		"greeting": "您好 {name}！",
		"items":    "{count, plural, one {# 个} other {# 个}}",
		"zh_only":  "仅中文 {y}",
	})
	cfg := testConfig(dir)
	v2, _, err := Rebuild(context.Background(), v1, cfg)
	if err != nil {
		t.Fatal(err)
	}
	vFull, _, err := Build(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	inc := resolvedRaw(v2)
	full := resolvedRaw(vFull)
	if len(inc) != len(full) {
		t.Fatalf("incremental has %d resolutions, full has %d", len(inc), len(full))
	}
	for k, v := range full {
		if inc[k] != v {
			t.Errorf("mismatch at %s:\n inc: %s\nfull: %s", k, inc[k], v)
		}
	}
}

// ---- restart / snapshot tests ----

func TestSnapshotSaveLoad(t *testing.T) {
	dir := t.TempDir()
	v1 := buildTestCatalog(t, dir)
	store := NewStore()
	store.Publish(v1)
	snapDir := t.TempDir()
	if err := store.SaveSnapshot(snapDir); err != nil {
		t.Fatal(err)
	}

	store2 := NewStore()
	v2, report, err := store2.LoadSnapshot(snapDir)
	if err != nil {
		t.Fatal(err)
	}
	if v2.ID != v1.ID {
		t.Errorf("ID = %d, want %d", v2.ID, v1.ID)
	}
	for _, e := range report.Errors {
		if e.Code != "MISSING_TRANSLATION" {
			t.Errorf("unexpected errors: %v", report.Errors)
		}
	}
	r1 := resolvedRaw(v1)
	r2 := resolvedRaw(v2)
	for k, v := range r1 {
		if r2[k] != v {
			t.Errorf("mismatch at %s: %q vs %q", k, r2[k], v)
		}
	}
}

func TestSnapshotCorruptionFallsBack(t *testing.T) {
	dir := t.TempDir()
	v1 := buildTestCatalog(t, dir)
	store := NewStore()
	store.Publish(v1)
	snapDir := t.TempDir()
	if err := store.SaveSnapshot(snapDir); err != nil {
		t.Fatal(err)
	}
	// Corrupt the current snapshot.
	if err := os.WriteFile(filepath.Join(snapDir, "snapshot.json"), []byte(`{"corrupt":`), 0o644); err != nil {
		t.Fatal(err)
	}
	store2 := NewStore()
	v2, _, err := store2.LoadSnapshot(snapDir)
	if err != nil {
		t.Fatalf("expected fallback to prev snapshot, got %v", err)
	}
	if v2.ID != v1.ID {
		t.Errorf("fell back to wrong version: %d vs %d", v2.ID, v1.ID)
	}
}

func TestSnapshotNoSnapshot(t *testing.T) {
	store := NewStore()
	_, _, err := store.LoadSnapshot(t.TempDir())
	if !errors.Is(err, ErrNoSnapshot) {
		t.Errorf("expected ErrNoSnapshot, got %v", err)
	}
}

// ---- concurrency tests ----

func TestConcurrentQueriesAndRebuilds(t *testing.T) {
	dir := t.TempDir()
	buildTestCatalog(t, dir)
	store := NewStore()
	cfg := testConfig(dir)
	if _, _, err := store.Update(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	errCh := make(chan error, 16)

	// Readers: continuously query, always against a complete version.
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			values := map[string]any{"name": "Alice", "count": 2, "x": 1, "y": 2, "z": 3}
			for {
				select {
				case <-ctx.Done():
					return
				default:
				}
				v := store.Current()
				if v == nil {
					errCh <- fmt.Errorf("nil version")
					return
				}
				for _, lk := range []struct{ locale, key string }{
					{"zh_TW", "greeting"}, {"zh_TW", "items"}, {"zh_TW", "only_en"},
					{"en", "items"}, {"zh", "zh_only"},
				} {
					out, r, err := v.Query(lk.locale, lk.key, values)
					if err != nil {
						errCh <- fmt.Errorf("query %s/%s: %v", lk.locale, lk.key, err)
						return
					}
					// Consistency: rendered output must come from the source
					// locale's catalog entry of this same version.
					if r.SourceLocale != r.Chain[len(r.Chain)-1].Locale {
						errCh <- fmt.Errorf("chain does not end at source locale")
						return
					}
					src := v.Locales[r.SourceLocale]
					if src == nil || src.Entries[r.Key] == nil || src.Entries[r.Key].Raw != r.Entry.Raw {
						errCh <- fmt.Errorf("resolution entry not present in source locale catalog")
						return
					}
					if out == "" {
						errCh <- fmt.Errorf("empty render output")
						return
					}
				}
			}
		}()
	}

	// Writers: rebuild repeatedly with changing content.
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				select {
				case <-ctx.Done():
					return
				default:
				}
				writeLocaleAtomic(t, dir, "en", map[string]string{
					"greeting": "Hello {name}!",
					"items":    fmt.Sprintf("{count, plural, one {# item v%d} other {# items v%d}}", i, i),
					"only_en":  "English only {x}",
				})
				if _, _, err := store.Update(context.Background(), cfg); err != nil {
					errCh <- err
					return
				}
			}
		}(i)
	}

	// Let the race run for 2 seconds of wall time, then stop.
	time.Sleep(2 * time.Second)
	cancel()
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
}

func TestSlowQueryDoesNotBlockRebuild(t *testing.T) {
	dir := t.TempDir()
	v1 := buildTestCatalog(t, dir)
	store := NewStore()
	store.Publish(v1)
	cfg := testConfig(dir)

	// A "slow query" holds a reference to the old version.
	old := store.Current()
	done := make(chan struct{})
	go func() {
		defer close(done)
		// Simulate a slow query by holding the version reference and doing
		// repeated lookups; rebuilds must still advance.
		for i := 0; i < 1000; i++ {
			_ = old.Resolve("en", "greeting")
		}
	}()

	for i := 0; i < 50; i++ {
		writeLocale(t, dir, "en", map[string]string{
			"greeting": fmt.Sprintf("Hello {name} v%d!", i),
			"items":    "{count, plural, one {# item} other {# items}}",
			"only_en":  "English only {x}",
		})
		if _, _, err := store.Update(context.Background(), cfg); err != nil {
			t.Fatal(err)
		}
	}
	<-done
	if store.Current().ID == v1.ID {
		t.Error("rebuilds did not advance while a slow query held the old version")
	}
	// Old version must still be fully queryable.
	if r := old.Resolve("en", "greeting"); r == nil || r.Entry.Raw != "Hello {name}!" {
		t.Error("old version was mutated by rebuild")
	}
}

// ---- config tests ----

func TestConfigMaxEntries(t *testing.T) {
	dir := t.TempDir()
	buildTestCatalog(t, dir)
	cfg := testConfig(dir)
	cfg.MaxEntries = 3 // total entries across all locales exceeds this
	store := NewStore()
	_, _, err := store.Update(context.Background(), cfg)
	if !errors.Is(err, ErrCatalogTooLarge) {
		t.Errorf("expected ErrCatalogTooLarge, got %v", err)
	}
	if store.Current() != nil {
		t.Error("oversized catalog must not be published")
	}
}

func TestConfigChangeAffectsSubsequentOnly(t *testing.T) {
	dir := t.TempDir()
	v1 := buildTestCatalog(t, dir)
	store := NewStore()
	store.Publish(v1)

	// Change the fallback config: zh_TW now falls back directly to en.
	cfg2 := testConfig(dir)
	cfg2.Fallback = map[string][]string{
		"zh_TW": {"en"},
		"zh":    {"en"},
	}
	v2, _, err := store.Update(context.Background(), cfg2)
	if err != nil {
		t.Fatal(err)
	}
	if r := v2.Resolve("zh_TW", "items"); r == nil || r.SourceLocale != "en" {
		t.Errorf("new version should resolve zh_TW/items from en, got %v", r)
	}
	// Old version still resolves via the old chain.
	if r := v1.Resolve("zh_TW", "items"); r == nil || r.SourceLocale != "zh" {
		t.Errorf("old version should still resolve from zh, got %v", r)
	}
}

func TestPseudoInBuild(t *testing.T) {
	dir := t.TempDir()
	writeLocale(t, dir, "en", map[string]string{
		"greeting": "Hello {name}!",
		"items":    "{count, plural, one {# item} other {# items}}",
	})
	cfg := testConfig(dir)
	cfg.Pseudo.Enabled = true
	v, report, err := Build(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", report.Errors)
	}
	ps := v.Locales[v.PseudoLocale]
	if ps == nil {
		t.Fatal("pseudo locale missing")
	}
	e := ps.Entries["greeting"]
	if e == nil {
		t.Fatal("pseudo greeting missing")
	}
	if !strings.Contains(e.Raw, "[!!") || !strings.Contains(e.Raw, "!!]") {
		t.Errorf("pseudo raw not marked: %q", e.Raw)
	}
	if !strings.Contains(e.Raw, "{name}") {
		t.Errorf("placeholder lost in pseudo: %q", e.Raw)
	}
	// Pseudo entry renders identically through the same renderer.
	out, err := Render(e.AST, "en", map[string]any{"name": "Alice"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Alice") {
		t.Errorf("rendered pseudo missing name: %q", out)
	}
}
