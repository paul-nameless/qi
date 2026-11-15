package ui

import (
	"fmt"
	"strings"
)

type Frame struct {
	Rows     []string
	Row, Col int
}

type visualRow struct {
	start, end int
	soft       bool
}

type textLayout struct {
	rows     []visualRow
	row, col int
}

func cellWidth(buf []rune, i int) int {
	if buf[i] == 0xFE0F && i > 0 && runeWidth(buf[i-1]) == 1 {
		return 1
	}
	return runeWidth(buf[i])
}

func clusterWidth(buf []rune, i int) int {
	w := cellWidth(buf, i)
	for j := i + 1; j < len(buf) && buf[j] != '\n' && runeWidth(buf[j]) == 0; j++ {
		w += cellWidth(buf, j)
	}
	return w
}

func spanWidth(buf []rune, from, to int) int {
	w := 0
	for i := from; i < to; i++ {
		w += cellWidth(buf, i)
	}
	return w
}

func layoutRows(pw, width int, buf []rune, pos int) textLayout {
	pos = min(max(pos, 0), len(buf))
	avail := max(width-pw, 1)
	var rows []visualRow
	start, w, soft := 0, 0, false
	for i, r := range buf {
		if r == '\n' {
			rows = closeLine(rows, visualRow{start, i, soft}, w >= avail && pos == i)
			start, w, soft = i+1, 0, false
			continue
		}
		if runeWidth(r) > 0 && w > 0 && w+clusterWidth(buf, i) > avail {
			rows = append(rows, visualRow{start, i, soft})
			start, w, soft = i, 0, true
		}
		w += cellWidth(buf, i)
	}
	rows = closeLine(rows, visualRow{start, len(buf), soft}, w >= avail && pos == len(buf))
	return placeCursor(rows, pw, buf, pos)
}

func closeLine(rows []visualRow, last visualRow, cursorAfter bool) []visualRow {
	rows = append(rows, last)
	if cursorAfter {
		rows = append(rows, visualRow{last.end, last.end, true})
	}
	return rows
}

func placeCursor(rows []visualRow, pw int, buf []rune, pos int) textLayout {
	row := 0
	for i, r := range rows {
		if r.start <= pos {
			row = i
		}
	}
	return textLayout{rows: rows, row: row, col: pw + spanWidth(buf, rows[row].start, pos)}
}

func rowText(buf []rune, r visualRow) string {
	out := make([]rune, 0, r.end-r.start)
	for _, c := range buf[r.start:r.end] {
		if c < 0x20 || c == 0x7f {
			c = ' '
		}
		out = append(out, c)
	}
	return string(out)
}

func scrollTop(top, cursor, total, limit int) int {
	if total <= limit {
		return 0
	}
	top = min(top, cursor)
	if cursor >= top+limit {
		top = cursor - limit + 1
	}
	return min(max(top, 0), total-limit)
}

func maxRows(height int) int {
	return max(height-3, 1)
}

func (l textLayout) frame(prompt string, buf []rune, top, limit int) Frame {
	indent := strings.Repeat(" ", VisibleLen(prompt))
	end := min(top+limit, len(l.rows))
	f := Frame{Row: l.row - top, Col: l.col}
	for i := top; i < end; i++ {
		lead := indent
		if i == 0 {
			lead = prompt
		}
		f.Rows = append(f.Rows, lead+rowText(buf, l.rows[i]))
	}
	return f
}

func echoText(prompt string, width int, buf []rune) string {
	indent := strings.Repeat(" ", VisibleLen(prompt))
	l := layoutRows(VisibleLen(prompt), width, buf, 0)
	var b strings.Builder
	b.WriteString(prompt)
	for i, r := range l.rows {
		if i > 0 {
			b.WriteString("\n" + indent)
		}
		b.WriteString(rowText(buf, r))
	}
	return b.String()
}

func moveVertical(pw, width int, buf []rune, pos, dir, goal int) (int, int, bool) {
	l := layoutRows(pw, width, buf, pos)
	target := l.row + dir
	if target < 0 || target >= len(l.rows) {
		return pos, goal, false
	}
	if goal < 0 {
		goal = l.col - pw
	}
	last := l.rows[target].end
	if target+1 < len(l.rows) && l.rows[target+1].soft && last > l.rows[target].start {
		last--
	}
	return posAtColumn(buf, l.rows[target].start, last, goal), goal, true
}

func posAtColumn(buf []rune, start, last, goal int) int {
	i, w := start, 0
	for i < last && w+cellWidth(buf, i) <= goal {
		w += cellWidth(buf, i)
		i++
	}
	for i < last && i > start && runeWidth(buf[i]) == 0 {
		i++
	}
	return i
}

func lineStart(buf []rune, pos int) int {
	for pos > 0 && buf[pos-1] != '\n' {
		pos--
	}
	return pos
}

func lineEnd(buf []rune, pos int) int {
	for pos < len(buf) && buf[pos] != '\n' {
		pos++
	}
	return pos
}

func cursorMove(n int, dir byte) string {
	if n <= 0 {
		return ""
	}
	return fmt.Sprintf("\x1b[%d%c", n, dir)
}
