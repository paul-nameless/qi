package regex

import (
	"math"
	"strings"
	"unicode"
	"unicode/utf16"
)

type nodeKind uint8

const (
	nEmpty nodeKind = iota
	nChar
	nSet
	nSeq
	nAlt
	nGroup
	nRepeat
	nBol
	nEol
	nWordB
	nNotWordB
	nLook
	nBackref
)

const infinite = -1

type node struct {
	kind   nodeKind
	r      rune
	set    *charset
	folded bool
	subs   []*node
	idx    int
	min    int
	max    int
	greedy bool
	neg    bool
	behind bool
	name   string
	capLo  int
	capHi  int
}

type parser struct {
	src       []rune
	pos       int
	unicode   bool
	icase     bool
	multiline bool
	dotall    bool
	ncap      int
	totalCaps int
	hasNamed  bool
	names     []string
	namedRefs []*node
	depth     int
}

type syntaxErr struct{ msg string }

func (e *syntaxErr) Error() string { return e.msg }

func parsePattern(pattern string, f flagSet) (root *node, names []string, err error) {
	p := &parser{unicode: f.unicode, icase: f.icase, multiline: f.multiline, dotall: f.dotall}
	p.src = sourceUnits(pattern, f.unicode)
	p.prescan()
	p.names = []string{""}
	defer func() {
		if r := recover(); r != nil {
			se, ok := r.(*syntaxErr)
			if !ok {
				panic(r)
			}
			err = se
		}
	}()
	root = p.parseDisjunction()
	if p.pos < len(p.src) {
		p.fail("Unmatched ')'")
	}
	p.resolveNamedRefs()
	root = simplify(root)
	markCaps(root)
	return root, p.names, nil
}

func sourceUnits(pattern string, unicodeMode bool) []rune {
	rs := []rune(pattern)
	if unicodeMode {
		return rs
	}
	out := make([]rune, 0, len(rs))
	for _, r := range rs {
		if r > maxUnit {
			hi, lo := utf16.EncodeRune(r)
			out = append(out, hi, lo)
			continue
		}
		out = append(out, r)
	}
	return out
}

func (p *parser) fail(msg string) {
	panic(&syntaxErr{msg})
}

func (p *parser) more() bool { return p.pos < len(p.src) }

func (p *parser) peek() rune {
	if p.pos < len(p.src) {
		return p.src[p.pos]
	}
	return -1
}

func (p *parser) peekAt(i int) rune {
	if p.pos+i < len(p.src) {
		return p.src[p.pos+i]
	}
	return -1
}

func (p *parser) eat(r rune) bool {
	if p.peek() == r {
		p.pos++
		return true
	}
	return false
}

func (p *parser) prescan() {
	inClass := false
	for i := 0; i < len(p.src); i++ {
		switch c := p.src[i]; {
		case c == '\\':
			i++
		case inClass:
			if c == ']' {
				inClass = false
			}
		case c == '[':
			inClass = true
		case c == '(':
			if i+1 < len(p.src) && p.src[i+1] == '?' {
				if i+3 < len(p.src) && p.src[i+2] == '<' && p.src[i+3] != '=' && p.src[i+3] != '!' {
					p.totalCaps++
					p.hasNamed = true
				}
				continue
			}
			p.totalCaps++
		}
	}
}

func (p *parser) parseDisjunction() *node {
	first := p.parseAlternative()
	if p.peek() != '|' {
		return first
	}
	alt := &node{kind: nAlt, subs: []*node{first}}
	for p.eat('|') {
		alt.subs = append(alt.subs, p.parseAlternative())
	}
	return alt
}

func (p *parser) parseAlternative() *node {
	seq := &node{kind: nSeq}
	for p.more() && p.peek() != '|' && p.peek() != ')' {
		seq.subs = append(seq.subs, p.parseTerm()...)
	}
	switch len(seq.subs) {
	case 0:
		return &node{kind: nEmpty}
	case 1:
		return seq.subs[0]
	}
	return seq
}

func (p *parser) parseTerm() []*node {
	atoms, quantifiable := p.parseAtom()
	if len(atoms) == 0 {
		return nil
	}
	last := atoms[len(atoms)-1]
	min, max, greedy, ok := p.parseQuantifier()
	if !ok {
		return atoms
	}
	if !quantifiable {
		p.fail("Nothing to repeat")
	}
	atoms[len(atoms)-1] = &node{kind: nRepeat, min: min, max: max, greedy: greedy, subs: []*node{last}}
	return atoms
}

