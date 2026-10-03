package catalog

import "testing"

func mustParse(t *testing.T, raw string) *Message {
	t.Helper()
	m, err := Parse("en", "k", raw)
	if err != nil {
		t.Fatalf("Parse(%q): %v", raw, err)
	}
	return m
}

func render(t *testing.T, raw string, args map[string]any) string {
	t.Helper()
	out, err := Render(mustParse(t, raw), args)
	if err != nil {
		t.Fatalf("Render(%q): %v", raw, err)
	}
	return out
}

func TestRenderPlainAndArg(t *testing.T) {
	got := render(t, "Hello, {name}!", map[string]any{"name": "World"})
	if got != "Hello, World!" {
		t.Fatalf("got %q", got)
	}
}

func TestRenderMissingArgKept(t *testing.T) {
	got := render(t, "Hi {name}", nil)
	if got != "Hi {name}" {
		t.Fatalf("missing arg must not be swallowed, got %q", got)
	}
}

func TestRenderEscapes(t *testing.T) {
	got := render(t, "it''s '{ok'} '#", nil)
	if got != "it's {ok} #" {
		t.Fatalf("got %q", got)
	}
}

func TestRenderPlural(t *testing.T) {
	raw := "{n, plural, =0{no items} one{one item} other{# items}}"
	if got := render(t, raw, map[string]any{"n": 0}); got != "no items" {
		t.Fatalf("=0: got %q", got)
	}
	if got := render(t, raw, map[string]any{"n": 1}); got != "one item" {
		t.Fatalf("one: got %q", got)
	}
	if got := render(t, raw, map[string]any{"n": 5}); got != "5 items" {
		t.Fatalf("other: got %q", got)
	}
}

func TestRenderPluralOffset(t *testing.T) {
	raw := "{n, plural, offset:1 one{you and one other} other{you and # others}}"
	if got := render(t, raw, map[string]any{"n": 2}); got != "you and one other" {
		t.Fatalf("got %q", got)
	}
	if got := render(t, raw, map[string]any{"n": 5}); got != "you and 4 others" {
		t.Fatalf("got %q", got)
	}
}

func TestRenderSelectGender(t *testing.T) {
	raw := "{g, select, male{He} female{She} other{They}} liked {item}"
	if got := render(t, raw, map[string]any{"g": "female", "item": "it"}); got != "She liked it" {
		t.Fatalf("got %q", got)
	}
	if got := render(t, raw, map[string]any{"g": "unknown", "item": "x"}); got != "They liked x" {
		t.Fatalf("fallback to other: got %q", got)
	}
}

func TestRenderNested(t *testing.T) {
	raw := "{g, select, male{{n, plural, one{He has one} other{He has #}}} other{She has {n}}}"
	got := render(t, raw, map[string]any{"g": "male", "n": 3})
	if got != "He has 3" {
		t.Fatalf("got %q", got)
	}
}

func TestParseErrors(t *testing.T) {
	for _, bad := range []string{"{", "{name", "{n, plural, one{x}", "a } b", "{n, bogus, x}"} {
		if _, err := Parse("en", "k", bad); err == nil {
			t.Fatalf("expected parse error for %q", bad)
		}
	}
}

func TestPluralCategory(t *testing.T) {
	cases := []struct {
		lang string
		n    float64
		want string
	}{
		{"en", 1, "one"}, {"en", 2, "other"}, {"en", 0, "other"},
		{"ja", 1, "other"}, {"zh-CN", 1, "other"},
		{"fr", 0, "one"}, {"fr", 1.5, "one"}, {"fr", 2, "other"},
		{"ru", 1, "one"}, {"ru", 3, "few"}, {"ru", 5, "many"}, {"ru", 11, "many"}, {"ru", 21, "one"},
		{"ar", 0, "zero"}, {"ar", 2, "two"},
	}
	for _, c := range cases {
		if got := PluralCategory(c.lang, c.n); got != c.want {
			t.Errorf("PluralCategory(%s, %v) = %s, want %s", c.lang, c.n, got, c.want)
		}
	}
}
