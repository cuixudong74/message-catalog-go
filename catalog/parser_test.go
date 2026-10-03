package catalog

import "testing"

func TestParseBasic(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{"Hello {name}!", "ok"},
		{"{count, plural, one {# item} other {# items}}", "ok"},
		{"{g, select, male {He} female {She} other {They}}", "ok"},
		{"{g, gender, male {他} other {他们}}", "ok"},
		{"Use '{name}' to escape braces, '' is apostrophe", "ok"},
		{"Offset: {n, plural, offset:2 =0 {free} one {# day} other {# days}}", "ok"},
	}
	for _, c := range cases {
		nodes, err := Parse(c.raw)
		if err != nil {
			t.Errorf("Parse(%q) error: %v", c.raw, err)
			continue
		}
		if nodes == nil {
			t.Errorf("Parse(%q) returned nil nodes", c.raw)
		}
	}
}

func TestParseErrors(t *testing.T) {
	bad := []string{
		"Hello {name",          // unterminated placeholder
		"Hello }",              // stray brace
		"Hello '{name}",  // unterminated quote before syntax char
		"{name, plural, one {#} }", // missing other is validation, not parse - this should parse
		"{name, bogus, other {x}}",
		"{, plural, other {x}}",
	}
	wantErr := []bool{true, true, true, false, true, true}
	for i, raw := range bad {
		_, err := Parse(raw)
		if (err != nil) != wantErr[i] {
			t.Errorf("Parse(%q) err=%v, wantErr=%v", raw, err, wantErr[i])
		}
	}
}
