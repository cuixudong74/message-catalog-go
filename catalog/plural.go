package catalog

import "strings"

// PluralCategory 返回某语言下数值 n 的复数类别（CLDR 规则的精简实现，
// 覆盖常见语言；未覆盖的语言一律 other，行为确定）。
func PluralCategory(lang string, n float64) string {
	base := strings.SplitN(lang, "-", 2)[0]
	i := int64(n)
	isInt := n == float64(i)
	switch base {
	case "ja", "zh", "ko", "th", "vi", "id", "ms":
		return "other"
	case "en", "de", "nl", "sv", "da", "fi", "el", "it", "pt", "es":
		if isInt && i == 1 {
			return "one"
		}
		return "other"
	case "fr":
		if n >= 0 && n < 2 {
			return "one"
		}
		return "other"
	case "ru", "uk", "be", "hr", "sr", "bs":
		mod10, mod100 := i%10, i%100
		if isInt && mod10 == 1 && mod100 != 11 {
			return "one"
		}
		if isInt && mod10 >= 2 && mod10 <= 4 && (mod100 < 12 || mod100 > 14) {
			return "few"
		}
		if isInt && (mod10 == 0 || (mod10 >= 5 && mod10 <= 9) || (mod100 >= 11 && mod100 <= 14)) {
			return "many"
		}
		return "other"
	case "pl":
		mod10, mod100 := i%10, i%100
		if isInt && i == 1 {
			return "one"
		}
		if isInt && mod10 >= 2 && mod10 <= 4 && (mod100 < 12 || mod100 > 14) {
			return "few"
		}
		if isInt && i != 1 && (mod10 == 0 || mod10 == 1 || (mod10 >= 5 && mod10 <= 9) || (mod100 >= 12 && mod100 <= 14)) {
			return "many"
		}
		return "other"
	case "ar":
		if n == 0 {
			return "zero"
		}
		if n == 1 {
			return "one"
		}
		if n == 2 {
			return "two"
		}
		mod100 := i % 100
		if mod100 >= 3 && mod100 <= 10 {
			return "few"
		}
		if mod100 >= 11 && mod100 <= 99 {
			return "many"
		}
		return "other"
	default:
		return "other"
	}
}
