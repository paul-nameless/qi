package ui

import (
	"bytes"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

type KeyKind int

const (
	KeyRune KeyKind = iota
	KeyEnter
	KeyNewline
	KeyBackspace
	KeyDelete
	KeyLeft
	KeyRight
	KeyUp
	KeyDown
	KeyHome
	KeyEnd
	KeyWordLeft
	KeyWordRight
	KeyKillLine
	KeyKillEnd
	KeyKillWord
	KeyEOF
	KeyEsc
	KeyModeToggle
	KeyEditor
	KeyPaste
	KeyClear
	KeyHistory
	keyFocusIn
	keyFocusOut
	keyDeviceAttrs
)

const (
	modesOn  = "\x1b[?2004h\x1b[?1004h"
	modesOff = "\x1b[?2004l\x1b[?1004l"
)

type Key struct {
	Kind KeyKind
	Rune rune
	Text string
}

type Terminal struct {
	fd       int
	out      *os.File
	wakeR    int
	wakeW    int
	wake     chan struct{}
	orig     *unix.Termios
	Keys     chan Key
	paused   atomic.Bool
	pauseAck chan struct{}
	raw      chan []byte
	closed   atomic.Bool
	away     atomic.Bool
	synced   chan struct{}
	child    atomic.Bool
	once     sync.Once
}

func IsTerminal(f *os.File) bool {
	_, err := unix.IoctlGetTermios(int(f.Fd()), ioctlGetTermios)
	return err == nil
}

func OpenTerminal() (*Terminal, error) {
	fd := int(os.Stdin.Fd())
	orig, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
	if err != nil {
		return nil, err
	}
	var p [2]int
	if err := unix.Pipe(p[:]); err != nil {
		return nil, err
	}
	_ = unix.SetNonblock(p[0], true)
	t := &Terminal{fd: fd, out: os.Stdout, wakeR: p[0], wakeW: p[1], wake: make(chan struct{}, 1), orig: orig, Keys: make(chan Key, 256), raw: make(chan []byte, 64), pauseAck: make(chan struct{}, 1), synced: make(chan struct{}, 1)}
	if err := t.applyMode(); err != nil {
		t.closeWake()
		return nil, err
	}
	_, _ = t.out.WriteString(modesOn)
	OnCrash(t.Restore)
	go t.readLoop()
	go t.decodeLoop()
	return t, nil
}

func (t *Terminal) applyMode() error {
	st := *t.orig
	st.Lflag &^= unix.ECHO | unix.ICANON | unix.IEXTEN
	st.Lflag |= unix.ISIG
	st.Iflag &^= unix.IXON | unix.ICRNL | unix.INLCR
	st.Oflag |= unix.OPOST
	st.Cc[unix.VMIN] = 1
	st.Cc[unix.VTIME] = 0
	return unix.IoctlSetTermios(t.fd, ioctlSetTermios, &st)
}

func (t *Terminal) Restore() {
	t.once.Do(func() {
		t.closed.Store(true)
		_, _ = t.out.WriteString(modesOff)
		_ = unix.IoctlSetTermios(t.fd, ioctlSetTermios, t.orig)
		t.interrupt()
	})
}

func (t *Terminal) interrupt() {
	_, _ = unix.Write(t.wakeW, []byte{0})
	select {
	case t.wake <- struct{}{}:
	default:
	}
}

func (t *Terminal) closeWake() {
	unix.Close(t.wakeR)
	unix.Close(t.wakeW)
}

func (t *Terminal) Suspend() {
	t.sync()
	t.paused.Store(true)
	t.interrupt()
	select {
	case <-t.pauseAck:
	case <-time.After(200 * time.Millisecond):
	}
	_ = unix.IoctlSetTermios(t.fd, ioctlSetTermios, t.orig)
}

func (t *Terminal) sync() {
	select {
	case <-t.synced:
	default:
	}
	_, _ = t.out.WriteString(modesOff + "\x1b[c")
	select {
	case <-t.synced:
	case <-time.After(300 * time.Millisecond):
	}
}

func (t *Terminal) Resume() {
	_ = t.applyMode()
	_, _ = t.out.WriteString(modesOn)
	t.paused.Store(false)
	t.interrupt()
}

func (t *Terminal) readLoop() {
	defer close(t.raw)
	buf := make([]byte, 4096)
	for !t.closed.Load() {
		if t.paused.Load() {
			t.waitResume()
			continue
		}
		ready, err := t.poll()
		if err != nil {
			return
		}
		if !ready || t.paused.Load() || t.closed.Load() {
			continue
		}
		m, err := unix.Read(t.fd, buf)
		if err == unix.EINTR || err == unix.EAGAIN {
			continue
		}
		if err != nil || m <= 0 {
			return
		}
		t.raw <- append([]byte(nil), buf[:m]...)
	}
}

func (t *Terminal) waitResume() {
	select {
	case t.pauseAck <- struct{}{}:
	default:
	}
	<-t.wake
}

func (t *Terminal) poll() (bool, error) {
	fds := []unix.PollFd{{Fd: int32(t.fd), Events: unix.POLLIN}, {Fd: int32(t.wakeR), Events: unix.POLLIN}}
	if _, err := unix.Poll(fds, -1); err != nil {
		if err == unix.EINTR {
			return false, nil
		}
		return false, err
	}
	if fds[1].Revents != 0 {
		t.drainWake()
	}
	return fds[0].Revents != 0, nil
}

func (t *Terminal) drainWake() {
	var b [64]byte
	for {
		if n, err := unix.Read(t.wakeR, b[:]); n <= 0 || err != nil {
			return
		}
	}
}

func (t *Terminal) decodeLoop() {
	var pending []byte
	var paste []byte
	inPaste := false
	for {
		var chunk []byte
		var ok bool
		if len(pending) > 0 && pending[0] == 0x1b && !inPaste {
			select {
			case chunk, ok = <-t.raw:
			case <-time.After(80 * time.Millisecond):
				if len(pending) == 1 {
					t.Keys <- Key{Kind: KeyEsc}
				}
				pending = pending[:0]
				continue
			}
		} else {
			chunk, ok = <-t.raw
		}
		if !ok {
			if !t.closed.Load() {
				t.Keys <- Key{Kind: KeyEOF}
			}
			return
		}
		pending = append(pending, chunk...)
		for len(pending) > 0 {
			if inPaste {
				end := bytes.Index(pending, []byte("\x1b[201~"))
				if end < 0 {
					keep := 0
					for k := 1; k < 6 && k <= len(pending); k++ {
						if string(pending[len(pending)-k:]) == "\x1b[201~"[:k] {
							keep = k
						}
					}
					paste = append(paste, pending[:len(pending)-keep]...)
					pending = append([]byte{}, pending[len(pending)-keep:]...)
					break
				}
				paste = append(paste, pending[:end]...)
				pending = pending[end+6:]
				inPaste = false
				t.Keys <- Key{Kind: KeyPaste, Text: normalizePaste(string(paste))}
				paste = paste[:0]
				continue
			}
			k, n := decodeKey(pending)
			if n == 0 {
				break
			}
			pending = pending[n:]
			if k.Kind == KeyPaste {
				inPaste = true
				continue
			}
			if k.Kind == -1 || t.reply(k.Kind) {
				continue
			}
			t.Keys <- k
		}
	}
}

func (t *Terminal) reply(k KeyKind) bool {
	switch k {
	case keyFocusIn:
		t.away.Store(false)
	case keyFocusOut:
		t.away.Store(true)
	case keyDeviceAttrs:
		select {
		case t.synced <- struct{}{}:
		default:
		}
	default:
		return false
	}
	return true
}

func (t *Terminal) ChildRunning() bool {
	return t.child.Load()
}

func (t *Terminal) Away() bool {
	return t.away.Load()
}

func normalizePaste(s string) string {
	out := make([]rune, 0, len(s))
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		if r == '\r' {
			if i+1 < len(rs) && rs[i+1] == '\n' {
				continue
			}
			r = '\n'
		}
		out = append(out, r)
	}
	return string(out)
}

