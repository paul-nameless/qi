package jsvm

import (
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type tok uint8

const (
	tEOF tok = iota
	tIdent
	tKeyword
	tNum
	tStr
	tTemplate
	tRegex
	tPrivate
	tPunct
)

type token struct {
	t       tok
	s       string
	raw     string
	n       float64
	pos     int
	end     int
	nl      bool
	tmplEnd bool
	escaped bool
	bad     string
}

var keywords = map[string]bool{
	"break": true, "case": true, "catch": true, "class": true, "const": true, "continue": true,
	"debugger": true, "default": true, "delete": true, "do": true, "else": true, "export": true,
	"extends": true, "finally": true, "for": true, "function": true, "if": true, "import": true,
	"in": true, "instanceof": true, "new": true, "return": true, "super": true, "switch": true,
	"this": true, "throw": true, "try": true, "typeof": true, "var": true, "void": true,
	"while": true, "with": true, "null": true, "true": true, "false": true, "let": true,
	"yield": true, "enum": true,
}

type lexer struct {
	src string
	pos int
	err *syntaxErr
}

type syntaxErr struct {
	msg string
	pos int
}

func (l *lexer) fail(pos int, msg string) {
	if l.err == nil {
		l.err = &syntaxErr{msg: msg, pos: pos}
	}
	panic(l.err)
}

func (l *lexer) skipSpace() bool {
	nl := false
	src := l.src
	for l.pos < len(src) {
		c := src[l.pos]
		switch {
		case c == '\n':
			nl = true
			l.pos++
		case c == ' ' || c == '\t' || c == '\r' || c == '\v' || c == '\f':
			l.pos++
		case c == '/' && l.pos+1 < len(src) && src[l.pos+1] == '/':
			for l.pos < len(src) && src[l.pos] != '\n' {
				l.pos++
			}
		case c == '/' && l.pos+1 < len(src) && src[l.pos+1] == '*':
			end := strings.Index(src[l.pos+2:], "*/")
			if end < 0 {
				l.fail(l.pos, "Unterminated comment")
			}
			if strings.ContainsAny(src[l.pos:l.pos+2+end], "\n  ") {
				nl = true
			}
			l.pos += end + 4
		case c == '#' && l.pos == 0 && l.pos+1 < len(src) && src[1] == '!':
			for l.pos < len(src) && src[l.pos] != '\n' {
				l.pos++
			}
		case c >= 0x80:
			r, size := utf8.DecodeRuneInString(src[l.pos:])
			if r == 0x2028 || r == 0x2029 {
				nl = true
			} else if !isJSSpace(r) {
				return nl
			}
			l.pos += size
		default:
			return nl
		}
	}
	return nl
}

func isIDStart(r rune) bool {
	return r == '$' || r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= 0x80 && unicode.IsLetter(r))
}

func isIDPart(r rune) bool {
	return isIDStart(r) || (r >= '0' && r <= '9') || (r >= 0x80 && (unicode.IsDigit(r) || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r) || unicode.Is(unicode.Pc, r) || r == 0x200C || r == 0x200D))
}

var puncts = []string{
	">>>=", "...", "===", "!==", "**=", "<<=", ">>=", ">>>", "&&=", "||=", "??=",
	"=>", "==", "!=", "<=", ">=", "&&", "||", "??", "?.", "++", "--", "+=", "-=", "*=", "/=", "%=",
	"&=", "|=", "^=", "<<", ">>", "**",
	"{", "}", "(", ")", "[", "]", ";", ",", "<", ">", "+", "-", "*", "/", "%", "&", "|", "^",
	"!", "~", "?", ":", "=", ".", "@",
}

func (l *lexer) next() token {
	nl := l.skipSpace()
	t := l.scan()
	t.nl = nl
	return t
}

