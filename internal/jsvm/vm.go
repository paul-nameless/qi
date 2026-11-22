package jsvm

import (
	"runtime"
	"runtime/metrics"
	"strings"
	"sync/atomic"
)

type ctl uint8

const (
	ctlNone ctl = iota
	ctlBreak
	ctlContinue
	ctlReturn
)

type expr func(*frame) Value
type cond func(*frame) bool
type stmt func(*frame) ctl

type frame struct {
	vm     *VM
	slots  []Value
	cells  []*Value
	ups    []*Value
	this   Value
	fnObj  *object
	code   *funcCode
	parent *frame
	ret    Value
	co     *coro
	pos    int32
}

type jsThrow struct{ v Value }

type interruptSignal struct{}

type killSignal struct{}

type VM struct {
	global       *object
	globals      *globalStore
	objectRoot   *hclass
	arrayRoot    *hclass
	funcRoot     *hclass
	nullRoot     *hclass
	objectProto  *object
	funcProto    *object
	arrayProto   *object
	stringProto  *object
	numberProto  *object
	boolProto    *object
	errorProto   *object
	errorProtos  map[string]*object
	mapProto     *object
	setProto     *object
	promiseProto *object
	regexpProto  *object
	cellID       int
	dead         deadList
	dateProto    *object
	regexpRoot   *hclass
	joining      map[*object]bool
	iterProto    *object
	symbolProto  *object
	stack        []Value
	sp           int
	framePool    []*frame
	top          *frame
	depth        int
	maxDepth     int
	intr         atomic.Int32
	jobs         []job
	jobHead      int
	memLimit     int64
	tracked      int64
	nextCheck    int64
	stdout       func(level, text string)
	coros        map[*coro]struct{}
	memSample    []metrics.Sample
}

func (vm *VM) alloc(n int) []Value {
	if vm.sp+n > len(vm.stack) {
		size := len(vm.stack) * 2
		if size < vm.sp+n+64 {
			size = vm.sp + n + 64
		}
		vm.stack = make([]Value, size)
	}
	s := vm.stack[vm.sp : vm.sp+n : vm.sp+n]
	vm.sp += n
	return s
}

func (vm *VM) getFrame() *frame {
	if n := len(vm.framePool); n > 0 {
		f := vm.framePool[n-1]
		vm.framePool = vm.framePool[:n-1]
		return f
	}
	return &frame{vm: vm}
}

func (vm *VM) putFrame(f *frame) {
	f.slots, f.cells, f.ups, f.fnObj, f.code, f.parent = nil, nil, nil, nil, nil, nil
	f.this, f.ret, f.pos = undefined, undefined, 0
	vm.framePool = append(vm.framePool, f)
}

func (vm *VM) throw(v Value) {
	panic(&jsThrow{v})
}

func (vm *VM) throwError(kind, msg string) {
	panic(&jsThrow{objV(vm.makeError(kind, msg))})
}

func (vm *VM) throwTypeError(msg string) { vm.throwError("TypeError", msg) }

func (vm *VM) enterNative() {
	if vm.depth >= vm.maxDepth {
		vm.throwError("RangeError", "Maximum call stack size exceeded")
	}
	vm.depth++
}

func (vm *VM) checkInterrupt() {
	if vm.intr.Load() != 0 {
		panic(&interruptSignal{})
	}
}

func (vm *VM) track(n int) {
	vm.tracked += int64(n)
	if vm.tracked >= vm.nextCheck {
		vm.checkMemory()
	}
}

const memCheckEvery = 16 << 20

func (vm *VM) checkMemory() {
	vm.nextCheck = vm.tracked + memCheckEvery
	clear(vm.stack[vm.sp:])
	if vm.memLimit <= 0 {
		return
	}
	if vm.heapBytes() < uint64(vm.memLimit) {
		return
	}
	forceGC()
	if vm.heapBytes() < uint64(vm.memLimit) {
		return
	}
	vm.throwError("RangeError", "out of memory: heap limit exceeded")
}

