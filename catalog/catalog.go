package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// ErrNotFound 表示键在整条回退链上都不存在。
var ErrNotFound = errors.New("catalog: key not found in fallback chain")

// ErrLimitExceeded 表示目录超出配置的规模上限，本次加载被整体拒绝。
var ErrLimitExceeded = errors.New("catalog: limit exceeded")

// Snapshot 一个不可变的完整目录版本，查询只读，不加锁。
type Snapshot struct {
	Version uint64
	Msgs    map[string]map[string]*Message // lang -> key -> message
	Issues  []Issue
}

// fileState 记录单个目录文件的指纹与解析结果，供增量重建复用。
type fileState struct {
	hash string
	lang string
	msgs map[string]*Message // 解析成功的条目
}

// Catalog 并发安全的消息目录。
// 查询读 atomic 指针指向的完整 Snapshot，重建在副本上进行后整体切换，
// 因此任一时刻查询到的都是某个已提交的完整版本，慢查询不阻塞重建。
type Catalog struct {
	cfg     atomic.Pointer[Config] // 配置原子更新，只影响后续处理
	cur     atomic.Pointer[Snapshot]
	mu      sync.Mutex // 串行化 Load/Rebuild/Save
	version uint64
	files   map[string]fileState // 文件路径 -> 状态
}

func New(cfg Config) *Catalog {
	if cfg.Fallback == nil {
		cfg.Fallback = map[string][]string{}
	}
	c := &Catalog{files: map[string]fileState{}}
	c.SetConfig(cfg)
	c.cur.Store(&Snapshot{Msgs: map[string]map[string]*Message{}})
	return c
}

// Config 返回当前配置（配置变更只影响后续 Load/Rebuild）。
func (c *Catalog) Config() Config { return *c.cfg.Load() }

// SetConfig 更新配置，只影响后续处理，不改写已提交的版本。
func (c *Catalog) SetConfig(cfg Config) {
	if cfg.Fallback == nil {
		cfg.Fallback = map[string][]string{}
	}
	c.cfg.Store(&cfg)
}

// Current 返回当前已提交的版本。
func (c *Catalog) Current() *Snapshot { return c.cur.Load() }

// Load 从本地目录全量加载并原子提交新版本。
// 目录布局：dir/<lang>.json，内容为 {"key": "message"}。
// 单条解析/校验错误记录为 Issue，不让整批失败；
// 超出 MaxEntries 上限则整批拒绝，旧版本保持不变。
func (c *Catalog) Load(dir string) ([]Issue, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	snap, files, err := c.buildLocked(dir, nil)
	if err != nil {
		return nil, err
	}
	c.files = files
	c.cur.Store(snap)
	return snap.Issues, nil
}

// Rebuild 增量重建：只重新解析新增/修改/删除的文件，其余复用上次结果，
// 产出与全量 Load 完全一致的版本，并报告受影响的语言与条目。
func (c *Catalog) Rebuild(dir string) (Affected, []Issue, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	snap, files, err := c.buildLocked(dir, c.files)
	if err != nil {
		return Affected{}, nil, err
	}
	affected := diffAffected(c.files, files)
	c.files = files
	c.cur.Store(snap)
	return affected, snap.Issues, nil
}

// buildLocked 扫描目录构建新版本。prev 非 nil 时复用未变化文件的解析结果。
func (c *Catalog) buildLocked(dir string, prev map[string]fileState) (*Snapshot, map[string]fileState, error) {
	cfg := *c.cfg.Load()
	paths, err := listMessageFiles(dir)
	if err != nil {
		return nil, nil, err
	}
	files := make(map[string]fileState, len(paths))
	var issues []Issue
	total := 0
	for _, p := range paths {
		data, hash, err := readHashed(p)
		if err != nil {
			return nil, nil, err
		}
		lang := strings.TrimSuffix(filepath.Base(p), ".json")
		if old, ok := prev[p]; ok && old.hash == hash {
			files[p] = old // 未变化，直接复用
			total += len(old.msgs)
			continue
		}
		if cfg.MaxFileBytes > 0 && int64(len(data)) > cfg.MaxFileBytes {
			issues = append(issues, Issue{Lang: lang, Key: "*",
				Reason: ReasonLimitExceeded,
				Detail: fmt.Sprintf("file %s exceeds MaxFileBytes %d, skipped", p, cfg.MaxFileBytes)})
			files[p] = fileState{hash: hash, lang: lang, msgs: map[string]*Message{}}
			continue
		}
		msgs, fileIssues := parseFile(lang, data)
		issues = append(issues, fileIssues...)
		files[p] = fileState{hash: hash, lang: lang, msgs: msgs}
		total += len(msgs)
	}
	if cfg.MaxEntries > 0 && total > cfg.MaxEntries {
		return nil, nil, fmt.Errorf("%w: %d entries > MaxEntries %d", ErrLimitExceeded, total, cfg.MaxEntries)
	}

	// 合并各语言
	msgs := map[string]map[string]*Message{}
	for _, f := range files {
		if msgs[f.lang] == nil {
			msgs[f.lang] = map[string]*Message{}
		}
		for k, m := range f.msgs {
			msgs[f.lang][k] = m
		}
	}
	// 伪本地化产物：为每种语言生成 <lang>-pseudo
	if cfg.PseudoEnabled {
		for lang, m := range msgs {
			pm := make(map[string]*Message, len(m))
			for k, msg := range m {
				p := Pseudo(msg)
				p.Lang = lang + "-pseudo"
				pm[k] = p
			}
			msgs[lang+"-pseudo"] = pm
		}
	}
	issues = append(issues, Validate(msgs, cfg.BaseLocale)...)

	c.version++
	return &Snapshot{Version: c.version, Msgs: msgs, Issues: issues}, files, nil
}

