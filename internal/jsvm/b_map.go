package jsvm

func (vm *VM) thisMap(this Value, class objClass, name string) *mapData {
	if o, ok := this.r.(*object); ok && o.class == class {
		if md, ok := o.inner.(*mapData); ok {
			return md
		}
	}
	kind := "Map"
	if class == classSet {
		kind = "Set"
	}
	vm.throwTypeError("Method " + kind + ".prototype." + name + " called on incompatible receiver " + vm.safeString(this))
	return nil
}

func (vm *VM) setupMapSet() {
	mp := vm.newObject()
	vm.mapProto = mp
	vm.newCtor("Map", 0, mp, func(vm *VM, this Value, args []Value) Value {
		vm.throwTypeError("Constructor Map requires 'new'")
		return undefined
	}, func(vm *VM, args []Value, nt *object) Value {
		md := newMapData()
		o := &object{shape: vm.protoFrom(nt, mp).rootShape(), class: classMap, inner: md}
		if src := arg(args, 0); !src.isNullish() {
			vm.iterate(src, func(e Value) bool {
				eo, ok := e.r.(*object)
				if !ok {
					vm.throwTypeError("Iterator value " + vm.safeString(e) + " is not an entry object")
				}
				vm.track(96)
				if eo.class == classArray {
					md.set(unhole(arg(eo.arr, 0)), unhole(arg(eo.arr, 1)))
				} else {
					md.set(vm.getIndex(e, num(0)), vm.getIndex(e, num(1)))
				}
				return true
			})
		}
		return objV(o)
	})
	vm.method(mp, "get", 1, func(vm *VM, this Value, args []Value) Value {
		if e, ok := vm.thisMap(this, classMap, "get").get(arg(args, 0)); ok {
			return e.val
		}
		return undefined
	})
	vm.method(mp, "set", 2, func(vm *VM, this Value, args []Value) Value {
		md := vm.thisMap(this, classMap, "set")
		n := md.size
		md.set(arg(args, 0), arg(args, 1))
		if md.size != n {
			vm.track(96)
		}
		return this
	})
	vm.method(mp, "has", 1, func(vm *VM, this Value, args []Value) Value {
		_, ok := vm.thisMap(this, classMap, "has").get(arg(args, 0))
		return boolV(ok)
	})
	vm.method(mp, "delete", 1, func(vm *VM, this Value, args []Value) Value {
		return boolV(vm.thisMap(this, classMap, "delete").remove(arg(args, 0)))
	})
	vm.method(mp, "clear", 0, func(vm *VM, this Value, args []Value) Value {
		vm.thisMap(this, classMap, "clear").clear()
		return undefined
	})
	vm.method(mp, "forEach", 1, func(vm *VM, this Value, args []Value) Value {
		md := vm.thisMap(this, classMap, "forEach")
		fn := vm.callback(arg(args, 0))
		md.each(func(e *mapEntry) bool {
			vm.call3(fn, arg(args, 1), e.val, e.key, this)
			return true
		})
		return undefined
	})
	mapIter := func(name string, mode uint8) {
		vm.method(mp, name, 0, func(vm *VM, this Value, args []Value) Value {
			return vm.newIter(&iterState{md: vm.thisMap(this, classMap, name), mode: mode})
		})
	}
	mapIter("keys", iterKeys)
	mapIter("values", iterValues)
	mapIter("entries", iterEntries)
	mapCtor := vm.globals.lookup("Map").v.obj()
	vm.method(mapCtor, "groupBy", 2, func(vm *VM, this Value, args []Value) Value {
		md := newMapData()
		fn := vm.callback(arg(args, 1))
		i := 0
		vm.iterate(arg(args, 0), func(v Value) bool {
			k := vm.call2(fn, undefined, v, intV(i))
			i++
			e, ok := md.get(k)
			if !ok {
				md.set(k, vm.newArrayV(nil))
				e, _ = md.get(k)
			}
			a := e.val.obj()
			a.arr = append(a.arr, v)
			return true
		})
		return objV(&object{shape: mp.rootShape(), class: classMap, inner: md})
	})

	sp := vm.newObject()
	vm.setProto = sp
	vm.newCtor("Set", 0, sp, func(vm *VM, this Value, args []Value) Value {
		vm.throwTypeError("Constructor Set requires 'new'")
		return undefined
	}, func(vm *VM, args []Value, nt *object) Value {
		md := newMapData()
		o := &object{shape: vm.protoFrom(nt, sp).rootShape(), class: classSet, inner: md}
		if src := arg(args, 0); !src.isNullish() {
			vm.iterate(src, func(v Value) bool {
				md.set(v, undefined)
				return true
			})
			vm.track(md.size * 96)
		}
		return objV(o)
	})
	vm.method(sp, "add", 1, func(vm *VM, this Value, args []Value) Value {
		md := vm.thisMap(this, classSet, "add")
		if _, ok := md.get(arg(args, 0)); !ok {
			md.set(arg(args, 0), undefined)
			vm.track(96)
		}
		return this
	})
	vm.method(sp, "has", 1, func(vm *VM, this Value, args []Value) Value {
		_, ok := vm.thisMap(this, classSet, "has").get(arg(args, 0))
		return boolV(ok)
	})
	vm.method(sp, "delete", 1, func(vm *VM, this Value, args []Value) Value {
		return boolV(vm.thisMap(this, classSet, "delete").remove(arg(args, 0)))
	})
	vm.method(sp, "clear", 0, func(vm *VM, this Value, args []Value) Value {
		vm.thisMap(this, classSet, "clear").clear()
		return undefined
	})
	vm.method(sp, "forEach", 1, func(vm *VM, this Value, args []Value) Value {
		md := vm.thisMap(this, classSet, "forEach")
		fn := vm.callback(arg(args, 0))
		md.each(func(e *mapEntry) bool {
			vm.call3(fn, arg(args, 1), e.key, e.key, this)
			return true
		})
		return undefined
	})
	setIter := func(name string, mode uint8) {
		vm.method(sp, name, 0, func(vm *VM, this Value, args []Value) Value {
			return vm.newIter(&iterState{md: vm.thisMap(this, classSet, name), mode: mode, isSet: true})
		})
	}
	setIter("values", iterValues)
	setIter("keys", iterValues)
	setIter("entries", iterEntries)
	vm.setAlgebra(sp)
}

