package regex

import (
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	fChoice int32 = iota
	fCap
	fReg
	fRepG
	fRepL
	fRepGB
	fRepLB
)

const maxStack = 1 << 21

type frame struct {
	kind int32
	pc   int32
	pos  int
	a    int
}

type machine struct {
	re    *Regexp
	s     string
	caps  []int
	regs  []int
	stack []frame
	steps int
	limit int
	abort bool
	local subject
}

func newMachine(re *Regexp) *machine {
	m := &machine{re: re, caps: make([]int, 2*re.ncap), regs: make([]int, re.nregs)}
	for i := range m.caps {
		m.caps[i] = -1
	}
	return m
}

func (m *machine) push(kind int32, pc, pos, a int) {
	m.stack = append(m.stack, frame{kind: kind, pc: int32(pc), pos: pos, a: a})
	if len(m.stack) > maxStack {
		m.abort = true
	}
}

func (m *machine) charge() bool {
	m.steps++
	if m.steps > m.limit {
		m.abort = true
	}
	return m.abort
}

func (m *machine) chargeRun(n int) bool {
	m.steps += n >> 3
	if m.steps > m.limit {
		m.abort = true
	}
	return m.abort
}

func decode(s string, i int) (rune, int) {
	b := s[i]
	if b < 0x80 {
		return rune(b), 1
	}
	if b >= 0xC2 && b < 0xE0 && i+1 < len(s) && s[i+1]&0xC0 == 0x80 {
		return rune(b&0x1F)<<6 | rune(s[i+1]&0x3F), 2
	}
	if b == 0xED && i+2 < len(s) && s[i+1] >= 0xA0 {
		return rune(b&0x0F)<<12 | rune(s[i+1]&0x3F)<<6 | rune(s[i+2]&0x3F), 3
	}
	return utf8.DecodeRuneInString(s[i:])
}

func decodeLast(s string, i int) (rune, int) {
	b := s[i-1]
	if b < 0x80 {
		return rune(b), 1
	}
	if i >= 3 && s[i-3] == 0xED && s[i-2] >= 0xA0 {
		return decode(s, i-3)
	}
	return utf8.DecodeLastRuneInString(s[:i])
}

func charWidth(s string, i int) int {
	if s[i] < 0x80 {
		return 1
	}
	_, w := decode(s, i)
	return w
}

func stepBack(s string, i int) int {
	i--
	for k := 0; k < 3 && i > 0 && s[i]&0xC0 == 0x80; k++ {
		i--
	}
	return i
}

func isLineTermAt(s string, i int) bool {
	switch s[i] {
	case '\n', '\r':
		return true
	case 0xE2:
		return i+2 < len(s) && s[i+1] == 0x80 && (s[i+2] == 0xA8 || s[i+2] == 0xA9)
	}
	return false
}

func isLineTermBefore(s string, i int) bool {
	switch s[i-1] {
	case '\n', '\r':
		return true
	case 0xA8, 0xA9:
		return i >= 3 && s[i-3] == 0xE2 && s[i-2] == 0x80
	}
	return false
}

