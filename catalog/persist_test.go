package catalog

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func loadAndSave(t *testing.T, c *Catalog, msgDir, stateDir string) {
	t.Helper()
	if _, err := c.Load(msgDir); err != nil {
		t.Fatal(err)
	}
	if err := c.Save(stateDir); err != nil {
		t.Fatal(err)
	}
}

func TestRestartRecovery(t *testing.T) {
	msgDir := t.TempDir()
	stateDir := t.TempDir()
	writeLang(t, msgDir, "en", map[string]string{"a": "v1"})

	c1 := New(testConfig())
	loadAndSave(t, c1, msgDir, stateDir)

	// 模拟重启：新 Catalog 从落盘状态恢复
	c2 := New(testConfig())
	ok, err := c2.Restore(stateDir)
	if err != nil || !ok {
		t.Fatalf("restore: ok=%v err=%v", ok, err)
	}
	m, _, err := c2.Resolve("en", "a")
	if err != nil || m.Raw != "v1" {
		t.Fatalf("recovered value wrong: %v", err)
	}

	// 更新后再次落盘，重启应看到新版本
	writeLang(t, msgDir, "en", map[string]string{"a": "v2"})
	loadAndSave(t, c2, msgDir, stateDir)
	c3 := New(testConfig())
	if _, err := c3.Restore(stateDir); err != nil {
		t.Fatal(err)
	}
	m, _, err = c3.Resolve("en", "a")
	if err != nil || m.Raw != "v2" {
		t.Fatalf("expected v2 after restart, got %v (%v)", m, err)
	}
}

func TestCorruptedCurrentSlotFallsBack(t *testing.T) {
	msgDir := t.TempDir()
	stateDir := t.TempDir()
	writeLang(t, msgDir, "en", map[string]string{"a": "v1"})
	c := New(testConfig())
	loadAndSave(t, c, msgDir, stateDir) // slot-a = v1, CURRENT=a

	writeLang(t, msgDir, "en", map[string]string{"a": "v2"})
	loadAndSave(t, c, msgDir, stateDir) // slot-b = v2, CURRENT=b

	// 模拟落盘中断：CURRENT 已指向 b，但 b 内容损坏（写了一半）
	if err := os.WriteFile(filepath.Join(stateDir, "slot-b.json"), []byte(`{"version": 2, "langs": {"en": {"a":`), 0o644); err != nil {
		t.Fatal(err)
	}
	snap, err := LoadSnapshot(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if snap == nil {
		t.Fatal("must fall back to previous complete version")
	}
	m := snap.Msgs["en"]["a"]
	if m == nil || m.Raw != "v1" {
		t.Fatalf("expected complete v1, got %+v", m)
	}
}

func TestInterruptedSaveLeavesNoPartialState(t *testing.T) {
	msgDir := t.TempDir()
	stateDir := t.TempDir()
	writeLang(t, msgDir, "en", map[string]string{"a": "v1"})
	c := New(testConfig())
	loadAndSave(t, c, msgDir, stateDir)

	// 模拟中断残留的临时文件
	if err := os.WriteFile(filepath.Join(stateDir, "slot-b.json.tmp"), []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	snap, err := LoadSnapshot(stateDir)
	if err != nil || snap == nil {
		t.Fatalf("tmp residue must not affect recovery: %v", err)
	}
	if got := snap.Msgs["en"]["a"].Raw; got != "v1" {
		t.Fatalf("got %q", got)
	}
}

func TestNoHistoryIsDeterministic(t *testing.T) {
	stateDir := t.TempDir()
	snap, err := LoadSnapshot(stateDir)
	if err != nil || snap != nil {
		t.Fatalf("empty dir must yield (nil, nil), got %v %v", snap, err)
	}
	c := New(testConfig())
	ok, err := c.Restore(stateDir)
	if ok || err != nil {
		t.Fatalf("restore with no history: ok=%v err=%v", ok, err)
	}
	if _, _, err := c.Resolve("en", "a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("catalog must stay empty, got %v", err)
	}
}