func (l *lexer) scan() token {
	src := l.src
	start := l.pos
	if l.pos >= len(src) {
		return token{t: tEOF, pos: start, end: start}
	}
	c := src[l.pos]
	switch {
	case c == '"' || c == '\'':
		return l.scanString(c)
	case c == '`':
		l.pos++
		return l.scanTemplate(start)
	case c >= '0' && c <= '9' || (c == '.' && l.pos+1 < len(src) && src[l.pos+1] >= '0' && src[l.pos+1] <= '9'):
		return l.scanNumber()
	case c == '#':
		l.pos++
		id := l.scanIdentName()
		if id == "" {
			l.fail(start, "Invalid or unexpected token")
		}
		return token{t: tPrivate, s: id, pos: start, end: l.pos}
	}
	r, _ := utf8.DecodeRuneInString(src[l.pos:])
	if isIDStart(r) || c == '\\' {
		escaped := c == '\\' || strings.Contains(src[l.pos:min(len(src), l.pos+64)], "\\")
		id := l.scanIdentName()
		t := token{t: tIdent, s: id, pos: start, end: l.pos, escaped: escaped && l.pos-start != len(id)}
		if keywords[id] && !t.escaped {
			t.t = tKeyword
		}
		return t
	}
	for _, p := range puncts {
		if strings.HasPrefix(src[l.pos:], p) {
			if p == "?." && l.pos+2 < len(src) && src[l.pos+2] >= '0' && src[l.pos+2] <= '9' {
				continue
			}
			l.pos += len(p)
			return token{t: tPunct, s: p, pos: start, end: l.pos}
		}
	}
	l.fail(start, "Invalid or unexpected token")
	return token{}
}

func (l *lexer) scanIdentName() string {
	src := l.src
	start := l.pos
	var b strings.Builder
	escaped := false
	for l.pos < len(src) {
		c := src[l.pos]
		if c == '\\' {
			if !escaped {
				b.WriteString(src[start:l.pos])
				escaped = true
			}
			if l.pos+1 >= len(src) || src[l.pos+1] != 'u' {
				l.fail(l.pos, "Invalid or unexpected token")
			}
			l.pos += 2
			r := l.scanUnicodeEscape()
			b.WriteRune(r)
			continue
		}
		r, size := utf8.DecodeRuneInString(src[l.pos:])
		if !isIDPart(r) {
			break
		}
		if escaped {
			b.WriteRune(r)
		}
		l.pos += size
	}
	if escaped {
		return b.String()
	}
	return src[start:l.pos]
}

func (l *lexer) scanUnicodeEscape() rune {
	src := l.src
	if l.pos < len(src) && src[l.pos] == '{' {
		end := strings.IndexByte(src[l.pos:], '}')
		if end < 0 {
			l.fail(l.pos, "Invalid Unicode escape sequence")
		}
		v, err := strconv.ParseUint(src[l.pos+1:l.pos+end], 16, 32)
		if err != nil || v > 0x10FFFF {
			l.fail(l.pos, "Invalid Unicode escape sequence")
		}
		l.pos += end + 1
		return rune(v)
	}
	if l.pos+4 > len(src) {
		l.fail(l.pos, "Invalid Unicode escape sequence")
	}
	v, err := strconv.ParseUint(src[l.pos:l.pos+4], 16, 32)
	if err != nil {
		l.fail(l.pos, "Invalid Unicode escape sequence")
	}
	l.pos += 4
	return rune(v)
}

