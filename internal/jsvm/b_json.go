package jsvm

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

func (vm *VM) setupJSON() {
	j := vm.newObject()
	vm.setGlobal("JSON", objV(j))
	vm.method(j, "parse", 2, func(vm *VM, this Value, args []Value) Value {
		v := vm.jsonParse(vm.toString(arg(args, 0)))
		if fn, ok := arg(args, 1).r.(*object); ok && fn.isCallable() {
			root := vm.newObject()
			root.setOwnStr("", v)
			return vm.internalize(fn, root, strKey(""))
		}
		return v
	})
	vm.method(j, "stringify", 3, func(vm *VM, this Value, args []Value) Value {
		s, ok := vm.jsonStringifyOK(arg(args, 0), arg(args, 1), vm.jsonIndent(arg(args, 2)))
		if !ok {
			return undefined
		}
		return strV(s)
	})
}

func (vm *VM) jsonIndent(v Value) string {
	if o, ok := v.r.(*object); ok {
		switch o.class {
		case classNumber:
			v = num(o.inner.(float64))
		case classString:
			v = strVal(o.inner.(*jstr))
		}
	}
	switch r := v.r.(type) {
	case nil:
		n := int(math.Min(10, math.Max(0, toIntegerOrInf(v.n))))
		return strings.Repeat(" ", n)
	case *jstr:
		s := r.flat()
		if len(s) > 10 {
			s = r.substring(0, min(10, r.length()))
		}
		return s
	}
	return ""
}

func (vm *VM) internalize(fn *object, holder *object, key propKey) Value {
	vm.enterNative()
	defer func() { vm.depth-- }()
	val := vm.getProp(holder, key, objV(holder))
	if o, ok := val.r.(*object); ok {
		if o.class == classArray {
			for i := 0; i < len(o.arr); i++ {
				nv := vm.internalize(fn, o, strKey(strconv.Itoa(i)))
				if i < len(o.arr) {
					o.arr[i] = nv
				}
			}
		} else {
			for _, k := range o.ownKeys(true) {
				nv := vm.internalize(fn, o, k)
				if nv.isUndef() {
					o.deleteOwn(k)
				} else {
					vm.defineOwn(o, k, nv)
				}
			}
		}
	}
	return vm.call2(fn, objV(holder), strV(key.str()), val)
}

type jsonParser struct {
	vm    *VM
	s     string
	i     int
	stack []Value
	depth int
}

func (vm *VM) jsonParse(s string) Value {
	vm.reserve(len(s) * 8)
	vm.track(len(s) * 2)
	p := &jsonParser{vm: vm, s: s}
	p.ws()
	v := p.value()
	p.ws()
	if p.i < len(p.s) {
		p.fail()
	}
	return v
}

func (p *jsonParser) fail() {
	if p.i >= len(p.s) {
		p.vm.throwError("SyntaxError", "Unexpected end of JSON input")
	}
	r, _ := utf8.DecodeRuneInString(p.s[p.i:])
	snippet := p.s
	if len(snippet) > 30 {
		snippet = snippet[:30] + "..."
	}
	p.vm.throwError("SyntaxError", "Unexpected token '"+string(r)+"', \""+snippet+"\" is not valid JSON")
}

func (p *jsonParser) ws() {
	for p.i < len(p.s) {
		switch p.s[p.i] {
		case ' ', '\t', '\n', '\r':
			p.i++
		default:
			return
		}
	}
}

func (p *jsonParser) value() Value {
	if p.i >= len(p.s) {
		p.fail()
	}
	switch c := p.s[p.i]; {
	case c == '{':
		return p.object()
	case c == '[':
		return p.array()
	case c == '"':
		return Value{r: p.str()}
	case c == '-' || (c >= '0' && c <= '9'):
		return p.number()
	case strings.HasPrefix(p.s[p.i:], "true"):
		p.i += 4
		return vTrue
	case strings.HasPrefix(p.s[p.i:], "false"):
		p.i += 5
		return vFalse
	case strings.HasPrefix(p.s[p.i:], "null"):
		p.i += 4
		return null
	}
	p.fail()
	return undefined
}

