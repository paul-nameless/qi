package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
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

func TestEditorApply(t *testing.T) {
	cases := []struct {
		name, buf string
		pos       int
		key       Key
		want      string
		wantPos   int
	}{
		{"rune mid", "ac", 1, Key{Kind: KeyRune, Rune: 'b'}, "abc", 2},
		{"rune wide", "", 0, Key{Kind: KeyRune, Rune: 'é'}, "é", 1},
		{"paste", "a", 1, Key{Kind: KeyPaste, Text: "xy\nz"}, "axy\nz", 5},
		{"newline", "ab", 1, Key{Kind: KeyNewline}, "a\nb", 2},
		{"backspace", "abc", 2, Key{Kind: KeyBackspace}, "ac", 1},
		{"backspace at start", "abc", 0, Key{Kind: KeyBackspace}, "abc", 0},
		{"delete", "abc", 1, Key{Kind: KeyDelete}, "ac", 1},
		{"delete at end", "abc", 3, Key{Kind: KeyDelete}, "abc", 3},
		{"ctrl+d mid line deletes", "abc", 0, Key{Kind: KeyEOF}, "bc", 0},
		{"left", "abc", 2, Key{Kind: KeyLeft}, "abc", 1},
		{"left at start", "abc", 0, Key{Kind: KeyLeft}, "abc", 0},
		{"right", "abc", 2, Key{Kind: KeyRight}, "abc", 3},
		{"right at end", "abc", 3, Key{Kind: KeyRight}, "abc", 3},
		{"home", "abc", 2, Key{Kind: KeyHome}, "abc", 0},
		{"end", "abc", 1, Key{Kind: KeyEnd}, "abc", 3},
		{"word left", "foo bar", 7, Key{Kind: KeyWordLeft}, "foo bar", 4},
		{"word right", "foo bar", 0, Key{Kind: KeyWordRight}, "foo bar", 3},
		{"kill line", "foo bar", 4, Key{Kind: KeyKillLine}, "bar", 0},
		{"kill to end", "foo bar", 3, Key{Kind: KeyKillEnd}, "foo", 3},
		{"kill line keeps other lines", "one\ntwo three\nfour", 8, Key{Kind: KeyKillLine}, "one\nthree\nfour", 4},
		{"kill line at line start joins previous", "one\ntwo", 4, Key{Kind: KeyKillLine}, "onetwo", 3},
		{"kill line at buffer start", "one", 0, Key{Kind: KeyKillLine}, "one", 0},
		{"kill to end keeps other lines", "one\ntwo three\nfour", 7, Key{Kind: KeyKillEnd}, "one\ntwo\nfour", 7},
		{"kill to end at line end joins next", "one\ntwo", 3, Key{Kind: KeyKillEnd}, "onetwo", 3},
		{"kill to end at buffer end", "one", 3, Key{Kind: KeyKillEnd}, "one", 3},
		{"kill word", "foo bar.baz", 11, Key{Kind: KeyKillWord}, "foo bar.", 8},
		{"kill word over spaces", "foo bar  ", 9, Key{Kind: KeyKillWord}, "foo ", 4},
		{"esc clears", "foo", 2, Key{Kind: KeyEsc}, "", 0},
		{"left across newline", "ab\ncd", 3, Key{Kind: KeyLeft}, "ab\ncd", 2},
		{"right across newline", "ab\ncd", 2, Key{Kind: KeyRight}, "ab\ncd", 3},
		{"home on second line", "ab\ncd", 4, Key{Kind: KeyHome}, "ab\ncd", 3},
		{"end on first line", "ab\ncd", 0, Key{Kind: KeyEnd}, "ab\ncd", 2},
		{"up to previous row", "ab\ncd", 4, Key{Kind: KeyUp}, "ab\ncd", 1},
		{"down to next row", "ab\ncd", 1, Key{Kind: KeyDown}, "ab\ncd", 4},
		{"word left across newline", "foo\nbar", 4, Key{Kind: KeyWordLeft}, "foo\nbar", 0},
		{"word right across newline", "foo\nbar", 3, Key{Kind: KeyWordRight}, "foo\nbar", 7},
		{"kill word across newline", "foo\nbar", 4, Key{Kind: KeyKillWord}, "bar", 0},
		{"kill word right", "foo bar.baz", 3, Key{Kind: KeyKillWordRight}, "foo.baz", 3},
		{"kill word right mid word", "foo bar", 1, Key{Kind: KeyKillWordRight}, "f bar", 1},
		{"kill word right across newline", "foo\nbar baz", 3, Key{Kind: KeyKillWordRight}, "foo baz", 3},
		{"kill word right at end", "foo", 3, Key{Kind: KeyKillWordRight}, "foo", 3},
		{"unknown key ignored", "foo", 1, Key{Kind: KeyClear}, "foo", 1},
	}
	for _, c := range cases {
		e := testEditor(t)
		st := &lineState{buf: []rune(c.buf), pos: c.pos}
		idx, draft := len(e.history), ""
		e.apply(st, c.key, &idx, &draft)
		if string(st.buf) != c.want || st.pos != c.wantPos {
			t.Errorf("%s: got %q pos %d, want %q pos %d", c.name, string(st.buf), st.pos, c.want, c.wantPos)
		}
	}
}

