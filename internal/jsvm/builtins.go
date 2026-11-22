package jsvm

import (
	"math"
	"strconv"
	"strings"
)

type nativeFn struct {
	name   string
	length int
	fn     func(vm *VM, this Value, args []Value) Value
	ctor   func(vm *VM, args []Value, nt *object) Value
}

type boundFn struct {
	target *object
	this   Value
	args   []Value
	levels int
}

type nativeFunc = func(vm *VM, this Value, args []Value) Value

func arg(args []Value, i int) Value {
	if i < len(args) {
		return args[i]
	}
	return undefined
}

func (vm *VM) newNative(name string, length int, fn nativeFunc) *object {
	return &object{shape: vm.funcRoot, class: classFunction, inner: &nativeFn{name: name, length: length, fn: fn}}
}

func (vm *VM) method(o *object, name string, length int, fn nativeFunc) *object {
	f := vm.newNative(name, length, fn)
	o.defineHidden(name, objV(f))
	return f
}

func (vm *VM) setGlobal(name string, v Value) {
	b := vm.globals.bind(name)
	b.v, b.present, b.enum = v, true, false
}

func (vm *VM) newCtor(name string, length int, proto *object, fn nativeFunc, ctor func(vm *VM, args []Value, nt *object) Value) *object {
	c := &object{shape: vm.funcRoot, class: classFunction, inner: &nativeFn{name: name, length: length, fn: fn, ctor: ctor}}
	if proto != nil {
		c.addProp(strKey("prototype"), objV(proto), 0)
		proto.defineHidden("constructor", objV(c))
	}
	vm.setGlobal(name, objV(c))
	return c
}

func (vm *VM) protoFrom(nt *object, def *object) *object {
	if nt == nil {
		return def
	}
	if p, ok := vm.getV(objV(nt), strKey("prototype")).r.(*object); ok {
		return p
	}
	return def
}

func (vm *VM) setup() {
	vm.nullRoot = &hclass{}
	vm.objectProto = &object{shape: vm.nullRoot}
	vm.objectRoot = vm.objectProto.rootShape()
	vm.funcProto = &object{shape: vm.objectRoot}
	vm.funcRoot = vm.funcProto.rootShape()
	vm.arrayProto = &object{shape: vm.objectRoot}
	vm.arrayRoot = vm.arrayProto.rootShape()
	vm.stringProto = &object{shape: vm.objectRoot}
	vm.numberProto = &object{shape: vm.objectRoot}
	vm.boolProto = &object{shape: vm.objectRoot}
	vm.symbolProto = &object{shape: vm.objectRoot}
	vm.iterProto = &object{shape: vm.objectRoot}
	vm.globals = &globalStore{m: map[string]*gbind{}}
	vm.global = &object{shape: newRootShape(vm.objectProto), class: classGlobal, inner: vm.globals}
	vm.setGlobal("globalThis", objV(vm.global))
	vm.setGlobal("NaN", vNaN)
	vm.setGlobal("Infinity", num(math.Inf(1)))
	vm.setGlobal("undefined", undefined)
	vm.setupObject()
	vm.setupFunction()
	vm.setupErrors()
	vm.setupArray()
	vm.setupString()
	vm.setupNumber()
	vm.setupMath()
	vm.setupJSON()
	vm.setupMapSet()
	vm.setupPromise()
	vm.setupRegExp()
	vm.setupIterProto()
	vm.setupGlobals()
}

