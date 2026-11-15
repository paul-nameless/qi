package ui

import (
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"
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

func TestFocusEvents(t *testing.T) {
	tm := &Terminal{raw: make(chan []byte, 8), Keys: make(chan Key, 64)}
	go tm.decodeLoop()
	if tm.Away() {
		t.Fatal("away before any event")
	}
	tm.raw <- []byte("\x1b[Oa")
	if k := <-tm.Keys; k.Kind != KeyRune || k.Rune != 'a' {
		t.Errorf("focus event leaked as a key: %+v", k)
	}
	if !tm.Away() {
		t.Error("not away after focus-out")
	}
	tm.raw <- []byte("\x1b[Ib")
	<-tm.Keys
	if tm.Away() {
		t.Error("still away after focus-in")
	}
}

func TestDeviceAttrsReplySyncs(t *testing.T) {
	tm := &Terminal{raw: make(chan []byte, 8), Keys: make(chan Key, 64), synced: make(chan struct{}, 1)}
	go tm.decodeLoop()
	tm.raw <- []byte("\x1b[O\x1b[?62;22cx")
	if k := <-tm.Keys; k.Kind != KeyRune || k.Rune != 'x' {
		t.Errorf("device attributes reply leaked as a key: %+v", k)
	}
	select {
	case <-tm.synced:
	default:
		t.Error("device attributes reply did not signal sync")
	}
	if !tm.Away() {
		t.Error("focus-out before the reply was lost")
	}
}

func pipeTerminal(t *testing.T) (*Terminal, *os.File) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close(); w.Close() })
	var p [2]int
	if err := unix.Pipe(p[:]); err != nil {
		t.Fatal(err)
	}
	_ = unix.SetNonblock(p[0], true)
	t.Cleanup(func() { unix.Close(p[0]); unix.Close(p[1]) })
	tm := &Terminal{fd: int(r.Fd()), wakeR: p[0], wakeW: p[1], wake: make(chan struct{}, 1), raw: make(chan []byte, 8), Keys: make(chan Key, 8), pauseAck: make(chan struct{}, 1)}
	return tm, w
}

func TestReadLoopWakesForPauseAndClose(t *testing.T) {
	tm, w := pipeTerminal(t)
	go tm.readLoop()
	go tm.decodeLoop()
	w.Write([]byte("a"))
	if k := <-tm.Keys; k.Rune != 'a' {
		t.Fatalf("key %+v", k)
	}
	tm.paused.Store(true)
	tm.interrupt()
	select {
	case <-tm.pauseAck:
	case <-time.After(time.Second):
		t.Fatal("read loop did not acknowledge the pause")
	}
	w.Write([]byte("b"))
	select {
	case k := <-tm.Keys:
		t.Fatalf("read while paused: %+v", k)
	case <-time.After(50 * time.Millisecond):
	}
	tm.paused.Store(false)
	tm.interrupt()
	select {
	case k := <-tm.Keys:
		if k.Rune != 'b' {
			t.Fatalf("after resume: %+v", k)
		}
	case <-time.After(time.Second):
		t.Fatal("no input after resume")
	}
	tm.closed.Store(true)
	tm.interrupt()
	deadline := time.After(time.Second)
	for {
		select {
		case _, ok := <-tm.raw:
			if !ok {
				select {
				case k := <-tm.Keys:
					t.Errorf("key after close: %+v", k)
				case <-time.After(50 * time.Millisecond):
				}
				return
			}
		case <-deadline:
			t.Fatal("read loop kept running after close")
		}
	}
}

func TestReadLoopEOF(t *testing.T) {
	tm, w := pipeTerminal(t)
	go tm.readLoop()
	go tm.decodeLoop()
	w.Close()
	select {
	case k := <-tm.Keys:
		if k.Kind != KeyEOF {
			t.Errorf("key %+v", k)
		}
	case <-time.After(time.Second):
		t.Fatal("no EOF")
	}
}
