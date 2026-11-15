package ui

import (
	"testing"
	"time"
)

func TestDecodeKey(t *testing.T) {
	cases := []struct {
		in   string
		kind KeyKind
		n    int
	}{
		{"\r", KeyEnter, 1},
		{"\n", KeyNewline, 1},
		{"\x1b\r", KeyNewline, 2},
		{"\x1b[Z", KeyModeToggle, 3},
		{"\x1b[9;5u", KeyModeToggle, 6},
		{"\x1b[13;2u", KeyNewline, 7},
		{"\x1b[A", KeyUp, 3},
		{"\x1b[3~", KeyDelete, 4},
		{"\x12", KeyHistory, 1},
		{"\x1bx", -1, 2},
		{"\x1b[99X", -1, 5},
		{"é", KeyRune, 2},
		{"\xc3", KeyRune, 0},
	}
	for _, c := range cases {
		k, n := decodeKey([]byte(c.in))
		if n != c.n || (n > 0 && k.Kind != c.kind) {
			t.Errorf("%q: got kind %d n %d, want %d %d", c.in, k.Kind, n, c.kind, c.n)
		}
	}
}

func TestDecodeLoopPasteAndSplitRunes(t *testing.T) {
	tm := &Terminal{raw: make(chan []byte, 8), Keys: make(chan Key, 64)}
	go tm.decodeLoop()
	tm.raw <- []byte("\x1b[200~ab\r\nc\x1b[20")
	tm.raw <- []byte("1~x\xc3")
	tm.raw <- []byte("\xa9\x1b")
	var got []Key
	timeout := time.After(time.Second)
	for len(got) < 4 {
		select {
		case k := <-tm.Keys:
			got = append(got, k)
		case <-timeout:
			t.Fatalf("got %+v", got)
		}
	}
	if got[0].Kind != KeyPaste || got[0].Text != "ab\nc" {
		t.Errorf("paste %+v", got[0])
	}
	if got[1].Kind != KeyRune || got[1].Rune != 'x' || got[2].Rune != 'é' {
		t.Errorf("runes %+v %+v", got[1], got[2])
	}
	if got[3].Kind != KeyEsc {
		t.Errorf("lone esc %+v", got[3])
	}
}

func TestSanitize(t *testing.T) {
	if got := Sanitize("ok\x1b]52;c;ZXZpbA==\x07 \x1b[31mred\x1b[0m\r\n\tdone\x08"); got != "ok red\n\tdone" {
		t.Errorf("got %q", got)
	}
}

func TestFocusSupportReply(t *testing.T) {
	tm := &Terminal{raw: make(chan []byte, 8), Keys: make(chan Key, 64)}
	go tm.decodeLoop()
	tm.raw <- []byte("\x1b[?1004;0$yb\x1b[?1004;1$ya")
	for _, want := range "ba" {
		if k := <-tm.Keys; k.Rune != want {
			t.Fatalf("got %+v", k)
		}
	}
	if !tm.Focused() {
		t.Error("terminal that supports focus reporting should start focused")
	}
}

func TestFocusEvents(t *testing.T) {
	tm := &Terminal{raw: make(chan []byte, 8), Keys: make(chan Key, 64)}
	go tm.decodeLoop()
	if tm.Focused() {
		t.Fatal("focused before any event")
	}
	tm.raw <- []byte("\x1b[Ia\x1b[O")
	k := <-tm.Keys
	if k.Kind != KeyRune || k.Rune != 'a' {
		t.Errorf("focus events leaked as keys: %+v", k)
	}
	deadline := time.Now().Add(time.Second)
	for tm.focus.Load() != 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if tm.Focused() {
		t.Error("still focused after focus-out")
	}
	tm.raw <- []byte("\x1b[?1004;2$y")
	time.Sleep(20 * time.Millisecond)
	if tm.Focused() {
		t.Error("support reply overrode a real focus-out")
	}
	tm.raw <- []byte("\x1b[I")
	for !tm.Focused() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !tm.Focused() {
		t.Error("not focused after focus-in")
	}
}