func (vm *VM) setupGlobals() {
	vm.setupURI()
	vm.setGlobal("isNaN", objV(vm.newNative("isNaN", 1, func(vm *VM, this Value, args []Value) Value {
		f := vm.toNumber(arg(args, 0))
		return boolV(f != f)
	})))
	vm.setGlobal("isFinite", objV(vm.newNative("isFinite", 1, func(vm *VM, this Value, args []Value) Value {
		f := vm.toNumber(arg(args, 0))
		return boolV(!math.IsInf(f, 0) && f == f)
	})))
	vm.setGlobal("queueMicrotask", objV(vm.newNative("queueMicrotask", 1, func(vm *VM, this Value, args []Value) Value {
		fn := arg(args, 0)
		if o, ok := fn.r.(*object); !ok || !o.isCallable() {
			vm.throwTypeError("The \"callback\" argument must be of type function.")
		}
		vm.enqueue(job{fn: func() { vm.protect(func() { vm.call(fn, undefined, nil) }) }})
		return undefined
	})))
	vm.setGlobal("structuredClone", objV(vm.newNative("structuredClone", 1, func(vm *VM, this Value, args []Value) Value {
		return vm.structuredClone(arg(args, 0), map[*object]*object{})
	})))
	vm.setupDate()
	vm.setupConsole()
}

func jsParseInt(s string, radixV Value, vm *VM) float64 {
	s = trimJS(s)
	neg := false
	if s != "" && (s[0] == '+' || s[0] == '-') {
		neg = s[0] == '-'
		s = s[1:]
	}
	radix := 0
	if !radixV.isUndef() {
		radix = int(toInt32(vm.toNumber(radixV)))
	}
	if radix != 0 && (radix < 2 || radix > 36) {
		return math.NaN()
	}
	if (radix == 0 || radix == 16) && len(s) >= 2 && s[0] == '0' && (s[1] == 'x' || s[1] == 'X') {
		s = s[2:]
		radix = 16
	}
	if radix == 0 {
		radix = 10
	}
	f := parseIntRadix(s, radix)
	if neg {
		return -f
	}
	return f
}

func parseIntRadix(s string, radix int) float64 {
	n := 0
	for n < len(s) && digitVal(s[n]) < radix {
		n++
	}
	if n == 0 {
		return math.NaN()
	}
	s = s[:n]
	if radix == 10 {
		return parseIntDigits(s, 10, false)
	}
	if radix&(radix-1) == 0 {
		return parseIntDigits(s, radix, false)
	}
	const maxMultiplier = 0xFFFFFFFF / 36
	number := 0.0
	for i := 0; i < len(s); {
		part, multiplier := uint32(0), uint32(1)
		for i < len(s) {
			m := multiplier * uint32(radix)
			if m > maxMultiplier {
				break
			}
			part = part*uint32(radix) + uint32(digitVal(s[i]))
			multiplier = m
			i++
		}
		number = float64(number*float64(multiplier)) + float64(part)
	}
	return number
}

func jsParseFloat(s string) float64 {
	s = trimJS(s)
	if strings.HasPrefix(s, "Infinity") || strings.HasPrefix(s, "+Infinity") {
		return math.Inf(1)
	}
	if strings.HasPrefix(s, "-Infinity") {
		return math.Inf(-1)
	}
	end := 0
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	digits := false
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
		digits = true
		end = i
	}
	if i < len(s) && s[i] == '.' {
		i++
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
			digits = true
			end = i
		}
	}
	if !digits {
		return math.NaN()
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			j++
		}
		k := j
		for k < len(s) && s[k] >= '0' && s[k] <= '9' {
			k++
		}
		if k > j {
			end = k
		}
	}
	f, err := strconv.ParseFloat(s[:end], 64)
	if err != nil {
		if ne, ok := err.(*strconv.NumError); ok && ne.Err == strconv.ErrRange {
			return f
		}
		return math.NaN()
	}
	return f
}