func (vm *VM) setAlgebra(sp *object) {
	other := func(v Value) *mapData {
		if o, ok := v.r.(*object); ok && (o.class == classSet || o.class == classMap) {
			return o.inner.(*mapData)
		}
		vm.throwTypeError("unsupported: set operation argument must be a Set or Map")
		return nil
	}
	newSet := func(md *mapData) Value {
		vm.track(96 * md.size)
		return objV(&object{shape: sp.rootShape(), class: classSet, inner: md})
	}
	vm.method(sp, "union", 1, func(vm *VM, this Value, args []Value) Value {
		a, b := vm.thisMap(this, classSet, "union"), other(arg(args, 0))
		out := newMapData()
		a.each(func(e *mapEntry) bool { out.set(e.key, undefined); return true })
		b.each(func(e *mapEntry) bool { out.set(e.key, undefined); return true })
		return newSet(out)
	})
	vm.method(sp, "intersection", 1, func(vm *VM, this Value, args []Value) Value {
		a, b := vm.thisMap(this, classSet, "intersection"), other(arg(args, 0))
		out := newMapData()
		a.each(func(e *mapEntry) bool {
			if _, ok := b.get(e.key); ok {
				out.set(e.key, undefined)
			}
			return true
		})
		return newSet(out)
	})
	vm.method(sp, "difference", 1, func(vm *VM, this Value, args []Value) Value {
		a, b := vm.thisMap(this, classSet, "difference"), other(arg(args, 0))
		out := newMapData()
		a.each(func(e *mapEntry) bool {
			if _, ok := b.get(e.key); !ok {
				out.set(e.key, undefined)
			}
			return true
		})
		return newSet(out)
	})
	vm.method(sp, "symmetricDifference", 1, func(vm *VM, this Value, args []Value) Value {
		a, b := vm.thisMap(this, classSet, "symmetricDifference"), other(arg(args, 0))
		out := newMapData()
		a.each(func(e *mapEntry) bool {
			if _, ok := b.get(e.key); !ok {
				out.set(e.key, undefined)
			}
			return true
		})
		b.each(func(e *mapEntry) bool {
			if _, ok := a.get(e.key); !ok {
				out.set(e.key, undefined)
			}
			return true
		})
		return newSet(out)
	})
	subset := func(a, b *mapData) bool {
		ok := true
		a.each(func(e *mapEntry) bool {
			if _, has := b.get(e.key); !has {
				ok = false
				return false
			}
			return true
		})
		return ok
	}
	vm.method(sp, "isSubsetOf", 1, func(vm *VM, this Value, args []Value) Value {
		return boolV(subset(vm.thisMap(this, classSet, "isSubsetOf"), other(arg(args, 0))))
	})
	vm.method(sp, "isSupersetOf", 1, func(vm *VM, this Value, args []Value) Value {
		return boolV(subset(other(arg(args, 0)), vm.thisMap(this, classSet, "isSupersetOf")))
	})
	vm.method(sp, "isDisjointFrom", 1, func(vm *VM, this Value, args []Value) Value {
		a, b := vm.thisMap(this, classSet, "isDisjointFrom"), other(arg(args, 0))
		dis := true
		a.each(func(e *mapEntry) bool {
			if _, ok := b.get(e.key); ok {
				dis = false
				return false
			}
			return true
		})
		return boolV(dis)
	})
}

