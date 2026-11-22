package harness

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	frameRe    = regexp.MustCompile(`(?m)^\s+at `)
	locRe      = regexp.MustCompile(`(?:cell\.js|<eval>):(\d+)(?::\d+)?(?:\(\d+\))?`)
	gojaSynRe  = regexp.MustCompile(` at (?:cell\.js|<eval>):(\d+):\d+(?:\(\d+\))?$`)
	gojaLineRe = regexp.MustCompile(`(?:cell\.js|<eval>|\(anonymous\)): Line (\d+):\d+ `)
	moreErrsRe = regexp.MustCompile(` \(and \d+ more errors\)$`)
)

type ErrInfo struct {
	Name    string
	Message string
	Line    int
}

func Canon(out, val string, err error, off int) string {
	s := normLocs(out, off)
	if val != "" {
		s += "=> " + normLocs(val, off) + "\n"
	}
	if err != nil {
		s += ErrLine(err.Error(), off) + "\n"
	}
	return s
}

func normLocs(s string, off int) string {
	if !strings.Contains(s, "cell.js:") && !strings.Contains(s, "<eval>:") {
		return s
	}
	return locRe.ReplaceAllStringFunc(s, func(m string) string {
		n, _ := strconv.Atoi(locRe.FindStringSubmatch(m)[1])
		return "cell.js:" + strconv.Itoa(n-off)
	})
}

func ErrLine(msg string, off int) string {
	switch {
	case msg == errTimeout.Error():
		return "!! Timeout"
	case strings.HasPrefix(msg, "the script is waiting on a promise"):
		return "!! NeverSettles"
	}
	e := ParseErr(msg, off)
	line := "?"
	if e.Line > 0 {
		line = strconv.Itoa(e.Line)
	}
	return "!! " + e.Name + ": " + strings.ReplaceAll(e.Message, "\n", `\n`) + " @" + line
}

func ParseErr(msg string, off int) ErrInfo {
	head, stack := msg, ""
	if loc := frameRe.FindStringIndex(msg); loc != nil {
		head, stack = msg[:loc[0]], msg[loc[0]:]
	}
	head = dropStackHeader(strings.TrimSuffix(head, "\n"))
	line := 0
	if m := locRe.FindStringSubmatch(stack); m != nil {
		line, _ = strconv.Atoi(m[1])
		line -= off
	}
	if m := gojaSynRe.FindStringSubmatch(head); m != nil {
		head = head[:len(head)-len(m[0])]
		if line == 0 {
			line, _ = strconv.Atoi(m[1])
			line -= off
		}
	}
	if m := gojaLineRe.FindStringSubmatch(head); m != nil {
		head = strings.Replace(head, m[0], "", 1)
		if line == 0 {
			line, _ = strconv.Atoi(m[1])
			line -= off
		}
	}
	head = moreErrsRe.ReplaceAllString(head, "")
	name, message, ok := strings.Cut(head, ": ")
	if !ok {
		return ErrInfo{Name: "Error", Message: head, Line: line}
	}
	for strings.HasPrefix(message, name+": ") {
		message = strings.TrimPrefix(message, name+": ")
	}
	return ErrInfo{Name: name, Message: message, Line: line}
}

func dropStackHeader(head string) string {
	for k := strings.IndexByte(head, '\n'); k >= 0; {
		a, b := head[:k], head[k+1:]
		_, am, aok := strings.Cut(a, ": ")
		_, bm, bok := strings.Cut(b, ": ")
		if a == b || aok && bok && am == bm {
			return a
		}
		next := strings.IndexByte(head[k+1:], '\n')
		if next < 0 {
			break
		}
		k += next + 1
	}
	return head
}
