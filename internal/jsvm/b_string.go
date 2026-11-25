package jsvm

import (
	"math"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

func (vm *VM) thisStr(this Value, name string) *jstr {
	switch r := this.r.(type) {
	case *jstr:
		return r
	case undefT, nullT:
		vm.throwTypeError("String.prototype." + name + " called on null or undefined")
	case *object:
		if r.class == classString {
			return r.inner.(*jstr)
		}
	}
	return vm.toStr(this)
}

func (vm *VM) setupString() {
	p := vm.stringProto
	p.class = classString
	p.inner = newASCII("")
	build := func(vm *VM, args []Value) *jstr {
		if len(args) == 0 {
			return newASCII("")
		}
		if sym, ok := args[0].r.(*symbol); ok {
			return newString("Symbol(" + sym.desc + ")")
		}
		return vm.toStr(args[0])
	}
	ctor := vm.newCtor("String", 1, p, func(vm *VM, this Value, args []Value) Value {
		return strVal(build(vm, args))
	}, func(vm *VM, args []Value, nt *object) Value {
		return objV(&object{shape: vm.protoFrom(nt, p).rootShape(), class: classString, inner: build(vm, args)})
	})
	vm.method(ctor, "fromCharCode", 1, func(vm *VM, this Value, args []Value) Value {
		u := make([]uint16, len(args))
		for i, a := range args {
			u[i] = uint16(toUint32(vm.toNumber(a)))
		}
		return strV(unitsToString(u))
	})
	vm.method(ctor, "fromCodePoint", 1, func(vm *VM, this Value, args []Value) Value {
		var b strings.Builder
		for _, a := range args {
			f := vm.toNumber(a)
			if f != math.Trunc(f) || f < 0 || f > 0x10FFFF {
				vm.throwError("RangeError", "Invalid code point "+ToString(a))
			}
			b.WriteRune(rune(f))
		}
		return strV(b.String())
	})
	vm.method(ctor, "raw", 1, func(vm *VM, this Value, args []Value) Value {
		strs := vm.toObject(arg(args, 0))
		raw := vm.toObject(vm.getV(objV(strs), strKey("raw")))
		parts := vm.arrayLike(objV(raw))
		b := sbuf{vm: vm}
		for i, s := range parts {
			b.add(vm.toString(s))
			if i+1 < len(parts) && i+1 < len(args) {
				b.add(vm.toString(args[i+1]))
			}
		}
		return b.value()
	})
	m := func(name string, n int, fn func(vm *VM, s *jstr, args []Value) Value) {
		vm.method(p, name, n, func(vm *VM, this Value, args []Value) Value {
			s := vm.thisStr(this, name)
			vm.flat(s)
			if !s.isASCII() && s.n > 1<<20 {
				if e := s.x().more(); !e.reserved {
					e.reserved = true
					vm.reserve(2 * s.size())
				}
			}
			return fn(vm, s, args)
		})
	}
	m("toString", 0, func(vm *VM, s *jstr, args []Value) Value { return strVal(s) })
	m("valueOf", 0, func(vm *VM, s *jstr, args []Value) Value { return strVal(s) })
	m("charAt", 1, func(vm *VM, s *jstr, args []Value) Value {
		i := toIntegerOrInf(vm.toNumber(arg(args, 0)))
		if i < 0 || i >= float64(s.length()) {
			return emptyStr
		}
		return s.substr(int(i), int(i)+1)
	})
	m("charCodeAt", 1, func(vm *VM, s *jstr, args []Value) Value {
		i := toIntegerOrInf(vm.toNumber(arg(args, 0)))
		if i < 0 || i >= float64(s.length()) {
			return vNaN
		}
		return intV(s.charCodeAt(int(i)))
	})
	m("codePointAt", 1, func(vm *VM, s *jstr, args []Value) Value {
		i := toIntegerOrInf(vm.toNumber(arg(args, 0)))
		n := s.length()
		if i < 0 || i >= float64(n) {
			return undefined
		}
		c := s.charCodeAt(int(i))
		if c >= 0xD800 && c < 0xDC00 && int(i)+1 < n {
			d := s.charCodeAt(int(i) + 1)
			if d >= 0xDC00 && d < 0xE000 {
				return intV(int(utf16.DecodeRune(rune(c), rune(d))))
			}
		}
		return intV(c)
	})
	m("at", 1, func(vm *VM, s *jstr, args []Value) Value {
		i := int(toIntegerOrInf(vm.toNumber(arg(args, 0))))
		n := s.length()
		if i < 0 {
			i += n
		}
		if i < 0 || i >= n {
			return undefined
		}
		return s.substr(i, i+1)
	})
	m("indexOf", 1, func(vm *VM, s *jstr, args []Value) Value {
		sub := vm.toString(arg(args, 0))
		from := int(math.Max(0, math.Min(toIntegerOrInf(vm.toNumber(arg(args, 1))), float64(s.length()))))
		return intV(s.indexOf(sub, from))
	})
	m("lastIndexOf", 1, func(vm *VM, s *jstr, args []Value) Value {
		sub := vm.toString(arg(args, 0))
		from := s.length()
		if f := vm.toNumber(arg(args, 1)); f == f {
			from = int(math.Max(0, math.Min(math.Trunc(f), float64(s.length()))))
		}
		return intV(s.lastIndexOf(sub, from))
	})
	m("includes", 1, func(vm *VM, s *jstr, args []Value) Value {
		if _, re := asRegExp(arg(args, 0)); re != nil {
			vm.throwTypeError("First argument to String.prototype.includes must not be a regular expression")
		}
		sub := vm.toString(arg(args, 0))
		from := int(math.Max(0, math.Min(toIntegerOrInf(vm.toNumber(arg(args, 1))), float64(s.length()))))
		return boolV(s.indexOf(sub, from) >= 0)
	})
	m("startsWith", 1, func(vm *VM, s *jstr, args []Value) Value {
		if _, re := asRegExp(arg(args, 0)); re != nil {
			vm.throwTypeError("First argument to String.prototype.startsWith must not be a regular expression")
		}
		sub := vm.toString(arg(args, 0))
		if s.isASCII() && arg(args, 1).isUndef() {
			return boolV(strings.HasPrefix(s.flat(), sub))
		}
		from := int(math.Max(0, math.Min(toIntegerOrInf(vm.toNumber(arg(args, 1))), float64(s.length()))))
		rest := s.substring(from, s.length())
		return boolV(strings.HasPrefix(rest, sub))
	})
	m("endsWith", 1, func(vm *VM, s *jstr, args []Value) Value {
		if _, re := asRegExp(arg(args, 0)); re != nil {
			vm.throwTypeError("First argument to String.prototype.endsWith must not be a regular expression")
		}
		sub := vm.toString(arg(args, 0))
		end := s.length()
		if e := arg(args, 1); !e.isUndef() {
			end = int(math.Max(0, math.Min(toIntegerOrInf(vm.toNumber(e)), float64(end))))
		}
		return boolV(strings.HasSuffix(s.substring(0, end), sub))
	})
	m("slice", 2, func(vm *VM, s *jstr, args []Value) Value {
		n := s.length()
		a, b := relIndex(arg(args, 0), vm, n, 0), relIndex(arg(args, 1), vm, n, n)
		if b <= a {
			return emptyStr
		}
		return vm.substr(s, a, b)
	})
	m("substring", 2, func(vm *VM, s *jstr, args []Value) Value {
		n := s.length()
		clamp := func(v Value, def int) int {
			if v.isUndef() {
				return def
			}
			return int(math.Max(0, math.Min(toIntegerOrInf(vm.toNumber(v)), float64(n))))
		}
		a, b := clamp(arg(args, 0), 0), clamp(arg(args, 1), n)
		if a > b {
			a, b = b, a
		}
		return vm.substr(s, a, b)
	})
	m("substr", 2, func(vm *VM, s *jstr, args []Value) Value {
		n := s.length()
		a := relIndex(arg(args, 0), vm, n, 0)
		l := n - a
		if v := arg(args, 1); !v.isUndef() {
			l = int(math.Max(0, math.Min(toIntegerOrInf(vm.toNumber(v)), float64(n-a))))
		}
		if l <= 0 {
			return emptyStr
		}
		return vm.substr(s, a, a+l)
	})
	m("toUpperCase", 0, func(vm *VM, s *jstr, args []Value) Value { return vm.caseMap(s, jsToUpper) })
	m("toLowerCase", 0, func(vm *VM, s *jstr, args []Value) Value { return vm.caseMap(s, jsToLower) })
	m("toLocaleUpperCase", 0, func(vm *VM, s *jstr, args []Value) Value { return vm.caseMap(s, jsToUpper) })
	m("toLocaleLowerCase", 0, func(vm *VM, s *jstr, args []Value) Value { return vm.caseMap(s, jsToLower) })
	m("trim", 0, func(vm *VM, s *jstr, args []Value) Value { return derived(s, trimJS(s.flat())) })
	m("trimStart", 0, func(vm *VM, s *jstr, args []Value) Value { return derived(s, trimLeftJS(s.flat())) })
	m("trimEnd", 0, func(vm *VM, s *jstr, args []Value) Value { return derived(s, trimRightJS(s.flat())) })
	m("padStart", 2, func(vm *VM, s *jstr, args []Value) Value { return vm.pad(s, args, true) })
	m("padEnd", 2, func(vm *VM, s *jstr, args []Value) Value { return vm.pad(s, args, false) })
	m("repeat", 1, func(vm *VM, s *jstr, args []Value) Value {
		f := toIntegerOrInf(vm.toNumber(arg(args, 0)))
		if f < 0 || math.IsInf(f, 0) {
			vm.throwError("RangeError", "Invalid count value: "+ToString(arg(args, 0)))
		}
		if s.n == 0 || f == 0 {
			return emptyStr
		}
		if f*float64(s.n) > 1<<29 {
			vm.throwError("RangeError", "Invalid string length")
		}
		vm.reserve(int(f) * s.size())
		vm.track(int(f) * s.size())
		return strV(strings.Repeat(s.flat(), int(f)))
	})
	m("concat", 1, func(vm *VM, s *jstr, args []Value) Value {
		r := s
		for _, a := range args {
			r = vm.concat(r, vm.toStr(a))
		}
		return strVal(r)
	})
	m("split", 2, func(vm *VM, s *jstr, args []Value) Value { return vm.strSplit(s, arg(args, 0), arg(args, 1)) })
	m("replace", 2, func(vm *VM, s *jstr, args []Value) Value {
		return vm.strReplace(s, arg(args, 0), arg(args, 1), false)
	})
	m("replaceAll", 2, func(vm *VM, s *jstr, args []Value) Value {
		return vm.strReplace(s, arg(args, 0), arg(args, 1), true)
	})
	m("match", 1, func(vm *VM, s *jstr, args []Value) Value { return vm.strMatch(s, arg(args, 0)) })
	m("matchAll", 1, func(vm *VM, s *jstr, args []Value) Value { return vm.strMatchAll(s, arg(args, 0)) })
	m("search", 1, func(vm *VM, s *jstr, args []Value) Value { return vm.strSearch(s, arg(args, 0)) })
	m("localeCompare", 1, func(vm *VM, s *jstr, args []Value) Value {
		other := vm.toString(arg(args, 0))
		if vm.numericCollation(arg(args, 2)) {
			return intV(numericCompare(s.flat(), other))
		}
		return intV(localeCompare(s.flat(), other))
	})
	m("normalize", 0, func(vm *VM, s *jstr, args []Value) Value {
		form := "NFC"
		if f := arg(args, 0); !f.isUndef() {
			form = vm.toString(f)
		}
		switch form {
		case "NFC", "NFD", "NFKC", "NFKD":
		default:
			vm.throwError("RangeError", "The normalization form should be one of NFC, NFD, NFKC, NFKD.")
		}
		if s.isASCII() {
			return strVal(s)
		}
		vm.throwTypeError("unsupported: String.prototype.normalize on non-ASCII text")
		return undefined
	})
	m("isWellFormed", 0, func(vm *VM, s *jstr, args []Value) Value { return vTrue })
	m("toWellFormed", 0, func(vm *VM, s *jstr, args []Value) Value { return strVal(s) })
}

func (vm *VM) caseMap(s *jstr, fn func(string) string) Value {
	if !s.isASCII() {
		if 3*s.n > maxStringLen {
			vm.throwError("RangeError", "Invalid string length")
		}
		vm.reserve(3 * s.size())
	}
	out := fn(s.flat())
	if len(out) > maxStringLen {
		vm.throwError("RangeError", "Invalid string length")
	}
	vm.track(len(out))
	return strV(out)
}

func derived(parent *jstr, s string) Value {
	if len(s) == parent.size() {
		return strVal(parent)
	}
	if parent.isASCII() {
		return Value{r: newASCII(s)}
	}
	return strV(s)
}

func (vm *VM) substr(s *jstr, a, b int) Value {
	if !s.isASCII() {
		vm.track(4*(b-a) + 64)
	}
	return s.substr(a, b)
}

func trimLeftJS(s string) string {
	for len(s) > 0 {
		r, size := decodeRune(s)
		if !isJSSpace(r) {
			break
		}
		s = s[size:]
	}
	return s
}

func trimRightJS(s string) string {
	for len(s) > 0 {
		r, size := decodeLastRune(s)
		if !isJSSpace(r) {
			break
		}
		s = s[:len(s)-size]
	}
	return s
}

func (vm *VM) pad(s *jstr, args []Value, start bool) Value {
	tf := toIntegerOrInf(vm.toNumber(arg(args, 0)))
	fill := " "
	if f := arg(args, 1); !f.isUndef() {
		fill = vm.toString(f)
	}
	n := s.length()
	if tf <= float64(n) || fill == "" {
		return strVal(s)
	}
	if tf > maxStringLen {
		vm.throwError("RangeError", "Invalid string length")
	}
	target := int(tf)
	if fu := utf16Len(fill); target/fu*len(fill)+s.size() > maxStringLen {
		vm.throwError("RangeError", "Invalid string length")
	} else {
		vm.reserve(target / fu * len(fill))
	}
	if target <= n {
		return strVal(s)
	}
	if target > maxStringLen {
		vm.throwError("RangeError", "Invalid string length")
	}
	vm.reserve(target)
	vm.track(target)
	need := target - n
	fu := utf16Len(fill)
	var b strings.Builder
	for need >= fu {
		b.WriteString(fill)
		need -= fu
	}
	if need > 0 {
		fs := newString(fill)
		b.WriteString(fs.substring(0, need))
	}
	if start {
		return strV(b.String() + s.flat())
	}
	return strV(s.flat() + b.String())
}

func (vm *VM) numericCollation(opts Value) bool {
	o, ok := opts.r.(*object)
	if !ok {
		return false
	}
	numeric := false
	for _, k := range o.ownKeys(true) {
		v := vm.getProp(o, k, opts)
		switch k.str() {
		case "numeric":
			numeric = toBool(v)
		case "usage":
			if vm.toString(v) != "sort" {
				vm.throwTypeError("unsupported: localeCompare usage " + vm.toString(v))
			}
		default:
			vm.throwTypeError("unsupported: localeCompare option " + k.str())
		}
	}
	return numeric
}

func numericCompare(a, b string) int {
	if c := numericPrimary(a, b); c != 0 {
		return c
	}
	return localeCompare(a, b)
}

func numericPrimary(a, b string) int {
	for a != "" && b != "" {
		da, db := digitRun(a), digitRun(b)
		if da > 0 && db > 0 {
			na, nb := strings.TrimLeft(a[:da], "0"), strings.TrimLeft(b[:db], "0")
			if len(na) != len(nb) {
				if len(na) < len(nb) {
					return -1
				}
				return 1
			}
			if c := strings.Compare(na, nb); c != 0 {
				return c
			}
			a, b = a[da:], b[db:]
			continue
		}
		ra, sa := decodeRune(a)
		rb, sb := decodeRune(b)
		if wa, wb := collationWeight(ra, 0), collationWeight(rb, 0); wa != wb {
			if wa < wb {
				return -1
			}
			return 1
		}
		a, b = a[sa:], b[sb:]
	}
	switch {
	case a == "" && b == "":
		return 0
	case a == "":
		return -1
	}
	return 1
}

func digitRun(s string) int {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return i
}

func localeCompare(a, b string) int {
	if a == b {
		return 0
	}
	if i := commonPrefix(a, b); i > 0 {
		a, b = a[i:], b[i:]
	}
	for level := range 3 {
		if c := compareLevel(a, b, level); c != 0 {
			return c
		}
	}
	return strings.Compare(a, b)
}

func commonPrefix(a, b string) int {
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	for i > 0 && (i < len(a) && !utf8.RuneStart(a[i]) || i < len(b) && !utf8.RuneStart(b[i])) {
		i--
	}
	return i
}

func compareLevel(a, b string, level int) int {
	for a != "" && b != "" {
		ra, sa := decodeRune(a)
		rb, sb := decodeRune(b)
		if wa, wb := collationWeight(ra, level), collationWeight(rb, level); wa != wb {
			if wa < wb {
				return -1
			}
			return 1
		}
		a, b = a[sa:], b[sb:]
	}
	switch {
	case a == "" && b == "":
		return 0
	case a == "":
		return -1
	}
	return 1
}

func collationWeight(r rune, level int) int {
	base, accent := decompose(r)
	switch level {
	case 0:
		lower := unicode.ToLower(base)
		switch {
		case unicode.IsSpace(lower) || unicode.IsPunct(lower):
			return 0x100000 + int(lower)
		case unicode.IsSymbol(lower):
			return 0x200000 + int(lower)
		case unicode.IsDigit(lower):
			return 0x300000 + int(lower)
		}
		return 0x400000 + int(lower)
	case 1:
		return int(unicode.ToLower(accent))
	}
	if unicode.IsUpper(r) {
		return 1
	}
	return 0
}

const accentFrom = "ÀÁÂÃÄÅàáâãäåÇçÈÉÊËèéêëÌÍÎÏìíîïÑñÒÓÔÕÖØòóôõöøÙÚÛÜùúûüÝýÿĀāĂăĄąĆćĈĉĊċČčĎďĒēĔĕĖėĘęĚěĜĝĞğĠġĢģĤĥĨĩĪīĬĭĮįİĴĵĶķĹĺĻļĽľŁłŃńŅņŇňŌōŎŏŐőŔŕŖŗŘřŚśŜŝŞşŠšŢţŤťŨũŪūŬŭŮůŰűŲųŴŵŶŷŸŹźŻżŽž"
const accentTo = "AAAAAAaaaaaaCcEEEEeeeeIIIIiiiiNnOOOOOOooooooUUUUuuuuYyyAaAaAaCcCcCcCcDdEeEeEeEeEeGgGgGgGgHhIiIiIiIiIJjKkLlLlLlLlNnNnNnOoOoOoRrRrRrSsSsSsSsTtTtUuUuUuUuUuUuWwYyYZzZzZz"

var accentMap = func() map[rune]rune {
	m := map[rune]rune{}
	to := []rune(accentTo)
	for i, r := range []rune(accentFrom) {
		m[r] = to[i]
	}
	return m
}()

func decompose(r rune) (rune, rune) {
	if r < 0xC0 {
		return r, 0
	}
	if b, ok := accentMap[r]; ok {
		return b, r
	}
	return r, 0
}