func (p *jsonParser) enter() {
	p.depth++
	if p.depth > 5000 {
		p.vm.throwError("RangeError", "Maximum call stack size exceeded")
	}
	if p.depth%64 == 0 {
		p.vm.checkInterrupt()
	}
}

func (p *jsonParser) object() Value {
	p.enter()
	p.i++
	p.ws()
	shape := p.vm.objectRoot
	base := len(p.stack)
	var o *object
	if p.i < len(p.s) && p.s[p.i] == '}' {
		p.i++
		p.depth--
		return objV(&object{shape: shape})
	}
	for {
		if p.i >= len(p.s) || p.s[p.i] != '"' {
			p.fail()
		}
		key := p.rawKey()
		p.ws()
		if p.i >= len(p.s) || p.s[p.i] != ':' {
			p.fail()
		}
		p.i++
		p.ws()
		v := p.value()
		if o == nil {
			k := propKey(key)
			if idx := shape.lookup(k); idx >= 0 {
				p.stack[base+idx] = v
			} else if len(shape.props) >= dictMax || isIndexKey(key) {
				o = &object{shape: shape, slots: append([]Value(nil), p.stack[base:]...)}
				p.stack = p.stack[:base]
				p.vm.defineOwn(o, k, v)
			} else {
				shape = shape.withProp(k, fDefault)
				p.stack = append(p.stack, v)
			}
		} else {
			p.vm.defineOwn(o, propKey(key), v)
		}
		p.ws()
		if p.i >= len(p.s) {
			p.fail()
		}
		if p.s[p.i] == ',' {
			p.i++
			p.ws()
			continue
		}
		if p.s[p.i] == '}' {
			p.i++
			break
		}
		p.fail()
	}
	p.depth--
	if o != nil {
		return objV(o)
	}
	slots := make([]Value, len(p.stack)-base)
	copy(slots, p.stack[base:])
	clear(p.stack[base:])
	p.stack = p.stack[:base]
	return objV(&object{shape: shape, slots: slots})
}

func isIndexKey(k string) bool {
	if len(k) == 0 || k[0] < '0' || k[0] > '9' {
		return false
	}
	_, ok := arrayIndex(k)
	return ok
}

func (p *jsonParser) array() Value {
	p.enter()
	p.i++
	p.ws()
	base := len(p.stack)
	if p.i < len(p.s) && p.s[p.i] == ']' {
		p.i++
		p.depth--
		return objV(&object{shape: p.vm.arrayRoot, arr: []Value{}, class: classArray})
	}
	for {
		p.stack = append(p.stack, p.value())
		p.ws()
		if p.i >= len(p.s) {
			p.fail()
		}
		if p.s[p.i] == ',' {
			p.i++
			p.ws()
			continue
		}
		if p.s[p.i] == ']' {
			p.i++
			break
		}
		p.fail()
	}
	p.depth--
	vals := make([]Value, len(p.stack)-base)
	copy(vals, p.stack[base:])
	clear(p.stack[base:])
	p.stack = p.stack[:base]
	return objV(&object{shape: p.vm.arrayRoot, arr: vals, class: classArray})
}

func (p *jsonParser) number() Value {
	start := p.i
	s := p.s
	i := p.i
	neg := false
	if s[i] == '-' {
		neg = true
		i++
	}
	if i >= len(s) {
		p.i = i
		p.fail()
	}
	if s[i] == '0' {
		i++
	} else if s[i] >= '1' && s[i] <= '9' {
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
	} else {
		p.i = i
		p.fail()
	}
	simple := true
	if i < len(s) && s[i] == '.' {
		simple = false
		i++
		d := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if d == i {
			p.i = i
			p.fail()
		}
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		simple = false
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		d := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if d == i {
			p.i = i
			p.fail()
		}
	}
	p.i = i
	text := s[start:i]
	if simple && len(text) < 16 {
		n := 0
		for _, c := range []byte(strings.TrimPrefix(text, "-")) {
			n = n*10 + int(c-'0')
		}
		if neg {
			if n == 0 {
				return num(math.Copysign(0, -1))
			}
			return intV(-n)
		}
		return intV(n)
	}
	f, err := strconv.ParseFloat(text, 64)
	if err != nil {
		if ne, ok := err.(*strconv.NumError); !ok || ne.Err != strconv.ErrRange {
			p.fail()
		}
	}
	return num(f)
}

