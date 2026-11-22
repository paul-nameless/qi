package oracle

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/paul-nameless/qi/internal/jsvm/bench/harness"
)

var (
	errLineRe  = regexp.MustCompile(`^!! (\w+): .* @(\S+)$`)
	errNameRe  = regexp.MustCompile(`\b[A-Z]\w*Error\b`)
	phraseRe   = regexp.MustCompile(`(?i)cannot read propert|cannot set propert|is not a function|not a function|is not defined|not a constructor|has no member|cannot convert|invalid|unexpected|not iterable|assignment to constant|cannot access|maximum call stack|stack overflow|not an object|read-only|lexical variable|expecting|could not load module|undefined has no properties|null has no properties|is not a|must be|out of range|cannot be|not supported|circular|cyclic|precision|radix|illegal|unterminated|missing|duplicate|redeclar|already been declared|not allowed|before initialization|uninitialized`)
	stackyRe   = regexp.MustCompile(`cell\.js:\d`)
	errorAtEnd = regexp.MustCompile(`^!! `)
)

func errNames(s string) string {
	ns := errNameRe.FindAllString(s, -1)
	sort.Strings(ns)
	return strings.Join(ns, ",")
}

func sameLine(a, b string) bool { return a == b || a == "?" || b == "?" }

func wordingPair(a, b string, printsMessage bool) bool {
	if ma, mb := errLineRe.FindStringSubmatch(a), errLineRe.FindStringSubmatch(b); ma != nil || mb != nil {
		return ma != nil && mb != nil && ma[1] == mb[1] && sameLine(ma[2], mb[2])
	}
	if errNames(a) != errNames(b) {
		return false
	}
	return printsMessage || phraseRe.MatchString(a) && phraseRe.MatchString(b)
}

var messageRe = regexp.MustCompile(`\.message\b|\bString\(\s*e(rr|rror)?\s*\)|\$\{\s*e(rr|rror)?\s*\}`)

func PrintsMessage(code string) bool { return messageRe.MatchString(code) }

func WordingOnly(a, b string, printsMessage bool) (bool, []int) {
	la, lb := strings.Split(a, "\n"), strings.Split(b, "\n")
	if len(la) != len(lb) {
		return false, nil
	}
	var loose []int
	for i := range la {
		if la[i] == lb[i] {
			continue
		}
		if !wordingPair(la[i], lb[i], printsMessage) {
			return false, nil
		}
		loose = append(loose, i)
	}
	return len(loose) > 0, loose
}

func FirstDiff(a, b string) string {
	la, lb := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := 0; i < len(la) || i < len(lb); i++ {
		x, y := "<missing>", "<missing>"
		if i < len(la) {
			x = la[i]
		}
		if i < len(lb) {
			y = lb[i]
		}
		if x != y {
			return fmt.Sprintf("line %d: %q vs %q", i+1, cut(x, 160), cut(y, 160))
		}
	}
	return ""
}

func cut(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func messageStart(s string) int {
	at := len(s)
	if loc := phraseRe.FindStringIndex(s); loc != nil {
		at = loc[0]
	}
	if loc := errNameRe.FindStringIndex(s); loc != nil && loc[0] < at {
		at = loc[0]
	}
	return at
}

func labelPrefix(a, b string) string {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	n = min(n, messageStart(a), messageStart(b))
	for n > 0 && (unicode.IsLetter(rune(a[n-1])) || unicode.IsDigit(rune(a[n-1])) || a[n-1] >= 0x80) {
		n--
	}
	return a[:n]
}

func looseErrLine(me, mx []string, act string) bool {
	ma := errLineRe.FindStringSubmatch(act)
	if ma == nil || ma[1] != me[1] {
		return false
	}
	lines := []string{}
	for _, l := range []string{me[2], mx[2]} {
		if l != "?" {
			lines = append(lines, l)
		}
	}
	if len(lines) == 0 {
		return true
	}
	for _, l := range lines {
		if ma[2] == l {
			return true
		}
	}
	return false
}

func looseMatch(exp, alt, act string) bool {
	if act == exp || act == alt {
		return true
	}
	me, mx := errLineRe.FindStringSubmatch(exp), errLineRe.FindStringSubmatch(alt)
	if me != nil && mx != nil {
		return looseErrLine(me, mx, act)
	}
	if me != nil || mx != nil || strings.HasPrefix(act, "!! ") {
		return false
	}
	if missingGlobal(act) && !missingGlobal(exp) && !missingGlobal(alt) || strings.Contains(act, "unsupported:") {
		return false
	}
	pre := labelPrefix(exp, alt)
	if len(act) <= len(pre) || !strings.HasPrefix(act, pre) {
		return false
	}
	return errNames(act) == errNames(exp)
}

func Compare(exp, alt, act string) (bool, string) {
	if act == exp {
		return true, ""
	}
	if alt == "" || alt == exp {
		return false, FirstDiff(exp, act)
	}
	if act == alt {
		return true, ""
	}
	le, la, lx := strings.Split(exp, "\n"), strings.Split(alt, "\n"), strings.Split(act, "\n")
	if len(la) != len(le) {
		return false, "expected .out and .alt have different line counts"
	}
	if len(lx) != len(le) {
		return false, FirstDiff(exp, act)
	}
	for i := range le {
		if le[i] == la[i] {
			if lx[i] != le[i] {
				return false, FirstDiff(exp, act)
			}
			continue
		}
		if !looseMatch(le[i], la[i], lx[i]) {
			return false, fmt.Sprintf("line %d: %q vs %q (or %q)", i+1, cut(le[i], 120), cut(lx[i], 120), cut(la[i], 120))
		}
	}
	return true, ""
}

func PrintsStack(s string) bool { return stackyRe.MatchString(s) }

func OnlyError(s string) bool {
	lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	return len(lines) == 1 && errorAtEnd.MatchString(lines[0])
}

type StateStep struct {
	Name    string `json:"name"`
	Out     string `json:"out"`
	ErrName string `json:"err_name,omitempty"`
	ErrLine int    `json:"err_line,omitempty"`
}

func StateSteps(recs []harness.Rec) []StateStep {
	var steps []StateStep
	for _, r := range recs {
		st := StateStep{Name: r.Name, Out: r.Out}
		if r.Err != "" {
			e := harness.ParseErr(r.Err, 0)
			st.ErrName, st.ErrLine = e.Name, e.Line
		}
		steps = append(steps, st)
	}
	return steps
}

var NeutralTags = map[string]bool{"String.raw": true, "regex-named-group": true, "regex-flag-s": true, "regex-flag-u": true, "regex-flag-y": true}

func Tier1(tags []string) bool {
	for _, t := range tags {
		if !NeutralTags[t] {
			return false
		}
	}
	return true
}
