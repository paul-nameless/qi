package regex

import (
	"strings"
	"sync"
	"sync/atomic"
)

type Error struct{ Msg string }

func (e *Error) Error() string { return e.Msg }

const errTooComplex = "regular expression too complex"

const rescueSteps = 200_000

var stepLimit atomic.Int64

func init() { stepLimit.Store(10_000_000) }

func SetStepLimit(n int) int {
	return int(stepLimit.Swap(int64(n)))
}

const (
	EngineAuto = iota
	EngineBacktrack
	EngineRE2
)

type flagSet struct {
	hasIndices bool
	global     bool
	icase      bool
	multiline  bool
	dotall     bool
	unicode    bool
	sticky     bool
}

type scanKind uint8

const (
	scanNone scanKind = iota
	scanAnchor
	scanPrefix
	scanByte
	scanTable
	scanLine
)

type Regexp struct {
	source    string
	flags     string
	f         flagSet
	prog      []inst
	ncap      int
	nregs     int
	names     []string
	scan      scanKind
	prefix    string
	first     byte
	table     *[256]bool
	literal   string
	isLit     bool
	re2       *re2State
	engine    int
	cache     atomic.Pointer[machine]
	preferRE2 atomic.Bool
}

func parseFlags(flags string) (flagSet, string, error) {
	var f flagSet
	bad := &Error{"Invalid flags supplied to RegExp constructor '" + flags + "'"}
	var seen uint32
	for _, c := range flags {
		i := strings.IndexRune("dgimsuvy", c)
		if i < 0 || seen&(1<<i) != 0 {
			return f, "", bad
		}
		seen |= 1 << i
	}
	const u, v = 1 << 5, 1 << 6
	if seen&u != 0 && seen&v != 0 {
		return f, "", bad
	}
	if seen&v != 0 {
		return f, "", &Error{"unsupported: v flag"}
	}
	var b strings.Builder
	for i, c := range "dgimsuvy" {
		if seen&(1<<i) != 0 {
			b.WriteRune(c)
		}
	}
	f.hasIndices, f.global, f.icase = seen&1 != 0, seen&2 != 0, seen&4 != 0
	f.multiline, f.dotall, f.unicode, f.sticky = seen&8 != 0, seen&16 != 0, seen&u != 0, seen&128 != 0
	return f, b.String(), nil
}

var (
	cacheMu  sync.Mutex
	compiled = map[string]*Regexp{}
)

const cacheMax = 512

func Compile(pattern, flags string) (*Regexp, error) {
	_, canon, err := parseFlags(flags)
	if err != nil {
		return nil, err
	}
	key := canon + "/" + pattern
	cacheMu.Lock()
	re, ok := compiled[key]
	cacheMu.Unlock()
	if ok {
		return re, nil
	}
	re, err = compile(pattern, flags, EngineAuto)
	if err != nil {
		return nil, err
	}
	cacheMu.Lock()
	if len(compiled) >= cacheMax {
		clear(compiled)
	}
	compiled[key] = re
	cacheMu.Unlock()
	return re, nil
}

func CompileEngine(pattern, flags string, engine int) (*Regexp, error) {
	return compile(pattern, flags, engine)
}

func compile(pattern, flags string, engine int) (*Regexp, error) {
	f, canon, err := parseFlags(flags)
	if err != nil {
		return nil, err
	}
	root, names, err := parsePattern(pattern, f)
	if err != nil {
		return nil, &Error{"Invalid regular expression: /" + pattern + "/" + canon + ": " + err.Error()}
	}
	re := &Regexp{source: pattern, flags: canon, f: f, names: names, ncap: len(names), engine: engine}
	re.prog, re.nregs = compileProg(root, f)
	re.literal, re.isLit = pureLiteral(root)
	re.planScan(root)
	if engine != EngineBacktrack {
		re.re2 = newRE2(root, f)
	}
	return re, nil
}

func pureLiteral(root *node) (string, bool) {
	subs := []*node{root}
	if root.kind == nSeq {
		subs = root.subs
	}
	var b strings.Builder
	for _, n := range subs {
		if n.kind != nChar {
			return "", false
		}
		b.WriteString(encodeUnit(n.r))
	}
	return b.String(), b.Len() > 0
}

func (re *Regexp) planScan(root *node) {
	subs := []*node{root}
	if root.kind == nSeq {
		subs = root.subs
	}
	if subs[0].kind == nBol && !re.f.multiline {
		re.scan = scanAnchor
		return
	}
	fs := firstOf(root)
	if fs.nullable || fs.any {
		re.scan = scanNone
		return
	}
	if p := literalPrefix(subs); len(p) > 0 {
		re.prefix, re.first = p, p[0]
		re.scan = pick8(len(p) > 1, scanPrefix, scanByte)
		return
	}
	if subs[0].kind == nBol {
		re.scan = scanLine
		return
	}
	re.table = byteTable(newCharset(fs.ranges))
	n, last := 0, 0
	for b, ok := range re.table {
		if ok {
			n++
			last = b
		}
	}
	switch n {
	case 1:
		re.scan, re.first = scanByte, byte(last)
	case 256:
		re.scan = scanNone
	default:
		re.scan = scanTable
	}
}