func (p *jsonParser) rawKey() string {
	s := p.str()
	return s.s
}

func (p *jsonParser) str() *jstr {
	p.i++
	start := p.i
	s := p.s
	ascii := true
	for p.i < len(s) {
		c := s[p.i]
		if c == '"' {
			v := s[start:p.i]
			p.i++
			if ascii {
				return newASCII(v)
			}
			return &jstr{s: v, n: int32(len(v))}
		}
		if c == '\\' {
			return p.strSlow(start)
		}
		if c < 0x20 {
			p.fail()
		}
		if c >= 0x80 {
			ascii = false
		}
		p.i++
	}
	p.fail()
	return nil
}

func (p *jsonParser) strSlow(start int) *jstr {
	s := p.s
	b := []byte(s[start:p.i])
	for p.i < len(s) {
		c := s[p.i]
		switch {
		case c == '"':
			p.i++
			return newString(string(b))
		case c == '\\':
			p.i++
			if p.i >= len(s) {
				p.fail()
			}
			e := s[p.i]
			p.i++
			switch e {
			case '"', '\\', '/':
				b = append(b, e)
			case 'b':
				b = append(b, '\b')
			case 'f':
				b = append(b, '\f')
			case 'n':
				b = append(b, '\n')
			case 'r':
				b = append(b, '\r')
			case 't':
				b = append(b, '\t')
			case 'u':
				r := p.hex4()
				if r >= 0xD800 && r < 0xDC00 && strings.HasPrefix(s[p.i:], "\\u") {
					save := p.i
					p.i += 2
					r2 := p.hex4()
					if r2 >= 0xDC00 && r2 < 0xE000 {
						r = utf16.DecodeRune(r, r2)
					} else {
						p.i = save
					}
				}
				b = utf8.AppendRune(b, r)
			default:
				p.i--
				p.fail()
			}
		case c < 0x20:
			p.fail()
		default:
			b = append(b, c)
			p.i++
		}
	}
	p.fail()
	return nil
}

func (p *jsonParser) hex4() rune {
	if p.i+4 > len(p.s) {
		p.i = len(p.s)
		p.fail()
	}
	v, err := strconv.ParseUint(p.s[p.i:p.i+4], 16, 32)
	if err != nil {
		p.fail()
	}
	p.i += 4
	return rune(v)
}

type jsonWriter struct {
	vm       *VM
	b        []byte
	indent   string
	replacer *object
	allow    map[string]bool
	allowL   []propKey
	stack    []*object
}

func (vm *VM) jsonStringify(v, replacer Value, indent string) string {
	s, _ := vm.jsonStringifyOK(v, replacer, indent)
	return s
}

func (vm *VM) jsonStringifyOK(v, replacer Value, indent string) (string, bool) {
	w := &jsonWriter{vm: vm, indent: indent}
	if ro, ok := replacer.r.(*object); ok {
		if ro.isCallable() {
			w.replacer = ro
		} else if ro.class == classArray {
			w.allow = map[string]bool{}
			for _, x := range ro.arr {
				var k string
				switch r := x.r.(type) {
				case *jstr:
					k = r.flat()
				case nil:
					k = numberToString(x.n)
				case *object:
					if r.class != classString && r.class != classNumber {
						continue
					}
					k = vm.toString(x)
				default:
					continue
				}
				if !w.allow[k] {
					w.allow[k] = true
					w.allowL = append(w.allowL, strKey(k))
				}
			}
		}
	}
	if w.replacer != nil {
		holder := vm.newObject()
		holder.setOwnStr("", v)
		v = vm.call2(w.replacer, objV(holder), emptyStr, v)
	}
	if !w.value(v, nil, propKey("")) {
		return "", false
	}
	vm.track(len(w.b))
	return string(w.b), true
}

