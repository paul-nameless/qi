package jsvm

import (
	"math"
	"slices"
)

func (vm *VM) thisArr(this Value, name string) *object {
	o, ok := this.r.(*object)
	if !ok || o.class != classArray {
		if ok && o.class == classObject {
			vm.throwTypeError("unsupported: Array.prototype." + name + " on a non-array object")
		}
		vm.throwTypeError("Array.prototype." + name + " called on " + typeOf(this))
	}
	return o
}

func (vm *VM) mutableArr(this Value, name string) *object {
	a := vm.thisArr(this, name)
	if a.noExt {
		vm.throwTypeError("Cannot modify a non-extensible array with Array.prototype." + name)
	}
	return a
}

func (vm *VM) callback(v Value) *object {
	o, ok := v.r.(*object)
	if !ok || !o.isCallable() {
		vm.throwTypeError(vm.safeString(v) + " is not a function")
	}
	return o
}

func (vm *VM) call3(fn *object, this Value, a, b, c Value) Value {
	sp := vm.sp
	args := vm.alloc(3)
	args[0], args[1], args[2] = a, b, c
	r := vm.callObj(fn, this, args)
	vm.sp = sp
	return r
}

func (vm *VM) call2(fn *object, this Value, a, b Value) Value {
	sp := vm.sp
	args := vm.alloc(2)
	args[0], args[1] = a, b
	r := vm.callObj(fn, this, args)
	vm.sp = sp
	return r
}

