package ui

import "strings"

type lineKind int

const (
	linePending lineKind = iota
	linePlain
	lineHeading
	lineBullet
	lineFence
	lineCode
	lineTable
)

type markdown struct {
	out     strings.Builder
	pending string
	line    lineKind
	fence   int
	bold    bool
	code    bool
	link    bool
	prev    byte
	cur     string
	width   int
	base    string
	row     strings.Builder
	table   []string
}

func (m *markdown) feed(s string) string {
	m.out.Reset()
	m.pending += s
	for m.pending != "" {
		n := m.step(false)
		if n == 0 {
			break
		}
		m.pending = m.pending[n:]
	}
	return m.out.String()
}

func (m *markdown) flush() string {
	m.out.Reset()
	for m.pending != "" {
		m.pending = m.pending[m.step(true):]
	}
	if m.line == lineTable {
		m.endRow()
	}
	m.endTable()
	m.endLine()
	m.fence = 0
	return m.out.String()
}

func (m *markdown) step(final bool) int {
	if m.line == lineTable {
		return m.tableRow()
	}
	if m.pending[0] == '\n' {
		m.endTable()
		m.endLine()
		m.out.WriteByte('\n')
		return 1
	}
	switch m.line {
	case linePending:
		return m.startLine(final)
	case lineFence, lineCode:
		n := strings.IndexByte(m.pending, '\n')
		if n < 0 {
			n = len(m.pending)
		}
		m.out.WriteString(m.pending[:n])
		return n
	}
	return m.inline(final)
}

func (m *markdown) startLine(final bool) int {
	kind, skip, prefix, ok := classify(m.pending, m.fence, final)
	if !ok {
		return 0
	}
	if kind != lineTable {
		m.endTable()
	}
	if kind == lineFence {
		m.toggleFence()
	}
	m.line = kind
	m.style()
	m.out.WriteString(prefix)
	if skip == 0 {
		return m.step(final)
	}
	return skip
}

func (m *markdown) toggleFence() {
	if m.fence > 0 {
		m.fence = 0
		return
	}
	body, _ := listBody(strings.TrimLeft(m.pending, " \t"))
	m.fence = backticks(body)
}

func listBody(rest string) (string, bool) {
	if rest == "" {
		return "", true
	}
	n := 0
	if strings.IndexByte("-*+", rest[0]) >= 0 {
		n = 1
	} else {
		n = len(rest) - len(strings.TrimLeft(rest, "0123456789"))
		if n > 0 && n < len(rest) && rest[n] == '.' {
			n++
		} else if n < len(rest) {
			n = 0
		}
	}
	switch {
	case n == 0:
		return rest, false
	case n == len(rest):
		return "", true
	case rest[n] != ' ':
		return rest, false
	}
	return strings.TrimLeft(rest[n:], " "), false
}

func backticks(s string) int {
	return len(s) - len(strings.TrimLeft(s, "`"))
}

func classify(s string, fence int, final bool) (lineKind, int, string, bool) {
	line, _, hasNL := strings.Cut(s, "\n")
	final = final || hasNL
	rest := strings.TrimLeft(line, " \t")
	indent := len(line) - len(rest)
	body, partial := listBody(rest)
	if fence > 0 {
		body, partial = rest, false
	}
	if !final && (partial || strings.Trim(body, "` ") == "") {
		return linePending, 0, "", false
	}
	if fence > 0 {
		if backticks(rest) >= fence && strings.TrimRight(rest, "` ") == "" {
			return lineFence, 0, "", true
		}
		return lineCode, 0, "", true
	}
	if backticks(body) >= 3 {
		return lineFence, 0, "", true
	}
	if strings.HasPrefix(rest, "|") {
		return lineTable, 0, "", true
	}
	if hashes := len(rest) - len(strings.TrimLeft(rest, "#")); hashes > 0 && hashes <= 6 {
		switch after := rest[hashes:]; {
		case after == "" && !final:
			return linePending, 0, "", false
		case strings.HasPrefix(after, " "):
			return lineHeading, indent + hashes + 1, "", true
		}
	}
	if len(rest) > 1 && strings.IndexByte("-*+", rest[0]) >= 0 && rest[1] == ' ' {
		return lineBullet, indent + 2, line[:indent] + "• ", true
	}
	return linePlain, 0, "", true
}

func (m *markdown) inline(final bool) int {
	s := m.pending
	switch {
	case s[0] == '`':
		return m.toggleCode(final)
	case strings.HasPrefix(s, "**") && !m.code:
		return m.toggleBold(final)
	case s == "*" && !final && !m.code:
		return 0
	case s[0] == '[' && !m.code && !m.link:
		return m.openLink(final)
	case s[0] == ']' && m.link && !m.code:
		m.link = false
		m.style()
		m.text("]")
		return 1
	}
	n := len(s)
	if i := strings.IndexAny(s[1:], "`*\n[]"); i >= 0 {
		n = i + 1
	}
	m.text(s[:n])
	return n
}

