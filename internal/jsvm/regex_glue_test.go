package jsvm

import (
	"strings"
	"testing"

	"github.com/paul-nameless/qi/internal/jsvm/regex"
)

func TestRegexUTF16(t *testing.T) {
	t.Parallel()
	vm := New(Options{})
	cases := []struct{ src, want string }{
		{`"a😀".replace(/./g, "x")`, `"xxx"`},
		{`"a😀".replace(/./gu, "x")`, `"xx"`},
		{`"😀".match(/./u)[0].length + "|" + "😀".match(/./)[0].length`, `"2|1"`},
		{`[..."é😀b é".matchAll(/\w|é/g)].map(m => m.index)`, "[0,3,5]"},
		{`"é,😀;b".split(/[,;]/)`, `["é","😀","b"]`},
		{`"xé😀y".search(/y/)`, "4"},
		{`const r = /b/g; r.exec("😀b😀b"); const a = r.lastIndex; r.exec("😀b😀b"); [a, r.lastIndex]`, "[3,6]"},
		{`"é😀".replace(/(😀)/, "[$1|$'|$` + "`" + `]")`, `"é[😀||é]"`},
		{`"aé😀".replace(/é/, (m, i, s) => i + s.length)`, `"a5😀"`},
	}
	for _, c := range cases {
		got, err := evalJSON(t, vm, c.src)
		if err != nil || got != c.want {
			t.Errorf("%s = %s %v, want %s", c.src, got, err, c.want)
		}
	}
}

func TestHostStringsSanitized(t *testing.T) {
	vm := New(Options{})
	vm.Set("bad", Str("a\xffb\xc3"))
	got, err := evalJSON(t, vm, "[bad, bad.length, bad.charCodeAt(1)]")
	if err != nil || got != `["a�b�",4,65533]` {
		t.Errorf("got %s %v", got, err)
	}
	v, err := vm.JSONParse("{\"k\": \"x\xfey\"}")
	if err != nil {
		t.Fatal(err)
	}
	vm.Set("parsed", v)
	if got, _ := evalJSON(t, vm, "parsed.k.length"); got != "3" {
		t.Errorf("JSONParse length %s", got)
	}
	big := strings.Repeat("valid ascii ", 1<<16)
	if s := validUTF8(big); s != big {
		t.Error("valid input changed")
	}
	if n := testing.AllocsPerRun(10, func() { _ = validUTF8(big) }); n != 0 {
		t.Errorf("valid input copied: %v allocs", n)
	}
	if _, err := vm.RunCell("cell.js", "'\xff'.length"); err != nil {
		t.Errorf("invalid source byte: %v", err)
	}
}

type panicEngine struct{ reEngine }

func (panicEngine) ExecSubject(*regex.Subject, int) []int { panic("boom") }
func (panicEngine) ExecSubjectBytes(*regex.Subject, int) []int {
	panic(struct{ x int }{1})
}

func TestRegexPanicCatchable(t *testing.T) {
	t.Parallel()
	vm := New(Options{})
	re, err := vm.RunCell("cell.js", "/x/")
	if err != nil {
		t.Fatal(err)
	}
	rd := re.obj().inner.(*regexpData)
	rd.re.eng = panicEngine{rd.re.eng}
	vm.Set("re", re)
	got, err := evalJSON(t, vm, "const out = []; for (const s of ['😀', 'x']) { try { re.test(s) } catch (e) { out.push(e.name + ': ' + e.message) } } out")
	if err != nil || got != `["Error: regular expression failed: boom","Error: regular expression failed: {1}"]` {
		t.Errorf("got %s %v", got, err)
	}
	if v, err := vm.RunCell("cell.js", "/a(b)/.exec('xab')[1]"); err != nil || ToString(v) != "b" {
		t.Errorf("VM unusable after regex panic: %v %v", ToString(v), err)
	}
}
