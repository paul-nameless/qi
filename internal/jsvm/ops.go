package jsvm

import (
	"math"
	"strconv"
	"strings"
)

func (vm *VM) toPrimitive(v Value, hint string) Value {
	o, ok := v.r.(*object)
	if !ok {
		return v
	}
	if hint == "" {
		hint = "number"
		if o.class == classDate {
			hint = "string"
		}
	}
	order := [2]string{"valueOf", "toString"}
	if hint == "string" {
		order = [2]string{"toString", "valueOf"}
	}
	for _, name := range order {
		fn := vm.getProp(o, strKey(name), v)
		if fo, ok := fn.r.(*object); ok && fo.isCallable() {
			r := vm.callObj(fo, v, nil)
			if _, isObj := r.r.(*object); !isObj {
				return r
			}
		}
	}
	vm.throwTypeError("Cannot convert object to primitive value")
	return undefined
}

func (vm *VM) toNumber(v Value) float64 {
	switch v.r.(type) {
	case nil:
		return v.n
	case *object:
		return vm.toNumber(vm.toPrimitive(v, "number"))
	case *symbol:
		vm.throwTypeError("Cannot convert a Symbol value to a number")
	}
	return toNumberNoCall(v)
}

func (vm *VM) toNumeric(v Value) float64 {
	if v.r == nil {
		return v.n
	}
	return vm.toNumber(v)
}

func (vm *VM) toString(v Value) string {
	switch r := v.r.(type) {
	case *jstr:
		return vm.flat(r)
	case *object:
		return vm.toString(vm.toPrimitive(v, "string"))
	case *symbol:
		vm.throwTypeError("Cannot convert a Symbol value to a string")
	}
	return ToString(v)
}

func (vm *VM) toStr(v Value) *jstr {
	switch r := v.r.(type) {
	case *jstr:
		return r
	case nil:
		if v.n >= 0 && v.n < 10 && v.n == math.Trunc(v.n) {
			return asciiChars['0'+int(v.n)]
		}
		return newASCII(numberToString(v.n))
	case *object:
		return vm.toStr(vm.toPrimitive(v, "string"))
	}
	return newString(vm.toString(v))
}

func (vm *VM) add(a, b Value) Value {
	if a.r == nil && b.r == nil {
		return Value{n: a.n + b.n}
	}
	as, aok := a.r.(*jstr)
	bs, bok := b.r.(*jstr)
	if aok && bok {
		return Value{r: vm.concat(as, bs)}
	}
	if aok && b.r == nil {
		return Value{r: vm.concatNum(as, b.n, false)}
	}
	if bok && a.r == nil {
		return Value{r: vm.concatNum(bs, a.n, true)}
	}
	if _, ok := a.r.(*object); ok {
		a = vm.toPrimitive(a, "")
	}
	if _, ok := b.r.(*object); ok {
		b = vm.toPrimitive(b, "")
	}
	_, aok = a.r.(*jstr)
	_, bok = b.r.(*jstr)
	if aok || bok {
		return Value{r: vm.concat(vm.toStr(a), vm.toStr(b))}
	}
	return num(vm.toNumber(a) + vm.toNumber(b))
}

const maxStringLen = 1<<29 - 24

func (vm *VM) concatNum(s *jstr, f float64, numFirst bool) *jstr {
	var nb [32]byte
	d := appendNumber(nb[:0], f)
	n := s.n + len(d)
	if n >= ropeMin {
		ds := newASCII(string(d))
		if numFirst {
			return vm.concat(ds, s)
		}
		return vm.concat(s, ds)
	}
	s.scan()
	var b strings.Builder
	b.Grow(n)
	if numFirst {
		b.Write(d)
		b.WriteString(s.flat())
	} else {
		b.WriteString(s.flat())
		b.Write(d)
	}
	vm.track(n + 48)
	return &jstr{s: b.String(), n: n, info: strKnown | s.info&(strASCII|strAstral)}
}

func (vm *VM) concat(a, b *jstr) *jstr {
	if a.n+b.n > maxStringLen {
		vm.throwError("RangeError", "Invalid string length")
	}
	vm.track(b.n + 64)
	if a.n+b.n >= 1<<26 {
		vm.reserve(a.n + b.n)
	}
	return concatStrings(a, b)
}