func (vm *VM) reserve(n int) {
	if vm.memLimit <= 0 || n < 1<<20 {
		return
	}
	if vm.heapBytes()+uint64(n) < uint64(vm.memLimit) {
		return
	}
	forceGC()
	if vm.heapBytes()+uint64(n) < uint64(vm.memLimit) {
		return
	}
	vm.throwError("RangeError", "out of memory: heap limit exceeded")
}

func (vm *VM) reserveValues(n int) { vm.reserve(n * 24) }

func (vm *VM) ownKeys(o *object, enumOnly bool) []propKey {
	if o.class == classArray && len(o.arr) > 1<<16 {
		vm.reserve(len(o.arr) * 96)
		vm.track(len(o.arr) * 96)
	}
	return o.ownKeys(enumOnly)
}

func (vm *VM) flat(s *jstr) string {
	if s.isRope() {
		vm.reserve(s.size())
		vm.track(s.size())
	}
	return s.flat()
}

func (b *sbuf) addByte(c byte) {
	if b.Len() >= b.Cap() {
		if b.Len()+1 > maxStringLen {
			b.vm.throwError("RangeError", "Invalid string length")
		}
		b.vm.reserve(2 * (b.Len() + 1))
	}
	b.WriteByte(c)
}

func (vm *VM) appendV(dst []Value, vs ...Value) []Value {
	need := len(dst) + len(vs)
	if need <= cap(dst) {
		return append(dst, vs...)
	}
	if need > 1<<14 {
		vm.reserveValues(need + need/4)
		vm.track((need - cap(dst)) * 24)
	}
	return append(dst, vs...)
}

type sbuf struct {
	strings.Builder
	vm *VM
}

func (b *sbuf) add(s string) {
	need := b.Len() + len(s)
	if need > maxStringLen {
		b.vm.throwError("RangeError", "Invalid string length")
	}
	if need > b.Cap() {
		b.vm.reserve(2 * need)
	}
	b.WriteString(s)
}

func (b *sbuf) value() Value {
	b.vm.track(b.Len())
	return strV(b.String())
}

func (vm *VM) heapBytes() uint64 {
	if vm.memSample == nil {
		vm.memSample = []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}, {Name: "/memory/classes/heap/stacks:bytes"}}
	}
	metrics.Read(vm.memSample)
	var total uint64
	for _, s := range vm.memSample {
		if s.Value.Kind() == metrics.KindUint64 {
			total += s.Value.Uint64()
		}
	}
	return total
}

type vmState struct {
	top   *frame
	depth int
	sp    int
	stack []Value
}

func (vm *VM) save() vmState { return vmState{vm.top, vm.depth, vm.sp, vm.stack} }

func (vm *VM) restore(s vmState) {
	vm.top, vm.depth, vm.sp, vm.stack = s.top, s.depth, s.sp, s.stack
}

func (vm *VM) protect(fn func()) (exc *jsThrow) {
	st := vm.save()
	defer func() {
		if r := recover(); r != nil {
			if t, ok := vm.asThrow(r); ok {
				vm.restore(st)
				exc = t
				return
			}
			panic(r)
		}
	}()
	fn()
	return nil
}

func (vm *VM) asThrow(r any) (*jsThrow, bool) {
	switch t := r.(type) {
	case *jsThrow:
		return t, true
	case runtime.Error:
		o := vm.makeError("Error", "internal error: "+t.Error())
		o.defineHidden("name", strV("InternalError"))
		return &jsThrow{objV(o)}, true
	}
	return nil, false
}

func (vm *VM) call(fnV Value, this Value, args []Value) Value {
	o, ok := fnV.r.(*object)
	if !ok || o.class != classFunction {
		vm.throwTypeError(describe(fnV) + " is not a function")
	}
	return vm.callObj(o, this, args)
}

