package tools

import (
	"strings"
	"testing"
)

func TestApplyEdit(t *testing.T) {
	src := "func a() {\n\treturn 1\n}\n\nfunc b() {\n\treturn 2\n}\n"
	cases := []struct {
		name, old, new, want string
		all                  bool
		err                  bool
	}{
		{name: "exact", old: "return 1", new: "return 10", want: "func a() {\n\treturn 10\n}\n\nfunc b() {\n\treturn 2\n}\n"},
		{name: "ambiguous", old: "}\n", new: "};\n", err: true},
		{name: "replace all", old: "return", new: "yield", all: true, want: "func a() {\n\tyield 1\n}\n\nfunc b() {\n\tyield 2\n}\n"},
		{name: "line numbers", old: "2\t\treturn 1\n3\t}", new: "2\t\treturn 11\n3\t}", want: "func a() {\n\treturn 11\n}\n\nfunc b() {\n\treturn 2\n}\n"},
		{name: "trimmed", old: "func b() {\n  return 2\n}", new: "func b() {\n\treturn 3\n}", want: "func a() {\n\treturn 1\n}\n\nfunc b() {\n\treturn 3\n}\n"},
		{name: "missing", old: "nope", new: "x", err: true},
		{name: "empty", old: "", new: "x", err: true},
	}
	for _, c := range cases {
		got, _, err := applyEdit(src, c.old, c.new, c.all)
		if c.err {
			if err == nil {
				t.Errorf("%s: expected error", c.name)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestApplyEditCRLFAndAmbiguity(t *testing.T) {
	src := "a\r\n  b\r\nc\r\n"
	got, _, err := applyEdit(src, "\tb\nc", "B\nC", false)
	if err != nil {
		t.Fatal(err)
	}
	if got != "a\r\nB\r\nC\r\n" {
		t.Errorf("crlf trimmed: %q", got)
	}
	got, _, err = applyEdit("x\r\ny\r\n", "x\ny", "z", false)
	if err != nil || got != "z\r\n" {
		t.Errorf("crlf exact: %q %v", got, err)
	}
	if _, _, err := applyEdit("  f()\nf()\n", "f() ", "g()", false); err == nil || !strings.Contains(err.Error(), "2 places") {
		t.Errorf("ambiguous trimmed match: %v", err)
	}
	if _, _, err := applyEdit("a", "a", "a", false); err == nil {
		t.Error("identical strings accepted")
	}
	got, _, err = applyEdit("a\n  b\nc\n", "b ", "", false)
	if err != nil || got != "a\nc\n" {
		t.Errorf("trimmed delete: %q %v", got, err)
	}
}
