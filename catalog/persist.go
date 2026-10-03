package catalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// 落盘格式：双槽位 + CURRENT 指针，保证中断后要么完整旧版本、要么完整新版本。
//
//	dir/slot-a.json   dir/slot-b.json   dir/CURRENT ("a" 或 "b")
//
// 写入顺序：先写非活动槽位（tmp + rename 原子替换），再原子更新 CURRENT。
// 在任意点中断，CURRENT 指向的槽位要么是完整旧版本，要么是完整新版本。

type diskSnapshot struct {
	Version uint64                       `json:"version"`
	Langs   map[string]map[string]string `json:"langs"` // lang -> key -> raw
}

// Save 把当前已提交版本原子落盘到 dir。
func (c *Catalog) Save(dir string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Save(dir, c.cur.Load())
}

// Save 把快照原子落盘到 dir。
func Save(dir string, s *Snapshot) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	ds := diskSnapshot{Version: s.Version, Langs: map[string]map[string]string{}}
	for lang, msgs := range s.Msgs {
		ds.Langs[lang] = make(map[string]string, len(msgs))
		for k, m := range msgs {
			ds.Langs[lang][k] = m.Raw
		}
	}
	data, err := json.Marshal(ds)
	if err != nil {
		return err
	}
	next := nextSlot(dir)
	if err := atomicWrite(filepath.Join(dir, "slot-"+next+".json"), data); err != nil {
		return err
	}
	// 槽位完整后再切换 CURRENT
	if err := atomicWrite(filepath.Join(dir, "CURRENT"), []byte(next)); err != nil {
		return err
	}
	return syncDir(dir)
}

// LoadSnapshot 从 dir 恢复上一个完整版本；CURRENT 指向的槽位损坏时
// 回退到另一个槽位；两者都不可用返回 (nil, nil) 表示无历史版本。
func LoadSnapshot(dir string) (*Snapshot, error) {
	cur, err := os.ReadFile(filepath.Join(dir, "CURRENT"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	slots := []string{string(cur)}
	if string(cur) == "a" {
		slots = append(slots, "b")
	} else {
		slots = append(slots, "a")
	}
	for _, slot := range slots {
		snap, err := readSlot(dir, slot)
		if err == nil {
			return snap, nil
		}
	}
	// CURRENT 存在但两个槽位都不可读：数据损坏，按无版本处理，行为确定
	return nil, nil
}

// Restore 从 dir 恢复版本并提交为当前版本；无历史版本时保持现状。
func (c *Catalog) Restore(dir string) (bool, error) {
	snap, err := LoadSnapshot(dir)
	if err != nil || snap == nil {
		return false, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if snap.Version > c.version {
		c.version = snap.Version
	}
	c.cur.Store(snap)
	return true, nil
}

func readSlot(dir, slot string) (*Snapshot, error) {
	data, err := os.ReadFile(filepath.Join(dir, "slot-"+slot+".json"))
	if err != nil {
		return nil, err
	}
	var ds diskSnapshot
	if err := json.Unmarshal(data, &ds); err != nil {
		return nil, err
	}
	snap := &Snapshot{Version: ds.Version, Msgs: map[string]map[string]*Message{}}
	for lang, msgs := range ds.Langs {
		snap.Msgs[lang] = make(map[string]*Message, len(msgs))
		for k, raw := range msgs {
			m, err := Parse(lang, k, raw)
			if err != nil {
				return nil, fmt.Errorf("slot %s: %w", slot, err)
			}
			snap.Msgs[lang][k] = m
		}
	}
	return snap, nil
}

func nextSlot(dir string) string {
	cur, err := os.ReadFile(filepath.Join(dir, "CURRENT"))
	if err == nil && string(cur) == "a" {
		return "b"
	}
	return "a"
}

// atomicWrite 写临时文件 + fsync + rename，保证目标文件要么旧要么新。
func atomicWrite(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
