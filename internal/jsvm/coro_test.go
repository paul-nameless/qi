package jsvm

import (
	"runtime"
	"testing"
	"time"
)

func settleGoroutines(t *testing.T, vm *VM, want int) int {
	t.Helper()
	n := 0
	for i := 0; i < 50; i++ {
		runtime.GC()
		vm.RunJobs()
		if n = runtime.NumGoroutine(); n <= want {
			return n
		}
		time.Sleep(10 * time.Millisecond)
	}
	return n
}

func TestParkedCoroutinesReleased(t *testing.T) {
	vm := New(Options{})
	if _, err := vm.RunCell("cell.js", "1"); err != nil {
		t.Fatal(err)
	}
	base := settleGoroutines(t, vm, 0)
	for i := 0; i < 20; i++ {
		p, err := vm.RunCell("cell.js", "globalThis.n = (globalThis.n || 0) + 1; await new Promise(() => {}); globalThis.after = 1")
		if err != nil {
			t.Fatal(err)
		}
		vm.RunJobs()
		if st, _ := vm.PromiseResult(p); st != PromisePending {
			t.Fatalf("cell settled: %v", st)
		}
	}
	if got := settleGoroutines(t, vm, base); got > base {
		t.Errorf("goroutines after never-settling cells: %d, baseline %d, coros %d", got, base, len(vm.coros))
	}
	v, err := vm.RunCell("cell.js", "[n, typeof after]")
	if s, _ := vm.JSONStringify(v); err != nil || s != `[20,"undefined"]` {
		t.Errorf("state after release: %s %v", s, err)
	}
	keep, err := vm.RunCell("cell.js", "globalThis.pending = new Promise(r => { globalThis.resolveLater = r }); (async () => { globalThis.got = await pending })(); 1")
	if err != nil || ToString(keep) != "1" {
		t.Fatal(err)
	}
	timer := time.AfterFunc(20*time.Millisecond, vm.Interrupt)
	_, err = vm.RunCell("cell.js", "const p2 = (async () => { await new Promise(() => {}) })(); while (true) {}")
	timer.Stop()
	vm.ClearInterrupt()
	if err != ErrInterrupted {
		t.Fatalf("want interrupt, got %v", err)
	}
	if got := settleGoroutines(t, vm, base+1); got > base+1 {
		t.Errorf("goroutines after interrupted cell: %d, want %d", got, base+1)
	}
	if _, err := vm.RunCell("cell.js", "resolveLater(42)"); err != nil {
		t.Fatal(err)
	}
	vm.RunJobs()
	if v, _ := vm.RunCell("cell.js", "got"); ToString(v) != "42" {
		t.Errorf("reachable pending activation was released: got %s", ToString(v))
	}
	if got := settleGoroutines(t, vm, base); got > base {
		t.Errorf("goroutines at end: %d, baseline %d", got, base)
	}
}