func (vm *VM) structuredClone(v Value, seen map[*object]*object) Value {
	o, ok := v.r.(*object)
	if !ok {
		return v
	}
	vm.enterNative()
	defer func() { vm.depth-- }()
	if c, ok := seen[o]; ok {
		return objV(c)
	}
	switch o.class {
	case classArray:
		c := vm.newArray(make([]Value, len(o.arr)))
		c.holes = o.holes
		seen[o] = c
		for i, x := range o.arr {
			c.arr[i] = vm.structuredClone(x, seen)
		}
		return objV(c)
	case classMap, classSet:
		md := newMapData()
		proto := vm.mapProto
		if o.class == classSet {
			proto = vm.setProto
		}
		c := &object{shape: proto.rootShape(), class: o.class, inner: md}
		seen[o] = c
		o.inner.(*mapData).each(func(e *mapEntry) bool {
			vm.track(96)
			md.set(vm.structuredClone(e.key, seen), vm.structuredClone(e.val, seen))
			return true
		})
		return objV(c)
	case classError:
		c := vm.makeError("Error", "")
		seen[o] = c
		for _, k := range []string{"name", "message", "stack"} {
			c.defineHidden(k, vm.getProp(o, strKey(k), v))
		}
		return objV(c)
	case classDate:
		c := vm.newDate(dateValue(o), nil)
		seen[o] = c
		return objV(c)
	case classObject, classRegExp:
		if o.class == classRegExp {
			return objV(vm.newRegExpObj(o.inner.(*regexpData).re))
		}
		c := vm.newObject()
		seen[o] = c
		for _, k := range o.ownKeys(true) {
			c.setOwn(k, vm.structuredClone(vm.getProp(o, k, v), seen))
		}
		return objV(c)
	}
	vm.throwError("DataCloneError", "object could not be cloned.")
	return undefined
}

