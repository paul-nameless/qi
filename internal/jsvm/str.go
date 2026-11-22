package jsvm

import (
	"github.com/paul-nameless/qi/internal/jsvm/regex"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

const (
	strKnown uint8 = 1 << iota
	strASCII
	strAstral
	strShared
)

const ropeMin = 256

const chunkMax = 256

type jstr struct {
	s    string
	rope *ropeNode
	n    int
	info uint8
	ext  *strExt
}

type ropeNode struct {
	left, right *jstr
}

type strExt struct {
	sub      *regex.Subject
	u16      []uint16
	ulen     int
	posB     int
	posU     int
	markB    []int32
	markU    []int32
	reserved bool
}

const markStep = 256

func (s *jstr) buildMarks(e *strExt) {
	str := s.flat()
	e.markB = make([]int32, 0, s.n/markStep+1)
	e.markU = make([]int32, 0, s.n/markStep+1)
	u, next := 0, 0
	for b := 0; b < len(str); {
		if u >= next {
			e.markB = append(e.markB, int32(b))
			e.markU = append(e.markU, int32(u))
			next += markStep
		}
		r, size := utf8.DecodeRuneInString(str[b:])
		if r >= 0x10000 {
			u += 2
		} else {
			u++
		}
		b += size
	}
}

func (s *jstr) nearest(e *strExt, byByte bool, target int) (int, int) {
	cur := e.posU
	if byByte {
		cur = e.posB
	}
	if cur <= target && target-cur < 4*markStep {
		return e.posB, e.posU
	}
	if s.n < 4*markStep {
		return 0, 0
	}
	if e.markB == nil {
		s.buildMarks(e)
	}
	marks := e.markU
	if byByte {
		marks = e.markB
	}
	lo, hi := 0, len(marks)
	for lo+1 < hi {
		mid := (lo + hi) / 2
		if int(marks[mid]) <= target {
			lo = mid
		} else {
			hi = mid
		}
	}
	return int(e.markB[lo]), int(e.markU[lo])
}

func (s *jstr) regexSubject() *regex.Subject {
	if s.info&strShared != 0 || s.n < 256 {
		return regex.NewSubject(s.flat())
	}
	e := s.x()
	if e.sub == nil {
		e.sub = regex.NewSubject(s.flat())
	}
	return e.sub
}

func (s *jstr) x() *strExt {
	if s.ext == nil {
		s.ext = &strExt{}
	}
	return s.ext
}

var asciiChars [128]*jstr

func init() {
	for i := range asciiChars {
		asciiChars[i] = &jstr{s: string(rune(i)), n: 1, info: strKnown | strASCII | strShared}
	}
}

func validUTF8(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	return strings.ToValidUTF8(s, "\uFFFD")
}

func newString(s string) *jstr {
	if len(s) == 1 && s[0] < 128 {
		return asciiChars[s[0]]
	}
	return &jstr{s: s, n: len(s)}
}

func newASCII(s string) *jstr {
	if len(s) == 1 && s[0] < 128 {
		return asciiChars[s[0]]
	}
	return &jstr{s: s, n: len(s), info: strKnown | strASCII}
}

func (s *jstr) flat() string {
	if s.rope == nil {
		return s.s
	}
	var buf strings.Builder
	buf.Grow(s.n)
	stack := []*jstr{s}
	for len(stack) > 0 {
		t := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for t.rope != nil {
			stack = append(stack, t.rope.right)
			t = t.rope.left
		}
		buf.WriteString(t.s)
	}
	s.s = buf.String()
	s.rope = nil
	return s.s
}

func concatStrings(a, b *jstr) *jstr {
	if a.n == 0 {
		return b
	}
	if b.n == 0 {
		return a
	}
	n := a.n + b.n
	a.scan()
	b.scan()
	info := strKnown | (a.info & b.info & strASCII) | ((a.info | b.info) & strAstral)
	var ext *strExt
	if info&strASCII == 0 {
		ext = &strExt{ulen: a.length() + b.length()}
	}
	if n < ropeMin {
		return &jstr{s: a.flat() + b.flat(), n: n, info: info, ext: ext}
	}
	if a.rope != nil && b.rope == nil && b.n < chunkMax {
		if r := a.rope.right; r.rope == nil && r.n+b.n <= chunkMax {
			r.scan()
			ri := strKnown | (r.info & b.info & strASCII) | ((r.info | b.info) & strAstral)
			merged := &jstr{s: r.s + b.s, n: r.n + b.n, info: ri}
			return &jstr{rope: &ropeNode{a.rope.left, merged}, n: n, info: info, ext: ext}
		}
	}
	return &jstr{rope: &ropeNode{a, b}, n: n, info: info, ext: ext}
}

func (s *jstr) scan() {
	if s.info&strKnown != 0 {
		return
	}
	str := s.flat()
	info := strKnown | strASCII
	for i := 0; i < len(str); i++ {
		c := str[i]
		if c >= 0x80 {
			info &^= strASCII
			if c >= 0xF0 {
				info |= strAstral
				break
			}
		}
	}
	s.info = info
}

func (s *jstr) isASCII() bool {
	s.scan()
	return s.info&strASCII != 0
}

func (s *jstr) units() []uint16 {
	if s.isASCII() {
		str := s.flat()
		u := make([]uint16, len(str))
		for i := 0; i < len(str); i++ {
			u[i] = uint16(str[i])
		}
		return u
	}
	e := s.x()
	if e.u16 == nil {
		str := s.flat()
		u := make([]uint16, 0, s.length())
		for _, r := range str {
			if r >= 0x10000 {
				r1, r2 := utf16.EncodeRune(r)
				u = append(u, uint16(r1), uint16(r2))
				continue
			}
			u = append(u, uint16(r))
		}
		e.u16 = u
	}
	return e.u16
}

func (s *jstr) length() int {
	if s.isASCII() {
		return s.n
	}
	e := s.x()
	if e.ulen == 0 {
		if e.u16 != nil {
			e.ulen = len(e.u16)
		} else {
			e.ulen = utf16Len(s.flat())
		}
	}
	return e.ulen
}

func (s *jstr) unitAt(off int) int {
	if s.isASCII() {
		return off
	}
	str := s.flat()
	e := s.x()
	if off < e.posB && e.posB-off <= 256 {
		u := e.posU - utf16Len(str[off:e.posB])
		e.posB, e.posU = off, u
		return u
	}
	b, u := s.nearest(e, true, off)
	u += utf16Len(str[b:off])
	e.posB, e.posU = off, u
	return u
}

func (s *jstr) charCodeAt(i int) int {
	if s.isASCII() {
		return int(s.flat()[i])
	}
	return int(s.units()[i])
}

func (s *jstr) substring(from, to int) string {
	if from >= to {
		return ""
	}
	if s.isASCII() {
		return s.flat()[from:to]
	}
	if s.ext == nil || s.ext.u16 == nil || to-from > 64 {
		b1 := s.byteOf(from)
		if s.unitAt(b1) == from {
			b2 := s.byteOf(to)
			if s.unitAt(b2) == to {
				return s.flat()[b1:b2]
			}
		}
	}
	return unitsToString(s.units()[from:to])
}

func (s *jstr) substr(from, to int) Value {
	if from == 0 && to == s.length() {
		return strVal(s)
	}
	if s.isASCII() {
		return Value{r: newASCII(s.flat()[from:to])}
	}
	return strV(s.substring(from, to))
}

func unitsToString(u []uint16) string {
	var b strings.Builder
	b.Grow(len(u))
	for i := 0; i < len(u); i++ {
		c := u[i]
		if c < 0x80 {
			b.WriteByte(byte(c))
			continue
		}
		if utf16.IsSurrogate(rune(c)) && c < 0xDC00 && i+1 < len(u) && u[i+1] >= 0xDC00 && u[i+1] < 0xE000 {
			b.WriteRune(utf16.DecodeRune(rune(c), rune(u[i+1])))
			i++
			continue
		}
		if utf16.IsSurrogate(rune(c)) {
			b.WriteRune(utf8.RuneError)
			continue
		}
		b.WriteRune(rune(c))
	}
	return b.String()
}

func (s *jstr) indexOf(sub string, from int) int {
	str := s.flat()
	if s.isASCII() {
		if from > len(str) {
			if sub == "" {
				return len(str)
			}
			return -1
		}
		i := strings.Index(str[from:], sub)
		if i < 0 {
			return -1
		}
		return i + from
	}
	if sub == "" {
		return min(from, s.length())
	}
	b := s.byteOf(from)
	i := strings.Index(str[b:], sub)
	if i < 0 {
		return -1
	}
	return s.unitAt(b + i)
}

func (s *jstr) lastIndexOf(sub string, from int) int {
	str := s.flat()
	if s.isASCII() {
		if !isASCIIString(sub) {
			return -1
		}
		end := from + len(sub)
		if end > len(str) {
			end = len(str)
		}
		return strings.LastIndex(str[:end], sub)
	}
	end := from + utf16Len(sub)
	if n := s.length(); end > n {
		end = n
	}
	e := s.byteOf(end)
	if e > 0 && s.unitAt(e) > end {
		_, size := utf8.DecodeLastRuneInString(str[:e])
		e -= size
	}
	i := strings.LastIndex(str[:e], sub)
	if i < 0 {
		return -1
	}
	return s.unitAt(i)
}

func (s *jstr) byteOf(units int) int {
	if s.isASCII() {
		return units
	}
	str := s.flat()
	e := s.x()
	if units < e.posU && e.posU-units <= 64 {
		b, u := e.posB, e.posU
		for u > units && b > 0 {
			r, size := utf8.DecodeLastRuneInString(str[:b])
			if r >= 0x10000 {
				if u-2 < units {
					break
				}
				u -= 2
			} else {
				u--
			}
			b -= size
		}
		if u == units {
			e.posB, e.posU = b, u
			return b
		}
	}
	b, u := s.nearest(e, false, units)
	for b < len(str) && u < units {
		r, size := utf8.DecodeRuneInString(str[b:])
		if r >= 0x10000 {
			u += 2
		} else {
			u++
		}
		b += size
	}
	e.posB, e.posU = b, u
	return b
}

func unitsEqual(a, b []uint16) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func isASCIIString(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

func utf16Len(s string) int {
	n := 0
	for i := 0; i < len(s); {
		c := s[i]
		if c < 0x80 {
			n++
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
		i += size
	}
	return n
}

func decodeRune(s string) (rune, int) { return utf8.DecodeRuneInString(s) }

func decodeLastRune(s string) (rune, int) { return utf8.DecodeLastRuneInString(s) }

func compareStrings(a, b *jstr) int {
	as, bs := a.flat(), b.flat()
	a.scan()
	b.scan()
	if (a.info|b.info)&strAstral == 0 {
		return strings.Compare(as, bs)
	}
	au, bu := a.units(), b.units()
	for i := 0; i < len(au) && i < len(bu); i++ {
		if au[i] != bu[i] {
			if au[i] < bu[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(au) < len(bu):
		return -1
	case len(au) > len(bu):
		return 1
	}
	return 0
}
