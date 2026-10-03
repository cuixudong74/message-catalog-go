package catalog

import (
	"fmt"
	"testing"
	"time"
)

// 超大规模目录：加载与查询表现可接受，且增量重建远快于全量。
func TestLargeCatalog(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	dir := t.TempDir()
	langs := []string{"en", "fr", "de", "ja", "zh", "ru", "es", "pt", "ko", "ar"}
	const perLang = 2000
	for _, lang := range langs {
		entries := map[string]string{}
		for i := 0; i < perLang; i++ {
			entries[fmt.Sprintf("key.%d", i)] = fmt.Sprintf("{%s} msg %d {n, plural, one{one} other{#}}", "user", i)
		}
		writeLang(t, dir, lang, entries)
	}

	cfg := testConfig()
	for _, l := range langs {
		if l != "en" {
			cfg.Fallback[l] = []string{"en"}
		}
	}
	c := New(cfg)
	start := time.Now()
	issues, err := c.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	loadDur := time.Since(start)
	t.Logf("full load of %d entries: %v (issues=%d)", len(langs)*perLang, loadDur, len(issues))
	if loadDur > 10*time.Second {
		t.Fatalf("load too slow: %v", loadDur)
	}

	start = time.Now()
	const queries = 20000
	for i := 0; i < queries; i++ {
		if _, _, err := c.Render("fr", fmt.Sprintf("key.%d", i%perLang),
			map[string]any{"user": "u", "n": i}); err != nil {
			t.Fatal(err)
		}
	}
	qDur := time.Since(start)
	t.Logf("%d queries: %v (%.0f qps)", queries, qDur, float64(queries)/qDur.Seconds())
	if qDur > 5*time.Second {
		t.Fatalf("queries too slow: %v", qDur)
	}

	// 增量：只改一个文件
	writeLang(t, dir, "en", map[string]string{"key.0": "changed {user}"})
	start = time.Now()
	aff, _, err := c.Rebuild(dir)
	if err != nil {
		t.Fatal(err)
	}
	incDur := time.Since(start)
	t.Logf("incremental rebuild (1 file): %v, affected=%+v", incDur, aff.Langs)
	if incDur > loadDur {
		t.Fatalf("incremental (%v) should not exceed full load (%v)", incDur, loadDur)
	}
	if len(aff.Langs) != 1 || aff.Langs[0] != "en" {
		t.Fatalf("only en should be affected: %+v", aff.Langs)
	}
}
