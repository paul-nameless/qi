package regex

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"
)

type re2State struct {
	expr      string
	multiline bool
	once      [4]sync.Once
	progs     [4]*regexp.Regexp
	errs      [4]error
}

func newRE2(root *node, f flagSet) *re2State {
	t := &re2Translator{f: f}
	if !t.eligible(root) {
		return nil
	}
	t.write(root)
	if t.bad {
		return nil
	}
	return &re2State{expr: t.b.String(), multiline: f.multiline}
}

func (st *re2State) variant(i int) string {
	switch i {
	case 0:
		return st.expr
	case 1:
		return `\A(?s:.)+?(` + st.expr + `)`
	case 2:
		return `\A(?:` + st.expr + `)`
	}
	return `\A(?s:.)(` + st.expr + `)`
}

func (st *re2State) prog(i int) *regexp.Regexp {
	st.once[i].Do(func() { st.progs[i], st.errs[i] = regexp.Compile(st.variant(i)) })
	return st.progs[i]
}

func (st *re2State) usable(m *machine, w string, cesu bool, sj *subject) bool {
	if st == nil || cesu || st.prog(0) == nil {
		return false
	}
	if !st.multiline {
		return true
	}
	if sj == nil {
		sj = m.subjectFor(w)
	}
	return !sj.lineTerms()
}

func (st *re2State) exec(dst []int, s string, start int, sticky bool) []int {
	v, base := 0, 0
	if start > 0 {
		_, w := utf8.DecodeLastRuneInString(s[:start])
		base = start - w
		v = 1
	}
	if sticky {
		v += 2
	}
	p := st.prog(v)
	if p == nil {
		panic(&Error{errTooComplex})
	}
	loc := p.FindStringSubmatchIndex(s[base:])
	if loc == nil {
		return nil
	}
	if v&1 == 1 {
		loc = loc[2:]
	}
	dst = append(dst[:0], loc...)
	for i := range dst {
		if dst[i] >= 0 {
			dst[i] += base
		}
	}
	return dst
}

type re2Translator struct {
	f   flagSet
	b   strings.Builder
	bad bool
}

func (t *re2Translator) eligible(n *node) bool {
	switch n.kind {
	case nLook, nBackref:
		return false
	case nWordB, nNotWordB:
		return !(t.f.unicode && t.f.icase)
	case nRepeat:
		if n.min > 1000 || n.max > 1000 {
			return false
		}
		sub := n.subs[0]
		if n.max != 0 && nullable(sub) {
			return false
		}
		if (n.max > 1 || n.max == infinite) && sub.capLo > 0 && !allMandatory(sub) {
			return false
		}
	}
	for _, s := range n.subs {
		if !t.eligible(s) {
			return false
		}
	}
	return true
}

func allMandatory(n *node) bool {
	for i := n.capLo; i <= n.capHi; i++ {
		if !mandatory(n, i) {
			return false
		}
	}
	return true
}

func mandatory(n *node, idx int) bool {
	if idx < n.capLo || idx > n.capHi || n.capLo == 0 {
		return false
	}
	switch n.kind {
	case nGroup:
		return n.idx == idx || mandatory(n.subs[0], idx)
	case nSeq:
		for _, s := range n.subs {
			if mandatory(s, idx) {
				return true
			}
		}
		return false
	case nAlt:
		for _, s := range n.subs {
			if !mandatory(s, idx) {
				return false
			}
		}
		return true
	case nRepeat:
		return n.min >= 1 && mandatory(n.subs[0], idx)
	}
	return false
}

func (t *re2Translator) write(n *node) {
	b := &t.b
	switch n.kind {
	case nEmpty:
		b.WriteString("(?:)")
	case nChar:
		t.char(n.r)
	case nSet:
		t.set(n.set)
	case nSeq:
		for _, s := range n.subs {
			t.write(s)
		}
	case nAlt:
		b.WriteString("(?:")
		for i, s := range n.subs {
			if i > 0 {
				b.WriteByte('|')
			}
			t.write(s)
		}
		b.WriteByte(')')
	case nGroup:
		b.WriteByte('(')
		t.write(n.subs[0])
		b.WriteByte(')')
	case nRepeat:
		t.repeat(n)
	case nBol:
		b.WriteString(pickStr(t.f.multiline, `(?m:^)`, `\A`))
	case nEol:
		b.WriteString(pickStr(t.f.multiline, `(?m:$)`, `\z`))
	case nWordB:
		b.WriteString(`\b`)
	case nNotWordB:
		b.WriteString(`\B`)
	default:
		t.bad = true
	}
}

func pickStr(c bool, a, b string) string {
	if c {
		return a
	}
	return b
}

func (t *re2Translator) repeat(n *node) {
	t.b.WriteString("(?:")
	t.write(n.subs[0])
	t.b.WriteByte(')')
	switch {
	case n.max == infinite:
		fmt.Fprintf(&t.b, "{%d,}", n.min)
	case n.min == n.max:
		fmt.Fprintf(&t.b, "{%d}", n.min)
	default:
		fmt.Fprintf(&t.b, "{%d,%d}", n.min, n.max)
	}
	if !n.greedy {
		t.b.WriteByte('?')
	}
}

func (t *re2Translator) char(r rune) {
	if r < 0x80 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
		t.b.WriteRune(r)
		return
	}
	fmt.Fprintf(&t.b, `\x{%x}`, r)
}

func (t *re2Translator) set(s *charset) {
	rs := s.ranges
	if len(rs) == 0 {
		t.b.WriteString(`[^\x{0}-\x{10ffff}]`)
		return
	}
	max := rune(maxUnit)
	if t.f.unicode {
		max = maxCodePt
	}
	if s.isFull(max) {
		t.b.WriteString(`(?s:.)`)
		return
	}
	t.b.WriteByte('[')
	for i := 0; i < len(rs); i += 2 {
		fmt.Fprintf(&t.b, `\x{%x}`, rs[i])
		if rs[i+1] != rs[i] {
			fmt.Fprintf(&t.b, `-\x{%x}`, rs[i+1])
		}
	}
	t.b.WriteByte(']')
}
