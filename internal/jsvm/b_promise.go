package jsvm

func (vm *VM) thisPromise(this Value, name string) *object {
	if o, ok := this.r.(*object); ok && o.class == classPromise {
		return o
	}
	vm.throwTypeError("Method Promise.prototype." + name + " called on incompatible receiver " + vm.safeString(this))
	return nil
}

func (vm *VM) setupPromise() {
	p := vm.newObject()
	vm.promiseProto = p
	ctor := vm.newCtor("Promise", 1, p, func(vm *VM, this Value, args []Value) Value {
		vm.throwTypeError("Promise constructor cannot be invoked without 'new'")
		return undefined
	}, func(vm *VM, args []Value, nt *object) Value {
		exec := arg(args, 0)
		eo, ok := exec.r.(*object)
		if !ok || !eo.isCallable() {
			vm.throwTypeError("Promise resolver " + vm.safeString(exec) + " is not a function")
		}
		po := vm.newPromise()
		if nt != nil {
			if pp := vm.protoFrom(nt, p); pp != p {
				po.setProto(pp)
			}
		}
		res, rej := vm.resolvingFunctions(po)
		if exc := vm.protect(func() { vm.call2(eo, undefined, res, rej) }); exc != nil {
			vm.callObj(rej.obj(), undefined, []Value{exc.v})
		}
		return objV(po)
	})
	vm.method(p, "then", 2, func(vm *VM, this Value, args []Value) Value {
		po := vm.thisPromise(this, "then")
		return objV(vm.then(po, arg(args, 0), arg(args, 1)))
	})
	vm.method(p, "catch", 1, func(vm *VM, this Value, args []Value) Value {
		return vm.invoke(this, "then", undefined, arg(args, 0))
	})
	vm.method(p, "finally", 1, func(vm *VM, this Value, args []Value) Value {
		fn, ok := arg(args, 0).r.(*object)
		if !ok || !fn.isCallable() {
			return vm.invoke(this, "then", arg(args, 0), arg(args, 0))
		}
		onFul := vm.newNative("", 1, func(vm *VM, _ Value, a []Value) Value {
			v := arg(a, 0)
			r := vm.callObj(fn, undefined, nil)
			pr := vm.promiseResolve(r)
			return objV(vm.then(pr, objV(vm.newNative("", 0, func(vm *VM, _ Value, _ []Value) Value { return v })), undefined))
		})
		onRej := vm.newNative("", 1, func(vm *VM, _ Value, a []Value) Value {
			e := arg(a, 0)
			r := vm.callObj(fn, undefined, nil)
			pr := vm.promiseResolve(r)
			return objV(vm.then(pr, objV(vm.newNative("", 0, func(vm *VM, _ Value, _ []Value) Value {
				vm.throw(e)
				return undefined
			})), undefined))
		})
		return vm.invoke(this, "then", objV(onFul), objV(onRej))
	})
	vm.method(ctor, "resolve", 1, func(vm *VM, this Value, args []Value) Value {
		return objV(vm.promiseResolve(arg(args, 0)))
	})
	vm.method(ctor, "reject", 1, func(vm *VM, this Value, args []Value) Value {
		po := vm.newPromise()
		vm.rejectPromise(po, arg(args, 0))
		return objV(po)
	})
	vm.method(ctor, "withResolvers", 0, func(vm *VM, this Value, args []Value) Value {
		po := vm.newPromise()
		res, rej := vm.resolvingFunctions(po)
		o := vm.newObject()
		o.setOwnStr("promise", objV(po))
		o.setOwnStr("resolve", res)
		o.setOwnStr("reject", rej)
		return objV(o)
	})
	vm.method(ctor, "all", 1, func(vm *VM, this Value, args []Value) Value {
		return vm.combinator(arg(args, 0), "all")
	})
	vm.method(ctor, "allSettled", 1, func(vm *VM, this Value, args []Value) Value {
		return vm.combinator(arg(args, 0), "allSettled")
	})
	vm.method(ctor, "any", 1, func(vm *VM, this Value, args []Value) Value {
		return vm.combinator(arg(args, 0), "any")
	})
	vm.method(ctor, "race", 1, func(vm *VM, this Value, args []Value) Value {
		out := vm.newPromise()
		var items []Value
		if exc := vm.protect(func() { items = append([]Value(nil), vm.collect(arg(args, 0))...) }); exc != nil {
			vm.rejectPromise(out, exc.v)
			return objV(out)
		}
		for _, it := range items {
			pr := vm.promiseResolve(it)
			vm.addReaction(pr, &reaction{native: func(v Value, rej bool) {
				if rej {
					vm.rejectPromise(out, v)
				} else {
					vm.resolvePromise(out, v)
				}
			}})
		}
		return objV(out)
	})
}

func (vm *VM) invoke(this Value, name string, args ...Value) Value {
	fn := vm.getV(this, strKey(name))
	return vm.call(fn, this, args)
}

func (vm *VM) combinator(iterable Value, kind string) Value {
	out := vm.newPromise()
	var items []Value
	if exc := vm.protect(func() { items = append([]Value(nil), vm.collect(iterable)...) }); exc != nil {
		vm.rejectPromise(out, exc.v)
		return objV(out)
	}
	n := len(items)
	results := make([]Value, n)
	remaining := n
	if n == 0 {
		if kind == "any" {
			e := vm.makeError("AggregateError", "All promises were rejected")
			e.defineHidden("errors", vm.newArrayV([]Value{}))
			vm.rejectPromise(out, objV(e))
		} else {
			vm.resolvePromise(out, vm.newArrayV(results))
		}
		return objV(out)
	}
	settled := func(status string, key string, v Value) Value {
		o := vm.newObject()
		o.setOwnStr("status", strV(status))
		o.setOwnStr(key, v)
		return objV(o)
	}
	for i, it := range items {
		idx := i
		pr := vm.promiseResolve(it)
		vm.addReaction(pr, &reaction{native: func(v Value, rej bool) {
			switch kind {
			case "all":
				if rej {
					vm.rejectPromise(out, v)
					return
				}
				results[idx] = v
			case "allSettled":
				if rej {
					results[idx] = settled("rejected", "reason", v)
				} else {
					results[idx] = settled("fulfilled", "value", v)
				}
			case "any":
				if !rej {
					vm.resolvePromise(out, v)
					return
				}
				results[idx] = v
			}
			remaining--
			if remaining > 0 {
				return
			}
			if kind == "any" {
				e := vm.makeError("AggregateError", "All promises were rejected")
				e.defineHidden("errors", vm.newArrayV(results))
				vm.rejectPromise(out, objV(e))
				return
			}
			vm.resolvePromise(out, vm.newArrayV(results))
		}})
	}
	return objV(out)
}
