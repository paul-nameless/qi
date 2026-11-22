package jsvm

import (
	"iter"
	"runtime"
)

const maxCoroutines = 100000

type coro struct {
	next    func() (struct{}, bool)
	stop    func()
	yield   func(struct{}) bool
	in      Value
	inThrow bool
	stack   []Value
	sp      int
	cell    int
	cleanup runtime.Cleanup
}

func (vm *VM) callAsync(fo *object, cl *closure, this Value, args []Value) Value {
	if len(vm.coros) >= maxCoroutines {
		vm.throwError("RangeError", "too many pending async calls")
	}
	vm.track(8192)
	args = append([]Value(nil), args...)
	p := vm.newPromise()
	co := &coro{cell: vm.cellID}
	co.next, co.stop = iter.Pull(func(yield func(struct{}) bool) {
		co.yield = yield
		vm.runAsyncBody(co, fo, cl, this, args, p)
	})
	vm.coros[co] = struct{}{}
	vm.resume(co, undefined, false)
	return objV(p)
}

func (vm *VM) runAsyncBody(co *coro, fo *object, cl *closure, this Value, args []Value, p *object) {
	defer func() {
		delete(vm.coros, co)
		if r := recover(); r != nil {
			if _, ok := r.(killSignal); ok {
				return
			}
			if t, ok := vm.asThrow(r); ok {
				vm.rejectPromise(p, t.v)
				return
			}
			panic(r)
		}
	}()
	v := vm.runAsyncFrame(co, fo, cl, this, args)
	vm.resolvePromise(p, v)
}

func (vm *VM) runAsyncFrame(co *coro, fo *object, cl *closure, this Value, args []Value) Value {
	code := cl.code
	if vm.depth >= vm.maxDepth {
		vm.throwError("RangeError", "Maximum call stack size exceeded")
	}
	vm.checkInterrupt()
	f := &frame{vm: vm, code: code, fnObj: fo, ups: cl.ups, parent: vm.top, co: co}
	if code.arrow {
		f.this = cl.this
	} else {
		f.this = this
	}
	f.slots = make([]Value, code.nslots)
	for i := range f.slots {
		f.slots[i] = undefined
	}
	if code.ncells > 0 {
		f.cells = make([]*Value, code.ncells)
	}
	vm.top = f
	vm.depth++
	code.enter(f, args)
	var ret Value
	if code.exprBody != nil {
		ret = code.exprBody(f)
	} else if code.body(f) == ctlReturn {
		ret = f.ret
	} else {
		ret = undefined
	}
	return ret
}

func (vm *VM) resume(co *coro, v Value, throw bool) {
	co.in, co.inThrow = v, throw
	st := vm.save()
	vm.stack, vm.sp = co.stack, co.sp
	co.next()
	co.stack, co.sp = vm.stack, vm.sp
	vm.restore(st)
}

func (vm *VM) await(f *frame, v Value) Value {
	co := f.co
	if co == nil {
		vm.throwError("SyntaxError", "await is only valid in async functions")
	}
	vm.park(co, vm.promiseResolve(v))
	if !co.yield(struct{}{}) {
		panic(killSignal{})
	}
	co.cleanup.Stop()
	vm.top = f
	f.parent = nil
	if co.inThrow {
		vm.throw(co.in)
	}
	return co.in
}

func (vm *VM) park(co *coro, p *object) {
	vm.addReaction(p, &reaction{co: co})
	clear(vm.stack[vm.sp:])
	co.in = undefined
	co.cleanup = runtime.AddCleanup(p, vm.markDead, co)
}

func (vm *VM) markDead(co *coro) {
	vm.deadMu.Lock()
	vm.dead = append(vm.dead, co)
	vm.deadMu.Unlock()
}

func (vm *VM) reapDead() {
	vm.deadMu.Lock()
	dead := vm.dead
	vm.dead = nil
	vm.deadMu.Unlock()
	for _, co := range dead {
		if _, ok := vm.coros[co]; ok {
			delete(vm.coros, co)
			co.stop()
		}
	}
}

func (vm *VM) killCell(cell int) {
	for co := range vm.coros {
		if co.cell == cell {
			delete(vm.coros, co)
			co.stop()
		}
	}
}

func (vm *VM) killCoroutines() {
	for len(vm.coros) > 0 {
		for co := range vm.coros {
			delete(vm.coros, co)
			co.stop()
		}
	}
}

const (
	pending uint8 = iota
	fulfilled
	rejected
)

type promise struct {
	state     uint8
	value     Value
	reactions []*reaction
	handled   bool
}

type reaction struct {
	onFul   Value
	onRej   Value
	derived *object
	co      *coro
	native  func(v Value, rejected bool)
}

