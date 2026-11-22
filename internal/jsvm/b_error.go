package jsvm

import "runtime"

func forceGC() { runtime.GC() }

func (vm *VM) setupErrors() {
	vm.errorProto = vm.newObject()
	vm.errorProtos = map[string]*object{}
	base := vm.errorCtor("Error", vm.errorProto, nil)
	vm.method(vm.errorProto, "toString", 0, func(vm *VM, this Value, args []Value) Value {
		o, ok := this.r.(*object)
		if !ok {
			vm.throwTypeError("Error.prototype.toString called on non-object")
		}
		name := vm.getProp(o, strKey("name"), this)
		msg := vm.getProp(o, strKey("message"), this)
		n := "Error"
		if !name.isUndef() {
			n = vm.toString(name)
		}
		m := ""
		if !msg.isUndef() {
			m = vm.toString(msg)
		}
		switch {
		case n == "":
			return strV(m)
		case m == "":
			return strV(n)
		}
		return strV(n + ": " + m)
	})
	for _, n := range []string{"TypeError", "RangeError", "ReferenceError", "SyntaxError", "EvalError", "URIError", "AggregateError"} {
		p := vm.newObjectProto(vm.errorProto)
		c := vm.errorCtor(n, p, base)
		c.setProto(base)
	}
}

func (vm *VM) errorCtor(name string, proto *object, base *object) *object {
	vm.errorProtos[name] = proto
	proto.defineHidden("name", strV(name))
	proto.defineHidden("message", strV(""))
	aggregate := name == "AggregateError"
	build := func(vm *VM, args []Value, nt *object) Value {
		o := &object{shape: vm.protoFrom(nt, proto).rootShape(), class: classError}
		msgArg, optArg := arg(args, 0), arg(args, 1)
		if aggregate {
			msgArg, optArg = arg(args, 1), arg(args, 2)
		}
		msg := ""
		if !msgArg.isUndef() {
			msg = vm.toString(msgArg)
			o.defineHidden("message", strV(msg))
		}
		if opts, ok := optArg.r.(*object); ok && vm.hasProperty(opts, strKey("cause")) {
			o.defineHidden("cause", vm.getProp(opts, strKey("cause"), optArg))
		}
		if aggregate {
			o.defineHidden("errors", vm.newArrayV(append([]Value(nil), vm.collect(arg(args, 0))...)))
		}
		vm.attachStack(o, name, msg)
		return objV(o)
	}
	return vm.newCtor(name, 1, proto, func(vm *VM, this Value, args []Value) Value {
		return build(vm, args, nil)
	}, build)
}