func (vm *VM) setupObject() {
	proto := vm.objectProto
	ctor := vm.newCtor("Object", 1, proto, func(vm *VM, this Value, args []Value) Value {
		v := arg(args, 0)
		if v.isNullish() {
			return objV(vm.newObject())
		}
		return objV(vm.toObject(v))
	}, func(vm *VM, args []Value, nt *object) Value {
		v := arg(args, 0)
		if v.isNullish() {
			return objV(vm.newObjectProto(vm.protoFrom(nt, vm.objectProto)))
		}
		return objV(vm.toObject(v))
	})
	vm.method(proto, "hasOwnProperty", 1, func(vm *VM, this Value, args []Value) Value {
		return boolV(vm.hasOwn(vm.toObject(this), vm.toPropertyKey(arg(args, 0))))
	})
	vm.method(proto, "isPrototypeOf", 1, func(vm *VM, this Value, args []Value) Value {
		o, ok := arg(args, 0).r.(*object)
		if !ok {
			return vFalse
		}
		t := vm.toObject(this)
		for p := o.proto(); p != nil; p = p.proto() {
			if p == t {
				return vTrue
			}
		}
		return vFalse
	})
	vm.method(proto, "propertyIsEnumerable", 1, func(vm *VM, this Value, args []Value) Value {
		o := vm.toObject(this)
		k := vm.toPropertyKey(arg(args, 0))
		if i, fl := o.getOwnSlot(k); i >= 0 {
			return boolV(fl&fEnumerable != 0)
		}
		return boolV(o.class == classArray && vm.hasOwn(o, k) && k.str() != "length")
	})
	vm.method(proto, "toString", 0, func(vm *VM, this Value, args []Value) Value {
		return strV(objectToString(this))
	})
	vm.method(proto, "toLocaleString", 0, func(vm *VM, this Value, args []Value) Value {
		return strV(vm.toString(this))
	})
	vm.method(proto, "valueOf", 0, func(vm *VM, this Value, args []Value) Value {
		return objV(vm.toObject(this))
	})
	vm.method(ctor, "keys", 1, func(vm *VM, this Value, args []Value) Value {
		o := vm.toObject(arg(args, 0))
		keys := vm.ownKeys(o, true)
		vals := make([]Value, len(keys))
		for i, k := range keys {
			vals[i] = strV(k.str())
		}
		return vm.newArrayV(vals)
	})
	vm.method(ctor, "values", 1, func(vm *VM, this Value, args []Value) Value {
		ov := arg(args, 0)
		o := vm.toObject(ov)
		if o.class == classArray && len(o.shape.props) == 0 && !o.holes {
			return vm.newArrayV(append([]Value(nil), o.arr...))
		}
		keys := vm.ownKeys(o, true)
		vals := make([]Value, len(keys))
		for i, k := range keys {
			vals[i] = vm.getProp(o, k, objV(o))
		}
		return vm.newArrayV(vals)
	})
	vm.method(ctor, "entries", 1, func(vm *VM, this Value, args []Value) Value {
		o := vm.toObject(arg(args, 0))
		keys := vm.ownKeys(o, true)
		vals := make([]Value, len(keys))
		for i, k := range keys {
			vals[i] = vm.newArrayV([]Value{strV(k.str()), vm.getProp(o, k, objV(o))})
		}
		return vm.newArrayV(vals)
	})
	vm.method(ctor, "assign", 2, func(vm *VM, this Value, args []Value) Value {
		t := vm.toObject(arg(args, 0))
		for _, src := range args[min(1, len(args)):] {
			if src.isNullish() {
				continue
			}
			so := vm.toObject(src)
			for _, k := range so.ownKeys(true) {
				vm.setProp(t, k, vm.getProp(so, k, src))
			}
		}
		return objV(t)
	})
	vm.method(ctor, "fromEntries", 1, func(vm *VM, this Value, args []Value) Value {
		o := vm.newObject()
		vm.iterate(arg(args, 0), func(e Value) bool {
			k := vm.getIndex(e, num(0))
			v := vm.getIndex(e, num(1))
			vm.defineOwn(o, vm.toPropertyKey(k), v)
			return true
		})
		return objV(o)
	})
	vm.method(ctor, "freeze", 1, func(vm *VM, this Value, args []Value) Value {
		if o, ok := arg(args, 0).r.(*object); ok {
			vm.freeze(o)
		}
		return arg(args, 0)
	})
	vm.method(ctor, "isFrozen", 1, func(vm *VM, this Value, args []Value) Value {
		o, ok := arg(args, 0).r.(*object)
		return boolV(!ok || o.isFrozen)
	})
	vm.method(ctor, "preventExtensions", 1, func(vm *VM, this Value, args []Value) Value {
		if o, ok := arg(args, 0).r.(*object); ok {
			o.noExt = true
		}
		return arg(args, 0)
	})
	vm.method(ctor, "seal", 1, func(vm *VM, this Value, args []Value) Value {
		if o, ok := arg(args, 0).r.(*object); ok {
			o.noExt = true
			if len(o.shape.props) > 0 {
				if !o.shape.dict {
					o.shape = o.shape.toDict()
				}
				for i := range o.shape.props {
					o.shape.props[i].flags &^= fConfigurable
				}
			}
		}
		return arg(args, 0)
	})
	vm.method(ctor, "isExtensible", 1, func(vm *VM, this Value, args []Value) Value {
		o, ok := arg(args, 0).r.(*object)
		return boolV(ok && !o.noExt)
	})
	vm.method(ctor, "create", 2, func(vm *VM, this Value, args []Value) Value {
		p := arg(args, 0)
		var o *object
		switch r := p.r.(type) {
		case *object:
			o = vm.newObjectProto(r)
		case nullT:
			o = vm.newObjectProto(nil)
		default:
			vm.throwTypeError("Object prototype may only be an Object or null: " + ToString(p))
		}
		if props, ok := arg(args, 1).r.(*object); ok {
			vm.defineProperties(o, props)
		}
		return objV(o)
	})
	vm.method(ctor, "getPrototypeOf", 1, func(vm *VM, this Value, args []Value) Value {
		o := vm.toObject(arg(args, 0))
		if p := o.proto(); p != nil {
			return objV(p)
		}
		return null
	})
	vm.method(ctor, "setPrototypeOf", 2, func(vm *VM, this Value, args []Value) Value {
		o, ok := arg(args, 0).r.(*object)
		if !ok {
			return arg(args, 0)
		}
		switch r := arg(args, 1).r.(type) {
		case *object:
			for p := r; p != nil; p = p.proto() {
				if p == o {
					vm.throwTypeError("Cyclic __proto__ value")
				}
			}
			o.setProto(r)
		case nullT:
			o.setProto(nil)
		default:
			vm.throwTypeError("Object prototype may only be an Object or null")
		}
		return arg(args, 0)
	})
	vm.method(ctor, "defineProperty", 3, func(vm *VM, this Value, args []Value) Value {
		o, ok := arg(args, 0).r.(*object)
		if !ok {
			vm.throwTypeError("Object.defineProperty called on non-object")
		}
		d, ok := arg(args, 2).r.(*object)
		if !ok {
			vm.throwTypeError("Property description must be an object")
		}
		vm.defineProperty(o, vm.toPropertyKey(arg(args, 1)), d)
		return arg(args, 0)
	})
	vm.method(ctor, "defineProperties", 2, func(vm *VM, this Value, args []Value) Value {
		o, ok := arg(args, 0).r.(*object)
		if !ok {
			vm.throwTypeError("Object.defineProperties called on non-object")
		}
		vm.defineProperties(o, vm.toObject(arg(args, 1)))
		return arg(args, 0)
	})
	vm.method(ctor, "getOwnPropertyNames", 1, func(vm *VM, this Value, args []Value) Value {
		o := vm.toObject(arg(args, 0))
		keys := vm.ownKeys(o, false)
		vals := make([]Value, 0, len(keys)+1)
		for _, k := range keys {
			vals = append(vals, strV(k.str()))
		}
		if o.class == classArray || o.class == classString {
			vals = append(vals, strV("length"))
		}
		return vm.newArrayV(vals)
	})
	vm.method(ctor, "getOwnPropertyDescriptor", 2, func(vm *VM, this Value, args []Value) Value {
		o := vm.toObject(arg(args, 0))
		k := vm.toPropertyKey(arg(args, 1))
		if !vm.hasOwn(o, k) {
			return undefined
		}
		flags := fDefault
		if i, fl := o.getOwnSlot(k); i >= 0 {
			flags = fl
		} else if o.isFrozen {
			flags = fEnumerable
		}
		d := vm.newObject()
		d.setOwnStr("value", vm.getProp(o, k, objV(o)))
		d.setOwnStr("writable", boolV(flags&fWritable != 0))
		d.setOwnStr("enumerable", boolV(flags&fEnumerable != 0))
		d.setOwnStr("configurable", boolV(flags&fConfigurable != 0))
		return objV(d)
	})
	vm.method(ctor, "hasOwn", 2, func(vm *VM, this Value, args []Value) Value {
		return boolV(vm.hasOwn(vm.toObject(arg(args, 0)), vm.toPropertyKey(arg(args, 1))))
	})
	vm.method(ctor, "is", 2, func(vm *VM, this Value, args []Value) Value {
		return boolV(sameValue(arg(args, 0), arg(args, 1)))
	})
	vm.method(ctor, "groupBy", 2, func(vm *VM, this Value, args []Value) Value {
		out := vm.newObjectProto(nil)
		fn := arg(args, 1)
		i := 0
		vm.iterate(arg(args, 0), func(v Value) bool {
			k := vm.toPropertyKey(vm.call(fn, undefined, []Value{v, intV(i)}))
			i++
			g := vm.getProp(out, k, objV(out))
			if g.isUndef() {
				g = vm.newArrayV(nil)
				vm.defineOwn(out, k, g)
			}
			ga := g.obj()
			ga.arr = append(ga.arr, v)
			return true
		})
		return objV(out)
	})
}

