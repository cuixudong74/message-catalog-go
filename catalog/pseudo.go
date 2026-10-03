package catalog

import "strings"

// Pseudo transforms a parsed message into its pseudo-localized form.
//
// Rules:
//   - literal text (including quoted/escaped text) is preserved structurally;
//     unquoted translatable text is accented, expanded and wrapped with
//     markers so truncation and hard-coded-string issues become visible;
//   - placeholders, plural/select/gender syntax, branch keys and offsets are
//     copied verbatim and never deformed or swallowed;
//   - the returned AST can be rendered by the exact same Render function.
func Pseudo(nodes []Node) []Node {
	out := make([]Node, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, pseudoNode(n))
	}
	return out
}

func pseudoNode(n Node) Node {
	switch t := n.(type) {
	case *Text:
		if t.Quoted {
			// Escaped literal structure: keep verbatim.
			return &Text{Value: t.Value, Quoted: true}
		}
		return &Text{Value: pseudoText(t.Value)}
	case *Placeholder:
		return &Placeholder{Name: t.Name}
	case *Plural:
		b := make([]Branch, len(t.Branches))
		for i, br := range t.Branches {
			b[i] = Branch{Key: br.Key, Exact: br.Exact, Value: Pseudo(br.Value)}
		}
		return &Plural{Name: t.Name, Offset: t.Offset, Branches: b}
	case *Select:
		b := make([]Branch, len(t.Branches))
		for i, br := range t.Branches {
			b[i] = Branch{Key: br.Key, Exact: br.Exact, Value: Pseudo(br.Value)}
		}
		return &Select{Name: t.Name, Branches: b}
	case *Gender:
		b := make([]Branch, len(t.Branches))
		for i, br := range t.Branches {
			b[i] = Branch{Key: br.Key, Exact: br.Exact, Value: Pseudo(br.Value)}
		}
		return &Gender{Name: t.Name, Branches: b}
	}
	return n
}

// pseudoText accents ASCII letters, expands the text length and wraps it with
// visible markers.
func pseudoText(s string) string {
	var sb strings.Builder
	sb.Grow(len(s) + len(s)/3 + 8)
	for _, r := range s {
		if r >= 0 && r <= 127 {
			if repl, ok := pseudoAccent[byte(r)]; ok {
				sb.WriteString(repl)
				continue
			}
		}
		sb.WriteRune(r)
	}
	expanded := sb.String()
	pad := len(s) / 3
	if pad < 2 {
		pad = 2
	}
	return "[!! " + expanded + strings.Repeat("ž", pad) + " !!]"
}

var pseudoAccent = map[byte]string{
	'a': "à", 'b': "þ", 'c': "ç", 'd': "ð", 'e': "é", 'f': "ƒ", 'g': "ĝ",
	'h': "ĥ", 'i': "î", 'j': "ĵ", 'k': "ķ", 'l': "ĺ", 'm': "ɱ", 'n': "ñ",
	'o': "ö", 'p': "ƥ", 'q': "ǫ", 'r': "ŕ", 's': "š", 't': "ţ", 'u': "ü",
	'v': "ṽ", 'w': "ŵ", 'x': "ẋ", 'y': "ý", 'z': "ž",
	'A': "Å", 'B': "Þ", 'C': "Ç", 'D': "Ð", 'E': "É", 'F': "Ƒ", 'G': "Ĝ",
	'H': "Ĥ", 'I': "Î", 'J': "Ĵ", 'K': "Ķ", 'L': "Ĺ", 'M': "Ɱ", 'N': "Ñ",
	'O': "Ö", 'P': "Þ", 'Q': "Ǫ", 'R': "Ŕ", 'S': "Š", 'T': "Ţ", 'U': "Ü",
	'V': "Ṽ", 'W': "Ŵ", 'X': "Ẋ", 'Y': "Ý", 'Z': "Ž",
}
