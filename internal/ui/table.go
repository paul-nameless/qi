package ui

import "strings"

type align int

const (
	alignLeft align = iota
	alignCenter
	alignRight
)

func renderTable(lines []string, width int) string {
	if len(lines) < 2 || !isSeparator(lines[1]) {
		return rawLines(lines)
	}
	header := splitRow(lines[0])
	aligns := parseAligns(splitRow(lines[1]), len(header))
	rows := [][]string{styleCells(header, true)}
	for _, l := range lines[2:] {
		cells := splitRow(l)
		if len(cells) > len(header) {
			return rawLines(lines)
		}
		rows = append(rows, styleCells(fitCells(cells, len(header)), false))
	}
	widths := columnWidths(rows, len(header))
	total := 3 * (len(widths) - 1)
	for _, w := range widths {
		total += w
	}
	if width > 0 && total > width {
		return rawLines(lines)
	}
	sep := Dim + " │" + Reset + " "
	var b strings.Builder
	for i, row := range rows {
		cells := make([]string, len(row))
		for j, c := range row {
			cells[j] = pad(c, widths[j], aligns[j])
		}
		b.WriteString(strings.TrimRight(strings.Join(cells, sep), " ") + "\n")
		if i == 0 {
			b.WriteString(ruleLine(widths) + "\n")
		}
	}
	return b.String()
}

func rawLines(lines []string) string {
	return strings.Join(lines, "\n") + "\n"
}

func isSeparator(line string) bool {
	cells := splitRow(line)
	for _, c := range cells {
		c = strings.TrimSuffix(strings.TrimPrefix(c, ":"), ":")
		if c == "" || strings.Trim(c, "-") != "" {
			return false
		}
	}
	return len(cells) > 0
}

func splitRow(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	if strings.HasSuffix(line, "|") && !strings.HasSuffix(line, `\|`) {
		line = line[:len(line)-1]
	}
	var cells []string
	var cur strings.Builder
	for i := 0; i < len(line); i++ {
		switch {
		case line[i] == '\\' && i+1 < len(line) && line[i+1] == '|':
			cur.WriteByte('|')
			i++
		case line[i] == '|':
			cells = append(cells, strings.TrimSpace(cur.String()))
			cur.Reset()
		default:
			cur.WriteByte(line[i])
		}
	}
	return append(cells, strings.TrimSpace(cur.String()))
}

func parseAligns(cells []string, n int) []align {
	aligns := make([]align, n)
	for i := 0; i < n && i < len(cells); i++ {
		c := cells[i]
		switch {
		case strings.HasPrefix(c, ":") && strings.HasSuffix(c, ":"):
			aligns[i] = alignCenter
		case strings.HasSuffix(c, ":"):
			aligns[i] = alignRight
		}
	}
	return aligns
}

func fitCells(cells []string, n int) []string {
	return append(cells, make([]string, n-len(cells))...)
}

func styleCells(cells []string, header bool) []string {
	out := make([]string, len(cells))
	for i, c := range cells {
		m := markdown{line: linePlain}
		if header {
			m.line = lineHeading
			m.style()
		}
		prefix := m.out.String()
		out[i] = prefix + m.feed(c) + m.flush()
	}
	return out
}

func columnWidths(rows [][]string, n int) []int {
	widths := make([]int, n)
	for _, row := range rows {
		for j, c := range row {
			widths[j] = max(widths[j], VisibleLen(c))
		}
	}
	return widths
}

func pad(s string, width int, a align) string {
	gap := width - VisibleLen(s)
	switch a {
	case alignRight:
		return strings.Repeat(" ", gap) + s
	case alignCenter:
		return strings.Repeat(" ", gap/2) + s + strings.Repeat(" ", gap-gap/2)
	}
	return s + strings.Repeat(" ", gap)
}

func ruleLine(widths []int) string {
	parts := make([]string, len(widths))
	for i, w := range widths {
		parts[i] = strings.Repeat("─", w)
	}
	return Dim + strings.Join(parts, "─┼─") + Reset
}