func TestEditorHistoryKeepsDraft(t *testing.T) {
	e := testEditor(t)
	st := &lineState{buf: []rune("draft"), pos: 5}
	idx, draft := len(e.history), ""
	steps := []struct {
		key  KeyKind
		want string
		idx  int
	}{
		{KeyUp, "b", 1},
		{KeyUp, "a", 0},
		{KeyUp, "a", 0},
		{KeyDown, "b", 1},
		{KeyDown, "draft", 2},
		{KeyDown, "draft", 2},
	}
	for i, s := range steps {
		e.apply(st, Key{Kind: s.key}, &idx, &draft)
		if string(st.buf) != s.want || idx != s.idx || st.pos != len(st.buf) {
			t.Errorf("step %d: buf %q idx %d pos %d, want %q idx %d", i, string(st.buf), idx, st.pos, s.want, s.idx)
		}
	}
}

func TestEditorRowsThenHistory(t *testing.T) {
	e := testEditor(t)
	e.history = []string{"a", "b\nc"}
	st := &lineState{buf: []rune("x\ny"), pos: 3}
	idx, draft := len(e.history), ""
	steps := []struct {
		key      KeyKind
		want     string
		pos, idx int
	}{
		{KeyUp, "x\ny", 1, 2},
		{KeyUp, "b\nc", 3, 1},
		{KeyUp, "b\nc", 1, 1},
		{KeyUp, "a", 1, 0},
		{KeyUp, "a", 1, 0},
		{KeyDown, "b\nc", 3, 1},
		{KeyDown, "x\ny", 3, 2},
		{KeyUp, "x\ny", 1, 2},
		{KeyDown, "x\ny", 3, 2},
		{KeyDown, "x\ny", 3, 2},
	}
	for i, s := range steps {
		e.apply(st, Key{Kind: s.key}, &idx, &draft)
		if string(st.buf) != s.want || idx != s.idx || st.pos != s.pos {
			t.Errorf("step %d: buf %q idx %d pos %d, want %q idx %d pos %d", i, string(st.buf), idx, st.pos, s.want, s.idx, s.pos)
		}
	}
}

func TestWordMotions(t *testing.T) {
	cases := []struct {
		buf         string
		pos         int
		left, right int
	}{
		{"", 0, 0, 0},
		{"foo", 0, 0, 3},
		{"foo", 3, 0, 3},
		{"foo bar", 4, 0, 7},
		{"foo bar", 5, 4, 7},
		{"foo  bar", 3, 0, 8},
		{"a.b_c d", 7, 6, 7},
		{"a.b_c d", 1, 0, 5},
		{"  x", 0, 0, 3},
		{"x  ", 3, 0, 3},
		{"日本 語", 4, 3, 4},
		{"日本 語", 0, 0, 2},
		{"--", 1, 0, 2},
	}
	for _, c := range cases {
		buf := []rune(c.buf)
		if got := wordLeft(buf, c.pos); got != c.left {
			t.Errorf("wordLeft(%q, %d) = %d, want %d", c.buf, c.pos, got, c.left)
		}
		if got := wordRight(buf, c.pos); got != c.right {
			t.Errorf("wordRight(%q, %d) = %d, want %d", c.buf, c.pos, got, c.right)
		}
	}
}