func (l *lexer) scanNumber() token {
	src := l.src
	start := l.pos
	if src[l.pos] == '0' && l.pos+1 < len(src) {
		base := 0
		switch src[l.pos+1] {
		case 'x', 'X':
			base = 16
		case 'o', 'O':
			base = 8
		case 'b', 'B':
			base = 2
		}
		if base != 0 {
			l.pos += 2
			ds := l.pos
			for l.pos < len(src) && (digitVal(src[l.pos]) < base || src[l.pos] == '_') {
				l.pos++
			}
			digits := strings.ReplaceAll(src[ds:l.pos], "_", "")
			l.checkNumEnd(start)
			if digits == "" {
				l.fail(start, "Invalid or unexpected token")
			}
			return token{t: tNum, n: parseIntDigits(digits, base, true), pos: start, end: l.pos}
		}
		if src[l.pos+1] >= '0' && src[l.pos+1] <= '9' {
			l.fail(start, "Octal literals are not allowed in strict mode.")
		}
	}
	for l.pos < len(src) && (src[l.pos] >= '0' && src[l.pos] <= '9' || src[l.pos] == '_') {
		l.pos++
	}
	if l.pos < len(src) && src[l.pos] == '.' {
		l.pos++
		for l.pos < len(src) && (src[l.pos] >= '0' && src[l.pos] <= '9' || src[l.pos] == '_') {
			l.pos++
		}
	}
	if l.pos < len(src) && (src[l.pos] == 'e' || src[l.pos] == 'E') {
		l.pos++
		if l.pos < len(src) && (src[l.pos] == '+' || src[l.pos] == '-') {
			l.pos++
		}
		ds := l.pos
		for l.pos < len(src) && src[l.pos] >= '0' && src[l.pos] <= '9' {
			l.pos++
		}
		if ds == l.pos {
			l.fail(start, "Invalid or unexpected token")
		}
	}
	if l.pos < len(src) && src[l.pos] == 'n' {
		l.fail(start, "unsupported: BigInt")
	}
	l.checkNumEnd(start)
	text := strings.ReplaceAll(src[start:l.pos], "_", "")
	f, err := strconv.ParseFloat(text, 64)
	if err != nil {
		if ne, ok := err.(*strconv.NumError); !ok || ne.Err != strconv.ErrRange {
			l.fail(start, "Invalid or unexpected token")
		}
		if f == 0 {
			f = 0
		} else {
			f = math.Inf(1)
		}
	}
	return token{t: tNum, n: f, pos: start, end: l.pos}
}

func (l *lexer) checkNumEnd(start int) {
	if l.pos < len(l.src) {
		if l.src[l.pos] == 'n' {
			l.fail(start, "unsupported: BigInt")
		}
		r, _ := utf8.DecodeRuneInString(l.src[l.pos:])
		if isIDStart(r) || (r >= '0' && r <= '9') {
			l.fail(start, "Invalid or unexpected token")
		}
	}
}

func (l *lexer) scanString(q byte) token {
	src := l.src
	start := l.pos
	l.pos++
	var b []byte
	seg := l.pos
	for {
		if l.pos >= len(src) {
			l.fail(start, "Invalid or unexpected token")
		}
		c := src[l.pos]
		if c == q {
			break
		}
		if c == '\n' || c == '\r' {
			l.fail(start, "Invalid or unexpected token")
		}
		if c == '\\' {
			if b == nil {
				b = make([]byte, 0, l.pos-seg+16)
			}
			b = append(b, src[seg:l.pos]...)
			l.pos++
			var bad string
			b, bad = l.scanEscape(b, false)
			if bad != "" {
				l.fail(l.pos, bad)
			}
			seg = l.pos
			continue
		}
		l.pos++
	}
	var s string
	if b == nil {
		s = src[seg:l.pos]
	} else {
		s = string(append(b, src[seg:l.pos]...))
	}
	l.pos++
	return token{t: tStr, s: s, pos: start, end: l.pos}
}

func (l *lexer) scanEscape(b []byte, tmpl bool) ([]byte, string) {
	src := l.src
	if l.pos >= len(src) {
		return b, "Invalid or unexpected token"
	}
	c := src[l.pos]
	l.pos++
	switch c {
	case 'n':
		return append(b, '\n'), ""
	case 't':
		return append(b, '\t'), ""
	case 'r':
		return append(b, '\r'), ""
	case 'b':
		return append(b, '\b'), ""
	case 'f':
		return append(b, '\f'), ""
	case 'v':
		return append(b, '\v'), ""
	case '\r':
		if l.pos < len(src) && src[l.pos] == '\n' {
			l.pos++
		}
		return b, ""
	case '\n':
		return b, ""
	case '0':
		if l.pos < len(src) && src[l.pos] >= '0' && src[l.pos] <= '9' {
			return b, "Octal escape sequences are not allowed in strict mode."
		}
		return append(b, 0), ""
	case 'x':
		if l.pos+2 > len(src) {
			return b, "Invalid hexadecimal escape sequence"
		}
		v, err := strconv.ParseUint(src[l.pos:l.pos+2], 16, 8)
		if err != nil {
			return b, "Invalid hexadecimal escape sequence"
		}
		l.pos += 2
		return utf8.AppendRune(b, rune(v)), ""
	case 'u':
		r, bad := l.scanU16Escape()
		if bad != "" {
			return b, bad
		}
		if r >= 0xD800 && r < 0xDC00 && strings.HasPrefix(src[l.pos:], "\\u") {
			save := l.pos
			l.pos += 2
			r2, bad2 := l.scanU16Escape()
			if bad2 == "" && r2 >= 0xDC00 && r2 < 0xE000 {
				return utf8.AppendRune(b, (r-0xD800)<<10+(r2-0xDC00)+0x10000), ""
			}
			l.pos = save
		}
		return utf8.AppendRune(b, r), ""
	}
	if c >= '1' && c <= '9' {
		return b, "Octal escape sequences are not allowed in strict mode."
	}
	if c >= 0x80 {
		l.pos--
		r, size := utf8.DecodeRuneInString(src[l.pos:])
		l.pos += size
		if r == 0x2028 || r == 0x2029 {
			return b, ""
		}
		return utf8.AppendRune(b, r), ""
	}
	return append(b, c), ""
}

