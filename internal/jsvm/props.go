package jsvm

import (
	"slices"
	"strconv"
	"strings"
)

func (vm *VM) getV(v Value, k propKey) Value {
	switch r := v.r.(type) {
	case *object:
		return vm.getProp(r, k, v)
	case *jstr:
		if !k.isSym() {
			if k.str() == "length" {
				return intV(r.length())
			}
			if i, ok := arrayIndex(k.str()); ok {
				if i < r.length() {
					return r.substr(i, i+1)
				}
				return undefined
			}
		}
		return vm.getProp(vm.stringProto, k, v)
	case nil:
		return vm.getProp(vm.numberProto, k, v)
	case boolT:
		return vm.getProp(vm.boolProto, k, v)
	case *symbol:
		if k.str() == "description" {
			return strV(r.desc)
		}
		return vm.getProp(vm.symbolProto, k, v)
	}
	vm.throwTypeError("Cannot read properties of " + ToString(v) + " (reading '" + k.String() + "')")
	return undefined
}

func (vm *VM) getIndex(v Value, kv Value) Value {
	if kv.r == nil {
		i := int(kv.n)
		if float64(i) == kv.n && i >= 0 {
			switch r := v.r.(type) {
			case *object:
				if r.class == classArray {
					if i < len(r.arr) && !r.arr[i].isHole() {
						return r.arr[i]
					}
					return vm.getProp(r, vm.toPropertyKey(kv), v)
				}
			case *jstr:
				if r.isASCII() {
					s := r.flat()
					if i < len(s) {
						return Value{r: asciiChars[s[i]]}
					}
					return undefined
				}
				if i < r.length() {
					return r.substr(i, i+1)
				}
				return undefined
			}
		}
	}
	if v.isNullish() {
		vm.throwTypeError("Cannot read properties of " + ToString(v) + " (reading '" + vm.toString(kv) + "')")
	}
	return vm.getV(v, vm.toPropertyKey(kv))
}

func (vm *VM) getProp(o *object, k propKey, recv Value) Value {
	for p := o; p != nil; p = p.shape.proto {
		if i := p.shape.lookup(k); i >= 0 {
			return p.slots[i]
		}
		if p.class != classObject {
			if v, ok := vm.getExotic(p, k); ok {
				return v
			}
		}
	}
	return undefined
}

func (vm *VM) getCached(o *object, k propKey, ic *getIC, recv Value) Value {
	s := o.shape
	if i := s.lookup(k); i >= 0 {
		if !s.dict && o.class != classGlobal {
			ic.shape, ic.idx, ic.holder, ic.hshape = s, i, nil, nil
		}
		return o.slots[i]
	}
	if o.class != classObject {
		if v, ok := vm.getExotic(o, k); ok {
			return v
		}
	}
	p := s.proto
	if p == nil {
		return undefined
	}
	if i := p.shape.lookup(k); i >= 0 {
		if !s.dict && !p.shape.dict && o.class != classGlobal {
			ic.shape, ic.idx, ic.holder, ic.hshape = s, i, p, p.shape
		}
		return p.slots[i]
	}
	return vm.getProp(p, k, recv)
}

func (vm *VM) getExotic(o *object, k propKey) (Value, bool) {
	if k.isSym() {
		return undefined, false
	}
	switch o.class {
	case classArray:
		if k.str() == "length" {
			return intV(len(o.arr)), true
		}
		if i, ok := arrayIndex(k.str()); ok {
			if i < len(o.arr) && !o.arr[i].isHole() {
				return o.arr[i], true
			}
			return undefined, false
		}
	case classFunction:
		return vm.functionProp(o, k.str())
	case classGlobal:
		if b := o.inner.(*globalStore).lookup(k.str()); b != nil {
			return b.v, true
		}
	case classString:
		s := o.inner.(*jstr)
		if k.str() == "length" {
			return intV(s.length()), true
		}
		if i, ok := arrayIndex(k.str()); ok && i < s.length() {
			return s.substr(i, i+1), true
		}
	case classMap, classSet:
		if k.str() == "size" {
			if md, ok := o.inner.(*mapData); ok {
				return intV(md.size), true
			}
		}
	case classRegExp:
		return vm.regexpProp(o, k.str())
	case classError:
		if k == "stack" {
			return vm.errorStack(o)
		}
	}
	return undefined, false
}

