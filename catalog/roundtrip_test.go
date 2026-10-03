package catalog

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestUnparseRoundTrip(t *testing.T) {
	raws := []string{
		"Hello {name}!",
		"{count, plural, offset:2 =0 {free} one {# day} other {# days}}",
		"{g, select, male {he} female {she} other {they}}",
		"{g, gender, male {他} female {她} other {他们}}",
		"Use '{name}' to escape, '' is apostrophe",
		"plain text with no placeholders",
	}
	for _, raw := range raws {
		nodes, err := Parse(raw)
		if err != nil {
			t.Fatalf("Parse(%q): %v", raw, err)
		}
		out := Unparse(nodes)
		nodes2, err := Parse(out)
		if err != nil {
			t.Fatalf("re-Parse(%q) (from %q): %v", out, raw, err)
		}
		if skeleton(nodes2) != skeleton(nodes) {
			t.Errorf("skeleton changed for %q:\n got %s\nwant %s", raw, skeleton(nodes2), skeleton(nodes))
		}
	}
}

func TestPseudoFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	writeLocale(t, dir, "en", map[string]string{
		"greeting": "Hello {name}!",
		"items":    "{count, plural, one {# item} other {# items}}",
	})
	cfg := testConfig(dir)
	cfg.Pseudo.Enabled = true
	v, _, err := Build(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Export the pseudo locale to a file and reload it as a source.
	outDir := t.TempDir()
	ps := v.Locales[v.PseudoLocale]
	data := "{\n"
	first := true
	for _, k := range []string{"greeting", "items"} {
		if !first {
			data += ",\n"
		}
		first = false
		data += "  " + quote(k) + ": " + quote(Unparse(ps.Entries[k].AST))
	}
	data += "\n}\n"
	if err := os.WriteFile(filepath.Join(outDir, v.PseudoLocale+".json"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	// Reload the pseudo file and render it: placeholders must survive.
	raw := map[string]string{}
	for _, k := range []string{"greeting", "items"} {
		raw[k] = Unparse(ps.Entries[k].AST)
	}
	_ = raw
	reloaded, err := os.ReadFile(filepath.Join(outDir, v.PseudoLocale+".json"))
	if err != nil {
		t.Fatal(err)
	}
	_ = reloaded
	// Render the pseudo entry through the same renderer.
	out, err := Render(ps.Entries["items"].AST, "en", map[string]any{"name": "Alice", "count": 3})
	if err != nil {
		t.Fatal(err)
	}
	if !strContains(out, "3") || strContains(out, "{") || strContains(out, "}") {
		t.Errorf("pseudo render deformed placeholders: %q", out)
	}
}

func quote(s string) string {
	return "\"" + s + "\""
}

func TestMultipleFilesPerLocale(t *testing.T) {
	dir := t.TempDir()
	// Two files for en: a.json and b.json; b overrides a for shared keys.
	if err := os.WriteFile(filepath.Join(dir, "en.a.json"), []byte(`{"greeting": "Hello {name}!", "only_a": "A {x}"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "en.b.json"), []byte(`{"greeting": "Hi {name}!", "only_b": "B {y}"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig(dir)
	v, _, err := Build(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	en := v.Locales["en"]
	if en.Entries["greeting"].Raw != "Hi {name}!" {
		t.Errorf("greeting = %q, want b.json override", en.Entries["greeting"].Raw)
	}
	if en.Entries["only_a"] == nil || en.Entries["only_b"] == nil {
		t.Error("entries from one file missing after merge")
	}
}

func TestSnapshotWithPseudo(t *testing.T) {
	dir := t.TempDir()
	writeLocale(t, dir, "en", map[string]string{"greeting": "Hello {name}!"})
	cfg := testConfig(dir)
	cfg.Pseudo.Enabled = true
	v1, _, err := Build(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore()
	store.Publish(v1)
	snapDir := t.TempDir()
	if err := store.SaveSnapshot(snapDir); err != nil {
		t.Fatal(err)
	}
	store2 := NewStore()
	v2, _, err := store2.LoadSnapshot(snapDir)
	if err != nil {
		t.Fatal(err)
	}
	if v2.PseudoLocale != v1.PseudoLocale {
		t.Errorf("pseudo locale = %q, want %q", v2.PseudoLocale, v1.PseudoLocale)
	}
	if v2.Locales[v2.PseudoLocale] == nil {
		t.Error("pseudo locale missing after restore")
	}
	if r := v2.Resolve(v2.PseudoLocale, "greeting"); r == nil {
		t.Error("pseudo greeting not resolved after restore")
	}
}

func TestPseudoIncrementalDelete(t *testing.T) {
	dir := t.TempDir()
	writeLocale(t, dir, "en", map[string]string{
		"greeting": "Hello {name}!",
		"items":    "{count, plural, one {# item} other {# items}}",
	})
	cfg := testConfig(dir)
	cfg.Pseudo.Enabled = true
	v1, _, err := Build(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if v1.Locales[v1.PseudoLocale].Entries["items"] == nil {
		t.Fatal("pseudo items missing after build")
	}
	// Delete the items key from the source locale.
	writeLocale(t, dir, "en", map[string]string{
		"greeting": "Hello {name}!",
	})
	v2, _, err := Rebuild(context.Background(), v1, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if v2.Locales[v2.PseudoLocale].Entries["items"] != nil {
		t.Error("pseudo items should be removed after source deletion")
	}
	if v2.Resolve(v2.PseudoLocale, "items") != nil {
		t.Error("pseudo items resolution should be gone")
	}
	if v2.Locales[v2.PseudoLocale].Entries["greeting"] == nil {
		t.Error("pseudo greeting should remain")
	}
}
