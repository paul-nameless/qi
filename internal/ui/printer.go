package ui

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

const (
	Dim    = "\x1b[2m"
	Italic = "\x1b[3m"
	Bold   = "\x1b[1m"
	Red    = "\x1b[31m"
	Green  = "\x1b[32m"
	Yellow = "\x1b[33m"
	Cyan   = "\x1b[36m"
	Reset  = "\x1b[0m"
)

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

type toolState struct {
	label    string
	progress string
	start    time.Time
}

type Printer struct {
	mu          sync.Mutex
	out         *os.File
	TTY         bool
	atLineStart bool
	footerShown bool
	inReasoning bool
	running     map[string]*toolState
	order       []string
	draft       string
	frame       int
	ticking     bool
	stopTick    chan struct{}
	promptDraw  func()
	promptFinal func() string
	md          markdown
}

func NewPrinter(out *os.File) *Printer {
	return &Printer{out: out, TTY: IsTerminal(out), atLineStart: true, running: map[string]*toolState{}}
}

func (p *Printer) Color(code, s string) string {
	if !p.TTY {
		return s
	}
	return code + s + Reset
}

func (p *Printer) width() int {
	ws, err := unix.IoctlGetWinsize(int(p.out.Fd()), unix.TIOCGWINSZ)
	if err != nil || ws.Col == 0 {
		return 80
	}
	return int(ws.Col)
}

func (p *Printer) clearFooter() {
	if p.footerShown {
		_, _ = p.out.WriteString("\r\x1b[K")
		p.footerShown = false
	}
}

func (p *Printer) footerText() string {
	status := p.statusText()
	if p.draft == "" {
		return status
	}
	draft := p.Color(Cyan, "› ") + p.draft + p.Color(Dim, "  (enter to queue)")
	if status == "" {
		return draft
	}
	return draft + p.Color(Dim, "  ·  ") + status
}

func (p *Printer) statusText() string {
	if len(p.running) == 0 {
		return ""
	}
	var labels []string
	var oldest time.Time
	for _, id := range p.order {
		st, ok := p.running[id]
		if !ok {
			continue
		}
		l := st.label
		if st.progress != "" {
			l += " " + p.Color(Dim, "· "+st.progress)
		}
		labels = append(labels, l)
		if oldest.IsZero() || st.start.Before(oldest) {
			oldest = st.start
		}
	}
	secs := time.Since(oldest).Seconds()
	head := fmt.Sprintf("%s %d running ", spinner[p.frame%len(spinner)], len(labels))
	return p.Color(Yellow, head) + strings.Join(labels, ", ") + p.Color(Dim, fmt.Sprintf(" %.1fs", secs))
}

func (p *Printer) drawFooter() {
	if !p.TTY || !p.atLineStart {
		return
	}
	if p.promptDraw != nil {
		p.promptDraw()
		p.footerShown = true
		return
	}
	text := p.footerText()
	if text == "" {
		return
	}
	_, _ = p.out.WriteString("\r\x1b[K" + truncateVisible(text, p.width()-1))
	p.footerShown = true
}

func (p *Printer) write(s string) {
	if s == "" {
		return
	}
	p.clearFooter()
	_, _ = p.out.WriteString(s)
	p.atLineStart = strings.HasSuffix(s, "\n")
	p.drawFooter()
}

func (p *Printer) endReasoning() {
	if p.inReasoning {
		p.inReasoning = false
		if p.TTY {
			p.clearFooter()
			_, _ = p.out.WriteString(Reset)
		}
		if !p.atLineStart {
			p.write("\n")
		}
		p.write("\n")
	}
}

func Sanitize(s string) string {
	if !strings.ContainsAny(s, "\x1b\r\x07\x08\x7f") && !hasControl(s) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == 0x1b:
			i = escEnd(s, i)
			continue
		case c == '\n' || c == '\t':
			b.WriteByte(c)
		case c < 0x20 || c == 0x7f:
		default:
			b.WriteByte(c)
		}
		i++
	}
	return b.String()
}

func hasControl(s string) bool {
	for i := 0; i < len(s); i++ {
		if (s[i] < 0x20 && s[i] != '\n' && s[i] != '\t') || s[i] == 0x7f {
			return true
		}
	}
	return false
}

func (p *Printer) Locked(fn func()) {
	p.mu.Lock()
	defer p.mu.Unlock()
	fn()
}

func (p *Printer) Text(s string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.endReasoning()
	s = Sanitize(s)
	if p.TTY {
		p.md.width = p.width()
		s = p.md.feed(s)
	}
	p.write(s)
}

func (p *Printer) breakLine() {
	if !p.atLineStart && p.TTY {
		p.write(p.md.feed("\n"))
	}
	if !p.atLineStart {
		p.write("\n")
	}
}

func (p *Printer) flushMarkdown() {
	if p.TTY {
		p.write(p.md.flush())
	}
}

func (p *Printer) Reasoning(s string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.inReasoning {
		p.breakLine()
		p.inReasoning = true
	}
	s = Sanitize(s)
	if p.TTY {
		p.write(Dim + Italic + s + Reset)
		p.atLineStart = strings.HasSuffix(s, "\n")
		return
	}
	p.write(s)
}