func (p *parser) parseAtom() ([]*node, bool) {
	c := p.peek()
	switch c {
	case '^':
		p.pos++
		return []*node{{kind: nBol}}, false
	case '$':
		p.pos++
		return []*node{{kind: nEol}}, false
	case '\\':
		return p.parseAtomEscape()
	case '(':
		return p.parseGroup()
	case '.':
		p.pos++
		return []*node{p.dotNode()}, true
	case '[':
		return []*node{p.parseClass()}, true
	case '*', '+', '?':
		p.fail("Nothing to repeat")
	case '{':
		if p.unicode {
			if p.validBraceAt(p.pos) {
				p.fail("Nothing to repeat")
			}
			p.fail("Lone quantifier brackets")
		}
		if p.validBraceAt(p.pos) {
			p.fail("Nothing to repeat")
		}
	case '}', ']':
		if p.unicode {
			p.fail("Lone quantifier brackets")
		}
	}
	p.pos++
	return []*node{p.charNode(c)}, true
}

func (p *parser) dotNode() *node {
	max := p.maxChar()
	if p.dotall {
		return &node{kind: nSet, set: newCharset([]rune{0, max})}
	}
	return &node{kind: nSet, set: newCharset(negateRanges(lineTermRanges, max))}
}

func (p *parser) maxChar() rune {
	if p.unicode {
		return maxCodePt
	}
	return maxUnit
}

func (p *parser) charNode(c rune) *node {
	if p.icase {
		cls := foldClass(c, p.unicode)
		if len(cls) > 1 {
			rs := make([]rune, 0, 2*len(cls))
			for _, m := range cls {
				rs = append(rs, m, m)
			}
			return &node{kind: nSet, set: newCharset(rs), folded: true, r: c}
		}
	}
	return &node{kind: nChar, r: c}
}

func (p *parser) setNode(rs []rune) *node {
	if p.icase {
		rs = foldClosure(rs, p.unicode)
	}
	return &node{kind: nSet, set: newCharset(rs)}
}

func (p *parser) validBraceAt(i int) bool {
	save := p.pos
	p.pos = i
	_, _, ok := p.parseBraces()
	p.pos = save
	return ok
}

func (p *parser) parseQuantifier() (min, max int, greedy, ok bool) {
	switch p.peek() {
	case '*':
		p.pos++
		min, max = 0, infinite
	case '+':
		p.pos++
		min, max = 1, infinite
	case '?':
		p.pos++
		min, max = 0, 1
	case '{':
		var good bool
		min, max, good = p.parseBraces()
		if !good {
			if p.unicode {
				p.fail("Incomplete quantifier")
			}
			return 0, 0, false, false
		}
		if max != infinite && min > max {
			p.fail("numbers out of order in {} quantifier")
		}
	default:
		return 0, 0, false, false
	}
	greedy = !p.eat('?')
	return min, max, greedy, true
}

func (p *parser) parseBraces() (min, max int, ok bool) {
	start := p.pos
	if !p.eat('{') {
		return 0, 0, false
	}
	min, ok = p.parseInt()
	if !ok {
		p.pos = start
		return 0, 0, false
	}
	max = min
	if p.eat(',') {
		if p.peek() == '}' {
			max = infinite
		} else if max, ok = p.parseInt(); !ok {
			p.pos = start
			return 0, 0, false
		}
	}
	if !p.eat('}') {
		p.pos = start
		return 0, 0, false
	}
	return min, max, true
}

func (p *parser) parseInt() (int, bool) {
	n, digits := 0, 0
	for c := p.peek(); c >= '0' && c <= '9'; c = p.peek() {
		if n <= (math.MaxInt32-9)/10 {
			n = n*10 + int(c-'0')
		} else {
			n = math.MaxInt32
		}
		p.pos++
		digits++
	}
	return n, digits > 0
}

const maxNesting = 256