func (vm *VM) newPromise() *object {
	vm.track(96)
	return &object{shape: vm.promiseProto.rootShape(), class: classPromise, inner: &promise{}}
}

func promiseOf(v Value) *promise {
	if o, ok := v.r.(*object); ok && o.class == classPromise {
		return o.inner.(*promise)
	}
	return nil
}

func (vm *VM) settle(po *object, state uint8, v Value) {
	p := po.inner.(*promise)
	if p.state != pending {
		return
	}
	p.state, p.value = state, v
	rs := p.reactions
	p.reactions = nil
	for _, r := range rs {
		vm.enqueue(job{r: r, v: v, rej: state == rejected})
	}
}

func (vm *VM) rejectPromise(po *object, v Value) { vm.settle(po, rejected, v) }

func (vm *VM) resolvePromise(po *object, x Value) {
	if po.inner.(*promise).state != pending {
		return
	}
	xo, ok := x.r.(*object)
	if !ok {
		vm.settle(po, fulfilled, x)
		return
	}
	if xo == po {
		vm.rejectPromise(po, objV(vm.makeError("TypeError", "Chaining cycle detected for promise #<Promise>")))
		return
	}
	var then Value
	if exc := vm.protect(func() { then = vm.getV(x, strKey("then")) }); exc != nil {
		vm.rejectPromise(po, exc.v)
		return
	}
	tf, ok := then.r.(*object)
	if !ok || !tf.isCallable() {
		vm.settle(po, fulfilled, x)
		return
	}
	if xo.class == classPromise && tf == vm.promiseThen() {
		vm.enqueue(job{fn: func() {
			vm.addReaction(xo, &reaction{native: func(v Value, rej bool) {
				if rej {
					vm.rejectPromise(po, v)
				} else {
					vm.resolvePromise(po, v)
				}
			}})
		}})
		return
	}
	vm.enqueue(job{fn: func() {
		res, rej := vm.resolvingFunctions(po)
		if exc := vm.protect(func() { vm.callObj(tf, x, []Value{res, rej}) }); exc != nil {
			vm.callObj(rej.obj(), undefined, []Value{exc.v})
		}
	}})
}

func (vm *VM) promiseThen() *object {
	i := vm.promiseProto.shape.lookupStr("then")
	if i < 0 {
		return nil
	}
	return vm.promiseProto.slots[i].obj()
}

func (vm *VM) resolvingFunctions(po *object) (Value, Value) {
	done := false
	res := vm.newNative("", 1, func(vm *VM, this Value, args []Value) Value {
		if !done {
			done = true
			vm.resolvePromise(po, arg(args, 0))
		}
		return undefined
	})
	rej := vm.newNative("", 1, func(vm *VM, this Value, args []Value) Value {
		if !done {
			done = true
			vm.rejectPromise(po, arg(args, 0))
		}
		return undefined
	})
	return objV(res), objV(rej)
}

func (vm *VM) addReaction(po *object, r *reaction) {
	p := po.inner.(*promise)
	p.handled = true
	if p.state == pending {
		p.reactions = append(p.reactions, r)
		return
	}
	vm.enqueue(job{r: r, v: p.value, rej: p.state == rejected})
}

func (vm *VM) promiseResolve(v Value) *object {
	if o, ok := v.r.(*object); ok && o.class == classPromise && o.proto() == vm.promiseProto {
		return o
	}
	p := vm.newPromise()
	vm.resolvePromise(p, v)
	return p
}

func (vm *VM) runJob(j job) {
	if j.fn != nil {
		j.fn()
		return
	}
	r := j.r
	switch {
	case r.co != nil:
		vm.resume(r.co, j.v, j.rej)
	case r.native != nil:
		r.native(j.v, j.rej)
	default:
		h := r.onFul
		if j.rej {
			h = r.onRej
		}
		ho, ok := h.r.(*object)
		if !ok || !ho.isCallable() {
			if r.derived != nil {
				if j.rej {
					vm.rejectPromise(r.derived, j.v)
				} else {
					vm.resolvePromise(r.derived, j.v)
				}
			}
			return
		}
		var res Value
		exc := vm.protect(func() { res = vm.callObj(ho, undefined, []Value{j.v}) })
		if r.derived == nil {
			return
		}
		if exc != nil {
			vm.rejectPromise(r.derived, exc.v)
		} else {
			vm.resolvePromise(r.derived, res)
		}
	}
}

func (vm *VM) then(po *object, onFul, onRej Value) *object {
	d := vm.newPromise()
	vm.addReaction(po, &reaction{onFul: onFul, onRej: onRej, derived: d})
	return d
}
