package regex

import (
	"slices"
	"strings"
	"unicode/utf8"
)

type opcode uint8

const (
	opMatch opcode = iota
	opByte
	opSet
	opStr
	opSplit
	opJmp
	opSave
	opReset
	opBol
	opEol
	opBolM
	opEolM
	opWordB
	opPeek
	opLook
	opBackref
	opRepeat
	opLoopInit
	opLoopBranch
	opLoopStart
	opLoopEnd
)

type inst struct {
	op     opcode
	back   bool
	neg    bool
	greedy bool
	poss   bool
	fold   bool
	c      byte
	n      int
	n2     int
	min    int
	max    int
	x      int
	y      int
	set    *charset
	str    string
	follow *[256]bool
}

type compiler struct {
	prog    []inst
	nregs   int
	f       flagSet
	maxChar rune
}

func compileProg(root *node, f flagSet) ([]inst, int) {
	c := &compiler{f: f, maxChar: maxUnit}
	if f.unicode {
		c.maxChar = maxCodePt
	}
	c.node(root, false, true)
	c.emit(inst{op: opMatch})
	return c.prog, c.nregs
}

func (c *compiler) emit(i inst) int {
	c.prog = append(c.prog, i)
	return len(c.prog) - 1
}

func (c *compiler) pc() int { return len(c.prog) }

func (c *compiler) node(n *node, back, atEnd bool) {
	switch n.kind {
	case nEmpty:
	case nChar:
		c.char(n.r, back)
	case nSet:
		c.set(n.set, back)
	case nSeq:
		c.seq(n.subs, back, atEnd)
	case nAlt:
		c.alt(n, back, atEnd)
	case nGroup:
		open, close := 2*n.idx, 2*n.idx+1
		if back {
			open, close = close, open
		}
		c.emit(inst{op: opSave, n: open})
		c.node(n.subs[0], back, false)
		c.emit(inst{op: opSave, n: close})
	case nRepeat:
		c.repeat(n, back, atEnd, nil)
	case nBol:
		c.emit(inst{op: pick(c.f.multiline, opBolM, opBol)})
	case nEol:
		c.emit(inst{op: pick(c.f.multiline, opEolM, opEol)})
	case nWordB, nNotWordB:
		c.emit(inst{op: opWordB, neg: n.kind == nNotWordB, fold: c.f.unicode && c.f.icase})
	case nLook:
		c.look(n)
	case nBackref:
		c.emit(inst{op: opBackref, n: n.idx, fold: c.f.icase, back: back})
	}
}

func pick(cond bool, a, b opcode) opcode {
	if cond {
		return a
	}
	return b
}

func (c *compiler) char(r rune, back bool) {
	if r < 0x80 {
		c.emit(inst{op: opByte, c: byte(r), back: back})
		return
	}
	c.emit(inst{op: opStr, str: encodeUnit(r), back: back})
}

func (c *compiler) set(s *charset, back bool) {
	if r, ok := s.single(); ok {
		c.char(r, back)
		return
	}
	c.emit(inst{op: opSet, set: s, back: back})
}

func encodeUnit(r rune) string {
	if r >= 0xD800 && r <= 0xDFFF {
		return string([]byte{0xE0 | byte(r>>12), 0x80 | byte(r>>6)&0x3F, 0x80 | byte(r)&0x3F})
	}
	return string(utf8.AppendRune(nil, r))
}

func (c *compiler) seq(subs []*node, back, atEnd bool) {
	if back {
		for _, sub := range slices.Backward(subs) {
			c.node(sub, true, false)
		}
		return
	}
	for i := 0; i < len(subs); i++ {
		if j := literalRun(subs, i); j-i > 1 {
			var b strings.Builder
			for _, n := range subs[i:j] {
				b.WriteString(encodeUnit(n.r))
			}
			c.emit(inst{op: opStr, str: b.String()})
			i = j - 1
			continue
		}
		n := subs[i]
		last := atEnd && i == len(subs)-1
		if n.kind == nRepeat {
			c.repeat(n, false, last, subs[i+1:])
			continue
		}
		c.node(n, false, last)
	}
}