func (vm *VM) functionProp(o *object, name string) (Value, bool) {
	switch name {
	case "name":
		switch fn := o.inner.(type) {
		case *closure:
			return strV(fn.code.name), true
		case *nativeFn:
			return strV(fn.name), true
		case *boundFn:
			k, t := fn.levels, fn.target
			n, _ := vm.functionProp(t, "name")
			if k*6 > maxStringLen {
				vm.throwError("RangeError", "Invalid string length")
			}
			return strV(strings.Repeat("bound ", k) + ToString(n)), true
		}
	case "length":
		switch fn := o.inner.(type) {
		case *closure:
			return intV(fn.code.length), true
		case *nativeFn:
			return intV(fn.length), true
		case *boundFn:
			return intV(0), true
		}
	case "prototype":
		cl, ok := o.inner.(*closure)
		if !ok || cl.code.arrow || cl.code.async || cl.code.method {
			return undefined, false
		}
		p := vm.newObject()
		p.defineHidden("constructor", objV(o))
		o.addProp(strKey("prototype"), objV(p), fWritable)
		return objV(p), true
	}
	return undefined, false
}

func (vm *VM) setV(v Value, k propKey, val Value) {
	switch r := v.r.(type) {
	case *object:
		vm.setProp(r, k, val)
		return
	case undefT, nullT:
		vm.throwTypeError("Cannot set properties of " + ToString(v) + " (setting '" + k.String() + "')")
	}
	vm.throwTypeError("Cannot create property '" + k.String() + "' on " + typeOf(v) + " '" + ToString(v) + "'")
}

func (vm *VM) setProp(o *object, k propKey, v Value) {
	if i := o.shape.lookup(k); i >= 0 {
		if o.shape.props[i].flags&fWritable == 0 {
			vm.throwTypeError("Cannot assign to read only property '" + k.String() + "' of object")
		}
		o.slots[i] = v
		return
	}
	if o.class != classObject && vm.setExotic(o, k, v) {
		return
	}
	if o.noExt {
		vm.throwTypeError("Cannot add property " + k.String() + ", object is not extensible")
	}
	o.addProp(k, v, fDefault)
	vm.track(32)
}

func (vm *VM) setCached(o *object, k propKey, v Value, ic *setIC) {
	s := o.shape
	if i := s.lookup(k); i >= 0 {
		if s.props[i].flags&fWritable == 0 {
			vm.throwTypeError("Cannot assign to read only property '" + k.String() + "' of object")
		}
		o.slots[i] = v
		if !s.dict {
			ic.shape, ic.idx = s, i
		}
		return
	}
	if o.class != classObject || o.noExt {
		vm.setProp(o, k, v)
		return
	}
	o.addProp(k, v, fDefault)
	vm.track(32)
	if !s.dict && !o.shape.dict && len(o.slots) == len(o.shape.props) {
		ic.from, ic.to = s, o.shape
	}
}

func (vm *VM) setExotic(o *object, k propKey, v Value) bool {
	if k.isSym() {
		return false
	}
	switch o.class {
	case classArray:
		if k.str() == "length" {
			vm.setArrayLength(o, v)
			return true
		}
		if i, ok := arrayIndex(k.str()); ok {
			vm.setArrayIndex(o, i, v)
			return true
		}
	case classGlobal:
		g := o.inner.(*globalStore)
		b := g.bind(k.str())
		b.v, b.present = v, true
		return true
	}
	return false
}

const maxArrayGap = 1 << 24

func (vm *VM) setArrayIndex(o *object, i int, v Value) {
	if o.noExt {
		if i < len(o.arr) && !o.frozen() {
			o.arr[i] = v
			return
		}
		vm.throwTypeError("Cannot assign to read only property '" + strconv.Itoa(i) + "' of object")
	}
	if i < len(o.arr) {
		o.arr[i] = v
		return
	}
	if i-len(o.arr) > maxArrayGap {
		vm.throwError("RangeError", "unsupported: sparse array index "+strconv.Itoa(i))
	}
	vm.reserveValues(i + 1 - len(o.arr))
	vm.track((i + 1 - len(o.arr)) * 24)
	o.holes = o.holes || len(o.arr) < i
	for len(o.arr) < i {
		o.arr = append(o.arr, holeV)
	}
	o.arr = append(o.arr, v)
}

