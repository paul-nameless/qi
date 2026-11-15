package agent

import (
	"strconv"
	"strings"
)

type skillMeta struct {
	Name                   string
	Description            string
	DisableModelInvocation bool
}

func ParseFrontmatter(data string) (skillMeta, string) {
	var m skillMeta
	if !strings.HasPrefix(data, "---") {
		return m, data
	}
	rest := strings.TrimLeft(data[3:], "\r\n")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return m, data
	}
	fields := frontmatterFields(rest[:end])
	m.Name = fields["name"]
	m.Description = fields["description"]
	m.DisableModelInvocation = strings.EqualFold(fields["disable-model-invocation"], "true")
	body := rest[end+4:]
	if i := strings.IndexByte(body, '\n'); i >= 0 {
		body = body[i+1:]
	} else {
		body = ""
	}
	return m, body
}

func frontmatterFields(text string) map[string]string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	broken := strings.HasSuffix(text, "\n")
	if broken {
		lines = lines[:len(lines)-1]
	}
	out := map[string]string{}
	for i := 0; i < len(lines); {
		key, rest, ok := topLevelKey(lines[i])
		i++
		start := i
		for i < len(lines) && (strings.TrimSpace(lines[i]) == "" || indentOf(lines[i]) > 0) {
			i++
		}
		if ok {
			out[key] = scalarValue(rest, lines[start:i], broken || i < len(lines))
		}
	}
	return out
}

func topLevelKey(line string) (string, string, bool) {
	if line == "" || indentOf(line) > 0 || line[0] == '#' || line[0] == '-' {
		return "", "", false
	}
	key, rest, ok := strings.Cut(line, ":")
	for ok && rest != "" && rest[0] != ' ' && rest[0] != '\t' {
		var more string
		more, rest, ok = strings.Cut(rest, ":")
		key += ":" + more
	}
	if !ok {
		return "", "", false
	}
	key = strings.TrimSpace(key)
	if len(key) >= 2 && (key[0] == '"' || key[0] == '\'') && key[len(key)-1] == key[0] {
		key = key[1 : len(key)-1]
	}
	return key, strings.TrimSpace(rest), true
}

func indentOf(line string) int {
	return len(line) - len(strings.TrimLeft(line, " \t"))
}

func scalarValue(first string, more []string, broken bool) string {
	switch {
	case first == "":
		return plainContinuation(more, broken)
	case first[0] == '|' || first[0] == '>':
		return blockScalar(first, more, broken)
	case first[0] == '"':
		return doubleQuoted(joinQuoted(first, more))
	case first[0] == '\'':
		return singleQuoted(joinQuoted(first, more))
	}
	return plainScalar(first, more)
}

func stripComment(s string) string {
	if strings.HasPrefix(s, "#") {
		return ""
	}
	if i := strings.Index(s, " #"); i >= 0 {
		s = s[:i]
	}
	if i := strings.Index(s, "\t#"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func plainContinuation(more []string, broken bool) string {
	for i, l := range more {
		t := strings.TrimSpace(l)
		switch {
		case t == "" || t[0] == '#':
			continue
		case t[0] == '"' || t[0] == '\'':
			return scalarValue(t, more[i+1:], broken)
		case t == "-" || strings.HasPrefix(t, "- ") || strings.Contains(t, ": ") || strings.HasSuffix(t, ":"):
			return ""
		}
		break
	}
	return plainScalar("", more)
}

func plainScalar(first string, more []string) string {
	first = stripComment(first)
	parts := []string{first}
	for _, l := range more {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "#") {
			break
		}
		parts = append(parts, stripComment(t))
	}
	s := foldLines(parts)
	switch s {
	case "~", "null", "Null", "NULL":
		return ""
	}
	return s
}

func foldLines(parts []string) string {
	var b strings.Builder
	empties := 0
	for _, p := range parts {
		if p == "" {
			empties++
			continue
		}
		if b.Len() > 0 {
			if empties == 0 {
				b.WriteByte(' ')
			} else {
				b.WriteString(strings.Repeat("\n", empties))
			}
		}
		b.WriteString(p)
		empties = 0
	}
	return b.String()
}

func blockScalar(header string, more []string, broken bool) string {
	literal := header[0] == '|'
	chomp, indent := byte(0), 0
	for _, c := range stripComment(header[1:]) {
		switch {
		case c == '-' || c == '+':
			chomp = byte(c)
		case c >= '1' && c <= '9':
			indent = int(c - '0')
		}
	}
	if indent == 0 {
		for _, l := range more {
			if strings.TrimSpace(l) != "" {
				indent = len(l) - len(strings.TrimLeft(l, " "))
				break
			}
		}
	}
	lines := make([]string, len(more))
	for i, l := range more {
		if len(l) >= indent && strings.TrimSpace(l[:indent]) == "" {
			lines[i] = l[indent:]
		} else {
			lines[i] = strings.TrimLeft(l, " ")
		}
	}
	trailing := 0
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
		trailing++
	}
	text := foldBlock(lines)
	if literal {
		text = strings.Join(lines, "\n")
	}
	return chompBlock(text, chomp, trailing, broken)
}