func (w *jsonWriter) prepare(v Value, holder *object, key propKey) Value {
	vm := w.vm
	if o, ok := v.r.(*object); ok {
		if o.class != classArray {
			tj := vm.getProp(o, strKey("toJSON"), v)
			if fo, ok := tj.r.(*object); ok && fo.isCallable() {
				v = vm.callObj(fo, v, []Value{strV(key.str())})
			}
		}
	}
	if w.replacer != nil && holder != nil {
		v = vm.call2(w.replacer, objV(holder), strV(key.str()), v)
	}
	if o, ok := v.r.(*object); ok {
		switch o.class {
		case classNumber:
			return num(vm.toNumber(v))
		case classString:
			return strVal(vm.toStr(v))
		case classBoolean:
			return boolV(o.inner.(bool))
		}
	}
	return v
}

func (w *jsonWriter) value(v Value, holder *object, key propKey) bool {
	if len(w.b) > maxStringLen {
		w.vm.throwError("RangeError", "Invalid string length")
	}
	if cap(w.b)-len(w.b) < 4096 && cap(w.b) > 1<<20 {
		w.vm.reserve(2 * cap(w.b))
	}
	if holder != nil {
		v = w.prepare(v, holder, key)
	} else if o, ok := v.r.(*object); ok && w.replacer == nil {
		if o.class != classArray && o.class != classObject {
			v = w.prepare(v, nil, key)
		} else if o.class == classObject && w.vm.hasProperty(o, strKey("toJSON")) {
			v = w.prepare(v, nil, key)
		}
	}
	switch r := v.r.(type) {
	case nil:
		if math.IsInf(v.n, 0) || v.n != v.n {
			w.b = append(w.b, "null"...)
		} else {
			w.b = appendNumber(w.b, v.n)
		}
	case *jstr:
		if r.n > 1<<20 {
			if len(w.b)+r.size() > maxStringLen {
				w.vm.throwError("RangeError", "Invalid string length")
			}
			w.vm.reserve(len(w.b) + 6*r.size())
		}
		w.b = appendQuoted(w.b, w.vm.flat(r))
		if len(w.b) > maxStringLen {
			w.vm.throwError("RangeError", "Invalid string length")
		}
	case boolT:
		if v.n != 0 {
			w.b = append(w.b, "true"...)
		} else {
			w.b = append(w.b, "false"...)
		}
	case nullT:
		w.b = append(w.b, "null"...)
	case *object:
		if r.isCallable() {
			return false
		}
		for _, s := range w.stack {
			if s == r {
				w.vm.throwTypeError("Converting circular structure to JSON")
			}
		}
		if len(w.stack) > 2000 {
			w.vm.throwError("RangeError", "Maximum call stack size exceeded")
		}
		w.vm.enterNative()
		defer func() { w.vm.depth-- }()
		w.stack = append(w.stack, r)
		if r.class == classArray {
			w.array(r)
		} else {
			w.object(r)
		}
		w.stack = w.stack[:len(w.stack)-1]
	default:
		return false
	}
	return true
}

func (w *jsonWriter) newline(depth int) {
	w.b = append(w.b, '\n')
	for range depth {
		w.b = append(w.b, w.indent...)
	}
}

func (w *jsonWriter) array(a *object) {
	if len(a.arr) == 0 {
		w.b = append(w.b, "[]"...)
		return
	}
	depth := len(w.stack)
	w.b = append(w.b, '[')
	for i := 0; i < len(a.arr); i++ {
		if i > 0 {
			w.b = append(w.b, ',')
		}
		if w.indent != "" {
			w.newline(depth)
		}
		var ok bool
		if w.replacer != nil || isObjectNeedingPrepare(a.arr[i]) {
			ok = w.value(unhole(a.arr[i]), a, strKey(strconv.Itoa(i)))
		} else {
			ok = w.value(a.arr[i], nil, propKey(""))
		}
		if !ok {
			w.b = append(w.b, "null"...)
		}
	}
	if w.indent != "" {
		w.newline(depth - 1)
	}
	w.b = append(w.b, ']')
}

func isObjectNeedingPrepare(v Value) bool {
	o, ok := v.r.(*object)
	return ok && o.class != classArray && o.class != classObject
}

