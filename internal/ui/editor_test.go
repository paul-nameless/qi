package ui

import (
	"os"
	"testing"
)

func testEditor(t *testing.T) *Editor {
	f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	e := &Editor{T: &Terminal{Keys: make(chan Key, 8)}, P: NewPrinter(f), history: []string{"a", "b"}}
	e.OnAt = func() (string, bool) { return "x.go ", true }
	e.OnSlash = func() (string, bool) { return "/help", true }
	e.OnHistory = func(h []string) (string, bool) {
		h[0] = "mutated"
		return h[len(h)-1], true
	}
	e.OnImage = func() (string, bool) { return "/tmp/p.png", true }
	return e
}

func TestEditorHooks(t *testing.T) {
	at := Key{Kind: KeyRune, Rune: '@'}
	cases := []struct {
		name, buf string
		pos       int
		key       Key
		want      string
		handled   bool
		submit    bool
	}{
		{"@ at start", "", 0, at, "x.go ", true, false},
		{"@ after space", "see ", 4, at, "see x.go ", true, false},
		{"@ mid word", "me", 2, at, "me", false, false},
		{"/ on empty line", "", 0, Key{Kind: KeyRune, Rune: '/'}, "/help", true, true},
		{"/ after text", "a", 1, Key{Kind: KeyRune, Rune: '/'}, "a", false, false},
		{"ctrl+r", "draft", 5, Key{Kind: KeyHistory}, "b", true, false},
		{"ctrl+v", "see ", 4, Key{Kind: KeyPasteImage}, "see [image 1] ", true, false},
	}
	for _, c := range cases {
		e := testEditor(t)
		st := &lineState{buf: []rune(c.buf), pos: c.pos}
		submit, handled := e.hook(st, c.key)
		if string(st.buf) != c.want || handled != c.handled || submit != c.submit {
			t.Errorf("%s: buf %q handled %v submit %v", c.name, string(st.buf), handled, submit)
		}
		if e.history[0] != "a" {
			t.Errorf("%s: history picker changed the editor's history", c.name)
		}
	}
}

func TestEditorHookCancelAndPending(t *testing.T) {
	e := testEditor(t)
	e.OnAt = func() (string, bool) { return "", false }
	st := &lineState{}
	if _, handled := e.hook(st, Key{Kind: KeyRune, Rune: '@'}); !handled || string(st.buf) != "@" {
		t.Errorf("cancelled picker should keep @: %q", string(st.buf))
	}
	e = testEditor(t)
	e.T.Keys <- Key{Kind: KeyRune, Rune: 'x'}
	st = &lineState{}
	if _, handled := e.hook(st, Key{Kind: KeyRune, Rune: '@'}); handled {
		t.Error("picker opened while keys were pending")
	}
}