func literalRun(subs []*node, i int) int {
	j := i
	for j < len(subs) && subs[j].kind == nChar {
		j++
	}
	return j
}

func (c *compiler) alt(n *node, back, atEnd bool) {
	var jumps []int
	for i, sub := range n.subs {
		if i == len(n.subs)-1 {
			c.node(sub, back, atEnd)
			break
		}
		split := c.emit(inst{op: opSplit})
		c.prog[split].x = c.pc()
		c.node(sub, back, atEnd)
		jumps = append(jumps, c.emit(inst{op: opJmp}))
		c.prog[split].y = c.pc()
	}
	for _, j := range jumps {
		c.prog[j].x = c.pc()
	}
}

func singleSet(n *node) *charset {
	switch n.kind {
	case nChar:
		return newCharset([]rune{n.r, n.r})
	case nSet:
		return n.set
	}
	return nil
}

func (c *compiler) repeat(n *node, back, atEnd bool, rest []*node) {
	sub := n.subs[0]
	if n.max == 0 {
		return
	}
	if s := singleSet(sub); s != nil {
		in := inst{op: opRepeat, set: s, min: n.min, max: n.max, greedy: n.greedy, back: back}
		if n.max == infinite {
			in.max = int(^uint(0) >> 1)
		}
		if !back && n.greedy {
			in.poss, in.follow = c.possessive(s, n.min, atEnd, rest)
		}
		c.emit(in)
		return
	}
	if nullable(sub) || n.min > 1 || n.max > 1 && n.max != infinite {
		c.counted(n, back)
		return
	}
	reset := sub.capLo > 0
	switch {
	case n.min == 1 && n.max == 1:
		c.node(sub, back, atEnd)
	case n.max == 1:
		split := c.emit(inst{op: opSplit})
		body := c.pc()
		c.node(sub, back, false)
		c.setSplit(split, body, c.pc(), n.greedy)
	case n.min == 0:
		split := c.emit(inst{op: opSplit})
		body := c.pc()
		c.resetCaps(sub, reset)
		c.node(sub, back, false)
		c.emit(inst{op: opJmp, x: split})
		c.setSplit(split, body, c.pc(), n.greedy)
	default:
		body := c.pc()
		c.resetCaps(sub, reset)
		c.node(sub, back, false)
		split := c.emit(inst{op: opSplit})
		c.setSplit(split, body, c.pc(), n.greedy)
	}
}

func (c *compiler) setSplit(split, body, exit int, greedy bool) {
	if greedy {
		c.prog[split].x, c.prog[split].y = body, exit
		return
	}
	c.prog[split].x, c.prog[split].y = exit, body
}

func (c *compiler) resetCaps(sub *node, reset bool) {
	if reset {
		c.emit(inst{op: opReset, n: 2 * sub.capLo, n2: 2*sub.capHi + 1})
	}
}

func (c *compiler) counted(n *node, back bool) {
	sub := n.subs[0]
	r := c.nregs
	c.nregs += 2
	max := n.max
	if max == infinite {
		max = int(^uint(0) >> 1)
	}
	c.emit(inst{op: opLoopInit, n: r})
	branch := c.emit(inst{op: opLoopBranch, n: r, min: n.min, max: max, greedy: n.greedy})
	start := inst{op: opLoopStart, n: r, n2: -1}
	if sub.capLo > 0 {
		start.min, start.n2 = 2*sub.capLo, 2*sub.capHi+1
	}
	c.emit(start)
	c.node(sub, back, false)
	c.emit(inst{op: opLoopEnd, n: r, min: n.min, x: branch})
	c.prog[branch].y = c.pc()
}

