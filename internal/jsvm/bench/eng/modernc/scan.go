package main

import "strings"

func isIdent(c byte) bool {
	return c == '_' || c == '$' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c >= 0x80
}

var regexAfter = map[string]bool{"return": true, "typeof": true, "case": true, "in": true, "of": true, "void": true, "delete": true, "throw": true, "new": true, "else": true, "do": true, "await": true, "yield": true, "instanceof": true}

func globalize(src string) string {
	var out strings.Builder
	type frame struct{ braces int }
	var tmpl []frame
	depth := 0
	prev := byte(';')
	prevWord := ""
	lineStart := true
	last := 0
	n := len(src)
	skipString := func(i int, q byte) int {
		for i++; i < n; i++ {
			switch src[i] {
			case '\\':
				i++
			case q, '\n':
				return i + 1
			}
		}
		return n
	}
	skipTemplate := func(i int) (int, bool) {
		for ; i < n; i++ {
			switch src[i] {
			case '\\':
				i++
			case '`':
				return i + 1, false
			case '$':
				if i+1 < n && src[i+1] == '{' {
					return i + 2, true
				}
			}
		}
		return n, false
	}
	for i := 0; i < n; {
		c := src[i]
		switch {
		case c == '\n':
			lineStart = true
			i++
		case c == ' ' || c == '\t' || c == '\r':
			i++
		case c == '/' && i+1 < n && src[i+1] == '/':
			for i < n && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < n && src[i+1] == '*':
			j := strings.Index(src[i+2:], "*/")
			if j < 0 {
				i = n
			} else {
				i += j + 4
			}
		case c == '"' || c == '\'':
			i = skipString(i, c)
			prev, prevWord, lineStart = 'v', "", false
		case c == '`':
			var open bool
			i, open = skipTemplate(i + 1)
			if open {
				tmpl = append(tmpl, frame{0})
				depth++
				prev, prevWord = '{', ""
			} else {
				prev, prevWord = 'v', ""
			}
			lineStart = false
		case c == '/':
			valueBefore := prev == 'v' || prev == ')' || prev == ']'
			if prev == 'w' && !regexAfter[prevWord] {
				valueBefore = true
			}
			if valueBefore {
				i++
				prev, prevWord = '/', ""
			} else {
				inClass := false
				for i++; i < n; i++ {
					ch := src[i]
					if ch == '\\' {
						i++
					} else if ch == '[' {
						inClass = true
					} else if ch == ']' {
						inClass = false
					} else if ch == '\n' || ch == '/' && !inClass {
						break
					}
				}
				i++
				for i < n && isIdent(src[i]) {
					i++
				}
				prev, prevWord = 'v', ""
			}
			lineStart = false
		case isIdent(c):
			j := i
			for j < n && isIdent(src[j]) {
				j++
			}
			word := src[i:j]
			stmtStart := depth == 0 && (prev == ';' || prev == '}' || lineStart && !strings.ContainsRune("=,([{+-*%&|?:<>!.", rune(prev)) && !(prev == 'w' && regexAfter[prevWord]))
			if stmtStart && prev != '.' {
				rest := strings.TrimLeft(src[j:], " \t")
				switch {
				case (word == "const" || word == "let") && rest != "" && (isIdent(rest[0]) || rest[0] == '{' || rest[0] == '['):
					out.WriteString(src[last:i])
					out.WriteString("var")
					last = j
				case word == "class" && rest != "" && isIdent(rest[0]):
					k := 0
					for k < len(rest) && isIdent(rest[k]) {
						k++
					}
					out.WriteString(src[last:i])
					out.WriteString("var " + rest[:k] + " = class")
					last = j
				}
			}
			if c >= '0' && c <= '9' {
				prev, prevWord = 'v', ""
			} else {
				prev, prevWord = 'w', word
			}
			lineStart = false
			i = j
		default:
			switch c {
			case '{', '(', '[':
				depth++
				if c == '{' && len(tmpl) > 0 {
					tmpl[len(tmpl)-1].braces++
				}
			case ')', ']':
				depth--
			case '}':
				if len(tmpl) > 0 && tmpl[len(tmpl)-1].braces == 0 {
					tmpl = tmpl[:len(tmpl)-1]
					depth--
					var open bool
					i, open = skipTemplate(i + 1)
					if open {
						tmpl = append(tmpl, frame{0})
						depth++
						prev, prevWord = '{', ""
					} else {
						prev, prevWord = 'v', ""
					}
					lineStart = false
					continue
				}
				if len(tmpl) > 0 {
					tmpl[len(tmpl)-1].braces--
				}
				depth--
			}
			prev, prevWord = c, ""
			lineStart = false
			i++
		}
	}
	out.WriteString(src[last:])
	return out.String()
}
