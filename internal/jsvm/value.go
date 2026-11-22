package jsvm

import (
	"math"
	"math/big"
	"math/bits"
	"strconv"
	"strings"
)

type Value struct {
	n float64
	r any
}

type undefT struct{}

type holeT struct{}

var holeV = Value{r: holeT{}}

func (v Value) isHole() bool { _, ok := v.r.(holeT); return ok }

func unhole(v Value) Value {
	if _, ok := v.r.(holeT); ok {
		return undefined
	}
	return v
}

type nullT struct{}
type boolT struct{}

type Kind int

const (
	KindUndefined Kind = iota
	KindNull
	KindBool
	KindNumber
	KindString
	KindObject
	KindFunction
	KindSymbol
)

var (
	undefined = Value{r: undefT{}}
	null      = Value{r: nullT{}}
	vTrue     = Value{n: 1, r: boolT{}}
	vFalse    = Value{r: boolT{}}
	vNaN      = Value{n: math.NaN()}
	emptyStr  = Value{r: &jstr{s: "", info: strKnown | strASCII | strShared}}
)

func Undefined() Value            { return undefined }
func Null() Value                 { return null }
func Number(f float64) Value      { return Value{n: f} }
func Str(s string) Value          { return Value{r: newString(validUTF8(s))} }
func Bool(b bool) Value           { return boolV(b) }
func num(f float64) Value         { return Value{n: f} }
func strV(s string) Value         { return Value{r: newString(s)} }
func objV(o *object) Value        { return Value{r: o} }
func strVal(s *jstr) Value        { return Value{r: s} }
func intV(i int) Value            { return Value{n: float64(i)} }
func (v Value) isUndef() bool     { _, ok := v.r.(undefT); return ok }
func (v Value) isNull() bool      { _, ok := v.r.(nullT); return ok }
func (v Value) IsUndefined() bool { return v.isUndef() }
func (v Value) IsNull() bool      { return v.isNull() }

func boolV(b bool) Value {
	if b {
		return vTrue
	}
	return vFalse
}

func (v Value) isNullish() bool {
	switch v.r.(type) {
	case undefT, nullT:
		return true
	}
	return false
}

func (v Value) obj() *object {
	o, _ := v.r.(*object)
	return o
}

func (v Value) str() (*jstr, bool) {
	s, ok := v.r.(*jstr)
	return s, ok
}

func (v Value) Kind() Kind {
	switch r := v.r.(type) {
	case nil:
		return KindNumber
	case undefT:
		return KindUndefined
	case nullT:
		return KindNull
	case boolT:
		return KindBool
	case *jstr:
		return KindString
	case *object:
		if r.isCallable() {
			return KindFunction
		}
		return KindObject
	case *symbol:
		return KindSymbol
	}
	return KindUndefined
}

func (v Value) String() string { return ToString(v) }

func (v Value) Float() float64 { return toNumberNoCall(v) }

func (v Value) Truthy() bool { return toBool(v) }

func toBool(v Value) bool {
	switch r := v.r.(type) {
	case nil:
		return v.n != 0 && v.n == v.n
	case boolT:
		return v.n != 0
	case *jstr:
		return r.n != 0
	case undefT, nullT:
		return false
	}
	return true
}

func typeOf(v Value) string {
	switch r := v.r.(type) {
	case nil:
		return "number"
	case undefT:
		return "undefined"
	case nullT:
		return "object"
	case boolT:
		return "boolean"
	case *jstr:
		return "string"
	case *symbol:
		return "symbol"
	case *object:
		if r.isCallable() {
			return "function"
		}
		return "object"
	}
	return "undefined"
}

type symbol struct {
	desc string
}

func ToString(v Value) string {
	switch r := v.r.(type) {
	case nil:
		return numberToString(v.n)
	case undefT:
		return "undefined"
	case nullT:
		return "null"
	case boolT:
		if v.n != 0 {
			return "true"
		}
		return "false"
	case *jstr:
		return r.flat()
	case *symbol:
		return "Symbol(" + r.desc + ")"
	case *object:
		return objectDescription(r)
	}
	return ""
}

func objectDescription(o *object) string {
	switch o.class {
	case classArray:
		return "[object Array]"
	case classFunction:
		return "function () { [native code] }"
	case classError:
		return "Error"
	}
	return "[object Object]"
}

func toNumberNoCall(v Value) float64 {
	switch r := v.r.(type) {
	case nil:
		return v.n
	case undefT:
		return math.NaN()
	case nullT:
		return 0
	case boolT:
		return v.n
	case *jstr:
		return stringToNumber(r.flat())
	}
	return math.NaN()
}

func isJSSpace(c rune) bool {
	switch c {
	case ' ', '\t', '\n', '\v', '\f', '\r', 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return c >= 0x2000 && c <= 0x200A
}

func trimJS(s string) string {
	start, end := 0, len(s)
	for start < end {
		c := s[start]
		if c < 0x80 {
			if !isJSSpace(rune(c)) {
				break
			}
			start++
			continue
		}
		r, size := decodeRune(s[start:])
		if !isJSSpace(r) {
			break
		}
		start += size
	}
	for end > start {
		c := s[end-1]
		if c < 0x80 {
			if !isJSSpace(rune(c)) {
				break
			}
			end--
			continue
		}
		r, size := decodeLastRune(s[start:end])
		if !isJSSpace(r) {
			break
		}
		end -= size
	}
	return s[start:end]
}

func stringToNumber(s string) float64 {
	s = trimJS(s)
	if s == "" {
		return 0
	}
	if len(s) > 2 && s[0] == '0' {
		base := 0
		switch s[1] {
		case 'x', 'X':
			base = 16
		case 'o', 'O':
			base = 8
		case 'b', 'B':
			base = 2
		}
		if base != 0 {
			return parseIntDigits(s[2:], base, true)
		}
	}
	switch s {
	case "Infinity", "+Infinity":
		return math.Inf(1)
	case "-Infinity":
		return math.Inf(-1)
	}
	if !validDecimal(s) {
		return math.NaN()
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		if ne, ok := err.(*strconv.NumError); ok && ne.Err == strconv.ErrRange {
			return f
		}
		return math.NaN()
	}
	return f
}

func validDecimal(s string) bool {
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	digits := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
		digits++
	}
	if i < len(s) && s[i] == '.' {
		i++
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
			digits++
		}
	}
	if digits == 0 {
		return false
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		ed := 0
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
			ed++
		}
		if ed == 0 {
			return false
		}
	}
	return i == len(s)
}

