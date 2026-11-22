package jsvm

import (
	"errors"
	"fmt"
	"runtime/debug"
)

type HostFunc func(vm *VM, args []Value) (Value, error)

type Options struct {
	MemoryLimit int64
	MaxDepth    int
	Stdout      func(level, text string)
}

var ErrInterrupted = errors.New("interrupted")

type PromiseState int

const (
	PromisePending PromiseState = iota
	PromiseFulfilled
	PromiseRejected
)

func New(opts Options) *VM {
	vm := &VM{maxDepth: opts.MaxDepth, memLimit: opts.MemoryLimit, stdout: opts.Stdout, coros: map[*coro]struct{}{}}
	if vm.maxDepth <= 0 {
		vm.maxDepth = 2000
	}
	vm.nextCheck = memCheckEvery
	vm.stack = make([]Value, 1024)
	vm.setup()
	return vm
}

func (vm *VM) guard(fn func()) (err error) {
	st := vm.save()
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		vm.restore(st)
		switch t := r.(type) {
		case *jsThrow:
			err = vm.exceptionFrom(t.v)
		case *interruptSignal:
			err = ErrInterrupted
		case killSignal:
			err = ErrInterrupted
		case *syntaxErr:
			err = &Exception{Name: "SyntaxError", Message: t.msg}
		default:
			if t, ok := vm.asThrow(r); ok {
				err = vm.exceptionFrom(t.v)
				return
			}
			err = fmt.Errorf("jsvm internal error: %v\n%s", r, debug.Stack())
		}
	}()
	fn()
	return nil
}

func (vm *VM) compile(name, src string) (fn *object, err error) {
	defer func() {
		if r := recover(); r != nil {
			fn, err = nil, fmt.Errorf("jsvm internal error: %v\n%s", r, debug.Stack())
		}
	}()
	src = validUTF8(src)
	s := newSource(name, src)
	prog, err := parseProgram(src)
	if err != nil {
		return nil, vm.syntaxException(s, err.(*syntaxErr))
	}
	if err := resolveProgram(prog); err != nil {
		return nil, vm.syntaxException(s, err.(*syntaxErr))
	}
	code, err := vm.compileProgram(s, prog)
	if err != nil {
		return nil, vm.syntaxException(s, err.(*syntaxErr))
	}
	return &object{shape: vm.funcRoot, class: classFunction, inner: &closure{code: code}}, nil
}

func (vm *VM) RunCell(name, src string) (Value, error) {
	fn, err := vm.compile(name, src)
	if err != nil {
		return undefined, err
	}
	vm.reapDead()
	vm.cellID++
	var res Value
	err = vm.guard(func() { res = vm.callObj(fn, objV(vm.global), nil) })
	if err != nil {
		if errors.Is(err, ErrInterrupted) {
			vm.killCell(vm.cellID)
		}
		return undefined, err
	}
	return res, nil
}

func (vm *VM) RunJobs() error {
	vm.reapDead()
	err := vm.guard(vm.runJobs)
	if errors.Is(err, ErrInterrupted) {
		vm.killCell(vm.cellID)
	}
	return err
}

func (vm *VM) PendingJobs() int { return len(vm.jobs) - vm.jobHead }

func (vm *VM) SetFunc(name string, fn HostFunc) {
	vm.setGlobal(name, objV(vm.newNative(name, 0, func(vm *VM, this Value, args []Value) Value {
		cp := append([]Value(nil), args...)
		v, err := fn(vm, cp)
		if err != nil {
			var ex *Exception
			if errors.As(err, &ex) && ex.Value.r != nil {
				vm.throw(ex.Value)
			}
			vm.throwError("Error", validUTF8(err.Error()))
		}
		return v
	})))
}

func (vm *VM) Set(name string, v Value) {
	b := vm.globals.bind(name)
	b.v, b.present = v, true
}

func (vm *VM) Get(name string) Value {
	if b := vm.globals.lookup(name); b != nil {
		return b.v
	}
	return undefined
}

func (vm *VM) Call(name string, args ...Value) (Value, error) {
	return vm.CallValue(vm.Get(name), undefined, args...)
}

func (vm *VM) CallValue(fn, this Value, args ...Value) (Value, error) {
	var res Value
	err := vm.guard(func() { res = vm.call(fn, this, args) })
	return res, err
}

func (vm *VM) Interrupt() { vm.intr.Store(1) }

func (vm *VM) ClearInterrupt() { vm.intr.Store(0) }

func (vm *VM) NewPromise() (Value, func(Value), func(Value)) {
	p := &object{shape: vm.promiseProto.rootShape(), class: classPromise, inner: &promise{}}
	return objV(p), func(v Value) { vm.resolvePromise(p, v) }, func(v Value) { vm.rejectPromise(p, v) }
}

func (vm *VM) PromiseResult(v Value) (PromiseState, Value) {
	p := promiseOf(v)
	if p == nil {
		return PromiseFulfilled, v
	}
	switch p.state {
	case fulfilled:
		return PromiseFulfilled, p.value
	case rejected:
		return PromiseRejected, p.value
	}
	return PromisePending, undefined
}

func (vm *VM) IsPromise(v Value) bool { return promiseOf(v) != nil }

func (vm *VM) Exception(v Value) *Exception { return vm.exceptionFrom(v) }

func (vm *VM) NewError(name, msg string) Value {
	o := vm.makeError("Error", validUTF8(msg))
	if name != "Error" {
		if p := vm.errorProtos[name]; p != nil {
			o.setProto(p)
		} else {
			o.defineHidden("name", strV(name))
		}
	}
	return objV(o)
}

func (vm *VM) NewObject() Value { return objV(vm.newObject()) }

func (vm *VM) NewArray(vals ...Value) Value {
	return objV(&object{shape: vm.arrayRoot, arr: append([]Value(nil), vals...), class: classArray})
}

func (vm *VM) GetProp(obj Value, key string) (Value, error) {
	var res Value
	err := vm.guard(func() { res = vm.getV(obj, strKey(key)) })
	return res, err
}

func (vm *VM) SetProp(obj Value, key string, v Value) error {
	return vm.guard(func() { vm.setV(obj, strKey(key), v) })
}

func (vm *VM) JSONStringify(v Value) (string, error) {
	var s string
	err := vm.guard(func() { s = vm.jsonStringify(v, undefined, "") })
	return s, err
}

func (vm *VM) JSONParse(s string) (Value, error) {
	var res Value
	err := vm.guard(func() { res = vm.jsonParse(validUTF8(s)) })
	return res, err
}

func (vm *VM) ToString(v Value) (string, error) {
	var s string
	err := vm.guard(func() { s = vm.toString(v) })
	return s, err
}

func (vm *VM) Close() {
	vm.killCoroutines()
	vm.jobs, vm.jobHead = nil, 0
}

func (vm *VM) AbandonPending() {
	vm.killCoroutines()
	vm.jobs, vm.jobHead = vm.jobs[:0], 0
}