func (m *markdown) toggleCode(final bool) int {
	if m.code {
		m.code = false
		m.style()
		return 1
	}
	n := backticks(m.pending)
	if n == len(m.pending) && !final {
		return 0
	}
	if n > 1 {
		m.text(m.pending[:n])
		return n
	}
	switch closed(m.pending[1:], "`", final) {
	case 0:
		return 0
	case -1:
		m.text("`")
		return 1
	}
	m.code = true
	m.style()
	return 1
}

func (m *markdown) toggleBold(final bool) int {
	s := m.pending
	if strings.Trim(s, "*") == "" && !final {
		return 0
	}
	if len(s) > 2 && s[2] == '*' {
		n := len(s) - len(strings.TrimLeft(s, "*"))
		m.text(s[:n])
		return n
	}
	if m.bold && closes(m.prev, s[2:]) {
		m.bold = false
		m.style()
		return 2
	}
	if !m.bold && len(s) > 2 && s[2] != ' ' && !isAlnum(m.prev) {
		switch closed(s[2:], "**", final) {
		case 0:
			return 0
		case 1:
			m.bold = true
			m.style()
			return 2
		}
	}
	m.text("**")
	return 2
}

const (
	maxLinkText = 500
	maxLinkURL  = 2000
)

func (m *markdown) openLink(final bool) int {
	switch linkAhead(m.pending, final) {
	case 0:
		return 0
	case 1:
		m.text("[")
		m.link = true
		m.style()
		return 1
	}
	m.text("[")
	return 1
}

func linkAhead(s string, final bool) int {
	line, _, hasNL := strings.Cut(s, "\n")
	done := hasNL || final
	end := strings.IndexByte(line, ']')
	switch {
	case end < 0 || end > maxLinkText:
		if done || len(line) > maxLinkText {
			return -1
		}
		return 0
	case end == 1:
		return -1
	case end+1 == len(line):
		if done {
			return -1
		}
		return 0
	case line[end+1] != '(':
		return -1
	}
	url := line[end+2:]
	close := strings.IndexByte(url, ')')
	switch {
	case close > 0 && close <= maxLinkURL:
		return 1
	case close == 0 || close > maxLinkURL || done || len(url) > maxLinkURL:
		return -1
	}
	return 0
}

const maxInlineSpan = 1000

func closed(s, marker string, final bool) int {
	line, _, hasNL := strings.Cut(s, "\n")
	for i := 1; i < min(len(line), maxInlineSpan); i++ {
		if !strings.HasPrefix(line[i:], marker) {
			continue
		}
		if i+len(marker) == len(line) && !hasNL && !final {
			return 0
		}
		if marker == "`" || closes(line[i-1], line[i+len(marker):]) {
			return 1
		}
	}
	switch {
	case len(line) >= maxInlineSpan:
		return 1
	case hasNL || final:
		return -1
	}
	return 0
}

func closes(prev byte, after string) bool {
	return prev != ' ' && (after == "" || !isAlnum(after[0]))
}

func isAlnum(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func (m *markdown) text(s string) {
	m.out.WriteString(s)
	m.prev = s[len(s)-1]
}

func (m *markdown) style() {
	codes := m.base
	switch {
	case m.line == lineFence:
		codes += Dim
	case m.line == lineCode || m.code:
		codes += Cyan
	}
	if m.bold || m.line == lineHeading {
		codes += Bold
	}
	if m.link {
		codes += Link
	}
	if codes == m.cur {
		return
	}
	m.out.WriteString(Reset + codes)
	m.cur = codes
}

func (m *markdown) endLine() {
	if m.cur != "" {
		m.out.WriteString(Reset)
	}
	m.line, m.bold, m.code, m.link, m.cur, m.prev = linePending, false, false, false, "", 0
}

func (m *markdown) tableRow() int {
	n := strings.IndexByte(m.pending, '\n')
	if n < 0 {
		m.row.WriteString(m.pending)
		return len(m.pending)
	}
	m.row.WriteString(m.pending[:n])
	m.endRow()
	m.line = linePending
	return n + 1
}

func (m *markdown) endRow() {
	m.table = append(m.table, m.row.String())
	m.row.Reset()
}

func (m *markdown) endTable() {
	if len(m.table) > 0 {
		t := renderTable(m.table, m.width)
		if m.base != "" {
			t = m.base + strings.ReplaceAll(t, Reset, Reset+m.base) + Reset
		}
		m.out.WriteString(t)
		m.table = nil
	}
}

func Markdown(s string, width int) string {
	m := markdown{width: width}
	return m.feed(s) + m.flush()
}
