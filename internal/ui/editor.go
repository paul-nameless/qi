package ui

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

var (
	ErrEOF       = errors.New("eof")
	ErrInterrupt = errors.New("interrupt")
)

type Editor struct {
	T         *Terminal
	P         *Printer
	Interrupt chan struct{}
	OnToggle  func() string
	IsImage   func(text string) []string
	OnSlash   func() (text string, submit bool)
	OnAt      func() (text string, ok bool)
	OnHistory func(entries []string) (text string, ok bool)
	OnImage   func() (path string, ok bool)
	images    []string
	histPath  string
	history   []string
}

func NewEditor(t *Terminal, p *Printer, histPath string) *Editor {
	e := &Editor{T: t, P: p, Interrupt: make(chan struct{}, 1), histPath: histPath}
	e.loadHistory()
	return e
}

func (e *Editor) loadHistory() {
	f, err := os.Open(e.histPath)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 8<<20)
	for sc.Scan() {
		var s string
		if json.Unmarshal(sc.Bytes(), &s) == nil && s != "" {
			e.history = append(e.history, s)
		}
	}
	if len(e.history) > 1000 {
		e.history = e.history[len(e.history)-1000:]
	}
}

func (e *Editor) addHistory(s string) {
	if strings.TrimSpace(s) == "" || (len(e.history) > 0 && e.history[len(e.history)-1] == s) {
		return
	}
	e.history = append(e.history, s)
	if err := os.MkdirAll(filepath.Dir(e.histPath), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(e.histPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	data, _ := json.Marshal(s)
	_, _ = f.Write(append(data, '\n'))
}

type lineState struct {
	prompt string
	buf    []rune
	pos    int
}

func (e *Editor) render(st *lineState) func() {
	return func() {
		width := e.P.width()
		pw := VisibleLen(st.prompt)
		avail := width - pw - 1
		if avail < 10 {
			avail = 10
		}
		disp := make([]rune, len(st.buf))
		for i, r := range st.buf {
			switch r {
			case '\n':
				disp[i] = '↵'
			case '\t':
				disp[i] = ' '
			default:
				disp[i] = r
			}
		}
		pos := min(max(st.pos, 0), len(disp))
		start := 0
		if pos >= avail {
			start = pos - avail + 1
		}
		end := min(start+avail, len(disp))
		start = min(start, end)
		s := "\r\x1b[K" + st.prompt + string(disp[start:end]) + "\r"
		if col := pw + pos - start; col > 0 {
			s += fmt.Sprintf("\x1b[%dC", col)
		}
		_, _ = e.P.out.WriteString(s)
	}
}

func (e *Editor) ReadLine(prompt, initial string) (string, error) {
	st := &lineState{prompt: prompt, buf: []rune(initial)}
	st.pos = len(st.buf)
	e.P.SetPrompt(e.render(st), func() string {
		return st.prompt + strings.ReplaceAll(string(st.buf), "\n", "\n  ")
	})
	defer e.P.SetPrompt(nil, nil)
	histIdx := len(e.history)
	draft := ""
	drain(e.Interrupt)
	for {
		var k Key
		select {
		case <-e.Interrupt:
			cleared := false
			e.P.Locked(func() {
				if len(st.buf) > 0 {
					st.buf, st.pos = nil, 0
					cleared = true
				}
			})
			if cleared {
				e.P.RenderPrompt()
				continue
			}
			e.P.FinishPrompt()
			return "", ErrInterrupt
		case k = <-e.T.Keys:
		}
		if submit, handled := e.hook(st, k); handled {
			if submit {
				var line string
				e.P.Locked(func() { line = string(st.buf) })
				e.P.FinishPrompt()
				e.addHistory(line)
				return line, nil
			}
			e.P.RenderPrompt()
			continue
		}
		switch k.Kind {
		case KeyEnter:
			var line string
			e.P.Locked(func() { line = string(st.buf) })
			e.P.FinishPrompt()
			e.addHistory(line)
			return line, nil
		case KeyEOF:
			empty := false
			e.P.Locked(func() { empty = len(st.buf) == 0 })
			if empty {
				time.Sleep(20 * time.Millisecond)
				empty = len(e.T.Keys) == 0
			}
			if empty {
				e.P.FinishPrompt()
				return "", ErrEOF
			}
		case KeyModeToggle:
			if e.OnToggle != nil {
				prompt := e.OnToggle()
				e.P.Locked(func() { st.prompt = prompt })
			}
		case KeyEditor:
			var cur string
			e.P.Locked(func() { cur = string(st.buf) })
			text, err := e.external(cur)
			if err == nil {
				e.P.Locked(func() { st.set(text) })
			}
		case KeyClear:
			e.P.Raw("\x1b[2J\x1b[H")
		}
		e.P.Locked(func() { e.apply(st, k, &histIdx, &draft) })
		e.P.RenderPrompt()
	}
}

func (e *Editor) hook(st *lineState, k Key) (submit, handled bool) {
	switch {
	case k.Kind == KeyRune && k.Rune == '/' && e.OnSlash != nil && e.quiet(st, func() bool { return len(st.buf) == 0 }):
		text, submit := e.OnSlash()
		e.P.Locked(func() { st.set(text) })
		return submit, true
	case k.Kind == KeyRune && k.Rune == '@' && e.OnAt != nil && e.quiet(st, st.atWordStart):
		text, ok := e.OnAt()
		if !ok {
			text = "@"
		}
		e.P.Locked(func() { st.insert([]rune(e.pasteText(text))) })
		return false, true
	case k.Kind == KeyHistory && e.OnHistory != nil:
		if text, ok := e.OnHistory(append([]string(nil), e.history...)); ok {
			e.P.Locked(func() { st.set(text) })
		}
		return false, true
	case k.Kind == KeyPasteImage && e.OnImage != nil:
		if path, ok := e.OnImage(); ok {
			e.P.Locked(func() { st.insert([]rune(e.attach(path))) })
		}
		return false, true
	}
	return false, false
}

func (e *Editor) quiet(st *lineState, cond func() bool) bool {
	ok := false
	e.P.Locked(func() { ok = cond() })
	if !ok {
		return false
	}
	time.Sleep(20 * time.Millisecond)
	return len(e.T.Keys) == 0
}

func (e *Editor) apply(st *lineState, k Key, histIdx *int, draft *string) {
	switch k.Kind {
	case KeyEOF, KeyDelete:
		if st.pos < len(st.buf) {
			st.buf = append(st.buf[:st.pos], st.buf[st.pos+1:]...)
		}
	case KeyRune:
		st.insert([]rune{k.Rune})
	case KeyPaste:
		st.insert([]rune(e.pasteText(k.Text)))
	case KeyNewline:
		st.insert([]rune{'\n'})
	case KeyBackspace:
		if st.pos > 0 {
			st.buf = append(st.buf[:st.pos-1], st.buf[st.pos:]...)
			st.pos--
		}
	case KeyLeft:
		st.pos = max(st.pos-1, 0)
	case KeyRight:
		st.pos = min(st.pos+1, len(st.buf))
	case KeyHome:
		st.pos = 0
	case KeyEnd:
		st.pos = len(st.buf)
	case KeyWordLeft:
		st.pos = wordLeft(st.buf, st.pos)
	case KeyWordRight:
		st.pos = wordRight(st.buf, st.pos)
	case KeyKillLine:
		st.buf = append([]rune{}, st.buf[st.pos:]...)
		st.pos = 0
	case KeyKillEnd:
		st.buf = st.buf[:st.pos]
	case KeyKillWord:
		w := wordLeft(st.buf, st.pos)
		st.buf = append(st.buf[:w], st.buf[st.pos:]...)
		st.pos = w
	case KeyUp:
		if *histIdx > 0 {
			if *histIdx == len(e.history) {
				*draft = string(st.buf)
			}
			*histIdx--
			st.buf = []rune(e.history[*histIdx])
			st.pos = len(st.buf)
		}
	case KeyDown:
		if *histIdx < len(e.history) {
			*histIdx++
			if *histIdx == len(e.history) {
				st.buf = []rune(*draft)
			} else {
				st.buf = []rune(e.history[*histIdx])
			}
			st.pos = len(st.buf)
		}
	case KeyEsc:
		st.buf, st.pos = nil, 0
	}
}

func (e *Editor) pasteText(text string) string {
	if e.IsImage == nil {
		return text
	}
	paths := e.IsImage(text)
	if len(paths) == 0 {
		return text
	}
	var b strings.Builder
	for _, p := range paths {
		b.WriteString(e.attach(p))
	}
	return b.String()
}

func (e *Editor) attach(path string) string {
	e.images = append(e.images, path)
	return fmt.Sprintf("[image %d] ", len(e.images))
}

func (e *Editor) TakeImages(line string) []string {
	var used []string
	for i, p := range e.images {
		if strings.Contains(line, fmt.Sprintf("[image %d]", i+1)) {
			used = append(used, p)
		}
	}
	e.images = nil
	return used
}

func (st *lineState) set(text string) {
	st.buf = []rune(text)
	st.pos = len(st.buf)
}

func (st *lineState) atWordStart() bool {
	return st.pos == 0 || unicode.IsSpace(st.buf[st.pos-1])
}

func (st *lineState) insert(rs []rune) {
	buf := make([]rune, 0, len(st.buf)+len(rs))
	buf = append(buf, st.buf[:st.pos]...)
	buf = append(buf, rs...)
	buf = append(buf, st.buf[st.pos:]...)
	st.buf = buf
	st.pos += len(rs)
}

func (e *Editor) external(text string) (string, error) {
	f, err := os.CreateTemp("", "aicode-*.md")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	_, werr := f.WriteString(text)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return "", fmt.Errorf("write draft for editor: %w", werr)
	}
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		editor = "vi"
	}
	if err := e.RunTTY(exec.Command("sh", "-c", editor+` "$1"`, "sh", f.Name())); err != nil {
		return "", err
	}
	data, err := os.ReadFile(f.Name())
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(data), "\n"), nil
}

