package catalog

import "testing"

func mustParse(t *testing.T, raw string) []Node {
	t.Helper()
	nodes, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse(%q): %v", raw, err)
	}
	return nodes
}

func TestRenderPlaceholder(t *testing.T) {
	nodes := mustParse(t, "Hello {name}, you have {count} messages")
	out, err := Render(nodes, "en", map[string]any{"name": "Alice", "count": 3})
	if err != nil {
		t.Fatal(err)
	}
	if want := "Hello Alice, you have 3 messages"; out != want {
		t.Errorf("got %q, want %q", out, want)
	}
}

func TestRenderPlural(t *testing.T) {
	raw := "{n, plural, offset:2 =0 {free} one {# day} other {# days}}"
	nodes := mustParse(t, raw)
	cases := []struct {
		n    int
		want string
	}{
		{0, "free"},   // exact "=0" matches raw value
		{2, "0 days"}, // no "=2" branch; adjusted=0 -> other
		{3, "1 day"},  // adjusted=1 -> one, # renders adjusted
		{5, "3 days"}, // adjusted=3 -> other
	}
	for _, c := range cases {
		out, err := Render(nodes, "en", map[string]any{"n": c.n})
		if err != nil {
			t.Fatalf("n=%d: %v", c.n, err)
		}
		if out != c.want {
			t.Errorf("n=%d: got %q, want %q", c.n, out, c.want)
		}
	}
}

func TestRenderSelectGender(t *testing.T) {
	sel := mustParse(t, "{g, select, male {he} female {she} other {they}}")
	out, _ := Render(sel, "en", map[string]any{"g": "female"})
	if out != "she" {
		t.Errorf("select: got %q", out)
	}
	out, _ = Render(sel, "en", map[string]any{"g": "unknown"})
	if out != "they" {
		t.Errorf("select fallback: got %q", out)
	}
	g := mustParse(t, "{g, gender, male {他} female {她} other {他们}}")
	out, _ = Render(g, "zh", map[string]any{"g": "male"})
	if out != "他" {
		t.Errorf("gender: got %q", out)
	}
}

func TestRenderHashEscaped(t *testing.T) {
	// '#' inside quotes must stay literal.
	nodes := mustParse(t, "{n, plural, one {# item, literal '#'} other {# items}}")
	out, err := Render(nodes, "en", map[string]any{"n": 1})
	if err != nil {
		t.Fatal(err)
	}
	if want := "1 item, literal #"; out != want {
		t.Errorf("got %q, want %q", out, want)
	}
}

func TestRenderMissingValue(t *testing.T) {
	nodes := mustParse(t, "Hello {name}")
	if _, err := Render(nodes, "en", map[string]any{}); err == nil {
		t.Error("expected error for missing value")
	}
}

func TestRenderZhPlural(t *testing.T) {
	// Chinese has no plural forms: always "other".
	nodes := mustParse(t, "{n, plural, one {# 个} other {# 个}}")
	out, err := Render(nodes, "zh", map[string]any{"n": 1})
	if err != nil {
		t.Fatal(err)
	}
	if want := "1 个"; out != want {
		t.Errorf("got %q, want %q", out, want)
	}
}
