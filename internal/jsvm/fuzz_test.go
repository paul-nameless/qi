package jsvm

import (
	"errors"
	"sync"
	"testing"
	"time"
)

var fuzzSeeds = []string{
	"let s = 0; for (let i = 0; i < 10; i++) s += i % 7; return s",
	"const {a, b: [c = 1, ...d]} = {a: 1, b: [2, 3]}; `x${a}y`; a?.b?.[c]?.(d)",
	"const f = async (x, {y} = {}) => { try { await x } catch { } finally { return y ?? 1 } }; await f(1)",
	"switch (1) { case 1: break; default: }",
	"/re[/]g/gi.test('s') ? 1 : 2; let x = 4 / 2 / 1",
	"({a: 1, b: 1, ['k']: 2, ...{r: 1}, m() { return this }}).m()",
	"for (const [k, v] of new Map([[1, 2]])) for (const q in {k}) while (0) do ; while (0)",
	"let a = 1\n++a\nreturn a",
	"JSON.stringify(JSON.parse('{\"a\":[1,2,{\"b\":null}]}'), null, 2)",
	"const d = new Date(Date.UTC(2024, 1, 29)); d.setUTCDate(d.getUTCDate() + 1); d.toISOString()",
	"'ß'.toUpperCase() + (0.1).toString(3) + encodeURIComponent('é') + [3, 1, 2].sort().join()",
	"let s = ''; for (let i = 0; i < 100; i++) s += i; s.replace(/\\d+/g, m => m.length).split('1').length",
	"function f(n) { return n < 1 ? 0 : f(n - 1) + 1 } f(50)",
	"Promise.all([1, 2].map(async x => x * 2)).then(r => r.join())",
}

func FuzzCompile(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		if len(src) > 4096 {
			return
		}
		vm := New(Options{})
		_, err := vm.compile("fuzz.js", src)
		var ex *Exception
		if err != nil && !errors.As(err, &ex) {
			t.Fatalf("compile %q: %v", src, err)
		}
	})
}

func FuzzRun(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		if len(src) > 2048 {
			return
		}
		vm := New(Options{MemoryLimit: 64 << 20, MaxDepth: 200})
		defer vm.Close()
		var mu sync.Mutex
		done := false
		timer := time.AfterFunc(20*time.Millisecond, func() {
			mu.Lock()
			if !done {
				vm.Interrupt()
			}
			mu.Unlock()
		})
		defer timer.Stop()
		check := func(stage string, err error) {
			var ex *Exception
			if err != nil && !errors.As(err, &ex) && !errors.Is(err, ErrInterrupted) {
				t.Fatalf("%s %q: %v", stage, src, err)
			}
			if ex != nil && ex.Name == "InternalError" {
				t.Fatalf("%s %q: internal error %v", stage, src, err)
			}
		}
		_, err := vm.RunCell("fuzz.js", src)
		check("run", err)
		check("jobs", vm.RunJobs())
		mu.Lock()
		done = true
		mu.Unlock()
		vm.ClearInterrupt()
		v, err := vm.RunCell("after.js", "1 + 1")
		if err != nil || ToString(v) != "2" {
			t.Fatalf("VM unusable after %q: %v %v", src, ToString(v), err)
		}
	})
}
