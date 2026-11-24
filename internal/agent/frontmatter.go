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
	before, after, ok := strings.Cut(rest, "\n---")
	if !ok {
		return m, data
	}
	fields := frontmatterFields(before)
	m.Name = fields["name"]
	m.Description = fields["description"]
	m.DisableModelInvocation = strings.EqualFold(fields["disable-model-invocation"], "true")
	body := after
	if i := strings.IndexByte(body, '\n'); i >= 0 {
		body = body[i+1:]
	} else {
		body = ""
	}
	return m, body
}

func frontmatterFields(text string) map[string]string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	out := map[string]string{}
	for i := 0; i < len(lines); {
		key, first, ok := topLevelKey(lines[i])
		i++
		start := i
		for i < len(lines) && (strings.TrimSpace(lines[i]) == "" || indentOf(lines[i]) > 0) {
			i++
		}
		if ok {
			out[key] = strings.Join(strings.Fields(scalarValue(first, lines[start:i])), " ")
		}
	}
	return out
}

func topLevelKey(line string) (string, string, bool) {
	if line == "" || indentOf(line) > 0 || line[0] == '#' || line[0] == '-' {
		return "", "", false
	}
	i := strings.Index(line+" ", ": ")
	if i < 0 {
		return "", "", false
	}
	key := strings.Trim(line[:i], `"'`)
	return key, strings.TrimSpace(line[min(i+1, len(line)):]), true
}

func indentOf(line string) int {
	return len(line) - len(strings.TrimLeft(line, " \t"))
}

func scalarValue(first string, more []string) string {
	switch {
	case first == "":
		return nextLineValue(more)
	case first[0] == '|' || first[0] == '>':
		return strings.Join(more, " ")
	case first[0] == '"' || first[0] == '\'':
		return quoted(first, more)
	}
	return plainScalar(first, more)
}

func quoted(first string, more []string) string {
	s := strings.Join(append([]string{first}, more...), " ")
	unquote := doubleQuoted
	if s[0] == '\'' {
		unquote = singleQuoted
	}
	v, end := unquote(s)
	if rest := strings.TrimSpace(s[end:]); rest != "" && rest[0] != '#' {
		return plainScalar(first, more)
	}
	return v
}

func nextLineValue(more []string) string {
	for i, l := range more {
		t := strings.TrimSpace(l)
		switch {
		case t == "" || t[0] == '#':
			continue
		case t[0] == '"' || t[0] == '\'':
			return scalarValue(t, more[i+1:])
		case t == "-" || strings.HasPrefix(t, "- ") || strings.Contains(t, ": ") || strings.HasSuffix(t, ":"):
			return ""
		}
		break
	}
	return plainScalar("", more)
}

func plainScalar(first string, more []string) string {
	parts := []string{stripComment(first)}
	for _, l := range more {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "#") {
			break
		}
		parts = append(parts, stripComment(t))
	}
	s := strings.Join(parts, " ")
	switch strings.TrimSpace(s) {
	case "~", "null", "Null", "NULL":
		return ""
	}
	return s
}

func stripComment(s string) string {
	if strings.HasPrefix(s, "#") {
		return ""
	}
	for _, sep := range []string{" #", "\t#"} {
		if i := strings.Index(s, sep); i >= 0 {
			s = s[:i]
		}
	}
	return s
}

func singleQuoted(s string) (string, int) {
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		if s[i] == '\'' {
			if i+1 >= len(s) || s[i+1] != '\'' {
				return b.String(), i + 1
			}
			i++
		}
		b.WriteByte(s[i])
	}
	return b.String(), len(s)
}

var hexWidth = map[byte]int{'x': 2, 'u': 4, 'U': 8}

var escapes = map[byte]string{'n': "\n", 't': "\t", 'r': "\r", '"': "\"", '\\': "\\", '/': "/", '0': ""}

func doubleQuoted(s string) (string, int) {
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		switch {
		case s[i] == '"':
			return b.String(), i + 1
		case s[i] != '\\' || i+1 >= len(s):
			b.WriteByte(s[i])
		default:
			i += unescape(&b, s[i+1:])
		}
	}
	return b.String(), len(s)
}

func unescape(b *strings.Builder, s string) int {
	if v, ok := escapes[s[0]]; ok {
		b.WriteString(v)
		return 1
	}
	if n := hexWidth[s[0]]; n > 0 && len(s) > n {
		if r, err := strconv.ParseUint(s[1:1+n], 16, 32); err == nil {
			b.WriteRune(rune(r))
			return 1 + n
		}
	}
	b.WriteByte(s[0])
	return 1
}