// diffAffected 对比前后文件状态，给出受影响的语言与条目。
func diffAffected(prev, next map[string]fileState) Affected {
	langSet := map[string]bool{}
	keySet := map[string]bool{}
	mark := func(f fileState) {
		langSet[f.lang] = true
		for k := range f.msgs {
			keySet[k] = true
		}
	}
	for p, n := range next {
		old, ok := prev[p]
		if !ok || old.hash != n.hash {
			mark(n)
			if ok {
				mark(old) // 修改：旧条目（可能含被删键）也受影响
			}
		}
	}
	for p, old := range prev {
		if _, ok := next[p]; !ok {
			mark(old) // 删除
		}
	}
	return Affected{Langs: sortedKeys(langSet), Keys: sortedKeys(keySet)}
}

// Resolve 按回退链解析某键。命中记录唯一且可解释：
// 链为 [lang] + Fallback[lang]，第一个命中即返回，Resolution 记录完整路径。
func (c *Catalog) Resolve(lang, key string) (*Message, Resolution, error) {
	return c.View().Resolve(lang, key)
}

func (s *Snapshot) resolve(cfg Config, lang, key string) (*Message, Resolution, error) {
	res := Resolution{Key: key, WantLang: lang}
	chain := append([]string{lang}, cfg.Fallback[lang]...)
	seen := map[string]bool{}
	for _, l := range chain {
		if seen[l] { // 回退链有环时去重，保证行为确定
			continue
		}
		seen[l] = true
		res.Chain = append(res.Chain, l)
		if m, ok := s.Msgs[l][key]; ok {
			res.ActualLang = l
			res.Found = true
			return m, res, nil
		}
	}
	return nil, res, fmt.Errorf("%w: %s (chain %s)", ErrNotFound, key, strings.Join(res.Chain, " -> "))
}

// Render 解析并渲染，返回结果与命中记录。
func (c *Catalog) Render(lang, key string, args map[string]any) (string, Resolution, error) {
	m, res, err := c.Resolve(lang, key)
	if err != nil {
		return "", res, err
	}
	out, err := Render(m, args)
	return out, res, err
}

// --- 文件读取 ---

func listMessageFiles(dir string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != dir && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".json") {
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(paths) // 固定顺序，保证可复现
	return paths, nil
}

func readHashed(p string) ([]byte, string, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(data)
	return data, hex.EncodeToString(sum[:]), nil
}

// parseFile 解析一个目录文件；单条错误记录为 Issue 并跳过该条目。
func parseFile(lang string, data []byte) (map[string]*Message, []Issue) {
	var raw map[string]string
	if err := json.Unmarshal(data, &raw); err != nil {
		return map[string]*Message{}, []Issue{{Lang: lang, Key: "*",
			Reason: ReasonParseError, Detail: "invalid JSON: " + err.Error()}}
	}
	msgs := make(map[string]*Message, len(raw))
	var issues []Issue
	for _, k := range sortedKeys(raw) {
		m, err := Parse(lang, k, raw[k])
		if err != nil {
			issues = append(issues, Issue{Lang: lang, Key: k,
				Reason: ReasonParseError, Detail: err.Error()})
			continue
		}
		msgs[k] = m
	}
	return msgs, issues
}

// View 是某个已提交版本的只读视图：
// 同一 View 上的多次查询保证来自同一个完整版本，
// 即使期间发生了重建也不会看到混合状态。
type View struct {
	snap *Snapshot
	cfg  Config
}

// View 获取当前已提交版本的视图。
func (c *Catalog) View() View {
	return View{snap: c.cur.Load(), cfg: *c.cfg.Load()}
}

// Version 该视图对应的版本号。
func (v View) Version() uint64 { return v.snap.Version }

// Resolve 在视图版本内按回退链解析。
func (v View) Resolve(lang, key string) (*Message, Resolution, error) {
	return v.snap.resolve(v.cfg, lang, key)
}

// Render 在视图版本内解析并渲染。
func (v View) Render(lang, key string, args map[string]any) (string, Resolution, error) {
	m, res, err := v.Resolve(lang, key)
	if err != nil {
		return "", res, err
	}
	out, err := Render(m, args)
	return out, res, err
}
