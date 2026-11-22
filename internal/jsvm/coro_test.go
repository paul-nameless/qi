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