func (vm *VM) setupArray() {
	p := vm.arrayProto
	ctor := vm.newCtor("Array", 1, p, func(vm *VM, this Value, args []Value) Value {
		return vm.arrayCtor(args)
	}, func(vm *VM, args []Value, nt *object) Value {
		return vm.arrayCtor(args)
	})
	vm.method(ctor, "isArray", 1, func(vm *VM, this Value, args []Value) Value {
		o, ok := arg(args, 0).r.(*object)
		return boolV(ok && o.class == classArray)
	})
	vm.method(ctor, "of", 0, func(vm *VM, this Value, args []Value) Value {
		return vm.newArrayV(append([]Value(nil), args...))
	})
	vm.method(ctor, "from", 1, func(vm *VM, this Value, args []Value) Value {
		src := arg(args, 0)
		var fn *object
		if m := arg(args, 1); !m.isUndef() {
			fn = vm.callback(m)
		}
		var vals []Value
		if vm.isIterable(src) {
			vals = append([]Value(nil), vm.collect(src)...)
		} else if o, ok := src.r.(*object); ok {
			vals = vm.arrayLike(objV(o))
		} else if src.isNullish() {
			vm.throwTypeError(ToString(src) + " is not iterable")
		}
		if fn != nil {
			thisArg := arg(args, 2)
			for i, v := range vals {
				vals[i] = vm.call2(fn, thisArg, v, intV(i))
			}
		}
		if vals == nil {
			vals = []Value{}
		}
		return vm.newArrayV(vals)
	})
	vm.method(p, "push", 1, func(vm *VM, this Value, args []Value) Value {
		a := vm.thisArr(this, "push")
		if a.noExt {
			vm.throwTypeError("Cannot add property " + numberToString(float64(len(a.arr))) + ", object is not extensible")
		}
		if len(a.arr)+len(args) > cap(a.arr) {
			vm.reserveValues(2 * (len(a.arr) + len(args)))
		}
		a.arr = append(a.arr, args...)
		vm.track(len(args) * 24)
		return intV(len(a.arr))
	})
	vm.method(p, "pop", 0, func(vm *VM, this Value, args []Value) Value {
		a := vm.mutableArr(this, "pop")
		if len(a.arr) == 0 {
			return undefined
		}
		v := a.arr[len(a.arr)-1]
		a.arr[len(a.arr)-1] = Value{}
		a.arr = a.arr[:len(a.arr)-1]
		return unhole(v)
	})
	vm.method(p, "shift", 0, func(vm *VM, this Value, args []Value) Value {
		a := vm.mutableArr(this, "shift")
		if len(a.arr) == 0 {
			return undefined
		}
		v := a.arr[0]
		a.arr[0] = Value{}
		a.arr = a.arr[1:]
		return unhole(v)
	})
	vm.method(p, "unshift", 1, func(vm *VM, this Value, args []Value) Value {
		a := vm.mutableArr(this, "unshift")
		a.arr = append(append(make([]Value, 0, len(a.arr)+len(args)), args...), a.arr...)
		return intV(len(a.arr))
	})
	vm.method(p, "slice", 2, func(vm *VM, this Value, args []Value) Value {
		a := vm.thisArr(this, "slice")
		n := len(a.arr)
		s, e := relIndex(arg(args, 0), vm, n, 0), relIndex(arg(args, 1), vm, n, n)
		e = min(e, len(a.arr))
		s = min(s, e)
		vm.reserveValues(e - s)
		return vm.holey(append([]Value(nil), a.arr[s:e]...), a.holes)
	})
	vm.method(p, "splice", 2, func(vm *VM, this Value, args []Value) Value {
		a := vm.mutableArr(this, "splice")
		removed := vm.splice(a.arr, args, &a.arr)
		return vm.holey(removed, a.holes)
	})
	vm.method(p, "toSpliced", 2, func(vm *VM, this Value, args []Value) Value {
		a := vm.thisArr(this, "toSpliced")
		var out []Value
		vm.splice(dense(a), args, &out)
		return vm.newArrayV(out)
	})
	vm.method(p, "concat", 1, func(vm *VM, this Value, args []Value) Value {
		a := vm.thisArr(this, "concat")
		total := len(a.arr)
		for _, x := range args {
			if o, ok := x.r.(*object); ok && o.class == classArray {
				total += len(o.arr)
			} else {
				total++
			}
		}
		vm.reserveValues(total)
		out := append(make([]Value, 0, total), a.arr...)
		holes := a.holes
		for _, x := range args {
			if o, ok := x.r.(*object); ok && o.class == classArray {
				out = append(out, o.arr...)
				holes = holes || o.holes
			} else {
				out = append(out, x)
			}
		}
		return vm.holey(out, holes)
	})
	vm.method(p, "join", 1, func(vm *VM, this Value, args []Value) Value {
		a := vm.thisArr(this, "join")
		sep := ","
		if s := arg(args, 0); !s.isUndef() {
			sep = vm.toString(s)
		}
		return strV(vm.join(a, sep))
	})
	vm.method(p, "toString", 0, func(vm *VM, this Value, args []Value) Value {
		o, ok := this.r.(*object)
		if !ok || o.class != classArray {
			return strV(objectToString(this))
		}
		return strV(vm.join(o, ","))
	})
	vm.method(p, "toLocaleString", 0, func(vm *VM, this Value, args []Value) Value {
		a := vm.thisArr(this, "toLocaleString")
		b := sbuf{vm: vm}
		for i := 0; i < len(a.arr); i++ {
			if i > 0 {
				b.add(",")
			}
			if v := unhole(a.arr[i]); !v.isNullish() {
				b.add(vm.toString(vm.invoke(v, "toLocaleString")))
			}
		}
		return b.value()
	})
	vm.method(p, "reverse", 0, func(vm *VM, this Value, args []Value) Value {
		a := vm.mutableArr(this, "reverse")
		slices.Reverse(a.arr)
		return this
	})
	vm.method(p, "toReversed", 0, func(vm *VM, this Value, args []Value) Value {
		a := vm.thisArr(this, "toReversed")
		out := append([]Value(nil), dense(a)...)
		slices.Reverse(out)
		return vm.newArrayV(out)
	})
	vm.method(p, "indexOf", 1, func(vm *VM, this Value, args []Value) Value {
		a := vm.thisArr(this, "indexOf")
		x := arg(args, 0)
		for i := relIndex(arg(args, 1), vm, len(a.arr), 0); i < len(a.arr); i++ {
			if strictEquals(a.arr[i], x) {
				return intV(i)
			}
		}
		return intV(-1)
	})
	vm.method(p, "lastIndexOf", 1, func(vm *VM, this Value, args []Value) Value {
		a := vm.thisArr(this, "lastIndexOf")
		x := arg(args, 0)
		start := len(a.arr) - 1
		if len(args) > 1 {
			start = relIndex(args[1], vm, len(a.arr), 0)
			if f := vm.toNumber(args[1]); f < 0 && -f > float64(len(a.arr)) {
				return intV(-1)
			}
			if start >= len(a.arr) {
				start = len(a.arr) - 1
			}
		}
		start = min(start, len(a.arr)-1)
		for i := start; i >= 0; i-- {
			if strictEquals(a.arr[i], x) {
				return intV(i)
			}
		}
		return intV(-1)
	})
	vm.method(p, "includes", 1, func(vm *VM, this Value, args []Value) Value {
		a := vm.thisArr(this, "includes")
		x := arg(args, 0)
		for i := relIndex(arg(args, 1), vm, len(a.arr), 0); i < len(a.arr); i++ {
			if sameValueZero(unhole(a.arr[i]), x) {
				return vTrue
			}
		}
		return vFalse
	})
	vm.method(p, "at", 1, func(vm *VM, this Value, args []Value) Value {
		a := vm.thisArr(this, "at")
		i := int(toIntegerOrInf(vm.toNumber(arg(args, 0))))
		if i < 0 {
			i += len(a.arr)
		}
		if i < 0 || i >= len(a.arr) {
			return undefined
		}
		return unhole(a.arr[i])
	})
	vm.method(p, "with", 2, func(vm *VM, this Value, args []Value) Value {
		a := vm.thisArr(this, "with")
		i := int(toIntegerOrInf(vm.toNumber(arg(args, 0))))
		if i < 0 {
			i += len(a.arr)
		}
		if i < 0 || i >= len(a.arr) {
			vm.throwError("RangeError", "Invalid index : "+ToString(arg(args, 0)))
		}
		out := append([]Value(nil), dense(a)...)
		out[i] = arg(args, 1)
		return vm.newArrayV(out)
	})
	vm.method(p, "fill", 1, func(vm *VM, this Value, args []Value) Value {
		a := vm.mutableArr(this, "fill")
		n := len(a.arr)
		s, e := relIndex(arg(args, 1), vm, n, 0), relIndex(arg(args, 2), vm, n, n)
		v := arg(args, 0)
		for i := s; i < e && i < len(a.arr); i++ {
			a.arr[i] = v
		}
		return this
	})
	vm.method(p, "copyWithin", 2, func(vm *VM, this Value, args []Value) Value {
		a := vm.thisArr(this, "copyWithin")
		n := len(a.arr)
		to := relIndex(arg(args, 0), vm, n, 0)
		from := relIndex(arg(args, 1), vm, n, 0)
		end := relIndex(arg(args, 2), vm, n, n)
		if a.noExt && a.isFrozen {
			vm.throwTypeError("Cannot assign to read only property '0' of object")
		}
		n = len(a.arr)
		count := min(end-from, n-to)
		if count > 0 && from < n && to < n {
			copy(a.arr[to:to+count], a.arr[from:min(from+count, n)])
		}
		return this
	})
	vm.method(p, "keys", 0, func(vm *VM, this Value, args []Value) Value {
		return vm.newIter(&iterState{arr: vm.thisArr(this, "keys"), mode: iterKeys})
	})
	vm.method(p, "values", 0, func(vm *VM, this Value, args []Value) Value {
		return vm.newIter(&iterState{arr: vm.thisArr(this, "values"), mode: iterValues})
	})
	vm.method(p, "entries", 0, func(vm *VM, this Value, args []Value) Value {
		return vm.newIter(&iterState{arr: vm.thisArr(this, "entries"), mode: iterEntries})
	})
	vm.method(p, "forEach", 1, func(vm *VM, this Value, args []Value) Value {
		a := vm.thisArr(this, "forEach")
		fn := vm.callback(arg(args, 0))
		t := arg(args, 1)
		for i, n := 0, len(a.arr); i < n && i < len(a.arr); i++ {
			if a.holes && a.arr[i].isHole() {
				continue
			}
			vm.call3(fn, t, a.arr[i], intV(i), this)
		}
		return undefined
	})
	vm.method(p, "map", 1, func(vm *VM, this Value, args []Value) Value {
		a := vm.thisArr(this, "map")
		fn := vm.callback(arg(args, 0))
		t := arg(args, 1)
		n := len(a.arr)
		vm.reserveValues(n)
		out := make([]Value, n)
		for i := range n {
			if i >= len(a.arr) || (a.holes && a.arr[i].isHole()) {
				out[i] = holeV
				continue
			}
			out[i] = vm.call3(fn, t, a.arr[i], intV(i), this)
		}
		return vm.holey(out, a.holes || len(a.arr) < n)
	})
	vm.method(p, "filter", 1, func(vm *VM, this Value, args []Value) Value {
		a := vm.thisArr(this, "filter")
		fn := vm.callback(arg(args, 0))
		t := arg(args, 1)
		out := []Value{}
		for i, n := 0, len(a.arr); i < n && i < len(a.arr); i++ {
			v := a.arr[i]
			if a.holes && v.isHole() {
				continue
			}
			if toBool(vm.call3(fn, t, v, intV(i), this)) {
				out = vm.appendV(out, v)
			}
		}
		return vm.newArrayV(out)
	})
	find := func(name string, last, index bool) {
		vm.method(p, name, 1, func(vm *VM, this Value, args []Value) Value {
			a := vm.thisArr(this, name)
			fn := vm.callback(arg(args, 0))
			t := arg(args, 1)
			n := len(a.arr)
			for k := range n {
				i := k
				if last {
					i = n - 1 - k
				}
				v := undefined
				if i < len(a.arr) {
					v = unhole(a.arr[i])
				}
				if toBool(vm.call3(fn, t, v, intV(i), this)) {
					if index {
						return intV(i)
					}
					return v
				}
			}
			if index {
				return intV(-1)
			}
			return undefined
		})
	}
	find("find", false, false)
	find("findIndex", false, true)
	find("findLast", true, false)
	find("findLastIndex", true, true)
	vm.method(p, "some", 1, func(vm *VM, this Value, args []Value) Value {
		a := vm.thisArr(this, "some")
		fn := vm.callback(arg(args, 0))
		for i, n := 0, len(a.arr); i < n && i < len(a.arr); i++ {
			if a.holes && a.arr[i].isHole() {
				continue
			}
			if toBool(vm.call3(fn, arg(args, 1), a.arr[i], intV(i), this)) {
				return vTrue
			}
		}
		return vFalse
	})
	vm.method(p, "every", 1, func(vm *VM, this Value, args []Value) Value {
		a := vm.thisArr(this, "every")
		fn := vm.callback(arg(args, 0))
		for i, n := 0, len(a.arr); i < n && i < len(a.arr); i++ {
			if a.holes && a.arr[i].isHole() {
				continue
			}
			if !toBool(vm.call3(fn, arg(args, 1), a.arr[i], intV(i), this)) {
				return vFalse
			}
		}
		return vTrue
	})
	reduce := func(name string, right bool) {
		vm.method(p, name, 1, func(vm *VM, this Value, args []Value) Value {
			a := vm.thisArr(this, name)
			fn := vm.callback(arg(args, 0))
			n := len(a.arr)
			k := 0
			var acc Value
			if len(args) > 1 {
				acc = args[1]
			} else {
				for k < n && a.arr[idx(right, n, k)].isHole() {
					k++
				}
				if k >= n {
					vm.throwTypeError("Reduce of empty array with no initial value")
				}
				acc = a.arr[idx(right, n, k)]
				k++
			}
			for ; k < n; k++ {
				i := idx(right, n, k)
				if i >= len(a.arr) || (a.holes && a.arr[i].isHole()) {
					continue
				}
				sp := vm.sp
				cargs := vm.alloc(4)
				cargs[0], cargs[1], cargs[2], cargs[3] = acc, a.arr[i], intV(i), this
				acc = vm.callObj(fn, undefined, cargs)
				vm.sp = sp
			}
			return acc
		})
	}
	reduce("reduce", false)
	reduce("reduceRight", true)
	vm.method(p, "sort", 1, func(vm *VM, this Value, args []Value) Value {
		a := vm.mutableArr(this, "sort")
		sorted := vm.sortValues(append([]Value(nil), a.arr...), arg(args, 0))
		copy(a.arr, sorted)
		return this
	})
	vm.method(p, "toSorted", 1, func(vm *VM, this Value, args []Value) Value {
		a := vm.thisArr(this, "toSorted")
		out := append([]Value(nil), dense(a)...)
		vm.sortValues(out, arg(args, 0))
		return vm.newArrayV(out)
	})
	vm.method(p, "flat", 0, func(vm *VM, this Value, args []Value) Value {
		a := vm.thisArr(this, "flat")
		depth := 1.0
		if d := arg(args, 0); !d.isUndef() {
			depth = toIntegerOrInf(vm.toNumber(d))
		}
		return vm.newArrayV(vm.flatten(nil, a.arr, depth, 0))
	})
	vm.method(p, "flatMap", 1, func(vm *VM, this Value, args []Value) Value {
		a := vm.thisArr(this, "flatMap")
		fn := vm.callback(arg(args, 0))
		out := []Value{}
		for i, n := 0, len(a.arr); i < n && i < len(a.arr); i++ {
			if a.holes && a.arr[i].isHole() {
				continue
			}
			r := vm.call3(fn, arg(args, 1), a.arr[i], intV(i), this)
			if o, ok := r.r.(*object); ok && o.class == classArray {
				for _, x := range o.arr {
					if !x.isHole() {
						out = vm.appendV(out, x)
					}
				}
			} else {
				out = vm.appendV(out, r)
			}
		}
		return vm.newArrayV(out)
	})
}