func (p *parser) parseGroup() ([]*node, bool) {
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > maxNesting {
		p.fail("Regular expression too large")
	}
	p.pos++
	if !p.eat('?') {
		p.ncap++
		idx := p.ncap
		p.names = append(p.names, "")
		sub := p.parseDisjunction()
		p.closeGroup()
		return []*node{{kind: nGroup, idx: idx, subs: []*node{sub}}}, true
	}
	switch {
	case p.eat(':'):
		sub := p.parseDisjunction()
		p.closeGroup()
		return []*node{sub}, true
	case p.eat('='), p.eat('!'):
		return p.parseLook(false, p.src[p.pos-1] == '!')
	case p.peek() == '<' && (p.peekAt(1) == '=' || p.peekAt(1) == '!'):
		p.pos += 2
		return p.parseLook(true, p.src[p.pos-1] == '!')
	case p.eat('<'):
		name := p.parseGroupName()
		for _, n := range p.names {
			if n == name {
				p.fail("Duplicate capture group name")
			}
		}
		p.ncap++
		idx := p.ncap
		p.names = append(p.names, name)
		sub := p.parseDisjunction()
		p.closeGroup()
		return []*node{{kind: nGroup, idx: idx, name: name, subs: []*node{sub}}}, true
	}
	p.fail("Invalid group")
	return nil, false
}

func (p *parser) parseLook(behind, neg bool) ([]*node, bool) {
	sub := p.parseDisjunction()
	p.closeGroup()
	n := &node{kind: nLook, behind: behind, neg: neg, subs: []*node{sub}}
	return []*node{n}, !behind && !p.unicode
}

func (p *parser) closeGroup() {
	if !p.eat(')') {
		p.fail("Unterminated group")
	}
}

func (p *parser) parseGroupName() string {
	var b strings.Builder
	for first := true; ; first = false {
		if p.eat('>') {
			if first {
				p.fail("Invalid capture group name")
			}
			return b.String()
		}
		c, ok := p.groupNameChar()
		if !ok || !(isIDStart(c) || !first && isIDPart(c)) {
			p.fail("Invalid capture group name")
		}
		b.WriteRune(c)
	}
}

func (p *parser) groupNameChar() (rune, bool) {
	if !p.more() {
		return 0, false
	}
	c := p.src[p.pos]
	p.pos++
	if c >= 0xD800 && c <= 0xDBFF && p.more() && p.src[p.pos] >= 0xDC00 && p.src[p.pos] <= 0xDFFF {
		c = utf16.DecodeRune(c, p.src[p.pos])
		p.pos++
	}
	if c != '\\' {
		return c, true
	}
	if !p.eat('u') {
		return 0, false
	}
	r, ok := p.parseUnicodeEscapeBody(true)
	return r, ok
}

func isIDStart(c rune) bool {
	return c == '$' || c == '_' || unicode.IsLetter(c) || unicode.Is(unicode.Nl, c) || unicode.Is(unicode.Other_ID_Start, c)
}

func isIDPart(c rune) bool {
	return isIDStart(c) || unicode.IsDigit(c) || unicode.In(c, unicode.Mn, unicode.Mc, unicode.Nd, unicode.Pc) || c == 0x200C || c == 0x200D
}

func (p *parser) parseAtomEscape() ([]*node, bool) {
	p.pos++
	if !p.more() {
		p.fail("\\ at end of pattern")
	}
	c := p.src[p.pos]
	switch c {
	case 'b':
		p.pos++
		return []*node{{kind: nWordB}}, false
	case 'B':
		p.pos++
		return []*node{{kind: nNotWordB}}, false
	case 'k':
		if p.unicode || p.hasNamed {
			return []*node{p.parseNamedRef()}, true
		}
	case '1', '2', '3', '4', '5', '6', '7', '8', '9':
		if n, ok := p.parseBackrefNumber(); ok {
			return []*node{{kind: nBackref, idx: n}}, true
		}
	}
	if rs, ok := p.parseClassEscapeSet(); ok {
		return []*node{p.setNode(rs)}, true
	}
	r := p.parseCharEscape(false)
	return []*node{p.charNode(r)}, true
}

func (p *parser) parseBackrefNumber() (int, bool) {
	start := p.pos
	n, _ := p.parseInt()
	if n <= p.totalCaps {
		return n, true
	}
	if p.unicode {
		p.fail("Invalid escape")
	}
	p.pos = start
	return 0, false
}

