package catalog

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeLang(t *testing.T, dir, lang string, entries map[string]string) {
	t.Helper()
	data := "{"
	first := true
	for k, v := range entries {
		if !first {
			data += ","
		}
		first = false
		data += fmt.Sprintf("%q:%q", k, v)
	}
	data += "}"
	if err := os.WriteFile(filepath.Join(dir, lang+".json"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func testConfig() Config {
	return Config{
		BaseLocale: "en",
		Fallback: map[string][]string{
			"fr-CA": {"fr", "en"},
			"fr":    {"en"},
			"de":    {"en"},
		},
	}
}

func TestFallbackChainMultiLevel(t *testing.T) {
	dir := t.TempDir()
	writeLang(t, dir, "en", map[string]string{"a": "en-a", "b": "en-b", "c": "en-c"})
	writeLang(t, dir, "fr", map[string]string{"a": "fr-a", "b": "fr-b"})
	writeLang(t, dir, "fr-CA", map[string]string{"a": "frca-a"})

	c := New(testConfig())
	if _, err := c.Load(dir); err != nil {
		t.Fatal(err)
	}

	cases := []struct{ key, wantText, wantLang string }{
		{"a", "frca-a", "fr-CA"}, // 本语言命中
		{"b", "fr-b", "fr"},      // 一级回退
		{"c", "en-c", "en"},      // 二级回退
	}
	for _, tc := range cases {
		m, res, err := c.Resolve("fr-CA", tc.key)
		if err != nil {
			t.Fatal(err)
		}
		if m.Raw != tc.wantText || res.ActualLang != tc.wantLang {
			t.Fatalf("key %s: got (%q, %s), want (%q, %s)", tc.key, m.Raw, res.ActualLang, tc.wantText, tc.wantLang)
		}
		if !res.Found || res.WantLang != "fr-CA" {
			t.Fatalf("resolution not explainable: %+v", res)
		}
	}

	// 链上都没有 → 唯一确定的未命中
	_, res, err := c.Resolve("fr-CA", "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	wantChain := []string{"fr-CA", "fr", "en"}
	if !reflect.DeepEqual(res.Chain, wantChain) {
		t.Fatalf("chain = %v, want %v", res.Chain, wantChain)
	}
}

func TestFallbackHitIsUnique(t *testing.T) {
	dir := t.TempDir()
	writeLang(t, dir, "en", map[string]string{"a": "en"})
	writeLang(t, dir, "fr", map[string]string{"a": "fr"})
	c := New(testConfig())
	if _, err := c.Load(dir); err != nil {
		t.Fatal(err)
	}
	// 同一键在多种语言存在时，命中唯一：链上第一个
	_, res, err := c.Resolve("fr", "a")
	if err != nil {
		t.Fatal(err)
	}
	if res.ActualLang != "fr" {
		t.Fatalf("hit must be unique and first in chain, got %s", res.ActualLang)
	}
}

func TestLoadParseErrorDoesNotFailBatch(t *testing.T) {
	dir := t.TempDir()
	writeLang(t, dir, "en", map[string]string{"good": "OK", "bad": "{unclosed"})
	c := New(testConfig())
	issues, err := c.Load(dir)
	if err != nil {
		t.Fatalf("single bad entry must not fail load: %v", err)
	}
	if !hasReason(issues, ReasonParseError) {
		t.Fatalf("expected parse error issue, got %v", issues)
	}
	m, _, err := c.Resolve("en", "good")
	if err != nil || m.Raw != "OK" {
		t.Fatalf("good entry must be usable: %v", err)
	}
}

func TestIncrementalEqualsFull(t *testing.T) {
	dir := t.TempDir()
	writeLang(t, dir, "en", map[string]string{"a": "1", "b": "2"})
	writeLang(t, dir, "fr", map[string]string{"a": "un"})
	c := New(testConfig())
	if _, err := c.Load(dir); err != nil {
		t.Fatal(err)
	}

	// 修改 en、删除 fr、新增 de
	writeLang(t, dir, "en", map[string]string{"a": "1", "b": "2", "c": "3"})
	if err := os.Remove(filepath.Join(dir, "fr.json")); err != nil {
		t.Fatal(err)
	}
	writeLang(t, dir, "de", map[string]string{"a": "eins"})

	aff, _, err := c.Rebuild(dir)
	if err != nil {
		t.Fatal(err)
	}
	inc := c.Current()

	// 全量参照
	full := New(testConfig())
	if _, err := full.Load(dir); err != nil {
		t.Fatal(err)
	}
	ref := full.Current()

	if len(inc.Msgs) != len(ref.Msgs) {
		t.Fatalf("incremental langs %v != full %v", reflect.ValueOf(inc.Msgs).MapKeys(), reflect.ValueOf(ref.Msgs).MapKeys())
	}
	for lang, msgs := range ref.Msgs {
		for k, m := range msgs {
			got := inc.Msgs[lang][k]
			if got == nil || got.Raw != m.Raw {
				t.Fatalf("incremental mismatch at %s:%s", lang, k)
			}
		}
	}

	// 受影响说明：en/de/fr 三种语言，键含 a/b/c
	wantLangs := []string{"de", "en", "fr"}
	if !reflect.DeepEqual(aff.Langs, wantLangs) {
		t.Fatalf("affected langs = %v, want %v", aff.Langs, wantLangs)
	}
	for _, k := range []string{"a", "b", "c"} {
		found := false
		for _, ak := range aff.Keys {
			if ak == k {
				found = true
			}
		}
		if !found {
			t.Fatalf("key %s should be affected: %v", k, aff.Keys)
		}
	}
}

func TestMaxEntriesLimit(t *testing.T) {
	dir := t.TempDir()
	writeLang(t, dir, "en", map[string]string{"a": "1", "b": "2", "c": "3"})
	cfg := testConfig()
	cfg.MaxEntries = 2
	c := New(cfg)
	_, err := c.Load(dir)
	if !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("want ErrLimitExceeded, got %v", err)
	}
	// 超限后旧版本（空）保持不变，行为确定
	if _, _, err := c.Resolve("en", "a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old version must be intact, got %v", err)
	}
	// 放宽上限后同样输入可复现地成功
	cfg.MaxEntries = 0
	c.SetConfig(cfg)
	if _, err := c.Load(dir); err != nil {
		t.Fatal(err)
	}
}

func TestMaxFileBytes(t *testing.T) {
	dir := t.TempDir()
	writeLang(t, dir, "en", map[string]string{"a": "1"})
	writeLang(t, dir, "big", map[string]string{"a": "1", "b": "2", "c": "3"})
	cfg := testConfig()
	cfg.MaxFileBytes = 20
	c := New(cfg)
	issues, err := c.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !hasReason(issues, ReasonLimitExceeded) {
		t.Fatalf("expected limit issue, got %v", issues)
	}
	// 超限文件被跳过，其他语言正常
	if _, _, err := c.Resolve("en", "a"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Resolve("big", "a"); !errors.Is(err, ErrNotFound) {
		t.Fatal("oversized file must be skipped deterministically")
	}
}

func TestPseudoLocaleGenerated(t *testing.T) {
	dir := t.TempDir()
	writeLang(t, dir, "en", map[string]string{"a": "Hello {name}"})
	cfg := testConfig()
	cfg.PseudoEnabled = true
	c := New(cfg)
	if _, err := c.Load(dir); err != nil {
		t.Fatal(err)
	}
	out, res, err := c.Render("en-pseudo", "a", map[string]any{"name": "Bo"})
	if err != nil {
		t.Fatal(err)
	}
	if res.ActualLang != "en-pseudo" {
		t.Fatalf("got %s", res.ActualLang)
	}
	want := "⟦" + pseudoText("Hello ") + "Bo" + "⟧"
	if out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}

func TestConfigChangeAffectsOnlyFuture(t *testing.T) {
	dir := t.TempDir()
	writeLang(t, dir, "en", map[string]string{"a": "en"})
	writeLang(t, dir, "de", map[string]string{"a": "de"})
	c := New(testConfig())
	if _, err := c.Load(dir); err != nil {
		t.Fatal(err)
	}
	// 改配置不影响已提交版本
	cfg := testConfig()
	cfg.Fallback = map[string][]string{"de": {}}
	c.SetConfig(cfg)
	_, res, _ := c.Resolve("de", "a")
	if res.ActualLang != "de" {
		t.Fatal("existing snapshot must be unaffected by config change")
	}
	// 但影响后续重建
	if _, err := c.Load(dir); err != nil {
		t.Fatal(err)
	}
	if _, res, _ := c.Resolve("de", "missing"); len(res.Chain) != 1 {
		t.Fatalf("new config must apply to later loads, chain=%v", res.Chain)
	}
}
