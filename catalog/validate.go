package catalog

import (
	"fmt"
	"sort"
	"strings"
)

// Validate 以 base 语言为参照校验所有语言的消息。
// 占位符名称/数量、复数与选择分支类别不一致的条目判为错误；
// 单条错误不影响其他条目。某语言缺失的键不算错误（由回退链处理）。
func Validate(msgs map[string]map[string]*Message, base string) []Issue {
	var issues []Issue
	baseMsgs := msgs[base]

	// 基准语言自身的结构问题（缺 other 分支等）
	for _, key := range sortedKeys(baseMsgs) {
		issues = append(issues, checkStructure(baseMsgs[key])...)
	}

	for lang, m := range msgs {
		if lang == base {
			continue
		}
		for _, key := range sortedKeys(m) {
			b := baseMsgs[key]
			if b == nil {
				continue // 基准没有该键，无法比对，跳过
			}
			issues = append(issues, checkStructure(m[key])...)
			issues = append(issues, compareWithBase(b, m[key])...)
		}
	}
	return issues
}

// signature 提取一条消息的占位符与分支签名。
type signature struct {
	args         map[string]int      // 占位符名 → 出现次数
	pluralCats   map[string][]string // 复数参数名 → 排序后的类别集（含 =N）
	selectCats   map[string][]string
	structIssues []Issue
}

func sigOf(m *Message) signature {
	s := signature{
		args:       map[string]int{},
		pluralCats: map[string][]string{},
		selectCats: map[string][]string{},
	}
	var walk func(nodes []Node)
	walk = func(nodes []Node) {
		for _, n := range nodes {
			switch t := n.(type) {
			case Arg:
				s.args[t.Name]++
			case Plural:
				s.args[t.Arg]++
				cats := make([]string, 0, len(t.Variants)+len(t.Exacts))
				for k := range t.Variants {
					cats = append(cats, k)
				}
				for k := range t.Exacts {
					cats = append(cats, fmt.Sprintf("=%d", k))
				}
				sort.Strings(cats)
				s.pluralCats[t.Arg] = cats
				if _, ok := t.Variants["other"]; !ok {
					s.structIssues = append(s.structIssues, Issue{
						Lang: m.Lang, Key: m.Key, Reason: ReasonMissingOther,
						Detail: fmt.Sprintf("plural %q missing required other branch", t.Arg),
					})
				}
				for _, body := range t.Variants {
					walk(body)
				}
				for _, body := range t.Exacts {
					walk(body)
				}
			case Select:
				s.args[t.Arg]++
				cats := make([]string, 0, len(t.Variants))
				for k := range t.Variants {
					cats = append(cats, k)
				}
				sort.Strings(cats)
				s.selectCats[t.Arg] = cats
				if _, ok := t.Variants["other"]; !ok {
					s.structIssues = append(s.structIssues, Issue{
						Lang: m.Lang, Key: m.Key, Reason: ReasonMissingOther,
						Detail: fmt.Sprintf("select %q missing required other branch", t.Arg),
					})
				}
				for _, body := range t.Variants {
					walk(body)
				}
			}
		}
	}
	walk(m.Nodes)
	return s
}

func checkStructure(m *Message) []Issue {
	return sigOf(m).structIssues
}

func compareWithBase(base, m *Message) []Issue {
	var issues []Issue
	bs, ms := sigOf(base), sigOf(m)

	// 占位符名称与数量
	for name, bc := range bs.args {
		mc, ok := ms.args[name]
		if !ok {
			issues = append(issues, Issue{Lang: m.Lang, Key: m.Key,
				Reason: ReasonPlaceholderName,
				Detail: fmt.Sprintf("missing placeholder {%s} (base has %d)", name, bc)})
		} else if mc != bc {
			issues = append(issues, Issue{Lang: m.Lang, Key: m.Key,
				Reason: ReasonPlaceholderCount,
				Detail: fmt.Sprintf("placeholder {%s} count %d, base %d", name, mc, bc)})
		}
	}
	for name := range ms.args {
		if _, ok := bs.args[name]; !ok {
			issues = append(issues, Issue{Lang: m.Lang, Key: m.Key,
				Reason: ReasonPlaceholderName,
				Detail: fmt.Sprintf("unexpected placeholder {%s} not in base", name)})
		}
	}

	// 复数分支类别
	for arg, bcats := range bs.pluralCats {
		mcats, ok := ms.pluralCats[arg]
		if !ok {
			continue // 已由占位符名称检查报告
		}
		if strings.Join(bcats, ",") != strings.Join(mcats, ",") {
			issues = append(issues, Issue{Lang: m.Lang, Key: m.Key,
				Reason: ReasonPluralCategory,
				Detail: fmt.Sprintf("plural {%s} categories [%s], base [%s]",
					arg, strings.Join(mcats, ","), strings.Join(bcats, ","))})
		}
	}

	// 选择分支类别
	for arg, bcats := range bs.selectCats {
		mcats, ok := ms.selectCats[arg]
		if !ok {
			continue
		}
		if strings.Join(bcats, ",") != strings.Join(mcats, ",") {
			issues = append(issues, Issue{Lang: m.Lang, Key: m.Key,
				Reason: ReasonSelectCategory,
				Detail: fmt.Sprintf("select {%s} categories [%s], base [%s]",
					arg, strings.Join(mcats, ","), strings.Join(bcats, ","))})
		}
	}
	return issues
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