func (p *parser) parseNamedRef() *node {
	p.pos++
	if !p.eat('<') {
		p.fail("Invalid named reference")
	}
	name := p.parseGroupName()
	n := &node{kind: nBackref, name: name}
	p.namedRefs = append(p.namedRefs, n)
	return n
}

func (p *parser) resolveNamedRefs() {
	for _, n := range p.namedRefs {
		found := false
		for i, nm := range p.names {
			if nm == n.name && i > 0 {
				n.idx = i
				found = true
			}
		}
		if !found {
			p.fail("Invalid named capture referenced")
		}
	}
}

func (p *parser) parseClassEscapeSet() ([]rune, bool) {
	c := p.src[p.pos]
	max := p.maxChar()
	var rs []rune
	switch c {
	case 'd':
		rs = digitRanges
	case 'D':
		rs = negateRanges(digitRanges, max)
	case 's':
		rs = spaceRanges
	case 'S':
		rs = negateRanges(spaceRanges, max)
	case 'w':
		rs = p.wordRanges()
	case 'W':
		rs = negateRanges(p.wordRanges(), max)
	case 'p', 'P':
		if !p.unicode {
			return nil, false
		}
		p.pos++
		rs = p.parseProperty()
		if c == 'P' {
			rs = negateRanges(rs, max)
		}
		return rs, true
	default:
		return nil, false
	}
	p.pos++
	return rs, true
}

func (p *parser) wordRanges() []rune {
	if p.unicode && p.icase {
		return wordUIRanges
	}
	return wordRanges
}

func (p *parser) parseProperty() []rune {
	if !p.eat('{') {
		p.fail("Invalid property name")
	}
	var name, value strings.Builder
	cur := &name
	for {
		c := p.peek()
		if c == -1 {
			p.fail("Invalid property name")
		}
		p.pos++
		if c == '}' {
			break
		}
		if c == '=' && cur == &name {
			cur = &value
			continue
		}
		if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			p.fail("Invalid property name")
		}
		cur.WriteRune(c)
	}
	if cur == &value && value.Len() == 0 {
		p.fail("Invalid property name")
	}
	rs, err := propertyRanges(name.String(), value.String())
	if err == errPropUnsupported {
		p.fail("unsupported: \\p{" + name.String() + "}")
	}
	if err != nil {
		p.fail("Invalid property name")
	}
	return rs
}

func (p *parser) parseCharEscape(inClass bool) rune {
	c := p.src[p.pos]
	p.pos++
	switch c {
	case 'f':
		return '\f'
	case 'n':
		return '\n'
	case 'r':
		return '\r'
	case 't':
		return '\t'
	case 'v':
		return '\v'
	case 'c':
		return p.parseControl(inClass)
	case '0':
		if d := p.peek(); d < '0' || d > '9' {
			return 0
		}
		if p.unicode {
			p.fail("Invalid decimal escape")
		}
		p.pos--
		return p.parseOctal()
	case '1', '2', '3', '4', '5', '6', '7':
		if p.unicode {
			p.fail("Invalid escape")
		}
		p.pos--
		return p.parseOctal()
	case 'x':
		if r, ok := p.parseHex(2); ok {
			return r
		}
		if p.unicode {
			p.fail("Invalid escape")
		}
		return 'x'
	case 'u':
		if r, ok := p.parseUnicodeEscapeBody(p.unicode); ok {
			return r
		}
		if p.unicode {
			p.fail("Invalid Unicode escape")
		}
		return 'u'
	}
	if p.unicode {
		if strings.ContainsRune("^$\\.*+?()[]{}|/", c) || inClass && c == '-' {
			return c
		}
		p.fail("Invalid escape")
	}
	return c
}

func (p *parser) parseControl(inClass bool) rune {
	if c := p.peek(); c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || inClass && !p.unicode && (c >= '0' && c <= '9' || c == '_') {
		p.pos++
		return c % 32
	}
	if p.unicode {
		p.fail("Invalid unicode escape")
	}
	p.pos--
	return '\\'
}

func (p *parser) parseOctal() rune {
	v := rune(0)
	for i := 0; i < 3; i++ {
		c := p.peek()
		if c < '0' || c > '7' || v*8+(c-'0') > 0377 {
			break
		}
		v = v*8 + (c - '0')
		p.pos++
	}
	return v
}

