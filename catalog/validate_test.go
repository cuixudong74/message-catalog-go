package catalog

import "testing"

func parseAll(t *testing.T, lang string, raws map[string]string) map[string]*Message {
	t.Helper()
	out := map[string]*Message{}
	for k, r := range raws {
		m, err := Parse(lang, k, r)
		if err != nil {
			t.Fatalf("parse %s:%s: %v", lang, k, err)
		}
		out[k] = m
	}
	return out
}

func issuesFor(issues []Issue, lang, key string) []Issue {
	var out []Issue
	for _, i := range issues {
		if i.Lang == lang && i.Key == key {
			out = append(out, i)
		}
	}
	return out
}

func hasReason(issues []Issue, r Reason) bool {
	for _, i := range issues {
		if i.Reason == r {
			return true
		}
	}
	return false
}

func TestValidateConsistent(t *testing.T) {
	msgs := map[string]map[string]*Message{
		"en": parseAll(t, "en", map[string]string{
			"cart": "{n, plural, one{# item} other{# items}} in {user}'s cart",
		}),
		"zh": parseAll(t, "zh", map[string]string{
			"cart": "{user} 的购物车里有 {n, plural, other{# 件商品}}",
		}),
	}
	// zh 的复数类别集与 en 不一致（缺 one），应报 plural_category_mismatch
	issues := Validate(msgs, "en")
	if !hasReason(issuesFor(issues, "zh", "cart"), ReasonPluralCategory) {
		t.Fatalf("expected plural category mismatch, got %v", issues)
	}
}

func TestValidatePlaceholderNameAndCount(t *testing.T) {
	msgs := map[string]map[string]*Message{
		"en": parseAll(t, "en", map[string]string{
			"a": "Hi {name}, you have {count} msgs from {name}",
			"b": "Bye {name}",
			"c": "Code {code}",
		}),
		"fr": parseAll(t, "fr", map[string]string{
			"a": "Salut {name}, {count} messages", // {name} 数量 1 vs 基准 2
			"b": "Au revoir {name}",
			"c": "Code {cde}", // 名称不一致
		}),
	}
	issues := issuesFor(Validate(msgs, "en"), "fr", "a")
	if !hasReason(issues, ReasonPlaceholderCount) {
		t.Fatalf("expected count mismatch, got %v", issues)
	}
	if !hasReason(issuesFor(Validate(msgs, "en"), "fr", "c"), ReasonPlaceholderName) {
		t.Fatalf("expected name mismatch, got %v", issues)
	}
	// 正确的条目 b 不应有误报
	if got := issuesFor(Validate(msgs, "en"), "fr", "b"); len(got) != 0 {
		t.Fatalf("unexpected issues for b: %v", got)
	}
}

func TestValidateMissingOther(t *testing.T) {
	msgs := map[string]map[string]*Message{
		"en": parseAll(t, "en", map[string]string{
			"x": "{n, plural, one{one}}",
		}),
	}
	issues := Validate(msgs, "en")
	if !hasReason(issuesFor(issues, "en", "x"), ReasonMissingOther) {
		t.Fatalf("expected missing other, got %v", issues)
	}
}

func TestValidateSelectCategories(t *testing.T) {
	msgs := map[string]map[string]*Message{
		"en": parseAll(t, "en", map[string]string{
			"g": "{g, select, male{He} female{She} other{They}}",
		}),
		"de": parseAll(t, "de", map[string]string{
			"g": "{g, select, male{Er} other{Sie}}", // 缺 female
		}),
	}
	issues := issuesFor(Validate(msgs, "en"), "de", "g")
	if !hasReason(issues, ReasonSelectCategory) {
		t.Fatalf("expected select category mismatch, got %v", issues)
	}
}

func TestValidateMissingKeyNotError(t *testing.T) {
	msgs := map[string]map[string]*Message{
		"en": parseAll(t, "en", map[string]string{"a": "A", "b": "B"}),
		"ja": parseAll(t, "ja", map[string]string{"a": "あ"}),
	}
	if issues := Validate(msgs, "en"); len(issues) != 0 {
		t.Fatalf("missing key must fall back, not error: %v", issues)
	}
}

func TestValidateSingleErrorDoesNotFailBatch(t *testing.T) {
	msgs := map[string]map[string]*Message{
		"en": parseAll(t, "en", map[string]string{
			"bad":  "Hi {name}",
			"good": "OK {code}",
		}),
		"fr": parseAll(t, "fr", map[string]string{
			"bad":  "Salut {nom}",
			"good": "OK {code}",
		}),
	}
	issues := Validate(msgs, "en")
	if len(issuesFor(issues, "fr", "bad")) == 0 {
		t.Fatal("bad entry should have issue")
	}
	if len(issuesFor(issues, "fr", "good")) != 0 {
		t.Fatal("good entry must be unaffected by bad entry")
	}
}