func (e *Editor) RunTTY(cmd *exec.Cmd) error {
	if cmd.Stdin == nil {
		cmd.Stdin = os.Stdin
	}
	if cmd.Stdout == nil {
		cmd.Stdout = os.Stdout
	}
	cmd.Stderr = os.Stderr
	e.P.Raw("\r\x1b[K")
	e.T.Suspend()
	e.T.child.Store(true)
	err := cmd.Run()
	e.T.child.Store(false)
	e.T.Resume()
	drain(e.Interrupt)
	e.P.RenderPrompt()
	return err
}

type Steer struct {
	OnQueue  func(string)
	OnCancel func()
	OnToggle func()
}

func (e *Editor) Collect(ctx context.Context, initial string, s Steer) string {
	buf := []rune(initial)
	e.P.SetDraft(string(buf))
	defer e.P.SetDraft("")
	for {
		select {
		case <-ctx.Done():
			return string(buf)
		case k := <-e.T.Keys:
			switch k.Kind {
			case KeyRune:
				buf = append(buf, k.Rune)
			case KeyPaste:
				buf = append(buf, []rune(e.pasteText(k.Text))...)
			case KeyNewline:
				buf = append(buf, '\n')
			case KeyBackspace:
				if len(buf) > 0 {
					buf = buf[:len(buf)-1]
				}
			case KeyKillLine:
				buf = nil
			case KeyKillWord:
				buf = buf[:wordLeft(buf, len(buf))]
			case KeyEnter:
				if strings.TrimSpace(string(buf)) != "" {
					s.OnQueue(string(buf))
					buf = nil
				}
			case KeyEsc:
				s.OnCancel()
			case KeyModeToggle:
				s.OnToggle()
			case KeyEOF:
				if len(buf) == 0 {
					s.OnCancel()
				}
			}
			e.P.SetDraft(string(buf))
		}
	}
}

func drain(ch chan struct{}) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

func isWord(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

func wordLeft(buf []rune, pos int) int {
	for pos > 0 && !isWord(buf[pos-1]) {
		pos--
	}
	for pos > 0 && isWord(buf[pos-1]) {
		pos--
	}
	return pos
}

func wordRight(buf []rune, pos int) int {
	for pos < len(buf) && !isWord(buf[pos]) {
		pos++
	}
	for pos < len(buf) && isWord(buf[pos]) {
		pos++
	}
	return pos
}
