package oracle

import "testing"

func TestCompare(t *testing.T) {
	exact := "a\nb\n=> 3\n"
	wexp := "x\ncaught Cannot read property 'q' of undefined\n!! TypeError: Cannot read property 'x' of undefined @2\n"
	walt := "x\ncaught cannot read property 'q' of undefined\n!! TypeError: cannot read property 'x' of null @2\n"
	qexp := "!! TypeError: object is not iterable @?\n"
	qalt := "!! TypeError: value is not iterable @1\n"
	cases := []struct {
		name          string
		exp, alt, act string
		ok            bool
	}{
		{"exact match", exact, "", exact, true},
		{"wrong stdout line", exact, "", "a\nc\n=> 3\n", false},
		{"missing line", exact, "", "a\n=> 3\n", false},
		{"extra line", exact, "", "a\nb\n=> 3\nmore\n", false},
		{"wrong result", exact, "", "a\nb\n=> 4\n", false},
		{"error instead of result", exact, "", "a\nb\n!! TypeError: x @3\n", false},
		{"alt verbatim", wexp, walt, walt, true},
		{"own wording", wexp, walt, "x\ncaught reading 'q' failed\n!! TypeError: Cannot read properties of undefined (reading 'x') @2\n", true},
		{"wrong error name", wexp, walt, "x\ncaught cannot read\n!! RangeError: Cannot read property 'x' of undefined @2\n", false},
		{"wrong error line", wexp, walt, "x\ncaught cannot read\n!! TypeError: Cannot read property 'x' of undefined @3\n", false},
		{"exact line in wording item", wexp, walt, "y\ncaught cannot read\n!! TypeError: m @2\n", false},
		{"wording item extra line", wexp, walt, wexp + "z\n", false},
		{"unknown goja line uses quickjs line", qexp, qalt, "!! TypeError: not iterable @1\n", true},
		{"unknown goja line, wrong line", qexp, qalt, "!! TypeError: not iterable @2\n", false},
		{"error token in loose stdout line", "caught TypeError: a\n", "caught TypeError: b\n", "caught RangeError: a\n", false},
		{"loose stdout line empty", "No initial value\n", "empty array\n", "\n", false},
		{"loose stdout line timeout", "No initial value\n", "empty array\n", "!! Timeout\n", false},
		{"loose stdout line own wording", "No initial value\n", "empty array\n", "Reduce of empty array with no initial value\n", true},
		{"loose stdout label kept", "caught: Cannot read property 'x' of undefined\n", "caught: cannot read property 'x' of null\n", "caught: Cannot read properties of undefined (reading 'x')\n", true},
		{"loose stdout label lost", "caught: Cannot read property 'x' of undefined\n", "caught: cannot read property 'x' of null\n", "Cannot read properties of undefined (reading 'x')\n", false},
		{"loose line hides a missing builtin", "Cannot convert a Symbol value to a string\n", "cannot convert symbol to string\n", "Symbol is not defined\n", false},
		{"stale alt with fewer lines", "a\nb\n", "a\n", "a\nc\n", false},
		{"alt line not an error line", "!! TypeError: x @1\n", "plain\n", "!! TypeError: y @1\n", false},
	}
	for _, c := range cases {
		if ok, d := Compare(c.exp, c.alt, c.act); ok != c.ok {
			t.Errorf("%s: Compare = %v (%s), want %v", c.name, ok, d, c.ok)
		}
	}
}

func TestWordingOnly(t *testing.T) {
	cases := []struct {
		name    string
		a, b    string
		message bool
		ok      bool
	}{
		{"error lines", "!! TypeError: A @2\n", "!! TypeError: b @2\n", false, true},
		{"error line numbers differ", "!! TypeError: A @2\n", "!! TypeError: A @3\n", false, false},
		{"error names differ", "!! TypeError: A @2\n", "!! RangeError: A @2\n", false, false},
		{"unknown line", "!! TypeError: A @?\n", "!! TypeError: b @4\n", false, true},
		{"number formatting", "1.10\n", "1.1\n", false, false},
		{"number formatting with message", "1.10\n", "1.1\n", true, true},
		{"phrase in both", "x is not a function\n", "not a function\n", false, true},
		{"phrase vs value", "x is not a function\n", "42\n", false, false},
		{"line count", "a\nb\n", "a\n", true, false},
		{"identical", "a\n", "a\n", false, false},
	}
	for _, c := range cases {
		if ok, _ := WordingOnly(c.a, c.b, c.message); ok != c.ok {
			t.Errorf("%s: WordingOnly = %v, want %v", c.name, ok, c.ok)
		}
	}
}

func TestTier1(t *testing.T) {
	if !Tier1(nil) || !Tier1([]string{"String.raw", "regex-flag-u"}) || Tier1([]string{"class"}) || Tier1([]string{"goja-parse-error"}) {
		t.Fatal("tier1 classification")
	}
}

func TestLoud(t *testing.T) {
	cases := []struct {
		name, exp, act string
		loud           bool
	}{
		{"unsupported after matching prefix", "a\nb\n", "a\n!! SyntaxError: unsupported: classes @2\n", true},
		{"unsupported after diverging prefix", "a\nb\n", "x\n!! SyntaxError: unsupported: classes @2\n", false},
		{"missing global", "x\n", "!! ReferenceError: Symbol is not defined @1\n", true},
		{"missing method", "x\n", "!! TypeError: [].copyWithin is not a function @1\n", true},
		{"user function missing", "x\n", "!! TypeError: helper is not a function @1\n", false},
		{"user global missing", "x\n", "!! ReferenceError: rows is not defined @1\n", false},
		{"silent wrong value", "1\n", "2\n", false},
	}
	for _, c := range cases {
		if got := Loud(c.exp, "", c.act); got != c.loud {
			t.Errorf("%s: Loud = %v, want %v", c.name, got, c.loud)
		}
	}
}
