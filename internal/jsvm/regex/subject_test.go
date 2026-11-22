package regex

import (
	"fmt"
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

func TestBackrefNotOvercharged(t *testing.T) {
	var b strings.Builder
	for i := 0; b.Len() < 20000; i++ {
		fmt.Fprintf(&b, "%x-", i*7919)
	}
	line := b.String()
	for _, p := range []string{`^(.+)\1`, `^(.+?)\1+$`} {
		re, _ := Compile(p, "")
		if err := execPanics(re, line); err != nil {
			t.Errorf("/%s/ on a %d-byte line: %v", p, len(line), err)
		}
	}
}

func TestSeekNotQuadratic(t *testing.T) {
	s := NewSubject("😀" + strings.Repeat("a", 200000))
	look, _ := Compile(`(?=(\w\w))`, "g")
	other, _ := Compile(`a`, "")
	done := make(chan int, 1)
	go func() {
		n := 0
		for pos := 0; ; {
			m := look.ExecSubject(s, pos)
			if m == nil {
				break
			}
			other.ExecSubject(s, 0)
			n++
			pos = m[1] + 1
		}
		done <- n
	}()
	select {
	case n := <-done:
		if n != 199999 {
			t.Errorf("matches: %d", n)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("offset conversion is quadratic")
	}
}

func TestBytesMidCharStartAndEmptyMidPair(t *testing.T) {
	re, _ := Compile(`x*`, "")
	if m := re.ExecBytes("😀", 1); !slices.Equal(m, []int{4, 4}) {
		t.Errorf("start inside an astral char: %v", m)
	}
	re, _ = Compile(`(?=\uDE00)`, "")
	if m := re.ExecBytes("😀", 0); m == nil || m[0] != m[1] {
		t.Errorf("empty match between surrogate halves widened: %v", m)
	}
}

func bounded(t *testing.T, name string, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			if r := recover(); r != nil {
				if _, ok := r.(*Error); !ok {
					panic(r)
				}
			}
		}()
		f()
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatalf("%s: not bounded by the step budget", name)
	}
}

func TestFailedBackrefComparesCharged(t *testing.T) {
	a := strings.Repeat("a", 200000)
	re1, _ := CompileEngine(`(?=(a*b))a+?\1`, "", EngineBacktrack)
	bounded(t, "lookahead capture", func() { re1.Exec(a+"b"+a+"b", 0) })
	re2, _ := CompileEngine(`(a*)\1b`, "i", EngineBacktrack)
	bounded(t, "fold", func() { re2.Exec(a, 0) })
	re3, _ := CompileEngine(`(a*)\1c`, "", EngineBacktrack)
	bounded(t, "late mismatch", func() { re3.Exec(a+"b"+a, 0) })
}

func TestSelfBackrefInLookbehindNotCharged(t *testing.T) {
	re, _ := Compile(`(?<=(\1.))[^a]`, "")
	if err := execPanics(re, strings.Repeat("a", 20000)); err != nil {
		t.Errorf("unset backreference charged: %v", err)
	}
}

func TestBytesGroupEndsMidPair(t *testing.T) {
	re, _ := Compile(`(?<=(\uD83D))`, "")
	if m := re.ExecBytes("😀", 0); !slices.Equal(m, []int{0, 0, 0, 4}) {
		t.Errorf("lookbehind group over a high surrogate: %v", m)
	}
	re, _ = Compile(`x`, "")
	if m := re.ExecBytes("éx", 1); !slices.Equal(m, []int{2, 3}) {
		t.Errorf("byte start inside a 2-byte char: %v", m)
	}
}
