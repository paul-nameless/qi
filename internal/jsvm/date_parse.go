package jsvm

import (
	"math"
	"strings"
)

func parseDate(s string) float64 {
	s = strings.TrimSpace(s)
	if t, ok := parseISODate(s); ok {
		return t
	}
	return parseLegacyDate(s)
}

type scanner struct {
	s string
	i int
}

func (sc *scanner) digits(n int) (int, bool) {
	if sc.i+n > len(sc.s) {
		return 0, false
	}
	v := 0
	for k := 0; k < n; k++ {
		c := sc.s[sc.i+k]
		if c < '0' || c > '9' {
			return 0, false
		}
		v = v*10 + int(c-'0')
	}
	sc.i += n
	return v, true
}

func (sc *scanner) eat(c byte) bool {
	if sc.i < len(sc.s) && sc.s[sc.i] == c {
		sc.i++
		return true
	}
	return false
}

func (sc *scanner) done() bool { return sc.i == len(sc.s) }

func parseISODate(s string) (float64, bool) {
	sc := &scanner{s: s}
	var year int
	var ok bool
	switch {
	case sc.eat('+'):
		year, ok = sc.digits(6)
	case sc.eat('-'):
		year, ok = sc.digits(6)
		if year == 0 {
			return 0, false
		}
		year = -year
	default:
		year, ok = sc.digits(4)
	}
	if !ok {
		return 0, false
	}
	month, day := 1, 1
	if sc.eat('-') {
		if month, ok = sc.digits(2); !ok || month < 1 || month > 12 {
			return math.NaN(), ok
		}
		if sc.eat('-') {
			if day, ok = sc.digits(2); !ok || day < 1 || day > 31 {
				return math.NaN(), ok
			}
		}
	}
	date := makeDay(float64(year), float64(month-1), float64(day))
	if sc.done() {
		return timeClip(makeDate(date, 0)), true
	}
	if !sc.eat('T') && !sc.eat(' ') {
		return 0, false
	}
	h, ok1 := sc.digits(2)
	if !ok1 || !sc.eat(':') {
		return 0, false
	}
	mi, ok2 := sc.digits(2)
	if !ok2 {
		return 0, false
	}
	sec, ms := 0, 0
	if sc.eat(':') {
		if sec, ok = sc.digits(2); !ok {
			return 0, false
		}
		if sc.eat('.') {
			start := sc.i
			for sc.i < len(s) && s[sc.i] >= '0' && s[sc.i] <= '9' {
				sc.i++
			}
			frac := s[start:sc.i]
			if frac == "" {
				return 0, false
			}
			for len(frac) < 3 {
				frac += "0"
			}
			for _, c := range frac[:3] {
				ms = ms*10 + int(c-'0')
			}
		}
	}
	if h > 24 || mi > 59 || sec > 59 || (h == 24 && (mi != 0 || sec != 0 || ms != 0)) {
		return math.NaN(), true
	}
	t := makeDate(date, makeTime(float64(h), float64(mi), float64(sec), float64(ms)))
	switch {
	case sc.eat('Z'):
	case sc.i < len(s) && (s[sc.i] == '+' || s[sc.i] == '-'):
		sign := 1.0
		if s[sc.i] == '-' {
			sign = -1
		}
		sc.i++
		oh, ok1 := sc.digits(2)
		sc.eat(':')
		om, ok2 := sc.digits(2)
		if !ok1 || !ok2 || oh > 23 || om > 59 {
			return 0, false
		}
		t -= sign * float64(oh*60+om) * 60000
	default:
		if !sc.done() {
			return 0, false
		}
		return timeClip(utcFromLocal(t)), true
	}
	if !sc.done() {
		return 0, false
	}
	return timeClip(t), true
}

type dateTok struct {
	kind byte
	num  int
	nlen int
	word string
}

func tokenizeDate(s string) []dateTok {
	var toks []dateTok
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			j := i
			n := 0
			for j < len(s) && s[j] >= '0' && s[j] <= '9' {
				if j-i < 9 {
					n = n*10 + int(s[j]-'0')
				}
				j++
			}
			toks = append(toks, dateTok{kind: 'n', num: n, nlen: j - i})
			i = j
		case c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
			j := i
			for j < len(s) && (s[j] >= 'a' && s[j] <= 'z' || s[j] >= 'A' && s[j] <= 'Z' || s[j] == '.') {
				j++
			}
			toks = append(toks, dateTok{kind: 'w', word: strings.ToLower(strings.TrimRight(s[i:j], "."))})
			i = j
		case c == '(':
			depth := 0
			for i < len(s) {
				if s[i] == '(' {
					depth++
				} else if s[i] == ')' {
					depth--
					if depth == 0 {
						i++
						break
					}
				}
				i++
			}
		case c == ' ' || c == ',' || c == '\t':
			i++
		default:
			toks = append(toks, dateTok{kind: c})
			i++
		}
	}
	return toks
}

func monthIndex(w string) int {
	if len(w) < 3 {
		return -1
	}
	for i, m := range months {
		if strings.HasPrefix(w, strings.ToLower(m)) && strings.HasPrefix(strings.ToLower(longMonths[i]), w) {
			return i
		}
	}
	return -1
}

var longMonths = [12]string{"January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"}

func isWeekday(w string) bool {
	if len(w) < 3 {
		return false
	}
	for _, d := range []string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"} {
		if strings.HasPrefix(d, w) {
			return true
		}
	}
	return false
}

