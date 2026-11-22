package main

import (
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/paul-nameless/qi/internal/jsvm/bench/oracle"
)

type verdict int

const (
	vPass verdict = iota
	vOracleBad
	vOracleSplit
	vMismatch
	vCrash
	vDeviation
)

type triple struct {
	goja, qjs, jsvm outcome
}

type diffLine struct {
	label string
	sig   string
	exp   string
	act   string
}

func same(a, b string) bool {
	if a == b {
		return true
	}
	ok, _ := oracle.WordingOnly(a, b, false)
	return ok
}

func internalError(s string) bool {
	return strings.Contains(s, "jsvm internal error")
}

var loneRe = regexp.MustCompile(`\\+ud[89a-f][0-9a-f]{2}`)

func deviationOnly(exp, act string) bool {
	if !loneRe.MatchString(exp) {
		return false
	}
	if loneRe.ReplaceAllString(exp, "\uFFFD") == act {
		return true
	}
	p := 0
	for p < len(exp) && p < len(act) && exp[p] == act[p] {
		p++
	}
	start := max(0, p-12)
	return loneRe.MatchString(exp[start:min(len(exp), p+12)])
}

var lineLabelRe = regexp.MustCompile(`^"([a-z]+\d*)" `)

func lineDiffs(exp, act string) []diffLine {
	le, la := strings.Split(exp, "\n"), strings.Split(act, "\n")
	var out []diffLine
	for i := 0; i < len(le) || i < len(la); i++ {
		x, y := "", ""
		if i < len(le) {
			x = le[i]
		}
		if i < len(la) {
			y = la[i]
		}
		if x == y || deviationOnly(x, y) || same(x, y) {
			continue
		}
		d := diffLine{sig: hunk(x, y), exp: x, act: y}
		if m := lineLabelRe.FindStringSubmatch(x); m != nil {
			d.label = m[1]
		}
		out = append(out, d)
		if len(le) != len(la) {
			break
		}
	}
	return out
}

func analyze(t triple) (verdict, []diffLine) {
	if t.goja.fail != "" || t.qjs.fail != "" {
		return vOracleBad, nil
	}
	if !same(t.goja.canon, t.qjs.canon) {
		return vOracleSplit, nil
	}
	if t.jsvm.fail != "" || internalError(t.jsvm.canon) {
		return vCrash, nil
	}
	if same(t.jsvm.canon, t.qjs.canon) || same(t.jsvm.canon, t.goja.canon) {
		return vPass, nil
	}
	d := lineDiffs(t.qjs.canon, t.jsvm.canon)
	if len(d) == 0 || len(lineDiffs(t.goja.canon, t.jsvm.canon)) == 0 {
		return vDeviation, nil
	}
	return vMismatch, d
}

func classify(t triple) verdict {
	v, _ := analyze(t)
	return v
}

type runner struct {
	goja, qjs, jsvm *engine
}

func (r *runner) run(src string) triple {
	var t triple
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); t.goja = r.goja.run(src) }()
	go func() { defer wg.Done(); t.qjs = r.qjs.run(src) }()
	go func() { defer wg.Done(); t.jsvm = r.jsvm.run(src) }()
	wg.Wait()
	return t
}

type minimizer struct {
	r      *runner
	want   verdict
	label  string
	budget int
	tests  int
}

func (m *minimizer) ok(t triple) bool {
	v, ds := analyze(t)
	if v != m.want {
		return false
	}
	if m.label == "" {
		return true
	}
	for _, d := range ds {
		if d.label == m.label {
			return true
		}
	}
	return false
}

func (m *minimizer) firstInteresting(cands []program) int {
	res := make([]bool, len(cands))
	var wg sync.WaitGroup
	var mu sync.Mutex
	for i := range cands {
		mu.Lock()
		if m.tests >= m.budget {
			mu.Unlock()
			break
		}
		m.tests++
		mu.Unlock()
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res[i] = m.ok(m.r.run(cands[i].source()))
		}(i)
	}
	wg.Wait()
	for i, ok := range res {
		if ok {
			return i
		}
	}
	return -1
}

type unitRef struct{ cell, unit int }

func refs(p program) []unitRef {
	var out []unitRef
	for c, cl := range p.cells {
		for u := range cl.units {
			out = append(out, unitRef{c, u})
		}
	}
	return out
}

func without(p program, drop map[unitRef]bool) program {
	q := program{cells: make([]cell, len(p.cells))}
	for c, cl := range p.cells {
		q.cells[c] = cell{head: cl.head, tail: cl.tail}
		for u, s := range cl.units {
			if !drop[unitRef{c, u}] {
				q.cells[c].units = append(q.cells[c].units, s)
			}
		}
	}
	return q
}

func (m *minimizer) ddminUnits(p program) program {
	n := 2
	for {
		rs := refs(p)
		if len(rs) < 2 || n > len(rs) {
			return p
		}
		chunk := (len(rs) + n - 1) / n
		var cands []program
		for start := 0; start < len(rs); start += chunk {
			drop := map[unitRef]bool{}
			for _, r := range rs[start:min(start+chunk, len(rs))] {
				drop[r] = true
			}
			cands = append(cands, without(p, drop))
		}
		if i := m.firstInteresting(cands); i >= 0 {
			p = cands[i]
			n = max(n-1, 2)
			continue
		}
		if n >= len(rs) || m.tests >= m.budget {
			return p
		}
		n = min(n*2, len(rs))
	}
}