func (vm *VM) setupIterProto() {
	p := vm.iterProto
	thisIter := func(vm *VM, this Value) *iterState {
		if o, ok := this.r.(*object); ok && o.class == classIter {
			return o.inner.(*iterState)
		}
		vm.throwTypeError("next method called on incompatible receiver")
		return nil
	}
	vm.method(p, "next", 0, func(vm *VM, this Value, args []Value) Value {
		v, ok := vm.iterNext(thisIter(vm, this))
		r := vm.newObject()
		r.setOwnStr("value", v)
		r.setOwnStr("done", boolV(!ok))
		return objV(r)
	})
	vm.method(p, "toArray", 0, func(vm *VM, this Value, args []Value) Value {
		thisIter(vm, this)
		return vm.newArrayV(append([]Value{}, vm.collect(this)...))
	})
	vm.method(p, "forEach", 1, func(vm *VM, this Value, args []Value) Value {
		thisIter(vm, this)
		fn := vm.callback(arg(args, 0))
		i := 0
		vm.iterate(this, func(v Value) bool {
			vm.call2(fn, undefined, v, intV(i))
			i++
			return true
		})
		return undefined
	})
	vm.method(p, "map", 1, func(vm *VM, this Value, args []Value) Value {
		st := thisIter(vm, this)
		fn := vm.callback(arg(args, 0))
		i := 0
		return vm.newIter(&iterState{fn: func() (Value, bool) {
			vm.enterNative()
			v, ok := vm.iterNext(st)
			vm.depth--
			if !ok {
				return undefined, false
			}
			r := vm.call2(fn, undefined, v, intV(i))
			i++
			return r, true
		}})
	})
	vm.method(p, "filter", 1, func(vm *VM, this Value, args []Value) Value {
		st := thisIter(vm, this)
		fn := vm.callback(arg(args, 0))
		i := 0
		return vm.newIter(&iterState{fn: func() (Value, bool) {
			for {
				vm.enterNative()
				v, ok := vm.iterNext(st)
				vm.depth--
				if !ok {
					return undefined, false
				}
				keep := toBool(vm.call2(fn, undefined, v, intV(i)))
				i++
				if keep {
					return v, true
				}
			}
		}})
	})
}