func parseIntDigits(s string, base int, strict bool) float64 {
	if s == "" {
		return math.NaN()
	}
	var f float64
	n := 0
	for i := 0; i < len(s); i++ {
		d := digitVal(s[i])
		if d >= base {
			if strict {
				return math.NaN()
			}
			break
		}
		f = f*float64(base) + float64(d)
		n++
	}
	if n == 0 {
		return math.NaN()
	}
	if f >= 1<<53 && base == 10 {
		if g, err := strconv.ParseFloat(s[:n], 64); err == nil {
			return g
		}
	}
	if f >= 1<<53 && base&(base-1) == 0 {
		return exactPow2(s[:n], base)
	}
	return f
}

func exactPow2(s string, base int) float64 {
	s = strings.TrimLeft(s, "0")
	if len(s)*bits.Len(uint(base-1)) > 1100 {
		return math.Inf(1)
	}
	b, ok := new(big.Int).SetString(s, base)
	if !ok {
		return math.NaN()
	}
	f, _ := new(big.Float).SetInt(b).Float64()
	return f
}

func digitVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'z':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'Z':
		return int(c-'A') + 10
	}
	return 99
}

func numberToString(f float64) string {
	if f == 0 {
		return "0"
	}
	if f != f {
		return "NaN"
	}
	if math.IsInf(f, 0) {
		if f > 0 {
			return "Infinity"
		}
		return "-Infinity"
	}
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return strconv.FormatInt(int64(f), 10)
	}
	return string(appendNumber(nil, f))
}

func appendNumber(b []byte, f float64) []byte {
	if f == 0 {
		return append(b, '0')
	}
	if f != f {
		return append(b, "NaN"...)
	}
	if math.IsInf(f, 0) {
		if f > 0 {
			return append(b, "Infinity"...)
		}
		return append(b, "-Infinity"...)
	}
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return strconv.AppendInt(b, int64(f), 10)
	}
	if f < 0 {
		b = append(b, '-')
		f = -f
	}
	var tmp [32]byte
	e := strconv.AppendFloat(tmp[:0], f, 'e', -1, 64)
	mant, exp := e, 0
	for i, c := range e {
		if c == 'e' {
			mant = e[:i]
			x, _ := strconv.Atoi(string(e[i+1:]))
			exp = x
			break
		}
	}
	var digits [24]byte
	k := 0
	for _, c := range mant {
		if c != '.' {
			digits[k] = c
			k++
		}
	}
	d := digits[:k]
	n := exp + 1
	switch {
	case k <= n && n <= 21:
		b = append(b, d...)
		for i := 0; i < n-k; i++ {
			b = append(b, '0')
		}
	case 0 < n && n <= 21:
		b = append(b, d[:n]...)
		b = append(b, '.')
		b = append(b, d[n:]...)
	case -6 < n && n <= 0:
		b = append(b, '0', '.')
		for i := 0; i < -n; i++ {
			b = append(b, '0')
		}
		b = append(b, d...)
	default:
		b = append(b, d[0])
		if k > 1 {
			b = append(b, '.')
			b = append(b, d[1:]...)
		}
		b = append(b, 'e')
		if n-1 >= 0 {
			b = append(b, '+')
		}
		b = strconv.AppendInt(b, int64(n-1), 10)
	}
	return b
}

func toInt32(f float64) int32 {
	if i := int32(f); float64(i) == f {
		return i
	}
	return int32(toUint32(f))
}

func toUint32(f float64) uint32 {
	if f != f || math.IsInf(f, 0) {
		return 0
	}
	if f >= 0 && f < 4294967296 {
		return uint32(f)
	}
	f = math.Trunc(f)
	f = math.Mod(f, 4294967296)
	if f < 0 {
		f += 4294967296
	}
	return uint32(f)
}

func toIntegerOrInf(f float64) float64 {
	if f != f {
		return 0
	}
	return math.Trunc(f)
}

func strictEquals(a, b Value) bool {
	switch ra := a.r.(type) {
	case nil:
		return b.r == nil && a.n == b.n
	case *jstr:
		rb, ok := b.r.(*jstr)
		return ok && (ra == rb || (ra.n == rb.n && ra.flat() == rb.flat()))
	case boolT:
		_, ok := b.r.(boolT)
		return ok && a.n == b.n
	case undefT:
		_, ok := b.r.(undefT)
		return ok
	case nullT:
		_, ok := b.r.(nullT)
		return ok
	}
	return a.r == b.r
}

func sameValueZero(a, b Value) bool {
	if a.r == nil && b.r == nil && a.n != a.n && b.n != b.n {
		return true
	}
	return strictEquals(a, b)
}

func sameValue(a, b Value) bool {
	if a.r == nil && b.r == nil {
		if a.n != a.n && b.n != b.n {
			return true
		}
		if a.n == 0 && b.n == 0 {
			return math.Signbit(a.n) == math.Signbit(b.n)
		}
		return a.n == b.n
	}
	return strictEquals(a, b)
}