type shapeJSON struct {
	keys   []propKey
	idx    []int
	quoted [][]byte
}

func (s *hclass) jsonInfo() *shapeJSON {
	if s.json != nil {
		return s.json
	}
	info := &shapeJSON{}
	hasIdx := false
	for i, p := range s.props {
		if p.flags&fEnumerable == 0 || p.flags&fDeleted != 0 || p.key.isSym() {
			continue
		}
		if _, ok := arrayIndex(p.key.str()); ok {
			hasIdx = true
		}
		info.keys = append(info.keys, p.key)
		info.idx = append(info.idx, i)
	}
	if hasIdx {
		pairs := make([]propKey, len(info.keys))
		copy(pairs, info.keys)
		orderIndexKeys(pairs)
		pos := map[propKey]int{}
		for i, k := range info.keys {
			pos[k] = info.idx[i]
		}
		info.keys = pairs
		for i, k := range pairs {
			info.idx[i] = pos[k]
		}
	}
	for _, k := range info.keys {
		info.quoted = append(info.quoted, appendQuoted(nil, k.str()))
	}
	if !s.dict {
		s.json = info
	}
	return info
}

func (w *jsonWriter) emit(o *object, depth int, first bool, qk []byte, k propKey, v Value) bool {
	mark := len(w.b)
	if !first {
		w.b = append(w.b, ',')
	}
	if w.indent != "" {
		w.newline(depth)
	}
	if qk != nil {
		w.b = append(w.b, qk...)
	} else {
		w.b = appendQuoted(w.b, k.str())
	}
	w.b = append(w.b, ':')
	if w.indent != "" {
		w.b = append(w.b, ' ')
	}
	var ok bool
	if w.replacer != nil || isObjectNeedingPrepare(v) || hasToJSON(w.vm, v) {
		ok = w.value(v, o, k)
	} else {
		ok = w.value(v, nil, propKey(""))
	}
	if !ok {
		w.b = w.b[:mark]
		return first
	}
	return false
}

func (w *jsonWriter) object(o *object) {
	depth := len(w.stack)
	w.b = append(w.b, '{')
	first := true
	if w.allow != nil {
		for _, k := range w.allowL {
			if w.vm.hasOwn(o, k) {
				first = w.emit(o, depth, first, nil, k, w.vm.getProp(o, k, objV(o)))
			}
		}
	} else if o.class == classObject && !o.shape.dict {
		info := o.shape.jsonInfo()
		for i, k := range info.keys {
			first = w.emit(o, depth, first, info.quoted[i], k, o.slots[info.idx[i]])
		}
	} else {
		for _, k := range o.ownKeys(true) {
			first = w.emit(o, depth, first, nil, k, w.vm.getProp(o, k, objV(o)))
		}
	}
	if !first && w.indent != "" {
		w.newline(depth - 1)
	}
	w.b = append(w.b, '}')
}

func hasToJSON(vm *VM, v Value) bool {
	o, ok := v.r.(*object)
	if !ok || o.class != classObject {
		return false
	}
	if o.shape.proto == vm.objectProto && o.shape.lookupStr("toJSON") < 0 && vm.objectProto.shape.lookupStr("toJSON") < 0 {
		return false
	}
	return vm.hasProperty(o, strKey("toJSON"))
}

const hexDigits = "0123456789abcdef"

func appendQuoted(b []byte, s string) []byte {
	b = append(b, '"')
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 0x20 && c != '"' && c != '\\' {
			continue
		}
		b = append(b, s[start:i]...)
		switch c {
		case '"':
			b = append(b, '\\', '"')
		case '\\':
			b = append(b, '\\', '\\')
		case '\n':
			b = append(b, '\\', 'n')
		case '\r':
			b = append(b, '\\', 'r')
		case '\t':
			b = append(b, '\\', 't')
		case '\b':
			b = append(b, '\\', 'b')
		case '\f':
			b = append(b, '\\', 'f')
		default:
			b = append(b, '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&15])
		}
		start = i + 1
	}
	b = append(b, s[start:]...)
	return append(b, '"')
}