type legacyDate struct {
	year, month, day int
	h, mi, sec, ms   int
	haveTime         bool
	tz               int
	haveTZ           bool
	pm, am           bool
	nums             []dateTok
}

func parseLegacyDate(s string) float64 {
	toks := tokenizeDate(s)
	if len(toks) == 0 {
		return math.NaN()
	}
	ld := &legacyDate{year: -1, month: -1, day: -1}
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		switch t.kind {
		case 'w':
			if !ld.word(t.word) {
				return math.NaN()
			}
		case 'n':
			if i+1 < len(toks) && toks[i+1].kind == ':' {
				n, ok := ld.clock(toks, i)
				if !ok {
					return math.NaN()
				}
				i = n
				continue
			}
			if i+2 < len(toks) && (toks[i+1].kind == '/' || toks[i+1].kind == '-') && toks[i+2].kind == 'n' {
				n, ok := ld.slashed(toks, i)
				if !ok {
					return math.NaN()
				}
				i = n
				continue
			}
			ld.nums = append(ld.nums, t)
		case '+', '-':
			if i+1 < len(toks) && toks[i+1].kind == 'n' && (ld.haveTime || ld.haveTZ) {
				n := toks[i+1]
				off := n.num
				if n.nlen <= 2 {
					off *= 100
					if i+3 < len(toks) && toks[i+2].kind == ':' && toks[i+3].kind == 'n' {
						off += toks[i+3].num
						i += 2
					}
				}
				mins := off/100*60 + off%100
				if t.kind == '-' {
					mins = -mins
				}
				ld.tz, ld.haveTZ = mins, true
				i++
				continue
			}
			return math.NaN()
		default:
			return math.NaN()
		}
	}
	return ld.result()
}

func (ld *legacyDate) word(w string) bool {
	switch {
	case w == "am" || w == "a.m":
		ld.am = true
	case w == "pm" || w == "p.m":
		ld.pm = true
	case w == "z" || w == "gmt" || w == "utc" || w == "ut":
		ld.haveTZ = true
	case isWeekday(w):
	case monthIndex(w) >= 0:
		if ld.month >= 0 {
			return false
		}
		ld.month = monthIndex(w)
	default:
		return false
	}
	return true
}

func (ld *legacyDate) clock(toks []dateTok, i int) (int, bool) {
	ld.h = toks[i].num
	if i+2 >= len(toks) || toks[i+2].kind != 'n' {
		return i, false
	}
	ld.mi = toks[i+2].num
	i += 2
	if i+2 < len(toks) && toks[i+1].kind == ':' && toks[i+2].kind == 'n' {
		ld.sec = toks[i+2].num
		i += 2
		if i+2 < len(toks) && toks[i+1].kind == '.' && toks[i+2].kind == 'n' {
			ms := toks[i+2]
			v := ms.num
			for l := ms.nlen; l < 3; l++ {
				v *= 10
			}
			for l := ms.nlen; l > 3; l-- {
				v /= 10
			}
			ld.ms = v
			i += 2
		}
	}
	ld.haveTime = true
	return i, true
}

func (ld *legacyDate) slashed(toks []dateTok, i int) (int, bool) {
	sep := toks[i+1].kind
	a, b := toks[i], toks[i+2]
	var c dateTok
	have3 := false
	if i+4 < len(toks) && toks[i+3].kind == sep && toks[i+4].kind == 'n' {
		c, have3 = toks[i+4], true
	}
	if a.nlen >= 3 {
		ld.year, ld.month = a.num, b.num-1
		ld.day = 1
		if have3 {
			ld.day = c.num
			return i + 4, true
		}
		return i + 2, true
	}
	ld.month, ld.day = a.num-1, b.num
	if have3 {
		ld.year = c.num
		if c.nlen <= 2 {
			ld.year = twoDigitYear(c.num)
		}
		return i + 4, true
	}
	return i + 2, true
}

func twoDigitYear(n int) int {
	if n < 50 {
		return 2000 + n
	}
	return 1900 + n
}

func (ld *legacyDate) result() float64 {
	for _, n := range ld.nums {
		switch {
		case n.nlen >= 3 || n.num > 31:
			if ld.year >= 0 {
				return math.NaN()
			}
			ld.year = n.num
		case ld.day < 0 && ld.month >= 0:
			ld.day = n.num
		case ld.day < 0 && ld.month < 0 && len(ld.nums) == 1:
			return math.NaN()
		case ld.day < 0:
			ld.day = n.num
		case ld.year < 0:
			ld.year = twoDigitYear(n.num)
		default:
			return math.NaN()
		}
	}
	if ld.year < 0 || ld.month < 0 {
		return math.NaN()
	}
	if ld.day < 0 {
		ld.day = 1
	}
	if ld.month > 11 || ld.day < 1 || ld.day > 31 {
		return math.NaN()
	}
	h := ld.h
	if ld.pm && h < 12 {
		h += 12
	}
	if ld.am && h == 12 {
		h = 0
	}
	if h > 24 || ld.mi > 59 || ld.sec > 59 {
		return math.NaN()
	}
	t := makeDate(makeDay(float64(ld.year), float64(ld.month), float64(ld.day)), makeTime(float64(h), float64(ld.mi), float64(ld.sec), float64(ld.ms)))
	if ld.haveTZ {
		return timeClip(t - float64(ld.tz)*60000)
	}
	return timeClip(utcFromLocal(t))
}