func (m *machine) run(pc, pos int) (int, bool) {
	base := len(m.stack)
	prog := m.re.prog
	s := m.s
	n := len(s)
	for {
		in := &prog[pc]
		switch in.op {
		case opByte:
			if !in.back {
				if pos < n && s[pos] == in.c {
					pos++
					pc++
					continue
				}
			} else if pos > 0 && s[pos-1] == in.c {
				pos--
				pc++
				continue
			}
		case opSet:
			if !in.back {
				if pos < n {
					if b := s[pos]; b < 0x80 {
						if in.set.hasASCII(b) {
							pos++
							pc++
							continue
						}
					} else if r, w := decode(s, pos); in.set.has(r) {
						pos += w
						pc++
						continue
					}
				}
			} else if pos > 0 {
				if r, w := decodeLast(s, pos); in.set.has(r) {
					pos -= w
					pc++
					continue
				}
			}
		case opStr:
			if !in.back {
				if strings.HasPrefix(s[pos:], in.str) {
					pos += len(in.str)
					pc++
					continue
				}
			} else if strings.HasSuffix(s[:pos], in.str) {
				pos -= len(in.str)
				pc++
				continue
			}
		case opSplit:
			m.push(fChoice, in.y, pos, 0)
			if m.abort {
				return 0, false
			}
			pc = in.x
			continue
		case opJmp:
			pc = in.x
			continue
		case opSave:
			m.push(fCap, 0, m.caps[in.n], in.n)
			m.caps[in.n] = pos
			pc++
			continue
		case opReset:
			m.resetCaps(in.n, in.n2)
			pc++
			continue
		case opBol:
			if pos == 0 {
				pc++
				continue
			}
		case opEol:
			if pos == n {
				pc++
				continue
			}
		case opBolM:
			if pos == 0 || isLineTermBefore(s, pos) {
				pc++
				continue
			}
		case opEolM:
			if pos == n || isLineTermAt(s, pos) {
				pc++
				continue
			}
		case opWordB:
			if m.isBoundary(pos, in.fold) != in.neg {
				pc++
				continue
			}
		case opPeek:
			if m.peek(in, pos) {
				pc++
				continue
			}
		case opLook:
			if m.look(in, pos) {
				pc = in.y
				continue
			}
			if m.abort {
				return 0, false
			}
		case opBackref:
			if p, ok := m.backref(in, pos); ok {
				pos = p
				pc++
				continue
			}
		case opRepeat:
			if p, ok := m.repeat(pc, in, pos); ok {
				pos = p
				pc++
				continue
			}
		case opLoopInit:
			m.push(fReg, 0, m.regs[in.n], in.n)
			m.regs[in.n] = 0
			pc++
			continue
		case opLoopBranch:
			if p, ok := m.loopBranch(pc, in, pos); ok {
				pc = p
				continue
			}
		case opLoopStart:
			m.push(fReg, 0, m.regs[in.n+1], in.n+1)
			m.regs[in.n+1] = pos
			if in.n2 >= 0 {
				m.resetCaps(in.min, in.n2)
			}
			pc++
			continue
		case opLoopEnd:
			if m.charge() {
				return 0, false
			}
			if pos != m.regs[in.n+1] || m.regs[in.n] < in.min {
				m.push(fReg, 0, m.regs[in.n], in.n)
				m.regs[in.n]++
				pc = in.x
				continue
			}
		case opMatch:
			return pos, true
		}
		var ok bool
		if pc, pos, ok = m.backtrack(base); !ok {
			return 0, false
		}
	}
}

func (m *machine) resetCaps(lo, hi int) {
	for i := lo; i <= hi; i++ {
		if m.caps[i] >= 0 {
			m.push(fCap, 0, m.caps[i], i)
			m.caps[i] = -1
		}
	}
}

func (m *machine) loopBranch(pc int, in *inst, pos int) (int, bool) {
	cnt := m.regs[in.n]
	switch {
	case cnt >= in.max:
		return in.y, true
	case cnt < in.min:
		return pc + 1, true
	case in.greedy:
		m.push(fChoice, in.y, pos, 0)
		return pc + 1, !m.abort
	}
	m.push(fChoice, pc+1, pos, 0)
	return in.y, !m.abort
}

func (m *machine) backtrack(base int) (int, int, bool) {
	for len(m.stack) > base && !m.abort {
		top := len(m.stack) - 1
		f := &m.stack[top]
		switch f.kind {
		case fCap:
			m.caps[f.a] = f.pos
			m.stack = m.stack[:top]
		case fReg:
			m.regs[f.a] = f.pos
			m.stack = m.stack[:top]
		case fChoice:
			m.stack = m.stack[:top]
			if m.charge() {
				return 0, 0, false
			}
			return int(f.pc), f.pos, true
		case fRepG:
			if p, ok := m.giveBack(f, top); ok {
				return int(f.pc) + 1, p, true
			}
		case fRepL:
			if p, ok := m.takeMore(f, top); ok {
				return int(f.pc) + 1, p, true
			}
		case fRepGB:
			if p, ok := m.giveBackB(f, top); ok {
				return int(f.pc) + 1, p, true
			}
		case fRepLB:
			if p, ok := m.takeMoreB(f, top); ok {
				return int(f.pc) + 1, p, true
			}
		}
	}
	return 0, 0, false
}

func (m *machine) unwind(base int) {
	for top := len(m.stack) - 1; top >= base; top-- {
		f := &m.stack[top]
		switch f.kind {
		case fCap:
			m.caps[f.a] = f.pos
		case fReg:
			m.regs[f.a] = f.pos
		}
	}
	m.stack = m.stack[:base]
}

func (m *machine) keepRestores(base int) {
	j := base
	for i := base; i < len(m.stack); i++ {
		if k := m.stack[i].kind; k == fCap || k == fReg {
			m.stack[j] = m.stack[i]
			j++
		}
	}
	m.stack = m.stack[:j]
}

func (m *machine) isWord(r rune, fold bool) bool {
	if fold {
		return wordUISet.has(r)
	}
	return r < 0x80 && wordASCII[r]
}

var wordASCII = func() (t [128]bool) {
	for c := '0'; c <= '9'; c++ {
		t[c] = true
	}
	for c := 'a'; c <= 'z'; c++ {
		t[c] = true
		t[c-32] = true
	}
	t['_'] = true
	return t
}()

