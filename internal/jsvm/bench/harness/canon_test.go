package harness

import (
	"errors"
	"testing"
)

func TestErrLine(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		off  int
		want string
	}{
		{"goja runtime repl", "TypeError: Cannot read property 'x' of undefined\nTypeError: Cannot read property 'x' of undefined\n\tat cell.js:3:14(5)\n\tat cell.js:6:3(2)\n", 1, "!! TypeError: Cannot read property 'x' of undefined @2"},
		{"goja multi-line message", "Error: multi\nline\nError: multi\nline\n\tat f (cell.js:3:9(3))\n\tat cell.js:5:10(8)\n", 1, `!! Error: multi\nline @2`},
		{"goja syntax", "SyntaxError: SyntaxError: Unexpected token ; at cell.js:3:11", 1, "!! SyntaxError: Unexpected token ; @2"},
		{"goja syntax with pc", "SyntaxError: Identifier 'c' has already been declared at cell.js:1:1(0)", 0, "!! SyntaxError: Identifier 'c' has already been declared @1"},
		{"goja tool error", "ToolError: boom: x\n\tat cell.js:3:19(6)\n\tat cell.js:6:3(2)", 1, "!! ToolError: boom: x @2"},
		{"quickjs runtime", "TypeError: cannot read property 'x' of null\n    at <eval> (<eval>:2:5)\n", 0, "!! TypeError: cannot read property 'x' of null @2"},
		{"quickjs syntax", "SyntaxError: unexpected token in expression: ';'\n    at <eval>:2:9", 0, "!! SyntaxError: unexpected token in expression: ';' @2"},
		{"quickjs nested", "Error: multi\nline\n    at f (<eval>:2:18)\n    at <eval> (<eval>:4:2)\n", 0, `!! Error: multi\nline @2`},
		{"v8 header after rename", "ToolError: no file\nError: no file\n    at f (cell.js:3:5)\n", 0, "!! ToolError: no file @3"},
		{"v8 header multi-line", "Error: a\nb\nError: a\nb\n    at cell.js:2:1", 0, `!! Error: a\nb @2`},
		{"thrown primitive", "Thrown: 42", 0, "!! Thrown: 42 @?"},
		{"empty message", "Error: \n    at <eval> (<eval>:1:1)", 0, "!! Error:  @1"},
		{"timeout", errTimeout.Error(), 0, "!! Timeout"},
		{"never settles", "the script is waiting on a promise that can never settle", 0, "!! NeverSettles"},
	}
	for _, c := range cases {
		if got := ErrLine(c.msg, c.off); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestCanon(t *testing.T) {
	got := Canon("a\nat cell.js:4:2(3)\n", "{\n \"x\": 1\n}", errors.New("RangeError: r\n    at <eval> (<eval>:3:1)"), 1)
	want := "a\nat cell.js:3\n=> {\n \"x\": 1\n}\n!! RangeError: r @2\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestSplitCells(t *testing.T) {
	cells := SplitCells("a\n//// cell\nb\n  //// cell  \nc")
	if len(cells) != 3 || cells[0] != "a" || cells[1] != "b" || cells[2] != "c" {
		t.Fatalf("got %q", cells)
	}
}

func TestTapePlayer(t *testing.T) {
	tr := &Transcript{Tape: []TapeEntry{
		{Cell: 0, Name: "bash", Args: `["ls"]`, OK: true, Val: `{"output":"a"}`},
		{Cell: 0, Name: "bash", Args: `["pwd"]`, OK: true, Val: `{"output":"b"}`},
		{Cell: 1, Name: "read", Args: `["x"]`, OK: false, Val: "missing"},
	}}
	p := newTapePlayer(tr)
	if a := p.answer("bash", `["pwd"]`); !a.OK || a.Val != `{"output":"b"}` {
		t.Fatalf("match by args: %+v", a)
	}
	if p.unused(0) != 1 {
		t.Fatalf("unused after one call: %d", p.unused(0))
	}
	if a := p.answer("bash", `["other"]`); !a.OK || a.Val != `{"output":"a"}` || p.fallbacks != 1 {
		t.Fatalf("fallback by sequence: %+v fallbacks %d", a, p.fallbacks)
	}
	if p.unused(0) != 0 || p.unused(1) != 1 {
		t.Fatalf("unused: %d %d", p.unused(0), p.unused(1))
	}
	if a := p.answer("bash", `["ls"]`); a.OK || p.misses != 1 {
		t.Fatalf("exhausted cell should miss: %+v", a)
	}
	if a := p.answer("read", `["x"]`); a.OK || p.misses != 2 {
		t.Fatalf("other cell entries are not visible: %+v", a)
	}
	p.cell = 1
	if a := p.answer("read", `["x"]`); a.OK || a.Val != "missing" {
		t.Fatalf("recorded failure: %+v", a)
	}
}
