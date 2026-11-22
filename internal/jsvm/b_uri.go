package jsvm

import (
	"strings"
	"unicode/utf8"
)

const uriUnreserved = "-_.!~*'()"
const uriReserved = ";/?:@&=+$,#"

func (vm *VM) setupURI() {
	enc := func(name, keep string) {
		vm.setGlobal(name, objV(vm.newNative(name, 1, func(vm *VM, this Value, args []Value) Value {
			src := vm.toString(arg(args, 0))
			vm.reserve(3 * len(src))
			out := uriEncode(src, keep)
			if len(out) > maxStringLen {
				vm.throwError("RangeError", "Invalid string length")
			}
			vm.track(len(out))
			return strV(out)
		})))
	}
	enc("encodeURIComponent", uriUnreserved)
	enc("encodeURI", uriUnreserved+uriReserved)
	dec := func(name, keep string) {
		vm.setGlobal(name, objV(vm.newNative(name, 1, func(vm *VM, this Value, args []Value) Value {
			s, ok := uriDecode(vm.toString(arg(args, 0)), keep)
			if !ok {
				vm.throwError("URIError", "URI malformed")
			}
			vm.track(len(s))
			return strV(s)
		})))
	}
	dec("decodeURIComponent", "")
	dec("decodeURI", uriReserved)
	vm.setGlobal("escape", objV(vm.newNative("escape", 1, func(vm *VM, this Value, args []Value) Value {
		src := vm.toStr(arg(args, 0))
		vm.reserve(6 * src.length())
		out := jsEscape(src)
		if len(out) > maxStringLen {
			vm.throwError("RangeError", "Invalid string length")
		}
		vm.track(len(out))
		return strV(out)
	})))
	vm.setGlobal("unescape", objV(vm.newNative("unescape", 1, func(vm *VM, this Value, args []Value) Value {
		src := vm.toString(arg(args, 0))
		vm.reserve(8 * len(src))
		vm.track(len(src))
		return strV(jsUnescape(src))
	})))
}

func jsEscape(s *jstr) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for _, c := range s.units() {
		switch {
		case c < 128 && (c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("@*_+-./", byte(c)) >= 0):
			b.WriteByte(byte(c))
		case c < 256:
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
		default:
			b.WriteString("%u")
			for sh := 12; sh >= 0; sh -= 4 {
				b.WriteByte(hex[(c>>uint(sh))&15])
			}
		}
	}
	return b.String()
}

func jsUnescape(s string) string {
	var u []uint16
	for _, r := range s {
		u = append(u, utf16Units(r)...)
	}
	out := make([]uint16, 0, len(u))
	for i := 0; i < len(u); i++ {
		if u[i] == '%' {
			if i+5 < len(u) && u[i+1] == 'u' {
				if v, ok := hexUnits(u[i+2 : i+6]); ok {
					out = append(out, v)
					i += 5
					continue
				}
			}
			if i+2 < len(u) {
				if v, ok := hexUnits(u[i+1 : i+3]); ok {
					out = append(out, v)
					i += 2
					continue
				}
			}
		}
		out = append(out, u[i])
	}
	return unitsToString(out)
}

func utf16Units(r rune) []uint16 {
	if r >= 0x10000 {
		r -= 0x10000
		return []uint16{uint16(0xD800 + (r >> 10)), uint16(0xDC00 + (r & 0x3FF))}
	}
	return []uint16{uint16(r)}
}

func hexUnits(u []uint16) (uint16, bool) {
	var v uint16
	for _, c := range u {
		if c > 127 {
			return 0, false
		}
		h, ok := unhex(byte(c))
		if !ok {
			return 0, false
		}
		v = v<<4 | uint16(h)
	}
	return v, true
}

func uriEncode(s, keep string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte(keep, c) >= 0 {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&15])
	}
	return b.String()
}

func unhex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

func pctByte(s string, i int) (byte, bool) {
	if i+2 >= len(s) || s[i] != '%' {
		return 0, false
	}
	h, ok1 := unhex(s[i+1])
	l, ok2 := unhex(s[i+2])
	return h<<4 | l, ok1 && ok2
}

func uriDecode(s, keep string) (string, bool) {
	if strings.IndexByte(s, '%') < 0 {
		return s, true
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			b.WriteByte(s[i])
			continue
		}
		c, ok := pctByte(s, i)
		if !ok {
			return "", false
		}
		if c < 0x80 {
			if strings.IndexByte(keep, c) >= 0 {
				b.WriteString(s[i : i+3])
			} else {
				b.WriteByte(c)
			}
			i += 2
			continue
		}
		n := 0
		switch {
		case c&0xE0 == 0xC0:
			n = 2
		case c&0xF0 == 0xE0:
			n = 3
		case c&0xF8 == 0xF0:
			n = 4
		default:
			return "", false
		}
		buf := []byte{c}
		for k := 1; k < n; k++ {
			cb, ok := pctByte(s, i+3*k)
			if !ok || cb&0xC0 != 0x80 {
				return "", false
			}
			buf = append(buf, cb)
		}
		r, size := utf8.DecodeRune(buf)
		if (r == utf8.RuneError && size == 1) || size != n {
			return "", false
		}
		b.Write(buf)
		i += 3*n - 1
	}
	return b.String(), true
}