func (m *machine) isBoundary(pos int, fold bool) bool {
	s := m.s
	if !fold {
		a := pos > 0 && s[pos-1] < 0x80 && wordASCII[s[pos-1]]
		b := pos < len(s) && s[pos] < 0x80 && wordASCII[s[pos]]
		return a != b
	}
	a, b := false, false
	if pos > 0 {
		r, _ := decodeLast(s, pos)
		a = m.isWord(r, true)
	}
	if pos < len(s) {
		r, _ := decode(s, pos)
		b = m.isWord(r, true)
	}
	return a != b
}

func (m *machine) peek(in *inst, pos int) bool {
	s := m.s
	hit := false
	if !in.back {
		if pos < len(s) {
			r, _ := decode(s, pos)
			hit = in.set.has(r)
		}
	} else if pos > 0 {
		r, _ := decodeLast(s, pos)
		hit = in.set.has(r)
	}
	return hit != in.neg
}

func (m *machine) look(in *inst, pos int) bool {
	base := len(m.stack)
	_, ok := m.run(in.x, pos)
	if m.abort {
		return false
	}
	if ok && m.chargeRun(len(m.stack)-base) {
		return false
	}
	if in.neg {
		if ok {
			m.unwind(base)
			return false
		}
		return true
	}
	if !ok {
		return false
	}
	m.keepRestores(base)
	return true
}

func (m *machine) backref(in *inst, pos int) (int, bool) {
	a, b := m.caps[2*in.n], m.caps[2*in.n+1]
	if a < 0 || b < 0 {
		return pos, true
	}
	var p, cost int
	var ok bool
	switch {
	case !in.fold && !in.back:
		p, cost, ok = m.backrefFwd(a, b, pos)
	case !in.fold:
		p, cost, ok = m.backrefBack(a, b, pos)
	case !in.back:
		p, cost, ok = m.backrefFold(a, b, pos)
	default:
		p, cost, ok = m.backrefFoldBack(a, b, pos)
	}
	if cost > 32 && m.chargeRun(cost) {
		return 0, false
	}
	return p, ok
}

func commonPrefix(x, y string) int {
	n := min(len(x), len(y))
	i := 0
	for i+64 <= n && x[i:i+64] == y[i:i+64] {
		i += 64
	}
	for i < n && x[i] == y[i] {
		i++
	}
	return i
}

func commonSuffix(x, y string) int {
	n := min(len(x), len(y))
	i := 0
	for i+64 <= n && x[len(x)-i-64:len(x)-i] == y[len(y)-i-64:len(y)-i] {
		i += 64
	}
	for i < n && x[len(x)-1-i] == y[len(y)-1-i] {
		i++
	}
	return i
}

func (m *machine) backrefFwd(a, b, pos int) (int, int, bool) {
	sub := m.s[a:b]
	if len(m.s)-pos < len(sub) {
		return 0, 0, false
	}
	k := commonPrefix(m.s[pos:], sub)
	return pos + len(sub), k, k == len(sub)
}

func (m *machine) backrefBack(a, b, pos int) (int, int, bool) {
	sub := m.s[a:b]
	if pos < len(sub) {
		return 0, 0, false
	}
	k := commonSuffix(m.s[:pos], sub)
	return pos - len(sub), k, k == len(sub)
}

func (m *machine) backrefFold(a, b, pos int) (int, int, bool) {
	s := m.s
	start := a
	for a < b {
		if pos >= len(s) {
			return 0, a - start, false
		}
		r1, w1 := decode(s, a)
		r2, w2 := decode(s, pos)
		if !m.sameFold(r1, r2) {
			return 0, a - start, false
		}
		a += w1
		pos += w2
	}
	return pos, a - start, true
}

func (m *machine) backrefFoldBack(a, b, pos int) (int, int, bool) {
	s := m.s
	end := b
	for b > a {
		if pos <= 0 {
			return 0, end - b, false
		}
		r1, w1 := decodeLast(s, b)
		r2, w2 := decodeLast(s, pos)
		if !m.sameFold(r1, r2) {
			return 0, end - b, false
		}
		b -= w1
		pos -= w2
	}
	return pos, end - b, true
}

func (m *machine) sameFold(a, b rune) bool {
	if a == b {
		return true
	}
	cls := foldClass(a, m.re.f.unicode)
	return slices.Contains(cls, b)
}

func (m *machine) matchOne(set *charset, pos int) int {
	s := m.s
	if pos >= len(s) {
		return 0
	}
	if b := s[pos]; b < 0x80 {
		if set.hasASCII(b) {
			return 1
		}
		return 0
	}
	if r, w := decode(s, pos); set.has(r) {
		return w
	}
	return 0
}