func objectToString(v Value) string {
	switch r := v.r.(type) {
	case undefT:
		return "[object Undefined]"
	case nullT:
		return "[object Null]"
	case nil:
		return "[object Number]"
	case *jstr:
		return "[object String]"
	case boolT:
		return "[object Boolean]"
	case *object:
		switch r.class {
		case classArray:
			return "[object Array]"
		case classFunction:
			return "[object Function]"
		case classError:
			return "[object Error]"
		case classRegExp:
			return "[object RegExp]"
		case classMap:
			return "[object Map]"
		case classSet:
			return "[object Set]"
		case classPromise:
			return "[object Promise]"
		case classDate:
			return "[object Date]"
		}
	}
	return "[object Object]"
}

func (vm *VM) freeze(o *object) {
	if o.isFrozen {
		return
	}
	o.noExt, o.isFrozen = true, true
	if len(o.shape.props) == 0 {
		return
	}
	if !o.shape.dict {
		o.shape = o.shape.toDict()
	}
	for i := range o.shape.props {
		o.shape.props[i].flags &^= fWritable | fConfigurable
	}
}

func (vm *VM) defineProperties(o *object, props *object) {
	for _, k := range props.ownKeys(true) {
		d, ok := vm.getProp(props, k, objV(props)).r.(*object)
		if !ok {
			vm.throwTypeError("Property description must be an object")
		}
		vm.defineProperty(o, k, d)
	}
}