func (l *lexer) scanU16Escape() (rune, string) {
	src := l.src
	if l.pos < len(src) && src[l.pos] == '{' {
		end := strings.IndexByte(src[l.pos:], '}')
		if end < 0 {
			return 0, "Invalid Unicode escape sequence"
		}
		v, err := strconv.ParseUint(src[l.pos+1:l.pos+end], 16, 32)
		if err != nil || v > 0x10FFFF {
			return 0, "Invalid Unicode escape sequence"
		}
		l.pos += end + 1
		return rune(v), ""
	}
	if l.pos+4 > len(src) {
		return 0, "Invalid Unicode escape sequence"
	}
	v, err := strconv.ParseUint(src[l.pos:l.pos+4], 16, 32)
	if err != nil {
		return 0, "Invalid Unicode escape sequence"
	}
	l.pos += 4
	return rune(v), ""
}

func (l *lexer) scanTemplate(start int) token {
	src := l.src
	var b []byte
	seg := l.pos
	rawStart := l.pos
	bad := ""
	for {
		if l.pos >= len(src) {
			l.fail(start, "Unterminated template literal")
		}
		c := src[l.pos]
		if c == '`' || (c == '$' && l.pos+1 < len(src) && src[l.pos+1] == '{') {
			raw := normCR(src[rawStart:l.pos])
			cooked := string(append(b, normCR(src[seg:l.pos])...))
			if b == nil {
				cooked = normCR(src[seg:l.pos])
			}
			end := c == '`'
			if end {
				l.pos++
			} else {
				l.pos += 2
			}
			return token{t: tTemplate, s: cooked, raw: raw, pos: start, end: l.pos, tmplEnd: end, bad: bad}
		}
		if c == '\\' {
			b = append(b, normCR(src[seg:l.pos])...)
			l.pos++
			var e string
			b, e = l.scanEscape(b, true)
			if e != "" && bad == "" {
				bad = e
			}
			seg = l.pos
			continue
		}
		l.pos++
	}
}

func normCR(s string) string {
	if strings.IndexByte(s, '\r') < 0 {
		return s
	}
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
}

func (l *lexer) scanRegex(start int) token {
	src := l.src
	l.pos = start + 1
	inClass := false
	for {
		if l.pos >= len(src) || src[l.pos] == '\n' {
			l.fail(start, "Invalid regular expression: missing /")
		}
		c := src[l.pos]
		if c == '\\' {
			l.pos += 2
			continue
		}
		if c == '[' {
			inClass = true
		} else if c == ']' {
			inClass = false
		} else if c == '/' && !inClass {
			break
		}
		l.pos++
	}
	body := src[start+1 : l.pos]
	l.pos++
	fs := l.pos
	for l.pos < len(src) {
		r, size := utf8.DecodeRuneInString(src[l.pos:])
		if !isIDPart(r) {
			break
		}
		l.pos += size
	}
	return token{t: tRegex, s: body, raw: src[fs:l.pos], pos: start, end: l.pos}
}