func (p *parser) parseHex(n int) (rune, bool) {
	v := rune(0)
	for i := 0; i < n; i++ {
		d := hexVal(p.peekAt(i))
		if d < 0 {
			return 0, false
		}
		v = v*16 + d
	}
	p.pos += n
	return v, true
}

func hexVal(c rune) rune {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10
	}
	return -1
}

func (p *parser) parseUnicodeEscapeBody(unicodeMode bool) (rune, bool) {
	if p.peek() == '{' && unicodeMode {
		return p.parseBracedCodePoint()
	}
	r, ok := p.parseHex(4)
	if !ok {
		return 0, false
	}
	if unicodeMode && r >= 0xD800 && r <= 0xDBFF && p.peek() == '\\' && p.peekAt(1) == 'u' {
		save := p.pos
		p.pos += 2
		if lo, ok := p.parseHex(4); ok && lo >= 0xDC00 && lo <= 0xDFFF {
			return utf16.DecodeRune(r, lo), true
		}
		p.pos = save
	}
	return r, true
}

func (p *parser) parseBracedCodePoint() (rune, bool) {
	p.pos++
	v, digits := rune(0), 0
	for d := hexVal(p.peek()); d >= 0; d = hexVal(p.peek()) {
		v = v*16 + d
		if v > maxCodePt {
			p.fail("Invalid Unicode escape")
		}
		p.pos++
		digits++
	}
	if digits == 0 || !p.eat('}') {
		p.fail("Invalid Unicode escape")
	}
	return v, true
}

func (p *parser) parseClass() *node {
	p.pos++
	negate := p.eat('^')
	var rs []rune
	for {
		if !p.more() {
			p.fail("Unterminated character class")
		}
		if p.eat(']') {
			break
		}
		lo, loSet := p.parseClassAtom()
		if p.peek() != '-' || p.peekAt(1) == ']' || p.peekAt(1) == -1 {
			rs = appendAtom(rs, lo, loSet)
			continue
		}
		p.pos++
		hi, hiSet := p.parseClassAtom()
		if loSet != nil || hiSet != nil {
			if p.unicode {
				p.fail("Invalid character class")
			}
			rs = appendAtom(rs, lo, loSet)
			rs = append(rs, '-', '-')
			rs = appendAtom(rs, hi, hiSet)
			continue
		}
		if lo > hi {
			p.fail("Range out of order in character class")
		}
		rs = append(rs, lo, hi)
	}
	if p.icase {
		rs = foldClosure(rs, p.unicode)
	}
	if negate {
		rs = negateRanges(rs, p.maxChar())
	}
	return &node{kind: nSet, set: newCharset(rs)}
}

func appendAtom(rs []rune, c rune, set []rune) []rune {
	if set != nil {
		return append(rs, set...)
	}
	return append(rs, c, c)
}

func (p *parser) parseClassAtom() (rune, []rune) {
	c := p.src[p.pos]
	if c != '\\' {
		p.pos++
		return c, nil
	}
	p.pos++
	if !p.more() {
		p.fail("\\ at end of pattern")
	}
	if rs, ok := p.parseClassEscapeSet(); ok {
		return 0, normRanges(rs)
	}
	switch e := p.src[p.pos]; e {
	case 'b':
		p.pos++
		return '\b', nil
	case '-':
		if p.unicode {
			p.pos++
			return '-', nil
		}
	case 'k', 'B':
		if !p.unicode {
			p.pos++
			return e, nil
		}
		p.fail("Invalid class escape")
	case '8', '9':
		if !p.unicode {
			p.pos++
			return e, nil
		}
		p.fail("Invalid class escape")
	}
	return p.parseCharEscape(true), nil
}

func simplify(n *node) *node {
	for i, s := range n.subs {
		n.subs[i] = simplify(s)
	}
	if n.kind != nAlt {
		return n
	}
	var rs []rune
	for _, s := range n.subs {
		switch s.kind {
		case nChar:
			rs = append(rs, s.r, s.r)
		case nSet:
			rs = append(rs, s.set.ranges...)
		default:
			return n
		}
	}
	return &node{kind: nSet, set: newCharset(rs)}
}