func foldBlock(lines []string) string {
	var b strings.Builder
	empties, started, prevNormal := 0, false, false
	for _, l := range lines {
		if l == "" {
			empties++
			continue
		}
		normal := l[0] != ' ' && l[0] != '\t'
		switch {
		case !started:
			b.WriteString(strings.Repeat("\n", empties))
		case normal && prevNormal && empties == 0:
			b.WriteByte(' ')
		case normal && prevNormal:
			b.WriteString(strings.Repeat("\n", empties))
		default:
			b.WriteString(strings.Repeat("\n", empties+1))
		}
		b.WriteString(l)
		empties, started, prevNormal = 0, true, normal
	}
	return b.String()
}

func chompBlock(text string, chomp byte, trailing int, broken bool) string {
	if chomp == '-' {
		return text
	}
	ended := text != "" && (trailing > 0 || broken)
	if chomp != '+' {
		if ended {
			return text + "\n"
		}
		return text
	}
	breaks := trailing
	if !broken && trailing > 0 {
		breaks--
	}
	if ended {
		breaks++
	}
	return text + strings.Repeat("\n", breaks)
}

func joinQuoted(first string, more []string) string {
	return strings.Join(append([]string{first}, more...), "\n")
}

func quotedLines(s string) string {
	lines := strings.Split(s, "\n")
	parts := make([]string, len(lines))
	for i, l := range lines {
		if i < len(lines)-1 {
			l = strings.TrimRight(l, " \t")
		}
		if i > 0 {
			l = strings.TrimLeft(l, " \t")
		}
		parts[i] = l
	}
	return foldLines(parts)
}

func singleQuoted(s string) string {
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		if s[i] != '\'' {
			b.WriteByte(s[i])
			continue
		}
		if i+1 < len(s) && s[i+1] == '\'' {
			b.WriteByte('\'')
			i++
			continue
		}
		break
	}
	return quotedLines(b.String())
}

func doubleQuoted(s string) string {
	var b strings.Builder
	var seg strings.Builder
	flush := func() {
		b.WriteString(quotedLines(seg.String()))
		seg.Reset()
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if c == '"' {
			break
		}
		if c != '\\' || i+1 >= len(s) {
			seg.WriteByte(c)
			continue
		}
		i++
		if s[i] == '\n' {
			for i+1 < len(s) && (s[i+1] == ' ' || s[i+1] == '\t') {
				i++
			}
			continue
		}
		flush()
		n := escapeValue(s[i:], &b)
		i += n - 1
	}
	flush()
	return b.String()
}

var simpleEscapes = map[byte]string{
	'0': "\x00", 'a': "\a", 'b': "\b", 't': "\t", '\t': "\t", 'n': "\n", 'v': "\v", 'f': "\f", 'r': "\r", 'e': "\x1b",
	' ': " ", '"': "\"", '/': "/", '\\': "\\", 'N': "\u0085", '_': " ", 'L': " ", 'P': " ",
}

func escapeValue(s string, b *strings.Builder) int {
	if v, ok := simpleEscapes[s[0]]; ok {
		b.WriteString(v)
		return 1
	}
	width := map[byte]int{'x': 2, 'u': 4, 'U': 8}[s[0]]
	if width > 0 && len(s) > width {
		if r, err := strconv.ParseUint(s[1:1+width], 16, 32); err == nil {
			b.WriteRune(rune(r))
			return 1 + width
		}
	}
	b.WriteByte('\\')
	b.WriteByte(s[0])
	return 1
}
