package catalog

import (
	"fmt"
	"strconv"
	"strings"
)

// Render 用 args 渲染消息。缺失的占位符参数按原样输出 {name}，
// 保证占位符不被吞掉；复数按消息所属语言的规则选择分支。
func Render(m *Message, args map[string]any) (string, error) {
	var b strings.Builder
	if err := renderNodes(&b, m.Lang, m.Nodes, args); err != nil {
		return "", fmt.Errorf("%s:%s: %w", m.Lang, m.Key, err)
	}
	return b.String(), nil
}

func renderNodes(b *strings.Builder, lang string, nodes []Node, args map[string]any) error {
	for _, n := range nodes {
		switch t := n.(type) {
		case Text:
			b.WriteString(t.Value)
		case Arg:
			v, ok := args[t.Name]
			if !ok {
				b.WriteString("{" + t.Name + "}")
				continue
			}
			b.WriteString(formatValue(t.Type, v))
		case Plural:
			num, ok := toNumber(args[t.Arg])
			if !ok {
				b.WriteString("{" + t.Arg + "}")
				continue
			}
			body := pickPlural(lang, t, num)
			if err := renderPluralBody(b, lang, body, num-float64(t.Offset), args); err != nil {
				return err
			}
		case Select:
			v, _ := args[t.Arg].(string)
			body, ok := t.Variants[v]
			if !ok {
				body, ok = t.Variants["other"]
				if !ok {
					return fmt.Errorf("select %q has no matching or other branch", t.Arg)
				}
			}
			if err := renderNodes(b, lang, body, args); err != nil {
				return err
			}
		}
	}
	return nil
}

func pickPlural(lang string, p Plural, n float64) []Node {
	if n == float64(int64(n)) {
		if body, ok := p.Exacts[int(n)]; ok {
			return body
		}
	}
	cat := PluralCategory(lang, n-float64(p.Offset))
	if body, ok := p.Variants[cat]; ok {
		return body
	}
	return p.Variants["other"] // 校验阶段已保证 other 存在
}

// renderPluralBody 渲染复数分支体，# 替换为 offset 后的数值。
func renderPluralBody(b *strings.Builder, lang string, nodes []Node, hash float64, args map[string]any) error {
	for _, n := range nodes {
		if txt, ok := n.(Text); ok {
			b.WriteString(strings.ReplaceAll(txt.Value, "#", formatNumber(hash)))
			continue
		}
		if err := renderNodes(b, lang, []Node{n}, args); err != nil {
			return err
		}
	}
	return nil
}

func formatValue(typ string, v any) string {
	switch typ {
	case "number":
		if f, ok := toNumber(v); ok {
			return formatNumber(f)
		}
	}
	return fmt.Sprint(v)
}

func formatNumber(f float64) string {
	if f == float64(int64(f)) {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func toNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, true
	case string:
		f, err := strconv.ParseFloat(n, 64)
		return f, err == nil
	}
	return 0, false
}
