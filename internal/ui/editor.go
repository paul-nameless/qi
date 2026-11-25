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
	ErrWake      = errors.New("wake")
)

type Editor struct {
	T         *Terminal
	P         *Printer
	Interrupt chan struct{}
	Wake      <-chan struct{}
	Woken     func() bool
	OnToggle  func() string
	IsImage   func(text string) []string
	OnSlash   func() (text string, submit bool)
	OnAt      func() (text string, ok bool)
	OnHistory func(entries []string) (text string, ok bool)
	OnCopyID  func()
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
	prompt  string
	buf     []rune
	pos     int
	top     int
	goal    int
	hasGoal bool
}

func (e *Editor) render(st *lineState) func(width, height int) Frame {
	return func(width, height int) Frame {
		l := layoutRows(VisibleLen(st.prompt), width, st.buf, st.pos)
		st.top = scrollTop(st.top, l.row, len(l.rows), maxRows(height))
		return l.frame(st.prompt, st.buf, st.top, maxRows(height))
	}
}

func (e *Editor) ReadLine(prompt, initial string) (string, error) {
	st := &lineState{prompt: prompt, buf: []rune(initial)}
	st.pos = len(st.buf)
	e.P.SetPrompt(e.render(st), func(width int) string {
		return echoText(st.prompt, width, st.buf)
	})
	defer e.P.SetPrompt(nil, nil)
	histIdx := len(e.history)
	draft := ""
	woken := e.woken()
	drain(e.Interrupt)
	for {
		if woken && e.emptyLine(st) {
			return "", ErrWake
		}
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
		case <-e.Wake:
			woken = e.woken()
			continue
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
			e.P.ClearScreen()
		}
		e.P.Locked(func() { e.apply(st, k, &histIdx, &draft) })
		if woken && e.emptyLine(st) {
			return "", ErrWake
		}
		e.P.RenderPrompt()
	}
}

func (e *Editor) woken() bool {
	return e.Woken != nil && e.Woken()
}

func (e *Editor) emptyLine(st *lineState) bool {
	empty := false
	e.P.Locked(func() { empty = len(st.buf) == 0 })
	return empty
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
	case k.Kind == KeyCopyID && e.OnCopyID != nil:
		e.OnCopyID()
		return false, true
	case k.Kind == KeyHistory && e.OnHistory != nil:
		if text, ok := e.OnHistory(append([]string(nil), e.history...)); ok {
			e.P.Locked(func() { st.set(text) })
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
	if k.Kind != KeyUp && k.Kind != KeyDown {
		st.hasGoal = false
	}
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
		st.pos = lineStart(st.buf, st.pos)
	case KeyEnd:
		st.pos = lineEnd(st.buf, st.pos)
	case KeyWordLeft:
		st.pos = wordLeft(st.buf, st.pos)
	case KeyWordRight:
		st.pos = wordRight(st.buf, st.pos)
	case KeyKillLine:
		from := lineStart(st.buf, st.pos)
		if from == st.pos {
			from = max(st.pos-1, 0)
		}
		st.buf = append(st.buf[:from], st.buf[st.pos:]...)
		st.pos = from
	case KeyKillEnd:
		to := lineEnd(st.buf, st.pos)
		if to == st.pos {
			to = min(st.pos+1, len(st.buf))
		}
		st.buf = append(st.buf[:st.pos], st.buf[to:]...)
	case KeyKillWord:
		w := wordLeft(st.buf, st.pos)
		st.buf = append(st.buf[:w], st.buf[st.pos:]...)
		st.pos = w
	case KeyKillWordRight:
		st.buf = append(st.buf[:st.pos], st.buf[wordRight(st.buf, st.pos):]...)
	case KeyUp:
		if !e.moveRow(st, -1) {
			e.historyBack(st, histIdx, draft)
		}
	case KeyDown:
		if !e.moveRow(st, 1) {
			e.historyForward(st, histIdx, draft)
		}
	case KeyEsc:
		st.buf, st.pos = nil, 0
	}
}

func (e *Editor) moveRow(st *lineState, dir int) bool {
	goal := -1
	if st.hasGoal {
		goal = st.goal
	}
	pos, goal, ok := moveVertical(VisibleLen(st.prompt), e.P.width(), st.buf, st.pos, dir, goal)
	if !ok {
		return false
	}
	st.pos, st.goal, st.hasGoal = pos, goal, true
	return true
}

func (e *Editor) historyBack(st *lineState, histIdx *int, draft *string) {
	if *histIdx == 0 {
		return
	}
	if *histIdx == len(e.history) {
		*draft = string(st.buf)
	}
	*histIdx--
	st.set(e.history[*histIdx])
}

func (e *Editor) historyForward(st *lineState, histIdx *int, draft *string) {
	if *histIdx >= len(e.history) {
		return
	}
	*histIdx++
	if *histIdx == len(e.history) {
		st.set(*draft)
	} else {
		st.set(e.history[*histIdx])
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
	st.hasGoal = false
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
	st.hasGoal = false
}

func (e *Editor) external(text string) (string, error) {
	f, err := os.CreateTemp("", "qi-*.md")
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
	e.P.ClearFooter()
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
			case KeyCopyID:
				if e.OnCopyID != nil {
					e.OnCopyID()
				}
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