func (m *minimizer) reduceText(p program, c, u int, pieces func(string) [][2]int, repls []string) program {
	for changed := true; changed && m.tests < m.budget; {
		changed = false
		spans := pieces(p.cells[c].units[u])
		sort.SliceStable(spans, func(i, j int) bool { return spans[i][1]-spans[i][0] > spans[j][1]-spans[j][0] })
		for _, sp := range spans {
			s := p.cells[c].units[u]
			var cands []program
			for _, rep := range repsFor(s, sp, repls) {
				if s[sp[0]:sp[1]] == rep {
					continue
				}
				q := p.clone()
				q.cells[c].units[u] = s[:sp[0]] + rep + s[sp[1]:]
				cands = append(cands, q)
			}
			if i := m.firstInteresting(cands); i >= 0 {
				p = cands[i]
				changed = true
				break
			}
		}
	}
	return p
}

func (m *minimizer) minimize(p program) program {
	p = m.ddminUnits(p)
	for _, r := range refs(p) {
		p = m.reduceText(p, r.cell, r.unit, statementSpans, []string{""})
		p = m.reduceText(p, r.cell, r.unit, parenSpans, []string{"0", "\"\"", "undefined", "[]"})
	}
	return m.ddminUnits(p)
}

func repsFor(s string, sp [2]int, repls []string) []string {
	if sp[0] == 0 || len(repls) == 1 {
		return repls
	}
	if strings.HasSuffix(s[:sp[0]], "Date") {
		return nil
	}
	c := s[sp[0]-1]
	if c == '_' || c == ')' || c == ']' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
		if s[sp[0]] == '(' {
			return []string{"()"}
		}
		return []string{"[0]"}
	}
	return repls
}

func scanUnit(s string, visit func(i int, c byte, depth int)) {
	depth := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '"' || c == '\'' {
			j := i + 1
			for j < len(s) && s[j] != c {
				if s[j] == '\\' {
					j++
				}
				j++
			}
			i = j
			continue
		}
		switch c {
		case '(', '[', '{':
			visit(i, c, depth)
			depth++
		case ')', ']', '}':
			depth--
			visit(i, c, depth)
		default:
			visit(i, c, depth)
		}
	}
}

func parenSpans(s string) [][2]int {
	var stack []int
	var out [][2]int
	scanUnit(s, func(i int, c byte, _ int) {
		switch c {
		case '(', '[':
			stack = append(stack, i)
		case ')', ']':
			if len(stack) > 0 {
				o := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				if i-o > 2 {
					out = append(out, [2]int{o, i + 1})
				}
			}
		}
	})
	return out
}

func statementSpans(s string) [][2]int {
	var out [][2]int
	type frame struct{ start int }
	var stack []frame
	scanUnit(s, func(i int, c byte, _ int) {
		switch c {
		case '{':
			stack = append(stack, frame{i + 1})
		case '}':
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case ';':
			if len(stack) > 0 {
				f := &stack[len(stack)-1]
				out = append(out, [2]int{f.start, i + 1})
				f.start = i + 1
			}
		}
	})
	return out
}

var (
	strRe   = regexp.MustCompile(`"(?:[^"\\]|\\.)*"`)
	numRe   = regexp.MustCompile(`\b\d+(\.\d+)?(e[+-]?\d+)?\b`)
	identRe = regexp.MustCompile(`\b([vfi])\d+\b`)
	spaceRe = regexp.MustCompile(`\s+`)
)

func shape(p program) string {
	var parts []string
	for _, c := range p.cells {
		for _, u := range c.units {
			s := strRe.ReplaceAllString(u, `"S"`)
			s = numRe.ReplaceAllString(s, "N")
			s = identRe.ReplaceAllString(s, "$1")
			parts = append(parts, spaceRe.ReplaceAllString(s, " "))
		}
	}
	return strings.Join(parts, " | ")
}

var labelRe = regexp.MustCompile(`^"s\d+" `)

func diffSignature(exp, act string) string {
	le, la := strings.Split(exp, "\n"), strings.Split(act, "\n")
	for i := 0; i < len(le) || i < len(la); i++ {
		x, y := "", ""
		if i < len(le) {
			x = le[i]
		}
		if i < len(la) {
			y = la[i]
		}
		if x != y {
			return norm(x) + " => " + norm(y)
		}
	}
	return "?"
}

func norm(s string) string {
	s = labelRe.ReplaceAllString(s, "")
	s = numRe.ReplaceAllString(s, "N")
	s = strRe.ReplaceAllString(s, `"S"`)
	if len(s) > 60 {
		s = s[:60]
	}
	return s
}

func hunk(x, y string) string {
	p := 0
	for p < len(x) && p < len(y) && x[p] == y[p] {
		p++
	}
	q := 0
	for q < len(x)-p && q < len(y)-p && x[len(x)-1-q] == y[len(y)-1-q] {
		q++
	}
	ctx := x[max(0, p-6):p]
	return clip(numRe.ReplaceAllString(ctx, "N")) + "[" + clip(numRe.ReplaceAllString(x[p:len(x)-q], "N")) + " => " + clip(numRe.ReplaceAllString(y[p:len(y)-q], "N")) + "]"
}

func clip(s string) string {
	if len(s) > 40 {
		return s[:40] + "~"
	}
	return s
}
