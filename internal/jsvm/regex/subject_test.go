package regex

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func freshGate() string {
	return strings.Repeat("lorem ", 50000) + strings.Repeat("ipsum dolor ", 50000)
}

func runGateOnce(t *testing.T, s string) time.Duration {
	words, _ := Compile(`\b\w+\b`, "g")
	look, _ := Compile(`o(?=r)`, "g")
	st := time.Now()
	if n := allMatches(words, s); n != 150000 {
		t.Fatalf("words: %d", n)
	}
	if out := replaceAll(look, s, "0"); len(out) != len(s) {
		t.Fatal("replace")
	}
	return time.Since(st)
}

func TestGateFreshEqualSubject(t *testing.T) {
	first := runGateOnce(t, freshGate())
	second := runGateOnce(t, freshGate())
	third := runGateOnce(t, freshGate())
	if best := min(second, third); best > 2*first+5*time.Millisecond {
		t.Errorf("rerun on a fresh equal subject: first %v, then %v / %v", first, second, third)
	}
}

func alternate(re *Regexp, a, b string) int {
	n, pa, pb := 0, 0, 0
	for pa >= 0 || pb >= 0 {
		if pa >= 0 {
			if m := re.ExecBytes(a, pa); m == nil {
				pa = -1
			} else {
				n++
				pa = m[1]
			}
		}
		if pb >= 0 {
			if m := re.ExecBytes(b, pb); m == nil {
				pb = -1
			} else {
				n++
				pb = m[1]
			}
		}
	}
	return n
}

func TestAlternatingEqualLengthSubjects(t *testing.T) {
	a := strings.Repeat("lorem ipsum dolor ", 20000)
	b := strings.Repeat("LOREM IPSUM DOLOR ", 20000)
	c := a[:len(a)-1] + "!"
	re, _ := Compile(`\w+`, "g")
	st := time.Now()
	single := allMatches(re, a)
	base := time.Since(st)
	for _, other := range []string{b, c} {
		st = time.Now()
		if n := alternate(re, a, other); n != 2*single {
			t.Fatalf("alternating: %d matches, want %d", n, 2*single)
		}
		if el := time.Since(st); el > 6*base+10*time.Millisecond {
			t.Errorf("alternating subjects took %v, single %v", el, base)
		}
	}
}

func TestSubjectAPI(t *testing.T) {
	re, _ := Compile(`(\p{L}+)\s`, "u")
	sub := NewSubject("日本 語x")
	if sub.IsASCII() || sub.String() != "日本 語x" {
		t.Error("subject accessors")
	}
	if got := re.ExecSubject(sub, 0); !slices.Equal(got, []int{0, 3, 0, 2}) {
		t.Errorf("ExecSubject = %v", got)
	}
	if got := re.ExecSubjectBytes(sub, 0); !slices.Equal(got, []int{0, 7, 0, 6}) {
		t.Errorf("ExecSubjectBytes = %v", got)
	}
	re, _ = Compile(`.b`, "")
	if got := re.ExecSubjectBytes(NewSubject("😀b"), 0); !slices.Equal(got, []int{0, 5}) {
		t.Errorf("non-u astral = %v", got)
	}
	words, _ := Compile(`\b\w+\b`, "g")
	gs := NewSubject(gateText)
	n := 0
	var buf []int
	for pos := 0; ; {
		buf = words.ExecSubjectBytesTo(buf, gs, pos)
		if buf == nil {
			break
		}
		n++
		pos = buf[1]
	}
	if n != 150000 {
		t.Errorf("subject gate words: %d", n)
	}
}

func TestBackrefCharged(t *testing.T) {
	re, _ := CompileEngine(`(a*)\1b`, "", EngineBacktrack)
	done := make(chan *Error, 1)
	go func() { done <- execPanics(re, strings.Repeat("a", 200000)) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("backreference compares not bounded")
	}
}
