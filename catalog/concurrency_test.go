package catalog

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// 并发查询与重建：任一 View 内的多键结果必须来自同一个完整版本，
// 慢查询（持有旧视图）不得阻塞重建推进。
func TestConcurrentQueryAndRebuild(t *testing.T) {
	dir := t.TempDir()
	writeLang(t, dir, "en", map[string]string{"a": "a-0", "b": "b-0"})
	c := New(testConfig())
	if _, err := c.Load(dir); err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// 慢查询：持有视图并检查多键一致性
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				v := c.View()
				ma, _, err := v.Resolve("en", "a")
				if err != nil {
					t.Error(err)
					return
				}
				time.Sleep(time.Millisecond) // 模拟慢查询，期间重建可能已推进
				mb, _, err := v.Resolve("en", "b")
				if err != nil {
					t.Error(err)
					return
				}
				// 同一视图内 a/b 必须来自同一版本：后缀编号一致
				sa, sb := ma.Raw[len(ma.Raw)-1], mb.Raw[len(mb.Raw)-1]
				if sa != sb {
					t.Errorf("mixed state in one view: a=%q b=%q", ma.Raw, mb.Raw)
					return
				}
			}
		}()
	}

	// 连续重建 50 个版本
	for i := 1; i <= 50; i++ {
		writeLang(t, dir, "en", map[string]string{
			"a": fmt.Sprintf("a-%d", i), "b": fmt.Sprintf("b-%d", i),
		})
		if _, _, err := c.Rebuild(dir); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()

	// 重建全部推进完成，最终版本可见
	m, _, err := c.Resolve("en", "a")
	if err != nil || m.Raw != "a-50" {
		t.Fatalf("rebuilds must not be blocked by slow queries, got %v %v", m, err)
	}
}

// 重建期间单次查询（非 View）也必须命中某个完整版本，不得报错或读到半更新状态。
func TestConcurrentSingleQueries(t *testing.T) {
	dir := t.TempDir()
	writeLang(t, dir, "en", map[string]string{"k": "v0"})
	writeLang(t, dir, "fr", map[string]string{"k": "f0"})
	c := New(testConfig())
	if _, err := c.Load(dir); err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			lang := "en"
			if i%2 == 1 {
				lang = "fr"
			}
			for {
				select {
				case <-stop:
					return
				default:
				}
				out, res, err := c.Render(lang, "k", nil)
				if err != nil {
					t.Error(err)
					return
				}
				if res.ActualLang != lang || out == "" {
					t.Errorf("bad result: %q %+v", out, res)
					return
				}
			}
		}(i)
	}
	for i := 1; i <= 30; i++ {
		writeLang(t, dir, "en", map[string]string{"k": fmt.Sprintf("v%d", i)})
		writeLang(t, dir, "fr", map[string]string{"k": fmt.Sprintf("f%d", i)})
		if _, _, err := c.Rebuild(dir); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
}