func (m *machine) matchOneBack(set *charset, pos int) int {
	if pos <= 0 {
		return 0
	}
	if r, w := decodeLast(m.s, pos); set.has(r) {
		return w
	}
	return 0
}

func (m *machine) repeat(pc int, in *inst, pos int) (int, bool) {
	if in.back {
		return m.repeatBack(pc, in, pos)
	}
	if !in.greedy {
		p, ok := m.advance(in.set, pos, in.min)
		if !ok {
			return 0, false
		}
		if in.min < in.max {
			m.push(fRepL, pc, p, in.min)
		}
		return p, !m.abort
	}
	s := m.s
	start := pos
	cnt := 0
	set := in.set
	if !set.nonASCII {
		for cnt < in.max && pos < len(s) && set.hasASCII(s[pos]) {
			pos++
			cnt++
		}
	} else if set.lit != "" {
		for cnt < in.max && strings.HasPrefix(s[pos:], set.lit) {
			pos += len(set.lit)
			cnt++
		}
	} else {
		for cnt < in.max {
			w := m.matchOne(set, pos)
			if w == 0 {
				break
			}
			pos += w
			cnt++
		}
	}
	if cnt < in.min || cnt > 32 && m.chargeRun(cnt) {
		return 0, false
	}
	if cnt > in.min && !in.poss {
		minPos := start + in.min
		if set.nonASCII {
			minPos, _ = m.advance(set, start, in.min)
		}
		m.push(fRepG, pc, pos, minPos)
	}
	return pos, true
}

func (m *machine) advance(set *charset, pos, k int) (int, bool) {
	for range k {
		w := m.matchOne(set, pos)
		if w == 0 {
			return 0, false
		}
		pos += w
	}
	return pos, true
}

func (m *machine) giveBack(f *frame, top int) (int, bool) {
	in := &m.re.prog[f.pc]
	s := m.s
	p := f.pos
	for {
		if !in.set.nonASCII {
			p--
		} else {
			p = stepBack(s, p)
		}
		if p < f.a {
			m.stack = m.stack[:top]
			return 0, false
		}
		if in.follow == nil || p < len(s) && in.follow[s[p]] {
			break
		}
	}
	if m.charge() {
		return 0, false
	}
	if p == f.a {
		m.stack = m.stack[:top]
	} else {
		f.pos = p
	}
	return p, true
}

func (m *machine) takeMore(f *frame, top int) (int, bool) {
	in := &m.re.prog[f.pc]
	w := m.matchOne(in.set, f.pos)
	if w == 0 || m.charge() {
		m.stack = m.stack[:top]
		return 0, false
	}
	p := f.pos + w
	f.pos = p
	f.a++
	if f.a >= in.max {
		m.stack = m.stack[:top]
	}
	return p, true
}

func (m *machine) repeatBack(pc int, in *inst, pos int) (int, bool) {
	if !in.greedy {
		p, ok := m.retreat(in.set, pos, in.min)
		if !ok {
			return 0, false
		}
		if in.min < in.max {
			m.push(fRepLB, pc, p, in.min)
		}
		return p, !m.abort
	}
	start := pos
	cnt := 0
	for cnt < in.max {
		w := m.matchOneBack(in.set, pos)
		if w == 0 {
			break
		}
		pos -= w
		cnt++
	}
	if cnt < in.min || cnt > 32 && m.chargeRun(cnt) {
		return 0, false
	}
	if cnt > in.min {
		limit, _ := m.retreat(in.set, start, in.min)
		m.push(fRepGB, pc, pos, limit)
	}
	return pos, true
}

func (m *machine) retreat(set *charset, pos, k int) (int, bool) {
	for range k {
		w := m.matchOneBack(set, pos)
		if w == 0 {
			return 0, false
		}
		pos -= w
	}
	return pos, true
}

func (m *machine) giveBackB(f *frame, top int) (int, bool) {
	p := f.pos + charWidth(m.s, f.pos)
	if p > f.a || m.charge() {
		m.stack = m.stack[:top]
		return 0, false
	}
	if p == f.a {
		m.stack = m.stack[:top]
	} else {
		f.pos = p
	}
	return p, true
}

func (m *machine) takeMoreB(f *frame, top int) (int, bool) {
	in := &m.re.prog[f.pc]
	w := m.matchOneBack(in.set, f.pos)
	if w == 0 || m.charge() {
		m.stack = m.stack[:top]
		return 0, false
	}
	p := f.pos - w
	f.pos = p
	f.a++
	if f.a >= in.max {
		m.stack = m.stack[:top]
	}
	return p, true
}
