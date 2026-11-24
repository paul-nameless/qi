package regex

import (
	"slices"
	"sync"
	"unicode"
	"unicode/utf8"
)

const (
	maxUnit   = 0xFFFF
	maxCodePt = unicode.MaxRune
)

type charset struct {
	ascii    [4]uint64
	ranges   []rune
	nonASCII bool
	lit      string
}

func newCharset(rs []rune) *charset {
	rs = normRanges(rs)
	c := &charset{ranges: rs}
	for i := 0; i < len(rs); i += 2 {
		lo, hi := rs[i], rs[i+1]
		if hi >= 0x80 {
			c.nonASCII = true
		}
		for r := lo; r <= hi && r < 0x80; r++ {
			c.ascii[r>>6] |= 1 << (r & 63)
		}
	}
	if r, ok := c.single(); ok && r >= 0x80 && utf8.ValidRune(r) && r != utf8.RuneError {
		c.lit = string(r)
	}
	return c
}

func (c *charset) hasASCII(b byte) bool {
	return c.ascii[b>>6]&(1<<(b&63)) != 0
}

func (c *charset) has(r rune) bool {
	if r < 0x80 {
		return r >= 0 && c.ascii[r>>6]&(1<<(r&63)) != 0
	}
	if !c.nonASCII {
		return false
	}
	rs := c.ranges
	if len(rs) == 2 {
		return r >= rs[0] && r <= rs[1]
	}
	lo, hi := 0, len(rs)/2
	for lo < hi {
		m := int(uint(lo+hi) >> 1)
		switch {
		case r < rs[2*m]:
			hi = m
		case r > rs[2*m+1]:
			lo = m + 1
		default:
			return true
		}
	}
	return false
}

func (c *charset) single() (rune, bool) {
	if len(c.ranges) == 2 && c.ranges[0] == c.ranges[1] {
		return c.ranges[0], true
	}
	return 0, false
}

func (c *charset) isFull(max rune) bool {
	return len(c.ranges) == 2 && c.ranges[0] == 0 && c.ranges[1] >= max
}

func (c *charset) intersects(o *charset) bool {
	a, b := c.ranges, o.ranges
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		if a[i+1] < b[j] {
			i += 2
		} else if b[j+1] < a[i] {
			j += 2
		} else {
			return true
		}
	}
	return false
}

func normRanges(rs []rune) []rune {
	if len(rs) <= 2 {
		return slices.Clone(rs)
	}
	type pair struct{ lo, hi rune }
	ps := make([]pair, 0, len(rs)/2)
	for i := 0; i < len(rs); i += 2 {
		ps = append(ps, pair{rs[i], rs[i+1]})
	}
	slices.SortFunc(ps, func(a, b pair) int { return int(a.lo - b.lo) })
	out := make([]rune, 0, len(rs))
	for _, p := range ps {
		n := len(out)
		if n > 0 && p.lo <= out[n-1]+1 {
			if p.hi > out[n-1] {
				out[n-1] = p.hi
			}
			continue
		}
		out = append(out, p.lo, p.hi)
	}
	return out
}

func negateRanges(rs []rune, max rune) []rune {
	rs = normRanges(rs)
	out := make([]rune, 0, len(rs)+2)
	next := rune(0)
	for i := 0; i < len(rs); i += 2 {
		if rs[i] > max {
			break
		}
		if rs[i] > next {
			out = append(out, next, rs[i]-1)
		}
		next = rs[i+1] + 1
	}
	if next <= max {
		out = append(out, next, max)
	}
	return out
}

func tableRanges(t *unicode.RangeTable) []rune {
	var out []rune
	for _, r := range t.R16 {
		out = appendStrided(out, rune(r.Lo), rune(r.Hi), rune(r.Stride))
	}
	for _, r := range t.R32 {
		out = appendStrided(out, rune(r.Lo), rune(r.Hi), rune(r.Stride))
	}
	return out
}

func appendStrided(out []rune, lo, hi, stride rune) []rune {
	if stride == 1 {
		return append(out, lo, hi)
	}
	for r := lo; r <= hi; r += stride {
		out = append(out, r, r)
	}
	return out
}

var (
	digitRanges  = []rune{'0', '9'}
	wordRanges   = []rune{'0', '9', 'A', 'Z', '_', '_', 'a', 'z'}
	wordUIRanges = []rune{'0', '9', 'A', 'Z', '_', '_', 'a', 'z', 0x17F, 0x17F, 0x212A, 0x212A}
	spaceRanges  = []rune{
		'\t', '\r', ' ', ' ', 0xA0, 0xA0, 0x1680, 0x1680, 0x2000, 0x200A,
		0x2028, 0x2029, 0x202F, 0x202F, 0x205F, 0x205F, 0x3000, 0x3000, 0xFEFF, 0xFEFF,
	}
	lineTermRanges = []rune{'\n', '\n', '\r', '\r', 0x2028, 0x2029}
)