func pick8(c bool, a, b scanKind) scanKind {
	if c {
		return a
	}
	return b
}

func literalPrefix(subs []*node) string {
	var b strings.Builder
	for _, n := range subs {
		switch n.kind {
		case nWordB, nNotWordB, nLook:
			if b.Len() == 0 {
				continue
			}
			return b.String()
		case nBol:
			if b.Len() == 0 {
				continue
			}
			return b.String()
		case nChar:
			b.WriteString(encodeUnit(n.r))
			continue
		case nGroup:
			if b.Len() == 0 {
				inner := n.subs[0]
				if inner.kind == nSeq {
					return literalPrefix(inner.subs)
				}
				return literalPrefix([]*node{inner})
			}
		}
		return b.String()
	}
	return b.String()
}

func (re *Regexp) Source() string {
	if re.source == "" {
		return "(?:)"
	}
	var b strings.Builder
	inClass := false
	rs := []rune(re.source)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch {
		case c == '\\' && i+1 < len(rs):
			b.WriteRune(c)
			i++
			c = rs[i]
			if writeLineTerm(&b, c) {
				continue
			}
		case c == '[':
			inClass = true
		case c == ']':
			inClass = false
		case c == '/' && !inClass:
			b.WriteString("\\/")
			continue
		}
		if !writeLineTermEsc(&b, c) {
			b.WriteRune(c)
		}
	}
	return b.String()
}

func writeLineTerm(b *strings.Builder, c rune) bool {
	switch c {
	case '\n':
		b.WriteString("n")
	case '\r':
		b.WriteString("r")
	case 0x2028:
		b.WriteString("u2028")
	case 0x2029:
		b.WriteString("u2029")
	default:
		return false
	}
	return true
}

func writeLineTermEsc(b *strings.Builder, c rune) bool {
	switch c {
	case '\n', '\r', 0x2028, 0x2029:
		b.WriteByte('\\')
		return writeLineTerm(b, c)
	}
	return false
}

func (re *Regexp) Flags() string        { return re.flags }
func (re *Regexp) Global() bool         { return re.f.global }
func (re *Regexp) IgnoreCase() bool     { return re.f.icase }
func (re *Regexp) Multiline() bool      { return re.f.multiline }
func (re *Regexp) DotAll() bool         { return re.f.dotall }
func (re *Regexp) Unicode() bool        { return re.f.unicode }
func (re *Regexp) Sticky() bool         { return re.f.sticky }
func (re *Regexp) HasIndices() bool     { return re.f.hasIndices }
func (re *Regexp) NumGroups() int       { return re.ncap }
func (re *Regexp) GroupNames() []string { return append([]string(nil), re.names...) }

func (re *Regexp) acquire() *machine {
	if m := re.cache.Swap(nil); m != nil {
		return m
	}
	return newMachine(re)
}

func (re *Regexp) release(m *machine) {
	m.s = ""
	m.local.s, m.local.cesu = "", ""
	if cap(m.stack) > 1<<16 {
		m.stack = nil
	}
	re.cache.Store(m)
}

func (m *machine) subjectFor(s string) *subject {
	if len(s) < subjCacheMin {
		m.local.reset(s)
		return &m.local
	}
	return sharedSubject(s)
}

func (re *Regexp) Exec(s string, startUTF16 int) []int {
	return re.ExecTo(nil, s, startUTF16)
}

func (re *Regexp) ExecTo(dst []int, s string, startUTF16 int) []int {
	m := re.acquire()
	defer re.release(m)
	return re.execUnits(m, dst, m.subjectFor(s), startUTF16)
}

func (re *Regexp) ExecSubject(sub *Subject, startUTF16 int) []int {
	return re.ExecSubjectTo(nil, sub, startUTF16)
}

func (re *Regexp) ExecSubjectTo(dst []int, sub *Subject, startUTF16 int) []int {
	m := re.acquire()
	defer re.release(m)
	return re.execUnits(m, dst, sub.sj, startUTF16)
}

func (re *Regexp) ExecSubjectBytes(sub *Subject, startByte int) []int {
	return re.ExecSubjectBytesTo(nil, sub, startByte)
}

func (re *Regexp) ExecSubjectBytesTo(dst []int, sub *Subject, startByte int) []int {
	m := re.acquire()
	defer re.release(m)
	return re.execBytesOn(m, dst, sub.sj, startByte)
}

func (re *Regexp) execUnits(m *machine, dst []int, sj *subject, start int) []int {
	if sj.ascii {
		return re.exec(m, dst, sj.s, max(start, 0), false, sj)
	}
	return re.execConverted(m, dst, sj, max(start, 0), cU16)
}

func (re *Regexp) execBytesOn(m *machine, dst []int, sj *subject, start int) []int {
	if !sj.needCesu(re.f.unicode) {
		return re.exec(m, dst, sj.s, charBoundary(sj.s, max(start, 0)), false, sj)
	}
	return re.execConverted(m, dst, sj, max(start, 0), cOrig)
}

