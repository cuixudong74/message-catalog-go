package catalog

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func benchMsg(i int) string {
	return "Hello {name}, you have {count, plural, one {# item} other {# items}} and {g, select, male {he} female {she} other {they}}"
}

func benchMsgZh(i int) string {
	return "你好 {name}，{count, plural, one {# 个} other {# 个}}"
}

func BenchmarkBuildLarge(b *testing.B) {
	dir := b.TempDir()
	const N = 100_000
	entries := map[string]string{}
	for i := 0; i < N; i++ {
		entries[fmt.Sprintf("key_%06d", i)] = benchMsg(i)
	}
	writeLocaleBench(b, dir, "en", entries)
	// A second locale with 80% of the keys, forcing fallback resolution.
	zh := map[string]string{}
	for i := 0; i < N*8/10; i++ {
		zh[fmt.Sprintf("key_%06d", i)] = benchMsgZh(i)
	}
	writeLocaleBench(b, dir, "zh", zh)
	cfg := testConfig(dir)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v, _, err := Build(context.Background(), cfg)
		if err != nil {
			b.Fatal(err)
		}
		_ = v
	}
}

func BenchmarkQueryLarge(b *testing.B) {
	dir := b.TempDir()
	const N = 100_000
	entries := map[string]string{}
	for i := 0; i < N; i++ {
		entries[fmt.Sprintf("key_%06d", i)] = benchMsg(i)
	}
	writeLocaleBench(b, dir, "en", entries)
	cfg := testConfig(dir)
	v, _, err := Build(context.Background(), cfg)
	if err != nil {
		b.Fatal(err)
	}
	values := map[string]any{"name": "Alice", "count": 3, "g": "female"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key_%06d", i%N)
		if _, _, err := v.Query("en", key, values); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkIncrementalLarge(b *testing.B) {
	dir := b.TempDir()
	const N = 100_000
	entries := map[string]string{}
	for i := 0; i < N; i++ {
		entries[fmt.Sprintf("key_%06d", i)] = benchMsg(i)
	}
	writeLocaleBench(b, dir, "en", entries)
	cfg := testConfig(dir)
	v, _, err := Build(context.Background(), cfg)
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Modify one key.
		entries["key_000000"] = "Hello {name} v" + strconv.Itoa(i) + ", {count, plural, one {# item} other {# items}}"
		writeLocaleBench(b, dir, "en", entries)
		v, _, err = Rebuild(context.Background(), v, cfg)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func writeLocaleBench(tb testing.TB, dir, locale string, entries map[string]string) {
	tb.Helper()
	path := filepath.Join(dir, locale+".json")
	f, err := os.Create(path)
	if err != nil {
		tb.Fatal(err)
	}
	defer f.Close()
	fmt.Fprintln(f, "{")
	first := true
	for k, v := range entries {
		if !first {
			fmt.Fprintln(f, ",")
		}
		first = false
		fmt.Fprintf(f, "  %q: %q", k, v)
	}
	fmt.Fprintln(f, "\n}")
}