func (vm *VM) defineProperty(o *object, k propKey, d *object) {
	dv := objV(d)
	if vm.hasProperty(d, strKey("get")) || vm.hasProperty(d, strKey("set")) {
		vm.throwTypeError("unsupported: accessor properties (get/set)")
	}
	flag := func(name string, cur bool) bool {
		if !vm.hasProperty(d, strKey(name)) {
			return cur
		}
		return toBool(vm.getV(dv, strKey(name)))
	}
	i, flags := o.getOwnSlot(k)
	exists := i >= 0
	if !exists && o.class == classArray && !k.isSym() {
		if idx, ok := arrayIndex(k.str()); ok {
			vm.setArrayIndex(o, idx, vm.getV(dv, strKey("value")))
			return
		}
		if k.str() == "length" {
			if vm.hasProperty(d, strKey("value")) {
				vm.setArrayLength(o, vm.getV(dv, strKey("value")))
			}
			return
		}
	}
	if exists && flags&fConfigurable == 0 {
		if vm.hasProperty(d, strKey("value")) && flags&fWritable != 0 {
			o.slots[i] = vm.getV(dv, strKey("value"))
			return
		}
		vm.throwTypeError("Cannot redefine property: " + k.String())
	}
	nf := propFlag(0)
	if flag("writable", exists && flags&fWritable != 0) {
		nf |= fWritable
	}
	if flag("enumerable", exists && flags&fEnumerable != 0) {
		nf |= fEnumerable
	}
	if flag("configurable", exists && flags&fConfigurable != 0) {
		nf |= fConfigurable
	}
	val := undefined
	if exists {
		val = o.slots[i]
	}
	if vm.hasProperty(d, strKey("value")) {
		val = vm.getV(dv, strKey("value"))
	}
	if exists {
		if flags == nf {
			o.slots[i] = val
			return
		}
		if !o.shape.dict {
			o.shape = o.shape.toDict()
		}
		o.shape.props[i].flags = nf
		o.slots[i] = val
		return
	}
	if o.noExt {
		vm.throwTypeError("Cannot define property " + k.String() + ", object is not extensible")
	}
	if o.class == classGlobal && !k.isSym() {
		b := vm.globals.bind(k.str())
		b.v, b.present, b.enum = val, true, nf&fEnumerable != 0
		return
	}
	o.addProp(k, val, nf)
}