func (vm *VM) holey(vals []Value, holes bool) Value {
	a := vm.newArray(vals)
	a.holes = holes
	return objV(a)
}

func idx(right bool, n, k int) int {
	if right {
		return n - 1 - k
	}
	return k
}

func (vm *VM) flatten(out, in []Value, depth float64, level int) []Value {
	if out == nil {
		out = []Value{}
	}
	if level > 1000 {
		vm.throwError("RangeError", "Maximum call stack size exceeded")
	}
	for _, v := range in {
		if v.isHole() {
			continue
		}
		if o, ok := v.r.(*object); ok && o.class == classArray && depth >= 1 {
			out = vm.flatten(out, o.arr, depth-1, level+1)
			continue
		}
		out = vm.appendV(out, v)
	}
	return out
}

func (vm *VM) arrayCtor(args []Value) Value {
	if len(args) == 1 && args[0].r == nil {
		f := args[0].n
		n := int(f)
		if float64(n) != f || n < 0 || f > 4294967295 {
			vm.throwError("RangeError", "Invalid array length")
		}
		if n > maxArrayGap {
			vm.throwError("RangeError", "unsupported: array length "+numberToString(f))
		}
		vm.reserveValues(n)
		vals := make([]Value, n)
		for i := range vals {
			vals[i] = holeV
		}
		return vm.holey(vals, n > 0)
	}
	return vm.newArrayV(append([]Value(nil), args...))
}

