package catalog

import "testing"

// skeleton renders an AST into a structure-only representation that ignores
// text content.
func skeleton(nodes []Node) string {
	var walk func(ns []Node, b *[]byte)
	walk = func(ns []Node, b *[]byte) {
		for _, n := range ns {
			switch t := n.(type) {
			case *Text:
				if t.Quoted {
					*b = append(*b, 'Q')
				} else {
					*b = append(*b, 'T')
				}
			case *Placeholder:
				*b = append(*b, '{')
				*b = append(*b, t.Name...)
				*b = append(*b, '}')
			case *Plural:
				*b = append(*b, "P:"...)
				*b = append(*b, t.Name...)
				*b = append(*b, ';')
				for _, br := range t.Branches {
					*b = append(*b, br.Key...)
					*b = append(*b, '=')
					walk(br.Value, b)
					*b = append(*b, ',')
				}
			case *Select:
				*b = append(*b, "S:"...)
				*b = append(*b, t.Name...)
				*b = append(*b, ';')
				for _, br := range t.Branches {
					*b = append(*b, br.Key...)
					*b = append(*b, '=')
					walk(br.Value, b)
					*b = append(*b, ',')
				}
			case *Gender:
				*b = append(*b, "G:"...)
				*b = append(*b, t.Name...)
				*b = append(*b, ';')
				for _, br := range t.Branches {
					*b = append(*b, br.Key...)
					*b = append(*b, '=')
					walk(br.Value, b)
					*b = append(*b, ',')
				}
			}
		}
	}
	var b []byte
	walk(nodes, &b)
	return string(b)
}

func TestPseudoStructurePreserved(t *testing.T) {
	raw := "Hello {name}! {n, plural, one {# item '{x}'} other {# items}} {g, select, male {he} other {they}}"
	nodes := mustParse(t, raw)
	ps := Pseudo(nodes)
	if got, want := skeleton(ps), skeleton(nodes); got != want {
		t.Errorf("skeleton changed:\n got %s\nwant %s", got, want)
	}
}

func TestPseudoRenderingConsistent(t *testing.T) {
	raw := "Hello {name}, you have {count, plural, one {# message} other {# messages}}"
	nodes := mustParse(t, raw)
	ps := Pseudo(nodes)
	out, err := Render(ps, "en", map[string]any{"name": "Alice", "count": 3})
	if err != nil {
		t.Fatal(err)
	}
	// Placeholders must survive: name substituted, # rendered, no braces left.
	if !strContains(out, "Alice") {
		t.Errorf("rendered output missing substituted name: %q", out)
	}
	if !strContains(out, "3") {
		t.Errorf("rendered output missing plural number: %q", out)
	}
	if strContainsAny(out, "{", "}") {
		t.Errorf("rendered output contains leftover braces: %q", out)
	}
	if !strContains(out, "[!!") || !strContains(out, "!!]") {
		t.Errorf("rendered output missing pseudo markers: %q", out)
	}
}

func TestPseudoEscapedBraces(t *testing.T) {
	raw := "Use '{name}' literally"
	nodes := mustParse(t, raw)
	ps := Pseudo(nodes)
	out, err := Render(ps, "en", map[string]any{"name": "Bob"})
	if err != nil {
		t.Fatal(err)
	}
	if !strContains(out, "{name}") {
		t.Errorf("escaped placeholder was deformed: %q", out)
	}
}

func strContains(s, sub string) bool {
	return len(s) >= len(sub) && (indexOf(s, sub) >= 0)
}

func strContainsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strContains(s, sub) {
			return true
		}
	}
	return false
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