func (re *Regexp) ExecBytes(s string, startByte int) []int {
	return re.ExecBytesTo(nil, s, startByte)
}

func (re *Regexp) ExecBytesTo(dst []int, s string, startByte int) []int {
	m := re.acquire()
	defer re.release(m)
	if re.f.unicode {
		return re.exec(m, dst, s, charBoundary(s, max(startByte, 0)), false, nil)
	}
	return re.execBytesOn(m, dst, m.subjectFor(s), startByte)
}

func charBoundary(s string, i int) int {
	for k := 0; k < 3 && i > 0 && i < len(s) && s[i]&0xC0 == 0x80; k++ {
		i++
	}
	return i
}

func (re *Regexp) execConverted(m *machine, dst []int, sj *subject, start, by int) []int {
	sj.mu.Lock()
	c := sj.cursor(re.f.unicode)
	p, ok := c.seek(start, by)
	for ok && by == cOrig && p[cOrig] < start && p[cWork] < len(c.w) {
		p = c.step(p)
	}
	sj.mu.Unlock()
	if !ok {
		return nil
	}
	res := re.exec(m, dst, c.w, p[cWork], c.cesu, sj)
	if res != nil {
		sj.mu.Lock()
		c.convert(res, by)
		sj.mu.Unlock()
	}
	return res
}

func (re *Regexp) exec(m *machine, dst []int, w string, start int, cesu bool, sj *subject) []int {
	if start > len(w) {
		return nil
	}
	if re.isLit {
		return re.execLiteral(dst, w, start)
	}
	if re.engine == EngineRE2 && re.re2.usable(m, w, cesu, sj) {
		return re.re2.exec(dst, w, start, re.f.sticky)
	}
	m.s = w
	m.steps = 0
	full := int(stepLimit.Load()) + 16*(len(w)-start)
	m.limit = full
	if re.re2 != nil && re.engine == EngineAuto {
		m.limit = min(full, rescueSteps+8*(len(w)-start))
	}
	if re.re2 != nil && re.engine == EngineAuto && re.preferRE2.Load() && re.re2.usable(m, w, cesu, sj) {
		return re.re2.exec(dst, w, start, re.f.sticky)
	}
	ok := m.search(start)
	if m.abort {
		m.recover()
		if re.re2.usable(m, w, cesu, sj) {
			re.preferRE2.Store(true)
			return re.re2.exec(dst, w, start, re.f.sticky)
		}
		if m.limit == full {
			panic(&Error{errTooComplex})
		}
		m.limit, m.steps = full, 0
		ok = m.search(start)
		if m.abort {
			m.recover()
			panic(&Error{errTooComplex})
		}
	}
	if !ok {
		return nil
	}
	dst = append(dst[:0], m.caps...)
	m.recover()
	return dst
}

func (m *machine) recover() {
	m.abort = false
	m.stack = m.stack[:0]
	for i := range m.caps {
		m.caps[i] = -1
	}
	clear(m.regs)
}

func (re *Regexp) execLiteral(dst []int, w string, start int) []int {
	if re.f.sticky {
		if !strings.HasPrefix(w[start:], re.literal) {
			return nil
		}
		return append(dst[:0], start, start+len(re.literal))
	}
	i := strings.Index(w[start:], re.literal)
	if i < 0 {
		return nil
	}
	return append(dst[:0], start+i, start+i+len(re.literal))
}

func (m *machine) search(start int) bool {
	re := m.re
	s := m.s
	sticky := re.f.sticky
	pos := start
	for {
		if sticky {
			if re.scan == scanAnchor && pos != 0 {
				return false
			}
		} else if pos = re.next(s, pos); pos < 0 {
			return false
		}
		end, ok := m.run(0, pos)
		if ok {
			m.caps[0], m.caps[1] = pos, end
			return true
		}
		if m.abort || sticky || pos >= len(s) {
			return false
		}
		pos += charWidth(s, pos)
	}
}

func (re *Regexp) next(s string, pos int) int {
	switch re.scan {
	case scanAnchor:
		if pos == 0 {
			return 0
		}
		return -1
	case scanPrefix:
		if i := strings.Index(s[pos:], re.prefix); i >= 0 {
			return pos + i
		}
		return -1
	case scanByte:
		if i := strings.IndexByte(s[pos:], re.first); i >= 0 {
			return pos + i
		}
		return -1
	case scanTable:
		t := re.table
		for pos < len(s) {
			if t[s[pos]] {
				return pos
			}
			pos++
		}
		return -1
	case scanLine:
		return nextLineStart(s, pos)
	}
	return pos
}

func nextLineStart(s string, pos int) int {
	if pos == 0 || pos <= len(s) && isLineTermBefore(s, pos) {
		return pos
	}
	for i := pos; i < len(s); i++ {
		if isLineTermAt(s, i) {
			if s[i] == 0xE2 {
				return i + 3
			}
			return i + 1
		}
	}
	return -1
}