func (vm *VM) splice(src []Value, args []Value, dst *[]Value) []Value {
	n := len(src)
	start := relIndex(arg(args, 0), vm, n, 0)
	del := n - start
	if len(args) == 0 {
		del = 0
	} else if len(args) > 1 {
		d := toIntegerOrInf(vm.toNumber(args[1]))
		del = int(math.Max(0, math.Min(d, float64(n-start))))
	}
	var items []Value
	if len(args) > 2 {
		items = args[2:]
	}
	removed := append([]Value{}, src[start:start+del]...)
	out := make([]Value, 0, n-del+len(items))
	out = append(out, src[:start]...)
	out = append(out, items...)
	out = append(out, src[start+del:]...)
	*dst = out
	return removed
}

func (vm *VM) join(a *object, sep string) string {
	if vm.joining == nil {
		vm.joining = map[*object]bool{}
	}
	if vm.joining[a] {
		return ""
	}
	vm.enterNative()
	vm.joining[a] = true
	defer vm.leaveJoin(a)
	b := sbuf{vm: vm}
	var num []byte
	for i, v := range a.arr {
		if i > 0 {
			b.add(sep)
		}
		switch r := v.r.(type) {
		case undefT, nullT, holeT:
		case *jstr:
			b.add(r.flat())
		case nil:
			num = appendNumber(num[:0], v.n)
			b.add(string(num))
		default:
			b.add(vm.toString(v))
		}
	}
	vm.track(b.Len())
	return b.String()
}