func (vm *VM) callObj(o *object, this Value, args []Value) Value {
	switch fn := o.inner.(type) {
	case *closure:
		return vm.callClosure(o, fn, this, args)
	case *nativeFn:
		if fn.fn == nil {
			vm.throwTypeError("Constructor " + fn.name + " requires 'new'")
		}
		if vm.depth >= vm.maxDepth {
			vm.throwError("RangeError", "Maximum call stack size exceeded")
		}
		vm.depth++
		r := fn.fn(vm, this, args)
		vm.depth--
		return r
	case *boundFn:
		all := make([]Value, 0, len(fn.args)+len(args))
		all = append(append(all, fn.args...), args...)
		vm.enterNative()
		r := vm.callObj(fn.target, fn.this, all)
		vm.depth--
		return r
	}
	vm.throwTypeError("not a function")
	return undefined
}

func describe(v Value) string {
	switch r := v.r.(type) {
	case *jstr:
		if r.n > 80 {
			return "\"" + r.substring(0, min(r.length(), 60)) + "...\""
		}
		return "\"" + r.flat() + "\""
	case *object:
		return "object"
	}
	return ToString(v)
}

func (vm *VM) callClosure(fo *object, cl *closure, this Value, args []Value) Value {
	code := cl.code
	if code.async {
		return vm.callAsync(fo, cl, this, args)
	}
	if vm.depth >= vm.maxDepth {
		vm.throwError("RangeError", "Maximum call stack size exceeded")
	}
	if vm.intr.Load() != 0 {
		panic(&interruptSignal{})
	}
	f := vm.getFrame()
	f.code, f.fnObj, f.ups, f.parent = code, fo, cl.ups, vm.top
	if code.arrow {
		f.this = cl.this
	} else {
		f.this = this
	}
	sp := vm.sp
	f.slots = vm.alloc(code.nslots)
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
	vm.top = f.parent
	vm.depth--
	clear(f.slots)
	vm.sp = sp
	vm.putFrame(f)
	return ret
}

func (vm *VM) construct(fnV Value, args []Value, newTarget *object) Value {
	o, ok := fnV.r.(*object)
	if !ok || o.class != classFunction {
		vm.throwTypeError(describe(fnV) + " is not a constructor")
	}
	if newTarget == nil {
		newTarget = o
	}
	switch fn := o.inner.(type) {
	case *nativeFn:
		if fn.ctor == nil {
			vm.throwTypeError(fn.name + " is not a constructor")
		}
		return fn.ctor(vm, args, newTarget)
	case *closure:
		if fn.code.arrow || fn.code.async || fn.code.method {
			vm.throwTypeError(fn.code.name + " is not a constructor")
		}
		proto := vm.getV(objV(newTarget), strKey("prototype"))
		var obj *object
		if po, ok := proto.r.(*object); ok {
			obj = vm.newObjectProto(po)
		} else {
			obj = vm.newObject()
		}
		r := vm.callClosure(o, fn, objV(obj), args)
		if _, ok := r.r.(*object); ok {
			return r
		}
		return objV(obj)
	case *boundFn:
		all := append(append([]Value{}, fn.args...), args...)
		vm.enterNative()
		r := vm.construct(objV(fn.target), all, nil)
		vm.depth--
		return r
	}
	vm.throwTypeError("not a constructor")
	return undefined
}

type job struct {
	r   *reaction
	v   Value
	fn  func()
	rej bool
}

func (vm *VM) enqueue(j job) { vm.jobs = append(vm.jobs, j) }

func (vm *VM) runJobs() {
	for vm.jobHead < len(vm.jobs) {
		j := vm.jobs[vm.jobHead]
		vm.jobs[vm.jobHead] = job{}
		vm.jobHead++
		vm.runJob(j)
	}
	vm.jobs = vm.jobs[:0]
	vm.jobHead = 0
}