func gcSettle() {
	for i := 0; i < 3; i++ {
		runtime.GC()
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSettledAwaitSurvivesGC(t *testing.T) {
	vm := New(Options{})
	steps := []string{
		"globalThis.done = 0; (async () => { await 1; await Promise.resolve(2); await new Promise(res => { globalThis.r = res }); done = 1 })(); 1",
		"r(); r = null; 1",
		"globalThis.q = Promise.resolve(3); (async () => { await q; done = 2 })(); q = null; 1",
	}
	want := []string{"0", "1", "2"}
	for i, src := range steps {
		if _, err := vm.RunCell("cell.js", src); err != nil {
			t.Fatal(err)
		}
		gcSettle()
		if err := vm.RunJobs(); err != nil {
			t.Fatal(err)
		}
		gcSettle()
		vm.RunJobs()
		v, _ := vm.RunCell("cell.js", "done")
		if ToString(v) != want[i] {
			t.Fatalf("step %d: done = %s, want %s (coros %d)", i, ToString(v), want[i], len(vm.coros))
		}
	}
}

func TestInterruptedJobsCancelLatestCell(t *testing.T) {
	vm := New(Options{})
	if _, err := vm.RunCell("cell.js", "globalThis.out = 0; (async () => { await new Promise(res => { globalThis.r = res }); out = 1 })(); 1"); err != nil {
		t.Fatal(err)
	}
	if _, err := vm.RunCell("cell.js", "Promise.resolve().then(() => { while (true) {} }); 1"); err != nil {
		t.Fatal(err)
	}
	timer := time.AfterFunc(20*time.Millisecond, vm.Interrupt)
	err := vm.RunJobs()
	timer.Stop()
	vm.ClearInterrupt()
	if err != ErrInterrupted {
		t.Fatalf("want interrupt, got %v", err)
	}
	vm.RunCell("cell.js", "r()")
	vm.RunJobs()
	if v, _ := vm.RunCell("cell.js", "out"); ToString(v) != "1" {
		t.Errorf("earlier cell's coroutine killed: out = %s", ToString(v))
	}
	vm.RunCell("cell.js", "globalThis.out = 0; (async () => { await new Promise(res => { globalThis.r = res }); out = 2 })(); Promise.resolve().then(() => { while (true) {} }); 1")
	timer = time.AfterFunc(20*time.Millisecond, vm.Interrupt)
	err = vm.RunJobs()
	timer.Stop()
	vm.ClearInterrupt()
	if err != ErrInterrupted {
		t.Fatalf("want interrupt, got %v", err)
	}
	vm.RunCell("cell.js", "r()")
	vm.RunJobs()
	if v, _ := vm.RunCell("cell.js", "out"); ToString(v) != "0" {
		t.Errorf("latest cell's coroutine resumed after interrupt: out = %s", ToString(v))
	}
}

func TestCallArgsNotRetained(t *testing.T) {
	vm := New(Options{})
	if _, err := vm.RunCell("cell.js", "1"); err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	v, err := vm.RunCell("cell.js", "JSON.parse(JSON.stringify(Array.from({length: 200000}, (_, i) => ({i, s: 'v' + i})))).length")
	if err != nil || ToString(v) != "200000" {
		t.Fatal(ToString(v), err)
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	if grow := int64(after.HeapAlloc) - int64(before.HeapAlloc); grow > 8<<20 {
		t.Errorf("heap grew by %d MB after the cell; temporaries retained", grow>>20)
	}
	runtime.KeepAlive(vm)
}

func TestInterruptInsideToString(t *testing.T) {
	for _, src := range []string{
		"throw {toString() { for (;;) {} }}",
		"try { [].map({toString() { for (;;) {} }}) } catch (e) { 'caught' }",
	} {
		vm := New(Options{})
		timer := time.AfterFunc(30*time.Millisecond, vm.Interrupt)
		_, err := vm.RunCell("cell.js", src)
		timer.Stop()
		if err != ErrInterrupted {
			t.Errorf("%s: got %v, want ErrInterrupted", src, err)
		}
		vm.ClearInterrupt()
		if v, err := vm.RunCell("cell.js", "1 + 1"); err != nil || ToString(v) != "2" {
			t.Errorf("after interrupt: %v %v", ToString(v), err)
		}
	}
}

func TestShapeTreeReleased(t *testing.T) {
	vm := New(Options{})
	vm.RunCell("cell.js", "1")
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	v, err := vm.RunCell("cell.js", "let n = 0; for (let r = 0; r < 2000; r++) { const idx = {}; for (let c = 0; c < 30; c++) idx['id' + r + '_' + c] = c; n += Object.keys(JSON.parse(JSON.stringify(idx))).length } n")
	if err != nil || ToString(v) != "60000" {
		t.Fatal(ToString(v), err)
	}
	runtime.GC()
	runtime.GC()
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(vm)
	if grow := int64(after.HeapAlloc) - int64(before.HeapAlloc); grow > 8<<20 {
		t.Errorf("heap grew by %d MB: shapes of dead objects retained", grow>>20)
	}
	v, err = vm.RunCell("cell.js", "const a = {x: 1, y: 2}, b = {x: 3, y: 4}; const g = o => o.y; [g(a), g(b), Object.keys(JSON.parse('{\"x\":1,\"y\":2}')).join()].join()")
	if err != nil || ToString(v) != "2,4,x,y" {
		t.Error(ToString(v), err)
	}
}

func TestMicrotaskFloodHitsLimit(t *testing.T) {
	vm := New(Options{MemoryLimit: 256 << 20})
	_, err := vm.RunCell("cell.js", "const f = () => {}; while (true) queueMicrotask(f)")
	if errName(err) != "RangeError" {
		t.Errorf("want RangeError, got %v", err)
	}
	vm.AbandonPending()
}

func TestNamedErrorStacks(t *testing.T) {
	vm := New(Options{})
	if s := vm.Exception(vm.NewError("TypeError", "x")).Error(); s != "TypeError: x" {
		t.Errorf("NewError: %q", s)
	}
	if s := vm.Exception(vm.NewError("ToolError", "boom")).Error(); s != "ToolError: boom" {
		t.Errorf("NewError custom: %q", s)
	}
	_, err := vm.RunCell("cell.js", "const e = new Error('m'); e.name = 'ValidationError'; throw e")
	if err == nil || err.Error() != "ValidationError: m\n    at cell.js:1:11" {
		t.Errorf("renamed error: %q", err)
	}
}

func TestInterruptInNestedHostCall(t *testing.T) {
	nested := map[string]func(vm *VM, args []Value) (Value, error){
		"CallValue": func(vm *VM, args []Value) (Value, error) { return vm.CallValue(args[0], Undefined()) },
		"Call":      func(vm *VM, args []Value) (Value, error) { return vm.Call("spin") },
		"JSONStringify": func(vm *VM, args []Value) (Value, error) {
			s, err := vm.JSONStringify(args[1])
			return Str(s), err
		},
	}
	for name, fn := range nested {
		vm := New(Options{})
		vm.SetFunc("nested", fn)
		timer := time.AfterFunc(30*time.Millisecond, vm.Interrupt)
		v, err := vm.RunCell("cell.js", "globalThis.spin = () => { for (;;) {} }; let r = 'none'; try { nested(spin, {toJSON() { for (;;) {} }}) } catch (e) { r = 'caught ' + e.message } r")
		timer.Stop()
		if err != ErrInterrupted {
			t.Errorf("%s: got %q %v, want ErrInterrupted", name, ToString(v), err)
		}
		vm.ClearInterrupt()
		if v, err := vm.RunCell("cell.js", "1 + 1"); err != nil || ToString(v) != "2" {
			t.Errorf("%s: after interrupt: %v %v", name, ToString(v), err)
		}
	}
}

func TestJobsQueuedBeforeThrowStayQueued(t *testing.T) {
	vm := New(Options{})
	_, err := vm.RunCell("cell.js", "globalThis.ran = 0; Promise.resolve().then(() => { ran = 1 }); (async () => { await null; ran += 10 })(); throw new Error('boom')")
	if errName(err) != "Error" {
		t.Fatalf("want Error, got %v", err)
	}
	if n := vm.PendingJobs(); n != 2 {
		t.Fatalf("pending jobs after failed cell: %d, want 2", n)
	}
	if err := vm.RunJobs(); err != nil {
		t.Fatal(err)
	}
	if v, _ := vm.RunCell("cell.js", "ran"); ToString(v) != "11" {
		t.Errorf("ran = %s, want 11", ToString(v))
	}
	vm.RunCell("cell.js", "Promise.resolve().then(() => { ran = 99 }); throw 1")
	vm.AbandonPending()
	vm.RunJobs()
	if v, _ := vm.RunCell("cell.js", "ran"); ToString(v) != "11" {
		t.Errorf("after AbandonPending ran = %s, want 11", ToString(v))
	}
}