func (vm *VM) relational(op string, a, b Value) bool {
	if a.r != nil || b.r != nil {
		a = vm.toPrimitive(a, "number")
		b = vm.toPrimitive(b, "number")
		as, aok := a.r.(*jstr)
		bs, bok := b.r.(*jstr)
		if aok && bok {
			c := compareStrings(as, bs)
			switch op {
			case "<":
				return c < 0
			case "<=":
				return c <= 0
			case ">":
				return c > 0
			}
			return c >= 0
		}
	}
	x, y := vm.toNumber(a), vm.toNumber(b)
	switch op {
	case "<":
		return x < y
	case "<=":
		return x <= y
	case ">":
		return x > y
	}
	return x >= y
}

func (vm *VM) looseEquals(a, b Value) bool {
	for {
		switch ra := a.r.(type) {
		case nil:
			switch b.r.(type) {
			case nil:
				return a.n == b.n
			case *jstr, boolT:
				return a.n == toNumberNoCall(b)
			case *object:
				b = vm.toPrimitive(b, "")
				continue
			}
			return false
		case undefT, nullT:
			return b.isNullish()
		case *jstr:
			switch rb := b.r.(type) {
			case *jstr:
				return ra.flat() == rb.flat()
			case nil, boolT:
				return toNumberNoCall(a) == toNumberNoCall(b)
			case *object:
				b = vm.toPrimitive(b, "")
				continue
			}
			return false
		case boolT:
			a = num(a.n)
			continue
		case *object:
			switch b.r.(type) {
			case *object:
				return a.r == b.r
			case undefT, nullT:
				return false
			}
			a = vm.toPrimitive(a, "")
			continue
		case *symbol:
			return a.r == b.r
		}
		return false
	}
}

func (vm *VM) toPropertyKey(v Value) propKey {
	switch r := v.r.(type) {
	case *jstr:
		return propKey(r.flat())
	case nil:
		if v.n >= 0 && v.n < 1e9 && v.n == math.Trunc(v.n) {
			return propKey(strconv.Itoa(int(v.n)))
		}
		return propKey(numberToString(v.n))
	case *symbol:
		return symKey(r)
	case *object:
		return vm.toPropertyKey(vm.toPrimitive(v, "string"))
	}
	return propKey(ToString(v))
}

func (vm *VM) instanceOf(v, ctor Value) bool {
	co, ok := ctor.r.(*object)
	if !ok || !co.isCallable() {
		vm.throwTypeError("Right-hand side of 'instanceof' is not callable")
	}
	for {
		b, ok := co.inner.(*boundFn)
		if !ok {
			break
		}
		co = b.target
		ctor = objV(co)
	}
	o, ok := v.r.(*object)
	if !ok {
		return false
	}
	pv := vm.getV(ctor, strKey("prototype"))
	proto, ok := pv.r.(*object)
	if !ok {
		vm.throwTypeError("Function has non-object prototype in instanceof check")
	}
	for p := o.proto(); p != nil; p = p.proto() {
		if p == proto {
			return true
		}
	}
	return false
}

func (vm *VM) toObject(v Value) *object {
	switch r := v.r.(type) {
	case *object:
		return r
	case *jstr:
		return &object{shape: vm.stringProto.rootShape(), class: classString, inner: r}
	case nil:
		return &object{shape: vm.numberProto.rootShape(), class: classNumber, inner: v.n}
	case boolT:
		return &object{shape: vm.boolProto.rootShape(), class: classBoolean, inner: v.n != 0}
	}
	vm.throwTypeError("Cannot convert undefined or null to object")
	return nil
}

func (vm *VM) toLength(v Value) int {
	f := toIntegerOrInf(vm.toNumber(v))
	if f <= 0 {
		return 0
	}
	if f > 1<<53-1 {
		f = 1<<53 - 1
	}
	if f >= float64(math.MaxInt) {
		return math.MaxInt
	}
	return int(f)
}

func relIndex(v Value, vm *VM, length int, def int) int {
	if v.isUndef() {
		return def
	}
	f := toIntegerOrInf(vm.toNumber(v))
	if f < 0 {
		f += float64(length)
		if f < 0 {
			return 0
		}
		return int(f)
	}
	if f > float64(length) {
		return length
	}
	return int(f)
}
