package util

import "strings"

// TODO: make Slug unicode-aware
func Slug(s string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(s)), " ", "-")
}

func Truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func IsBlank(s string) bool {
	return strings.TrimSpace(s) == ""
}

func Title(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func ContainsFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}

func Reverse(s string) string {
	r := []rune(s)
	for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
		r[i], r[j] = r[j], r[i]
	}
	return string(r)
}
