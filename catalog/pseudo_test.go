package catalog

import (
	"strings"
	"testing"
)

func TestPseudoExpandsAndMarks(t *testing.T) {
	m := mustParse(t, "Hello world")
	p := Pseudo(m)
	out, err := Render(p, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "⟦") || !strings.HasSuffix(out, "⟧") {
		t.Fatalf("missing markers: %q", out)
	}
	if len(out) <= len("Hello world") {
		t.Fatalf("expected expansion, got %q", out)
	}
	if !strings.Contains(out, "éè") { // e → éè
		t.Fatalf("vowel expansion missing: %q", out)
	}
}

func TestPseudoKeepsPlaceholders(t *testing.T) {
	raw := "Hi {name}, {n, plural, one{# file} other{# files}} for {user}"
	m := mustParse(t, raw)
	p := Pseudo(m)
	args := map[string]any{"name": "Ann", "user": "Bob", "n": 3}
	out, err := Render(p, args)
	if err != nil {
		t.Fatal(err)
	}
	// 占位符渲染后不得变形或被吞掉
	for _, want := range []string{"Ann", "Bob", "3"} {
		if !strings.Contains(out, want) {
			t.Fatalf("placeholder value %q lost or deformed: %q", want, out)
		}
	}
	// 原始占位符名不得出现在输出里
	if strings.Contains(out, "{name}") || strings.Contains(out, "{n,") {
		t.Fatalf("raw placeholder leaked: %q", out)
	}
}

func TestPseudoPreservesStructure(t *testing.T) {
	raw := "{g, select, male{He} female{She} other{They}} has {n, plural, one{# cat} other{# cats}}"
	m := mustParse(t, raw)
	p := Pseudo(m)
	// 结构签名必须一致（占位符、分支类别不变）
	bs, ps := sigOf(m), sigOf(p)
	if len(bs.args) != len(ps.args) || len(bs.pluralCats) != len(ps.pluralCats) || len(bs.selectCats) != len(ps.selectCats) {
		t.Fatal("pseudo changed message structure")
	}
	// 每个分支都能正常渲染
	for _, g := range []string{"male", "female", "other"} {
		if _, err := Render(p, map[string]any{"g": g, "n": 2}); err != nil {
			t.Fatalf("branch %s: %v", g, err)
		}
	}
}

func TestPseudoRenderConsistentWithServerRender(t *testing.T) {
	// 伪本地化产物渲染结果 == 对原文渲染结果逐段伪本地化：
	// 占位符替换的值不被伪本地化，其余文本规则一致。
	raw := "Value: {v} end"
	args := map[string]any{"v": "X"}
	plain, err := Render(mustParse(t, raw), args)
	if err != nil {
		t.Fatal(err)
	}
	pseudo, err := Render(Pseudo(mustParse(t, raw)), args)
	if err != nil {
		t.Fatal(err)
	}
	// 手工构造期望：⟦ + pseudoText("Value: ") + X + pseudoText(" end") + ⟧
	want := "⟦" + pseudoText("Value: ") + "X" + pseudoText(" end") + "⟧"
	if pseudo != want {
		t.Fatalf("pseudo render %q != expected %q (plain %q)", pseudo, want, plain)
	}
}

func TestPseudoDeterministic(t *testing.T) {
	m := mustParse(t, "Some {x} text")
	a, _ := Render(Pseudo(m), map[string]any{"x": "1"})
	b, _ := Render(Pseudo(m), map[string]any{"x": "1"})
	if a != b {
		t.Fatalf("pseudo not deterministic: %q vs %q", a, b)
	}
}
