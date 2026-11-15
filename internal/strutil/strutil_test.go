package strutil

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCutKeepsRunes(t *testing.T) {
	s := strings.Repeat("ж", 10)
	for n := 0; n <= len(s)+1; n++ {
		if a, b := CutStart(s, n), CutEnd(s, n); !utf8.ValidString(a) || !utf8.ValidString(b) || len(a) > n || len(b) > n {
			t.Errorf("n=%d: %q %q", n, a, b)
		}
	}
	if CutStart("abc", 2) != "ab" || CutEnd("abc", 2) != "bc" || CutStart("ab", 5) != "ab" {
		t.Error("ascii cuts")
	}
}
