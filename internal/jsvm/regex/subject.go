package regex

import (
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"
)

const (
	cWork = iota
	cU16
	cOrig
)

const subjCacheMin = 256

type subject struct {
	key     string
	s       string
	ascii   bool
	astral  bool
	lt      atomic.Int32
	mu      sync.Mutex
	cesu    string
	hasCesu bool
	anchor  [2][3]int
}

func (sj *subject) reset(s string) {
	sj.s = s
	sj.ascii = IsASCII(s)
	sj.astral = !sj.ascii && hasAstral(s)
	sj.lt.Store(0)
	sj.cesu, sj.hasCesu = "", false
	sj.anchor = [2][3]int{}
}

var (
	sharedMu   sync.Mutex
	sharedSubj [2]*subject
	sharedLast int
)

func sharedSubject(s string) *subject {
	sharedMu.Lock()
	defer sharedMu.Unlock()
	for i, sj := range sharedSubj {
		if sj != nil && len(sj.key) == len(s) && sampleEqual(sj.key, s) && sj.key == s {
			sj.key = s
			sharedLast = i
			return sj
		}
	}
	sj := &subject{key: s}
	sj.reset(s)
	sharedLast = 1 - sharedLast
	sharedSubj[sharedLast] = sj
	return sj
}

func sampleEqual(a, b string) bool {
	n := len(a)
	if n == 0 {
		return true
	}
	step := n/8 + 1
	for j := n - 1; j >= 0; j -= step {
		if a[j] != b[j] {
			return false
		}
	}
	return true
}

type Subject struct{ sj *subject }

func NewSubject(s string) *Subject {
	sj := &subject{}
	sj.reset(s)
	return &Subject{sj: sj}
}

func (sub *Subject) String() string { return sub.sj.s }
func (sub *Subject) IsASCII() bool  { return sub.sj.ascii }

func IsASCII(s string) bool {
	i := 0
	for ; i+8 <= len(s); i += 8 {
		v := uint64(s[i]) | uint64(s[i+1])<<8 | uint64(s[i+2])<<16 | uint64(s[i+3])<<24 |
			uint64(s[i+4])<<32 | uint64(s[i+5])<<40 | uint64(s[i+6])<<48 | uint64(s[i+7])<<56
		if v&0x8080808080808080 != 0 {
			return false
		}
	}
	for ; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

func hasAstral(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0xF0 {
			return true
		}
	}
	return false
}

func (sj *subject) needCesu(unicodeMode bool) bool {
	return !unicodeMode && sj.astral
}

func (sj *subject) lineTerms() bool {
	switch sj.lt.Load() {
	case 1:
		return false
	case 2:
		return true
	}
	has := strings.ContainsRune(sj.s, '\r') || strings.ContainsRune(sj.s, 0x2028) || strings.ContainsRune(sj.s, 0x2029)
	if has {
		sj.lt.Store(2)
	} else {
		sj.lt.Store(1)
	}
	return has
}

type cursor struct {
	sj   *subject
	w    string
	cesu bool
}

func (sj *subject) cursor(unicodeMode bool) cursor {
	c := sj.needCesu(unicodeMode)
	if !c {
		return cursor{sj: sj, w: sj.s}
	}
	if !sj.hasCesu {
		sj.cesu = toCESU(sj.s)
		sj.hasCesu = true
	}
	return cursor{sj: sj, w: sj.cesu, cesu: true}
}

func toCESU(s string) string {
	var b strings.Builder
	b.Grow(len(s) + len(s)/2)
	for i := 0; i < len(s); {
		r, w := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && w == 1 {
			b.WriteByte(0xFF)
			i++
			continue
		}
		i += w
		if r > 0xFFFF {
			r -= 0x10000
			b.WriteString(encodeUnit(0xD800 + r>>10))
			b.WriteString(encodeUnit(0xDC00 + r&0x3FF))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func (c cursor) anchor() *[3]int {
	if c.cesu {
		return &c.sj.anchor[1]
	}
	return &c.sj.anchor[0]
}

func (c cursor) step(p [3]int) [3]int {
	w := c.w
	wb := charWidth(w, p[cWork])
	u, o := 1, wb
	switch {
	case wb == 4:
		u = 2
	case wb == 3 && w[p[cWork]] == 0xED && w[p[cWork]+1] >= 0xA0:
		o = 0
		if w[p[cWork]+1] >= 0xB0 {
			o = 4
		}
	}
	return [3]int{p[cWork] + wb, p[cU16] + u, p[cOrig] + o}
}

func (c cursor) seek(target, by int) ([3]int, bool) {
	a := c.anchor()
	if target < a[by] {
		*a = [3]int{}
	}
	p := c.backUp(*a, by)
	for p[by] < target && p[cWork] < len(c.w) {
		q := c.step(p)
		if q[by] > target {
			break
		}
		p = q
	}
	p = c.backUp(p, by)
	*a = p
	if p[by] < target && p[cWork] >= len(c.w) {
		return p, false
	}
	return p, true
}

func (c cursor) midPair(p [3]int) bool {
	w := c.w
	return c.cesu && p[cWork] >= 3 && p[cWork]+1 < len(w) && w[p[cWork]] == 0xED && w[p[cWork]+1] >= 0xB0
}

func (c cursor) backUp(p [3]int, by int) [3]int {
	w := c.w
	if by == cOrig && c.cesu && p[cWork] >= 3 && p[cWork] < len(w) && w[p[cWork]] == 0xED && w[p[cWork]+1] >= 0xB0 {
		return [3]int{p[cWork] - 3, p[cU16] - 1, p[cOrig]}
	}
	return p
}

func (c cursor) convert(res []int, to int) {
	var buf [32]int
	idx := buf[:0]
	for i, v := range res {
		if v < 0 {
			continue
		}
		j := len(idx)
		idx = append(idx, i)
		for j > 0 && res[idx[j-1]] > v {
			idx[j] = idx[j-1]
			j--
		}
		idx[j] = i
	}
	var obuf [32]int
	out := obuf[:0]
	if len(idx) > len(obuf) {
		out = make([]int, 0, len(idx))
	}
	out = out[:len(idx)]
	for k, i := range idx {
		if k > 0 && res[i] == res[idx[k-1]] && i%2 == idx[k-1]%2 {
			out[k] = out[k-1]
			continue
		}
		p, _ := c.seek(res[i], cWork)
		if to == cOrig && i%2 == 1 && c.midPair(p) {
			p = c.step(p)
		}
		out[k] = p[to]
	}
	for k, i := range idx {
		res[i] = out[k]
	}
}