func (vm *VM) leaveJoin(a *object) {
	delete(vm.joining, a)
	vm.depth--
}

func (vm *VM) sortValues(vals []Value, cmpV Value) []Value {
	if !cmpV.isUndef() {
		fn := vm.callback(cmpV)
		slices.SortStableFunc(vals, func(a, b Value) int {
			if undefRank(a)|undefRank(b) != 0 {
				return undefLast(a, b)
			}
			r := vm.call2(fn, undefined, a, b)
			f := r.n
			if r.r != nil {
				f = vm.toNumber(r)
			}
			switch {
			case f < 0:
				return -1
			case f > 0:
				return 1
			}
			return 0
		})
		return vals
	}
	type keyed struct {
		v Value
		s *jstr
	}
	ks := make([]keyed, len(vals))
	for i, v := range vals {
		ks[i] = keyed{v: v}
		if undefRank(v) == 0 {
			ks[i].s = vm.toStr(v)
		}
	}
	slices.SortStableFunc(ks, func(a, b keyed) int {
		if a.s == nil || b.s == nil {
			return undefLast(a.v, b.v)
		}
		return compareStrings(a.s, b.s)
	})
	for i := range ks {
		vals[i] = ks[i].v
	}
	return vals
}

func undefRank(v Value) int {
	switch v.r.(type) {
	case undefT:
		return 1
	case holeT:
		return 2
	}
	return 0
}

func undefLast(a, b Value) int {
	return undefRank(a) - undefRank(b)
}