func (p *Printer) EndBlock() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.endReasoning()
	p.flushMarkdown()
	if !p.atLineStart {
		p.write("\n")
	}
}

func (p *Printer) Line(s string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.endReasoning()
	p.breakLine()
	p.write(s + "\n")
}

func (p *Printer) Info(s string) {
	p.Line(p.Color(Dim, Sanitize(s)))
}

func (p *Printer) Error(s string) {
	p.Line(p.Color(Red, Sanitize(s)))
}

func (p *Printer) ToolStart(id, label string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.endReasoning()
	p.flushMarkdown()
	if !p.atLineStart {
		p.write("\n")
	}
	p.running[id] = &toolState{label: Sanitize(label), start: time.Now()}
	p.order = append(p.order, id)
	p.startTicker()
	p.clearFooter()
	p.drawFooter()
}

func (p *Printer) ToolProgress(id, progress string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if st, ok := p.running[id]; ok {
		st.progress = lastLine(progress)
	}
}

func (p *Printer) ToolDone(id string, ok bool, line string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := p.running[id]
	delete(p.running, id)
	for i, oid := range p.order {
		if oid == id {
			p.order = append(p.order[:i], p.order[i+1:]...)
			break
		}
	}
	mark := p.Color(Green, "✓")
	if !ok {
		mark = p.Color(Red, "✗")
	}
	dur := ""
	if st != nil {
		dur = " " + formatDuration(time.Since(st.start))
	}
	if !p.atLineStart {
		p.write("\n")
	}
	p.write(mark + " " + p.Color(Dim, Sanitize(line)+dur) + "\n")
	if len(p.running) == 0 {
		p.stopTicker()
	}
}

func (p *Printer) SetDraft(s string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.draft = strings.ReplaceAll(s, "\n", "↵")
	p.clearFooter()
	p.drawFooter()
}

func (p *Printer) startTicker() {
	if p.ticking || !p.TTY {
		return
	}
	p.ticking = true
	p.stopTick = make(chan struct{})
	stop := p.stopTick
	go func() {
		defer CleanupOnPanic()
		t := time.NewTicker(100 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				p.mu.Lock()
				p.frame++
				if p.footerShown || p.atLineStart {
					p.clearFooter()
					p.drawFooter()
				}
				p.mu.Unlock()
			}
		}
	}()
}

func (p *Printer) stopTicker() {
	if p.ticking {
		close(p.stopTick)
		p.ticking = false
	}
	p.clearFooter()
	p.drawFooter()
}

func (p *Printer) SetPrompt(draw func(), final func() string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clearFooter()
	p.promptDraw, p.promptFinal = draw, final
	if draw == nil {
		return
	}
	if !p.atLineStart {
		_, _ = p.out.WriteString("\n")
		p.atLineStart = true
	}
	p.drawFooter()
}

func (p *Printer) RenderPrompt() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.promptDraw != nil {
		p.promptDraw()
		p.footerShown = true
	}
}

func (p *Printer) FinishPrompt() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.promptFinal != nil {
		_, _ = p.out.WriteString("\r\x1b[K" + p.promptFinal() + "\n")
	}
	p.promptDraw, p.promptFinal = nil, nil
	p.footerShown = false
	p.atLineStart = true
}

func (p *Printer) ClearFooter() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clearFooter()
}

func (p *Printer) Raw(s string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, _ = p.out.WriteString(s)
}

func formatDuration(d time.Duration) string {
	switch {
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	default:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
}

func lastLine(s string) string {
	s = strings.TrimRight(s, "\n\r ")
	if i := strings.LastIndexAny(s, "\n\r"); i >= 0 {
		s = s[i+1:]
	}
	return strings.TrimSpace(stripANSI(s))
}

func escEnd(s string, i int) int {
	j := i + 1
	if j >= len(s) {
		return j
	}
	switch s[j] {
	case '[':
		j++
		for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
			j++
		}
		return min(j+1, len(s))
	case ']', 'P', '_', '^':
		for j++; j < len(s); j++ {
			if s[j] == 0x07 {
				return j + 1
			}
			if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
				return j + 2
			}
		}
		return len(s)
	}
	return j + 1
}

func stripANSI(s string) string {
	return Sanitize(s)
}

func truncateVisible(s string, width int) string {
	if VisibleLen(s) <= width {
		return s
	}
	var b strings.Builder
	visible := 0
	for i := 0; i < len(s) && width > 0; {
		if s[i] == 0x1b {
			j := escEnd(s, i)
			b.WriteString(s[i:j])
			i = j
			continue
		}
		r, n := utf8.DecodeRuneInString(s[i:])
		if visible+runeWidth(r) > width-1 {
			b.WriteString("…")
			break
		}
		b.WriteRune(r)
		visible += runeWidth(r)
		i += n
	}
	return b.String() + Reset
}

func VisibleLen(s string) int {
	return displayWidth(stripANSI(s))
}

func (p *Printer) Width() int {
	return p.width()
}