func (vm *VM) setArrayLength(o *object, v Value) {
	f := vm.toNumber(v)
	n := int(f)
	if float64(n) != f || n < 0 {
		vm.throwError("RangeError", "Invalid array length")
	}
	if o.noExt {
		vm.throwTypeError("Cannot assign to read only property 'length' of object")
	}
	if n <= len(o.arr) {
		clear(o.arr[n:])
		o.arr = o.arr[:n]
		return
	}
	if n-len(o.arr) > maxArrayGap {
		vm.throwError("RangeError", "unsupported: sparse array length "+strconv.Itoa(n))
	}
	vm.reserveValues(n - len(o.arr))
	vm.track((n - len(o.arr)) * 24)
	o.holes = true
	for len(o.arr) < n {
		o.arr = append(o.arr, holeV)
	}
}

func (o *object) frozen() bool { return o.isFrozen }

func (vm *VM) defineOwn(o *object, k propKey, v Value) {
	if i := o.shape.lookup(k); i >= 0 {
		o.slots[i] = v
		return
	}
	if o.class == classArray && !k.isSym() {
		if i, ok := arrayIndex(k.str()); ok {
			vm.setArrayIndex(o, i, v)
			return
		}
	}
	if o.class == classGlobal && vm.setExotic(o, k, v) {
		return
	}
	o.addProp(k, v, fDefault)
	vm.track(32)
}

func (vm *VM) deleteProp(v Value, k propKey) bool {
	o, ok := v.r.(*object)
	if !ok {
		if v.isNullish() {
			vm.throwTypeError("Cannot convert undefined or null to object")
		}
		return true
	}
	if o.noExt && o.frozen() {
		vm.throwTypeError("Cannot delete property '" + k.String() + "' of object")
	}
	if o.class == classArray && !k.isSym() {
		if i, ok := arrayIndex(k.str()); ok {
			if i < len(o.arr) {
				o.arr[i] = holeV
				o.holes = true
			}
			return true
		}
	}
	if o.class == classGlobal && !k.isSym() {
		if b := o.inner.(*globalStore).lookup(k.str()); b != nil {
			b.present, b.v, b.konst = false, undefined, false
			return true
		}
	}
	if !o.deleteOwn(k) {
		vm.throwTypeError("Cannot delete property '" + k.String() + "' of object")
	}
	return true
}

func (vm *VM) hasOwn(o *object, k propKey) bool {
	if o.shape.lookup(k) >= 0 {
		return true
	}
	if o.class != classObject {
		if o.class == classFunction && k.str() == "prototype" {
			_, ok := vm.functionProp(o, "prototype")
			return ok
		}
		if o.class == classArray && !k.isSym() {
			if k.str() == "length" {
				return true
			}
			if i, ok := arrayIndex(k.str()); ok {
				return i < len(o.arr) && !o.arr[i].isHole()
			}
			return false
		}
		_, ok := vm.getExotic(o, k)
		return ok
	}
	return false
}

func (vm *VM) hasProperty(o *object, k propKey) bool {
	for p := o; p != nil; p = p.proto() {
		if vm.hasOwn(p, k) {
			return true
		}
	}
	return false
}

func (vm *VM) copyDataProps(dst *object, src Value, exclude []propKey) {
	switch r := src.r.(type) {
	case *object:
		if r.class == classObject && !r.shape.dict && len(exclude) == 0 && len(dst.shape.props) == 0 && dst.shape.proto == r.shape.proto && allDefault(r.shape) {
			dst.shape = r.shape
			dst.slots = append([]Value(nil), r.slots...)
			return
		}
		for _, k := range r.ownKeys(true) {
			if containsKey(exclude, k) {
				continue
			}
			vm.defineOwn(dst, k, vm.getProp(r, k, src))
		}
	case *jstr:
		n := r.length()
		for i := range n {
			k := strKey(strconv.Itoa(i))
			if !containsKey(exclude, k) {
				vm.defineOwn(dst, k, r.substr(i, i+1))
			}
		}
	}
}

func allDefault(s *hclass) bool {
	for _, p := range s.props {
		if p.flags != fDefault {
			return false
		}
	}
	return true
}

func containsKey(keys []propKey, k propKey) bool {
	return slices.Contains(keys, k)
}

func (vm *VM) forInKeys(o *object) []propKey {
	keys := vm.ownKeys(o, true)
	var seen map[propKey]bool
	for p := o.proto(); p != nil; p = p.proto() {
		extra := p.ownKeys(true)
		if len(extra) == 0 {
			continue
		}
		if seen == nil {
			seen = map[propKey]bool{}
			for _, k := range keys {
				seen[k] = true
			}
		}
		for _, k := range extra {
			if !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
	}
	return keys
}
