package catalog

import (
	"strconv"
	"strings"
)

// Unparse serializes a parsed AST back into an ICU message string. The result
// re-parses to the same AST (structure is preserved).
func Unparse(nodes []Node) string {
	var sb strings.Builder
	unparseNodes(&sb, nodes)
	return sb.String()
}

func unparseNodes(sb *strings.Builder, nodes []Node) {
	for _, n := range nodes {
		switch t := n.(type) {
		case *Text:
			if t.Quoted {
				sb.WriteByte('\'')
				sb.WriteString(strings.ReplaceAll(t.Value, "'", "''"))
				sb.WriteByte('\'')
			} else {
				sb.WriteString(t.Value)
			}
		case *Placeholder:
			sb.WriteByte('{')
			sb.WriteString(t.Name)
			sb.WriteByte('}')
		case *Plural:
			sb.WriteByte('{')
			sb.WriteString(t.Name)
			sb.WriteString(", plural,")
			if t.Offset != 0 {
				sb.WriteString(" offset:")
				sb.WriteString(strconv.Itoa(t.Offset))
			}
			unparseBranches(sb, t.Branches)
			sb.WriteByte('}')
		case *Select:
			sb.WriteByte('{')
			sb.WriteString(t.Name)
			sb.WriteString(", select,")
			unparseBranches(sb, t.Branches)
			sb.WriteByte('}')
		case *Gender:
			sb.WriteByte('{')
			sb.WriteString(t.Name)
			sb.WriteString(", gender,")
			unparseBranches(sb, t.Branches)
			sb.WriteByte('}')
		}
	}
}

func unparseBranches(sb *strings.Builder, branches []Branch) {
	for _, b := range branches {
		sb.WriteByte(' ')
		sb.WriteString(b.Key)
		sb.WriteByte(' ')
		sb.WriteByte('{')
		unparseNodes(sb, b.Value)
		sb.WriteByte('}')
	}
}