func decodeKey(b []byte) (Key, int) {
	c := b[0]
	switch c {
	case '\r':
		return Key{Kind: KeyEnter}, 1
	case '\n':
		return Key{Kind: KeyNewline}, 1
	case 0x7f, 0x08:
		return Key{Kind: KeyBackspace}, 1
	case 0x01:
		return Key{Kind: KeyHome}, 1
	case 0x05:
		return Key{Kind: KeyEnd}, 1
	case 0x02:
		return Key{Kind: KeyLeft}, 1
	case 0x06:
		return Key{Kind: KeyRight}, 1
	case 0x04:
		return Key{Kind: KeyEOF}, 1
	case 0x0b:
		return Key{Kind: KeyKillEnd}, 1
	case 0x15:
		return Key{Kind: KeyKillLine}, 1
	case 0x17:
		return Key{Kind: KeyKillWord}, 1
	case 0x07:
		return Key{Kind: KeyEditor}, 1
	case 0x0c:
		return Key{Kind: KeyClear}, 1
	case 0x12:
		return Key{Kind: KeyHistory}, 1
	case 0x10:
		return Key{Kind: KeyUp}, 1
	case 0x0e:
		return Key{Kind: KeyDown}, 1
	case 0x1b:
		return decodeEscape(b)
	}
	if c < 0x20 {
		return Key{Kind: -1}, 1
	}
	if !utf8.FullRune(b) {
		return Key{}, 0
	}
	r, n := utf8.DecodeRune(b)
	return Key{Kind: KeyRune, Rune: r}, n
}

