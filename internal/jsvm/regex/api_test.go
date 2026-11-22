package regex

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestCompileErrors(t *testing.T) {
	cases := []struct{ pattern, flags, msg string }{
		{"(", "", "Invalid regular expression: /(/: Unterminated group"},
		{"a)", "", "Invalid regular expression: /a)/: Unmatched ')'"},
		{"[a", "g", "Invalid regular expression: /[a/g: Unterminated character class"},
		{"a**", "", "Invalid regular expression: /a**/: Nothing to repeat"},
		{"a{2,1}", "", "Invalid regular expression: /a{2,1}/: numbers out of order in {} quantifier"},
		{"[z-a]", "", "Invalid regular expression: /[z-a]/: Range out of order in character class"},
		{"(?<n>a)(?<n>b)", "", "Invalid regular expression: /(?<n>a)(?<n>b)/: Duplicate capture group name"},
		{`\k<x>(?<a>.)`, "", `Invalid regular expression: /\k<x>(?<a>.)/: Invalid named capture referenced`},
		{"(?x)", "", "Invalid regular expression: /(?x)/: Invalid group"},
		{"{", "u", "Invalid regular expression: /{/u: Lone quantifier brackets"},
		{`\a`, "u", `Invalid regular expression: /\a/u: Invalid escape`},
		{`\p{Nope}`, "u", `Invalid regular expression: /\p{Nope}/u: Invalid property name`},
		{`\`, "", `Invalid regular expression: /\/: \ at end of pattern`},
		{"a", "gg", "Invalid flags supplied to RegExp constructor 'gg'"},
		{"a", "q", "Invalid flags supplied to RegExp constructor 'q'"},
		{"a", "v", "unsupported: v flag"},
		{"a", "gv", "unsupported: v flag"},
		{"a", "uv", "Invalid flags supplied to RegExp constructor 'uv'"},
	}
	for _, c := range cases {
		_, err := Compile(c.pattern, c.flags)
		var e *Error
		if !errors.As(err, &e) || e.Msg != c.msg {
			t.Errorf("Compile(%q, %q) = %v, want %q", c.pattern, c.flags, err, c.msg)
		}
	}
}

func TestAccessors(t *testing.T) {
	re, err := Compile(`(?<year>\d{4})-(\d\d)/x`, "yigsmd")
	if err != nil {
		t.Fatal(err)
	}
	if re.Flags() != "dgimsy" {
		t.Errorf("Flags = %q", re.Flags())
	}
	if !re.Global() || !re.IgnoreCase() || !re.Multiline() || !re.DotAll() || re.Unicode() || !re.Sticky() || !re.HasIndices() {
		t.Error("flag accessors")
	}
	if re.NumGroups() != 3 || !slices.Equal(re.GroupNames(), []string{"", "year", ""}) {
		t.Errorf("groups %d %q", re.NumGroups(), re.GroupNames())
	}
	if re.Source() != `(?<year>\d{4})-(\d\d)\/x` {
		t.Errorf("Source = %q", re.Source())
	}
	for _, c := range [][2]string{{"", "(?:)"}, {"[/]", "[/]"}, {`\/`, `\/`}, {"a\nb", `a\nb`}} {
		re, _ := Compile(c[0], "")
		if re.Source() != c[1] {
			t.Errorf("Source(%q) = %q, want %q", c[0], re.Source(), c[1])
		}
	}
}

func TestExecOffsets(t *testing.T) {
	cases := []struct {
		pattern, flags, s string
		start             int
		want              []int
	}{
		{`b`, "", "abc", 0, []int{1, 2}},
		{`b`, "", "abc", 2, nil},
		{`b`, "y", "abc", 0, nil},
		{`b`, "y", "abc", 1, []int{1, 2}},
		{`x*`, "", "ab", 2, []int{2, 2}},
		{`x*`, "", "ab", 3, nil},
		{`a`, "", "ab", -5, []int{0, 1}},
		{`(a)|(b)`, "", "b", 0, []int{0, 1, -1, -1, 0, 1}},
		{`é(.)`, "", "xéz", 0, []int{1, 3, 2, 3}},
		{`.`, "", "😀", 1, []int{1, 2}},
		{`.`, "u", "😀a", 1, []int{0, 2}},
		{`a`, "", "😀a", 2, []int{2, 3}},
		{`^`, "", "a", 1, nil},
		{`\bb`, "", "ab b", 1, []int{3, 4}},
		{`(?<=a)b`, "", "ab", 1, []int{1, 2}},
		{`^b`, "m", "a\nb", 1, []int{2, 3}},
		{`$`, "", "ab", 0, []int{2, 2}},
		{`abc`, "", strings.Repeat("x", 100) + "abc", 50, []int{100, 103}},
		{`abc`, "y", "xabc", 1, []int{1, 4}},
	}
	for _, c := range cases {
		for _, eng := range []int{EngineAuto, EngineBacktrack, EngineRE2} {
			re, err := CompileEngine(c.pattern, c.flags, eng)
			if err != nil {
				t.Fatal(err)
			}
			if got := re.Exec(c.s, c.start); !slices.Equal(got, c.want) {
				t.Errorf("eng %d: /%s/%s.Exec(%q, %d) = %v, want %v", eng, c.pattern, c.flags, c.s, c.start, got, c.want)
			}
		}
	}
}

func TestExecBytes(t *testing.T) {
	re, _ := Compile(`(\p{L}+)\s`, "u")
	s := "日本 語x"
	if got := re.ExecBytes(s, 0); !slices.Equal(got, []int{0, 7, 0, 6}) {
		t.Errorf("ExecBytes = %v", got)
	}
	if got := re.ExecBytes(s, 7); got != nil {
		t.Errorf("ExecBytes from 7 = %v", got)
	}
	re, _ = Compile(`.b`, "")
	if got := re.ExecBytes("😀b", 0); !slices.Equal(got, []int{0, 5}) {
		t.Errorf("non-u astral ExecBytes = %v", got)
	}
	if got := re.ExecBytes("ab", 3); got != nil {
		t.Errorf("past end = %v", got)
	}
	buf := make([]int, 0, 8)
	re, _ = Compile(`b+`, "")
	got := re.ExecBytesTo(buf, "abbc", 0)
	if !slices.Equal(got, []int{1, 3}) || &got[0] != &buf[:1][0] {
		t.Errorf("ExecBytesTo = %v", got)
	}
}

func TestIsASCII(t *testing.T) {
	for _, c := range []struct {
		s    string
		want bool
	}{{"", true}, {"abc", true}, {strings.Repeat("a", 17), true}, {strings.Repeat("a", 15) + "é", false}, {"é", false}} {
		if IsASCII(c.s) != c.want {
			t.Errorf("IsASCII(%q)", c.s)
		}
	}
}

func execPanics(re *Regexp, s string) (err *Error) {
	defer func() {
		if r := recover(); r != nil {
			err = r.(*Error)
		}
	}()
	re.Exec(s, 0)
	return nil
}

func TestStepBudget(t *testing.T) {
	s := strings.Repeat("a", 30) + "b"
	re, _ := CompileEngine(`(a+)+$`, "", EngineBacktrack)
	if err := execPanics(re, s); err == nil || err.Msg != "regular expression too complex" {
		t.Errorf("backtrack-only: %v", err)
	}
	re, _ = Compile(`(a+)+$`, "")
	if err := execPanics(re, s); err != nil {
		t.Errorf("auto should rescue with RE2: %v", err)
	}
	re, _ = Compile(`(a+)+\1$`, "")
	if err := execPanics(re, s); err == nil {
		t.Error("backreference pattern must hit the budget")
	}
	old := SetStepLimit(10)
	defer SetStepLimit(old)
	re, _ = CompileEngine(`(?:a|aa)*c`, "", EngineBacktrack)
	if err := execPanics(re, strings.Repeat("a", 25)); err == nil {
		t.Error("tiny limit must trip")
	}
	if re.Exec("c", 0) == nil {
		t.Error("machine unusable after budget panic")
	}
}

func TestConcurrentExec(t *testing.T) {
	re, _ := Compile(`(\w+)@(\w+)\.com`, "g")
	s := strings.Repeat("x a@b.com y ", 1000)
	done := make(chan int)
	for g := 0; g < 8; g++ {
		go func() {
			n := 0
			for pos := 0; ; {
				m := re.Exec(s, pos)
				if m == nil {
					break
				}
				n++
				pos = m[1]
			}
			done <- n
		}()
	}
	for g := 0; g < 8; g++ {
		if n := <-done; n != 1000 {
			t.Errorf("goroutine saw %d matches", n)
		}
	}
}

func TestLargeSubjects(t *testing.T) {
	s := strings.Repeat("ab", 1<<20)
	re, _ := Compile(`(?:a|b)*c`, "")
	if m := re.Exec(s, 0); m != nil {
		t.Errorf("unexpected match %v", m)
	}
	re, _ = Compile(`.*$`, "")
	if m := re.Exec(s, 0); !slices.Equal(m, []int{0, len(s)}) {
		t.Errorf(".*$ = %v", m)
	}
	re, _ = Compile(`^(?:ab)+$`, "")
	if m := re.Exec(s, 0); !slices.Equal(m, []int{0, len(s)}) {
		t.Errorf("(?:ab)+ = %v", m)
	}
}

func TestRescueUnavailable(t *testing.T) {
	cases := [][3]string{
		{`(?:a|aa)*c`, "", strings.Repeat("a", 25) + "😀"},
		{`^(?:a|aa)*c`, "m", strings.Repeat("a", 30) + "\r"},
	}
	for _, c := range cases {
		re, _ := Compile(c[0], c[1])
		if err := execPanics(re, c[2]); err != nil {
			t.Errorf("/%s/%s: %v", c[0], c[1], err)
		}
	}
}

func TestLookbehindRunCharged(t *testing.T) {
	re, _ := CompileEngine(`(?<=x*)[xz]y`, "", EngineBacktrack)
	s := strings.Repeat("x", 200000)
	done := make(chan *Error, 1)
	go func() { done <- execPanics(re, s) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("lookbehind scan not bounded")
	}
}

func TestEmptyCountedLoopsBounded(t *testing.T) {
	for _, p := range []string{`(?:){1000000000}`, `(a?){100000000}`, `(?:(?:(?:){1000}){1000}){1000}`} {
		re, _ := Compile(p, "")
		done := make(chan *Error, 1)
		go func() { done <- execPanics(re, "") }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatalf("%s: not bounded", p)
		}
	}
	s := strings.Repeat("ab", 3<<20)
	re, _ := Compile(`(?:(a)(b))*`, "")
	if m := re.Exec(s, 0); m == nil || m[1] != len(s) {
		t.Errorf("RE2 rescue after stack cap: %v", m)
	}
	re, _ = Compile(`(?:(a)(?=b)b)*`, "")
	if err := execPanics(re, s); err == nil {
		t.Error("stack cap must stop a long frame-pushing path")
	}
}

func TestUnicodeCategoryC(t *testing.T) {
	cases := []struct {
		pattern string
		want    bool
	}{{`\p{Cn}`, true}, {`\p{Unassigned}`, true}, {`^\p{C}$`, true}, {`\p{Assigned}`, false}, {`\P{Assigned}`, true}}
	for _, c := range cases {
		re, err := Compile(c.pattern, "u")
		if err != nil {
			t.Fatal(err)
		}
		if got := re.Exec("\u0378", 0) != nil; got != c.want {
			t.Errorf("%s on U+0378: %v, want %v", c.pattern, got, c.want)
		}
	}
}

func TestNestingLimit(t *testing.T) {
	deep := strings.Repeat("(", 100000) + strings.Repeat(")", 100000)
	if _, err := Compile(deep, ""); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Errorf("deep nesting: %v", err)
	}
	ok := strings.Repeat("(?:", 200) + "a" + strings.Repeat(")+", 200)
	if _, err := Compile(ok, ""); err != nil {
		t.Errorf("200 levels: %v", err)
	}
}

func TestInvalidUTF8Offsets(t *testing.T) {
	re, _ := Compile(`b`, "")
	for _, s := range []string{"\xffab", "\xF0\x9Fab", "😀\xffab"} {
		m := re.ExecBytes(s, 0)
		if m == nil || m[1] > len(s) || s[m[0]:m[1]] != "b" {
			t.Errorf("ExecBytes(%q) = %v", s, m)
		}
	}
}

func TestHalfPairBytesNotEmpty(t *testing.T) {
	re, _ := Compile(`.`, "")
	if m := re.ExecBytes("😀", 0); !slices.Equal(m, []int{0, 4}) {
		t.Errorf("high half: %v", m)
	}
	re, _ = Compile(`[^\x00-\x7F]`, "g")
	if m := re.ExecBytes("a😀", 1); !slices.Equal(m, []int{1, 5}) {
		t.Errorf("class on emoji: %v", m)
	}
	if m := re.Exec("a😀", 2); !slices.Equal(m, []int{2, 3}) {
		t.Errorf("UTF-16 low half: %v", m)
	}
}

func TestInvalidBytesInSets(t *testing.T) {
	cases := []struct {
		pattern, s string
		want       []int
	}{
		{`[^a]`, "a\xff😀", []int{1, 2}},
		{`[^a]`, "a\x80", []int{1, 2}},
	}
	for _, c := range cases {
		re, _ := Compile(c.pattern, "")
		if m := re.ExecBytes(c.s, 0); !slices.Equal(m, c.want) {
			t.Errorf("/%s/ on %q: %v, want %v", c.pattern, c.s, m, c.want)
		}
	}
}

func TestHugeRepeatCounts(t *testing.T) {
	for _, p := range []string{`(a)\3000000000`, `a{3000000000}a$`, `a{0,4294967295}`, `(a)\4294967297`} {
		re, err := Compile(p, "")
		if err != nil {
			t.Errorf("%s: %v", p, err)
			continue
		}
		re.Exec("ab", 0)
	}
	re, _ := Compile(`(a)\4294967297`, "")
	if m := re.Exec("aa", 0); m != nil && m[1] == 2 {
		t.Errorf("huge decimal escape acted as \\1: %v", m)
	}
}

func TestCompileCacheKey(t *testing.T) {
	if _, err := Compile("y", "g/x"); err == nil {
		t.Error("invalid flags accepted")
	}
	a, _ := Compile("a", "gi")
	b, _ := Compile("a", "ig")
	if a != b {
		t.Error("equivalent flag orders should share a cache entry")
	}
}
