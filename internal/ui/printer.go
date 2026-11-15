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
	promptDraw  func(width, height int) Frame
	promptFinal func(width int) string
	shown       shownFrame
	md          markdown
}

type shownFrame struct {
	widths   []int
	row, col int
	term     int
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

func (p *Printer) size() (int, int) {
	ws, err := unix.IoctlGetWinsize(int(p.out.Fd()), unix.TIOCGWINSZ)
	if err != nil || ws.Col == 0 {
		return 80, 24
	}
	if ws.Row == 0 {
		return int(ws.Col), 24
	}
	return int(ws.Col), int(ws.Row)
}

func (p *Printer) width() int {
	w, _ := p.size()
	return w
}

func (p *Printer) clearFooter() {
	_, _ = p.out.WriteString(p.clearSeq())
}

func (p *Printer) clearSeq() string {
	if !p.footerShown {
		return ""
	}
	p.footerShown = false
	w := p.width()
	seq := "\r" + cursorMove(p.shown.above(w), 'A') + "\x1b[2K"
	if p.shown.height(w) > 1 {
		seq += "\x1b[1B\x1b[J\x1b[1A"
	}
	return seq
}

func (s shownFrame) above(width int) int {
	n := s.span(s.widths[:s.row], width)
	if width < s.term && width > 0 {
		n += min(s.col/width, s.reflowed(s.widths[s.row], width)-1)
	}
	return n
}

func (s shownFrame) height(width int) int {
	return s.span(s.widths, width)
}

func (s shownFrame) span(widths []int, width int) int {
	n := 0
	for _, w := range widths {
		n += s.reflowed(w, width)
	}
	return n
}

func (s shownFrame) reflowed(w, width int) int {
	if width >= s.term || width <= 0 {
		return 1
	}
	return max(1, (w+width-1)/width)
}

func (p *Printer) frameSeq(f Frame, width int) string {
	if len(f.Rows) == 0 {
		return ""
	}
	p.shown = shownFrame{row: f.Row, col: f.Col, term: width}
	for _, r := range f.Rows {
		p.shown.widths = append(p.shown.widths, VisibleLen(r))
	}
	p.footerShown = true
	seq := "\r" + strings.Join(f.Rows, "\r\n") + cursorMove(len(f.Rows)-1-f.Row, 'A') + "\r"
	return seq + cursorMove(f.Col, 'C')
}

func (p *Printer) footerFrame(width, height int) Frame {
	if p.promptDraw != nil {
		return p.promptDraw(width, height)
	}
	status := p.statusText()
	if p.draft == "" {
		if status == "" {
			return Frame{}
		}
		row := truncateVisible(status, width-1)
		return Frame{Rows: []string{row}, Col: VisibleLen(row)}
	}
	return p.draftFrame(status, width, height)
}

func (p *Printer) draftFrame(status string, width, height int) Frame {
	prompt := p.Color(Cyan, "› ")
	buf := []rune(p.draft)
	l := layoutRows(VisibleLen(prompt), width, buf, len(buf))
	limit := maxRows(height - 1)
	f := l.frame(prompt, buf, scrollTop(0, l.row, len(l.rows), limit), limit)
	tail := p.Color(Dim, "(enter to queue)")
	if status != "" {
		tail += p.Color(Dim, "  ·  ") + status
	}
	last := len(f.Rows) - 1
	if VisibleLen(f.Rows[last])+2+VisibleLen(tail) <= width-1 {
		f.Rows[last] += "  " + tail
		return f
	}
	f.Rows = append(f.Rows, truncateVisible(tail, width-1))
	return f
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
	_, _ = p.out.WriteString(p.drawSeq())
}

func (p *Printer) drawSeq() string {
	if !p.TTY || !p.atLineStart {
		return ""
	}
	w, h := p.size()
	return p.frameSeq(p.footerFrame(w, h), w)
}

func (p *Printer) redraw() {
	clear := p.clearSeq()
	_, _ = p.out.WriteString(clear + p.drawSeq())
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
	p.redraw()
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
	p.draft = s
	p.redraw()
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
					p.redraw()
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
	p.redraw()
}

func (p *Printer) SetPrompt(draw func(width, height int) Frame, final func(width int) string) {
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
	if p.promptDraw == nil {
		return
	}
	clear := p.clearSeq()
	w, h := p.size()
	_, _ = p.out.WriteString(clear + p.frameSeq(p.promptDraw(w, h), w))
}

func (p *Printer) Redraw() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.footerShown {
		p.redraw()
	}
}

func (p *Printer) FinishPrompt() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.promptFinal != nil {
		seq := "\r\x1b[K"
		if p.footerShown {
			seq = p.clearSeq()
		}
		_, _ = p.out.WriteString(seq + p.promptFinal(p.width()) + "\n")
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

func (p *Printer) EndLine() {
	if !p.mu.TryLock() {
		_, _ = p.out.WriteString("\n")
		return
	}
	defer p.mu.Unlock()
	if p.footerShown || !p.atLineStart {
		below := ""
		if p.footerShown {
			below = cursorMove(len(p.shown.widths)-1-p.shown.row, 'B')
		}
		_, _ = p.out.WriteString(below + "\n")
		p.footerShown, p.atLineStart = false, true
	}
}

func (p *Printer) ClearScreen() {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, _ = p.out.WriteString("\x1b[2J\x1b[H")
	p.footerShown = false
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