func decodeEscape(b []byte) (Key, int) {
	if len(b) < 2 {
		return Key{}, 0
	}
	switch b[1] {
	case '[':
		end := -1
		for i := 2; i < len(b); i++ {
			if b[i] >= 0x40 && b[i] <= 0x7e {
				end = i
				break
			}
		}
		if end < 0 {
			if len(b) > 32 {
				return Key{Kind: -1}, len(b)
			}
			return Key{}, 0
		}
		seq := string(b[2 : end+1])
		return csiKey(seq), end + 1
	case 'O':
		if len(b) < 3 {
			return Key{}, 0
		}
		switch b[2] {
		case 'A':
			return Key{Kind: KeyUp}, 3
		case 'B':
			return Key{Kind: KeyDown}, 3
		case 'C':
			return Key{Kind: KeyRight}, 3
		case 'D':
			return Key{Kind: KeyLeft}, 3
		case 'H':
			return Key{Kind: KeyHome}, 3
		case 'F':
			return Key{Kind: KeyEnd}, 3
		}
		return Key{Kind: -1}, 3
	case '\r':
		return Key{Kind: KeyNewline}, 2
	case 'b':
		return Key{Kind: KeyWordLeft}, 2
	case 'f':
		return Key{Kind: KeyWordRight}, 2
	case 0x7f:
		return Key{Kind: KeyKillWord}, 2
	case 0x1b:
		return Key{Kind: KeyEsc}, 1
	}
	return Key{Kind: -1}, 2
}

func csiKey(seq string) Key {
	if strings.HasPrefix(seq, "?") && strings.HasSuffix(seq, "c") {
		return Key{Kind: keyDeviceAttrs}
	}
	switch seq {
	case "A":
		return Key{Kind: KeyUp}
	case "B":
		return Key{Kind: KeyDown}
	case "C":
		return Key{Kind: KeyRight}
	case "D":
		return Key{Kind: KeyLeft}
	case "H", "1~", "7~":
		return Key{Kind: KeyHome}
	case "F", "4~", "8~":
		return Key{Kind: KeyEnd}
	case "3~":
		return Key{Kind: KeyDelete}
	case "Z", "9;5u", "27;5;9~", "9;6u", "27;6;9~", "9;2u":
		return Key{Kind: KeyModeToggle}
	case "1;5D", "1;3D":
		return Key{Kind: KeyWordLeft}
	case "1;5C", "1;3C":
		return Key{Kind: KeyWordRight}
	case "13;2u", "13;5u", "27;2;13~", "27;5;13~":
		return Key{Kind: KeyNewline}
	case "200~":
		return Key{Kind: KeyPaste}
	case "I":
		return Key{Kind: keyFocusIn}
	case "O":
		return Key{Kind: keyFocusOut}
	}
	return Key{Kind: -1}
}