func (c *compiler) look(n *node) {
	sub := n.subs[0]
	if s := singleSet(sub); s != nil {
		c.emit(inst{op: opPeek, set: s, neg: n.neg, back: n.behind})
		return
	}
	at := c.emit(inst{op: opLook, neg: n.neg, back: n.behind, x: c.pc() + 1})
	c.node(sub, n.behind, false)
	c.emit(inst{op: opMatch})
	c.prog[at].y = c.pc()
}

func (c *compiler) possessive(s *charset, min int, atEnd bool, rest []*node) (bool, *[256]bool) {
	if len(rest) == 0 {
		return atEnd, nil
	}
	switch rest[0].kind {
	case nWordB:
		if min >= 1 && subsetOf(s, c.wordSet()) {
			return true, nil
		}
	case nEol:
		if !c.f.multiline {
			return true, nil
		}
	}
	fs := firstOfSeq(rest)
	if fs.nullable || fs.any {
		return false, nil
	}
	set := newCharset(fs.ranges)
	if !s.intersects(set) {
		return true, nil
	}
	return false, byteTable(set)
}

func (c *compiler) wordSet() *charset {
	if c.f.unicode && c.f.icase {
		return wordUISet
	}
	return newCharset(wordRanges)
}

func subsetOf(a, b *charset) bool {
	rs := a.ranges
	for i := 0; i < len(rs); i += 2 {
		for r := rs[i]; r <= rs[i+1]; r++ {
			if !b.has(r) {
				return false
			}
			if r-rs[i] > 512 {
				return false
			}
		}
	}
	return true
}

type first struct {
	ranges   []rune
	nullable bool
	any      bool
}

func firstOf(n *node) first {
	switch n.kind {
	case nEmpty, nBol, nEol, nWordB, nNotWordB, nLook:
		return first{nullable: true}
	case nChar:
		return first{ranges: []rune{n.r, n.r}}
	case nSet:
		return first{ranges: n.set.ranges}
	case nSeq:
		return firstOfSeq(n.subs)
	case nAlt:
		var out first
		for _, s := range n.subs {
			f := firstOf(s)
			out.ranges = append(out.ranges, f.ranges...)
			out.nullable = out.nullable || f.nullable
			out.any = out.any || f.any
		}
		return out
	case nGroup:
		return firstOf(n.subs[0])
	case nRepeat:
		if n.max == 0 {
			return first{nullable: true}
		}
		f := firstOf(n.subs[0])
		f.nullable = f.nullable || n.min == 0
		return f
	case nBackref:
		return first{any: true, nullable: true}
	}
	return first{any: true, nullable: true}
}

func firstOfSeq(subs []*node) first {
	var out first
	for _, s := range subs {
		f := firstOf(s)
		out.ranges = append(out.ranges, f.ranges...)
		out.any = out.any || f.any
		if !f.nullable {
			return out
		}
	}
	out.nullable = true
	return out
}

func nullable(n *node) bool {
	return firstOf(n).nullable
}

func byteTable(s *charset) *[256]bool {
	var t [256]bool
	if s.has(0xFFFD) {
		for b := 0x80; b < 0x100; b++ {
			t[b] = true
		}
	}
	rs := s.ranges
	for i := 0; i < len(rs); i += 2 {
		lo, hi := rs[i], rs[i+1]
		for r := lo; r <= hi && r < 0x80; r++ {
			t[r] = true
		}
		if hi < 0x80 {
			continue
		}
		if lo < 0x80 {
			lo = 0x80
		}
		for b := encodeUnit(lo)[0]; ; b++ {
			t[b] = true
			if b == encodeUnit(hi)[0] {
				break
			}
		}
	}
	return &t
}

func markCaps(n *node) {
	lo, hi := 0, 0
	for _, s := range n.subs {
		markCaps(s)
		if s.capLo > 0 && (lo == 0 || s.capLo < lo) {
			lo = s.capLo
		}
		if s.capHi > hi {
			hi = s.capHi
		}
	}
	if n.kind == nGroup {
		if lo == 0 || n.idx < lo {
			lo = n.idx
		}
		if n.idx > hi {
			hi = n.idx
		}
	}
	n.capLo, n.capHi = lo, hi
}