func sendKeys(e *Editor, keys ...Key) {
	for _, k := range keys {
		e.T.Keys <- k
	}
}

func runes(s string) []Key {
	var keys []Key
	for _, r := range s {
		keys = append(keys, Key{Kind: KeyRune, Rune: r})
	}
	return keys
}

func TestReadLine(t *testing.T) {
	e := testEditor(t)
	e.T.Keys = make(chan Key, 64)
	e.histPath = filepath.Join(t.TempDir(), "history")
	sendKeys(e, runes("helo")...)
	sendKeys(e, Key{Kind: KeyLeft}, Key{Kind: KeyRune, Rune: 'l'}, Key{Kind: KeyEnd}, Key{Kind: KeyNewline}, Key{Kind: KeyRune, Rune: '!'}, Key{Kind: KeyEnter})
	line, err := e.ReadLine("> ", "")
	if err != nil || line != "hello\n!" {
		t.Fatalf("got %q %v", line, err)
	}
	if e.history[len(e.history)-1] != "hello\n!" {
		t.Errorf("not added to history: %q", e.history)
	}
	if data, err := os.ReadFile(e.histPath); err != nil || string(data) != "\"hello\\n!\"\n" {
		t.Errorf("history file: %q %v", data, err)
	}
	sendKeys(e, Key{Kind: KeyEnter})
	if line, err := e.ReadLine("> ", "draft"); err != nil || line != "draft" {
		t.Errorf("initial text: %q %v", line, err)
	}
}

func TestReadLineInterruptAndEOF(t *testing.T) {
	e := testEditor(t)
	e.T.Keys = make(chan Key)
	e.Interrupt = make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		_, err := e.ReadLine("> ", "typed")
		done <- err
	}()
	e.T.Keys <- Key{Kind: KeyRune, Rune: 'x'}
	e.Interrupt <- struct{}{}
	e.Interrupt <- struct{}{}
	if err := <-done; !errors.Is(err, ErrInterrupt) {
		t.Errorf("interrupt on an empty line: %v", err)
	}
	e.T.Keys = make(chan Key, 64)
	sendKeys(e, Key{Kind: KeyEOF})
	if _, err := e.ReadLine("> ", ""); !errors.Is(err, ErrEOF) {
		t.Errorf("ctrl+d on an empty line: %v", err)
	}
	sendKeys(e, Key{Kind: KeyEOF}, Key{Kind: KeyEnter})
	if line, err := e.ReadLine("> ", "ab"); err != nil || line != "ab" {
		t.Errorf("ctrl+d at the end of text: %q %v", line, err)
	}
}

func TestReadLineToggleAndSlash(t *testing.T) {
	e := testEditor(t)
	e.T.Keys = make(chan Key, 64)
	e.histPath = filepath.Join(t.TempDir(), "history")
	toggled := 0
	e.OnToggle = func() string {
		toggled++
		return "ro> "
	}
	sendKeys(e, Key{Kind: KeyModeToggle}, Key{Kind: KeyRune, Rune: '/'})
	if line, err := e.ReadLine("> ", ""); err != nil || line != "/help" || toggled != 1 {
		t.Errorf("got %q %v toggled %d", line, err, toggled)
	}
}