var wordUISet = newCharset(wordUIRanges)

func canonNonU(r rune) rune {
	if r < 0x80 {
		if 'a' <= r && r <= 'z' {
			return r - 32
		}
		return r
	}
	if r > maxUnit || multiUpper(r) {
		return r
	}
	u := unicode.ToUpper(r)
	if u < 0x80 || u > maxUnit {
		return r
	}
	return u
}

func multiUpper(r rune) bool {
	switch {
	case r == 0xDF, r == 0x149, r == 0x1F0, r == 0x390, r == 0x3B0, r == 0x587:
		return true
	case r >= 0x1E96 && r <= 0x1E9A:
		return true
	case r >= 0x1F50 && r <= 0x1F56 && r%2 == 0:
		return true
	case r >= 0x1F80 && r <= 0x1FAF:
		return true
	case r >= 0x1FB2 && r <= 0x1FFC:
		return multiUpperGreek(r)
	case r >= 0xFB00 && r <= 0xFB06, r >= 0xFB13 && r <= 0xFB17:
		return true
	}
	return false
}

func multiUpperGreek(r rune) bool {
	switch r {
	case 0x1FB2, 0x1FB3, 0x1FB4, 0x1FB6, 0x1FB7, 0x1FBC,
		0x1FC2, 0x1FC3, 0x1FC4, 0x1FC6, 0x1FC7, 0x1FCC,
		0x1FD2, 0x1FD3, 0x1FD6, 0x1FD7,
		0x1FE2, 0x1FE3, 0x1FE4, 0x1FE6, 0x1FE7,
		0x1FF2, 0x1FF3, 0x1FF4, 0x1FF6, 0x1FF7, 0x1FFC:
		return true
	}
	return false
}

func foldOrbitU(r rune) []rune {
	out := []rune{r}
	for f, n := unicode.SimpleFold(r), 0; f != r && n < 8; f, n = unicode.SimpleFold(f), n+1 {
		if !slices.Contains(out, f) {
			out = append(out, f)
		}
	}
	return out
}

func canonU(r rune) rune {
	m := r
	for _, f := range foldOrbitU(r) {
		if f < m {
			m = f
		}
	}
	return m
}

type foldTable struct {
	members map[rune][]rune
	keys    []rune
	classes [][]rune
}

var (
	foldOnce [2]sync.Once
	foldTabs [2]*foldTable
)

func getFoldTable(unicodeMode bool) *foldTable {
	i := 0
	if unicodeMode {
		i = 1
	}
	foldOnce[i].Do(func() { foldTabs[i] = buildFoldTable(unicodeMode) })
	return foldTabs[i]
}

func buildFoldTable(unicodeMode bool) *foldTable {
	groups := map[rune][]rune{}
	if !unicodeMode {
		for _, cr := range unicode.CaseRanges {
			for r := rune(cr.Lo); r <= rune(cr.Hi) && r <= maxUnit; r++ {
				k := canonNonU(r)
				groups[k] = append(groups[k], r)
			}
		}
	} else {
		for r := rune(0); r <= 0x1FFFF; r++ {
			if unicode.SimpleFold(r) != r {
				k := canonU(r)
				groups[k] = append(groups[k], r)
			}
		}
	}
	t := &foldTable{members: map[rune][]rune{}}
	for _, ms := range groups {
		if len(ms) < 2 {
			continue
		}
		slices.Sort(ms)
		t.classes = append(t.classes, ms)
		for _, m := range ms {
			t.members[m] = ms
			t.keys = append(t.keys, m)
		}
	}
	slices.Sort(t.keys)
	return t
}

func foldClass(r rune, unicodeMode bool) []rune {
	if ms, ok := getFoldTable(unicodeMode).members[r]; ok {
		return ms
	}
	return []rune{r}
}

func foldClosure(rs []rune, unicodeMode bool) []rune {
	rs = normRanges(rs)
	set := newCharset(rs)
	out := slices.Clone(rs)
	for _, cls := range getFoldTable(unicodeMode).classes {
		hit := false
		for _, m := range cls {
			if set.has(m) {
				hit = true
				break
			}
		}
		if !hit {
			continue
		}
		for _, m := range cls {
			out = append(out, m, m)
		}
	}
	return normRanges(out)
}