func (vm *VM) setupFunction() {
	proto := vm.funcProto
	vm.newCtor("Function", 1, proto, func(vm *VM, this Value, args []Value) Value {
		vm.throwError("EvalError", "unsupported: Function constructor")
		return undefined
	}, func(vm *VM, args []Value, nt *object) Value {
		vm.throwError("EvalError", "unsupported: Function constructor")
		return undefined
	})
	vm.method(proto, "call", 1, func(vm *VM, this Value, args []Value) Value {
		var rest []Value
		if len(args) > 1 {
			rest = args[1:]
		}
		return vm.call(this, arg(args, 0), rest)
	})
	vm.method(proto, "apply", 2, func(vm *VM, this Value, args []Value) Value {
		var list []Value
		if a := arg(args, 1); !a.isNullish() {
			list = vm.arrayLike(a)
		}
		return vm.call(this, arg(args, 0), list)
	})
	vm.method(proto, "bind", 1, func(vm *VM, this Value, args []Value) Value {
		t, ok := this.r.(*object)
		if !ok || !t.isCallable() {
			vm.throwTypeError("Bind must be called on a function")
		}
		bf := &boundFn{target: t, this: arg(args, 0), levels: 1}
		var prior []Value
		if inner, ok := t.inner.(*boundFn); ok {
			bf.target, bf.this, bf.levels, prior = inner.target, inner.this, inner.levels+1, inner.args
		}
		extra := 0
		if len(args) > 1 {
			extra = len(args) - 1
		}
		vm.reserveValues(len(prior) + extra)
		vm.track(96 + 24*(len(prior)+extra))
		if len(prior)+extra > 0 {
			bf.args = make([]Value, 0, len(prior)+extra)
			bf.args = append(bf.args, prior...)
			if extra > 0 {
				bf.args = append(bf.args, args[1:]...)
			}
		}
		return objV(&object{shape: vm.funcRoot, class: classFunction, inner: bf})
	})
	vm.method(proto, "toString", 0, func(vm *VM, this Value, args []Value) Value {
		o, ok := this.r.(*object)
		if !ok || !o.isCallable() {
			vm.throwTypeError("Function.prototype.toString requires that 'this' be a Function")
		}
		if cl, ok := o.inner.(*closure); ok && cl.code.src != nil {
			return strV(cl.code.source())
		}
		n, _ := vm.functionProp(o, "name")
		return strV("function " + ToString(n) + "() { [native code] }")
	})
}

func (code *funcCode) source() string {
	if code.end > code.pos && code.end <= len(code.src.src) {
		return code.src.src[code.pos:code.end]
	}
	return "function " + code.name + "() { [code] }"
}

func (vm *VM) arrayLike(v Value) []Value {
	o, ok := v.r.(*object)
	if !ok {
		vm.throwTypeError("CreateListFromArrayLike called on non-object")
	}
	if o.class == classArray {
		return append([]Value(nil), dense(o)...)
	}
	n := vm.toLength(vm.getV(v, strKey("length")))
	if n > maxArrayGap {
		vm.throwError("RangeError", "Invalid array length")
	}
	vm.reserveValues(n)
	vm.track(n * 24)
	out := make([]Value, n)
	for i := range out {
		out[i] = vm.getProp(o, strKey(strconv.Itoa(i)), v)
	}
	return out
}

func (vm *VM) setupConsole() {
	con := vm.newObject()
	for _, level := range []string{"log", "error", "warn", "info", "debug"} {
		lv := level
		vm.method(con, lv, 0, func(vm *VM, this Value, args []Value) Value {
			parts := make([]string, len(args))
			for i, a := range args {
				parts[i] = vm.inspect(a)
			}
			if vm.stdout != nil {
				vm.stdout(lv, strings.Join(parts, " "))
			}
			return undefined
		})
	}
	vm.setGlobal("console", objV(con))
}

func (vm *VM) inspect(v Value) string {
	switch r := v.r.(type) {
	case *jstr:
		return r.flat()
	case undefT:
		return "undefined"
	case *object:
		if r.isCallable() {
			n, _ := vm.functionProp(r, "name")
			return "[Function: " + ToString(n) + "]"
		}
		if r.class == classError {
			if s, ok := vm.safeGet(r, "stack").r.(*jstr); ok {
				return s.flat()
			}
		}
		var s string
		if exc := vm.protect(func() { s = vm.jsonStringify(v, undefined, "  ") }); exc != nil || s == "" {
			return vm.safeString(v)
		}
		return s
	}
	return ToString(v)
}