func TestCollect(t *testing.T) {
	e := testEditor(t)
	e.T.Keys = make(chan Key, 64)
	var queued []string
	cancels, toggles := 0, 0
	s := Steer{
		OnQueue:  func(s string) { queued = append(queued, s) },
		OnCancel: func() { cancels++ },
		OnToggle: func() { toggles++ },
	}
	sendKeys(e, runes("fix it")...)
	sendKeys(e, Key{Kind: KeyEnter}, Key{Kind: KeyEnter})
	sendKeys(e, runes("foo bar")...)
	sendKeys(e, Key{Kind: KeyKillWord}, Key{Kind: KeyBackspace}, Key{Kind: KeyPaste, Text: "!"}, Key{Kind: KeyNewline})
	sendKeys(e, Key{Kind: KeyEsc}, Key{Kind: KeyModeToggle}, Key{Kind: KeyEOF}, Key{Kind: KeyCopyID})
	ctx, cancel := context.WithCancel(context.Background())
	e.OnCopyID = cancel
	draft := e.Collect(ctx, "", s)
	if !reflect.DeepEqual(queued, []string{"fix it"}) || draft != "foo!\n" || cancels != 1 || toggles != 1 {
		t.Errorf("queued %q draft %q cancels %d toggles %d", queued, draft, cancels, toggles)
	}
	sendKeys(e, Key{Kind: KeyKillLine}, Key{Kind: KeyEOF}, Key{Kind: KeyCopyID})
	ctx, cancel = context.WithCancel(context.Background())
	e.OnCopyID = cancel
	if draft := e.Collect(ctx, "keep", s); draft != "" || cancels != 2 {
		t.Errorf("ctrl+d on an emptied draft: %q cancels %d", draft, cancels)
	}
}

func TestTakeImages(t *testing.T) {
	e := testEditor(t)
	e.IsImage = func(text string) []string {
		if text == "/a.png /b.png" {
			return []string{"/a.png", "/b.png"}
		}
		return nil
	}
	if got := e.pasteText("/a.png /b.png"); got != "[image 1] [image 2] " {
		t.Errorf("paste: %q", got)
	}
	if got := e.pasteText("plain"); got != "plain" {
		t.Errorf("plain paste: %q", got)
	}
	if got := e.TakeImages("look at [image 2]"); !reflect.DeepEqual(got, []string{"/b.png"}) {
		t.Errorf("used images: %q", got)
	}
	if got := e.TakeImages("[image 1]"); got != nil {
		t.Errorf("images not cleared: %q", got)
	}
}

func TestLoadHistoryCapsAndSkipsBadLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history")
	var lines []string
	for i := range 1005 {
		lines = append(lines, fmt.Sprintf("%q", fmt.Sprint(i)))
	}
	lines = append(lines, "not json", `""`)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	e := &Editor{histPath: path}
	e.loadHistory()
	if len(e.history) != 1000 || e.history[0] != "5" || e.history[999] != "1004" {
		t.Errorf("history: %d entries, first %q", len(e.history), e.history[0])
	}
	e.addHistory("1004")
	e.addHistory("  ")
	if len(e.history) != 1000 {
		t.Errorf("duplicate or blank entry added: %d", len(e.history))
	}
}

func TestEndLine(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	p := NewPrinter(f)
	p.EndLine()
	p.Text("partial")
	p.EndLine()
	p.EndLine()
	p.mu.Lock()
	p.EndLine()
	p.mu.Unlock()
	data, _ := os.ReadFile(f.Name())
	if string(data) != "partial\n\n" {
		t.Errorf("output %q", data)
	}
}

func TestCollectCopiesTheSessionID(t *testing.T) {
	e := testEditor(t)
	copied := make(chan struct{}, 1)
	e.OnCopyID = func() { copied <- struct{}{} }
	e.T.Keys <- Key{Kind: KeyCopyID}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan string)
	go func() {
		done <- e.Collect(ctx, "", Steer{OnQueue: func(string) {}, OnCancel: func() {}, OnToggle: func() {}})
	}()
	select {
	case <-copied:
	case <-time.After(2 * time.Second):
		t.Error("alt+c during a turn did nothing")
	}
	cancel()
	<-done
}
