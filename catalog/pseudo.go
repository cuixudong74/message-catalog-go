package catalog

import "strings"

// 伪本地化规则（确定性的）：
//  1. 可翻译文本中的元音字母扩张为两个带重音的字符（约 +40% 长度）；
//  2. 整条消息用 ⟦ ⟧ 包裹作为标记；
//  3. 占位符、复数/选择分支结构、转义字符（解析后已还原）一律不变；
//     复数分支体中的 # 原样保留。
var vowelMap = map[rune]string{
	'a': "áà", 'e': "éè", 'i': "íì", 'o': "óò", 'u': "úù",
	'A': "ÁÀ", 'E': "ÉÈ", 'I': "ÍÌ", 'O': "ÓÒ", 'U': "ÚÙ",
}

// Pseudo 对消息做伪本地化，返回新消息（原消息不变）。
func Pseudo(m *Message) *Message {
	nodes := pseudoNodes(m.Nodes)
	out := make([]Node, 0, len(nodes)+2)
	out = append(out, Text{Value: "⟦"})
	out = append(out, nodes...)
	out = append(out, Text{Value: "⟧"})
	return &Message{Key: m.Key, Lang: m.Lang, Raw: m.Raw, Nodes: out}
}

func pseudoNodes(nodes []Node) []Node {
	out := make([]Node, 0, len(nodes))
	for _, n := range nodes {
		switch t := n.(type) {
		case Text:
			out = append(out, Text{Value: pseudoText(t.Value)})
		case Plural:
			p := Plural{Arg: t.Arg, Offset: t.Offset,
				Exacts: map[int][]Node{}, Variants: map[string][]Node{}}
			for k, body := range t.Exacts {
				p.Exacts[k] = pseudoNodes(body)
			}
			for k, body := range t.Variants {
				p.Variants[k] = pseudoNodes(body)
			}
			out = append(out, p)
		case Select:
			s := Select{Arg: t.Arg, Variants: map[string][]Node{}}
			for k, body := range t.Variants {
				s.Variants[k] = pseudoNodes(body)
			}
			out = append(out, s)
		default:
			out = append(out, n) // Arg 原样保留
		}
	}
	return out
}

func pseudoText(s string) string {
	var b strings.Builder
	b.Grow(len(s) * 2)
	for _, r := range s {
		if rep, ok := vowelMap[r]; ok {
			b.WriteString(rep)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
