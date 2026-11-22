package jsvm

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func evalJSON(t *testing.T, vm *VM, src string) (string, error) {
	t.Helper()
	v, err := vm.RunCell("cell.js", src)
	if err != nil {
		return "", err
	}
	if v.Kind() == KindUndefined {
		return "undefined", nil
	}
	return vm.JSONStringify(v)
}

func TestExpressions(t *testing.T) {
	cases := []struct{ src, want string }{
		{"1 + 2 * 3", "7"},
		{"'a' + 1 + 2", `"a12"`},
		{"[1, 2, 3].map(x => x * x)", "[1,4,9]"},
		{"let o = {a: 1, b: [2]}; o.b.push(3); o", `{"a":1,"b":[2,3]}`},
		{"const {a, ...r} = {a: 1, b: 2, c: 3}; [a, r]", `[1,{"b":2,"c":3}]`},
		{"`x${1 + 1}y`", `"x2y"`},
		{"[typeof null, typeof undefined, typeof (() => 1), typeof 1n === 'x']", ""},
		{"null ?? 'd'", `"d"`},
		{"({a: {b: 1}})?.a?.c?.d", "undefined"},
		{"[...new Set([3, 3, 1])]", "[3,1]"},
		{"Object.entries({x: 1}).map(([k, v]) => k + v).join()", `"x1"`},
		{"[10, 9, 1].sort()", "[1,10,9]"},
		{"[0.1 + 0.2, 1e21, 2 ** 53, -0]", "[0.30000000000000004,1e+21,9007199254740992,0]"},
		{"'héllo'.length + '😀'.length", "7"},
		{"parseInt('08') + Number('0x10')", "24"},
		{"JSON.parse('{\"2\":1,\"a\":2,\"1\":3}')", `{"1":3,"2":1,"a":2}`},
		{"let s = 0; for (const k in {b: 1, a: 2}) s += k.length; s", "2"},
		{"let r = []; for (let i = 0; i < 3; i++) r.push(() => i); r.map(f => f())", "[0,1,2]"},
		{"function f(a, b = a + 1, ...c) { return [a, b, c] } f(1)", "[1,2,[]]"},
		{"(() => { try { throw new TypeError('x') } catch (e) { return [e.name, e instanceof Error] } })()", `["TypeError",true]`},
		{"let n = 0; switch (2) { case 1: n = 1; case 2: n += 2; case 3: n += 3; break; default: n = 9 } n", "5"},
		{"'a-b-c'.split('-').reverse().join('+')", `"c+b+a"`},
		{"'x'.padStart(3, 'ab') + (12.345).toFixed(1)", `"abx12.3"`},
	}
	for _, c := range cases {
		if c.want == "" {
			continue
		}
		vm := New(Options{})
		got, err := evalJSON(t, vm, c.src)
		if err != nil {
			t.Errorf("%s: %v", c.src, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s = %s, want %s", c.src, got, c.want)
		}
	}
}

func TestSyntaxErrors(t *testing.T) {
	cases := []struct {
		src, msg string
		line     int
	}{
		{"const a = 1;\nconst b = ;", "Unexpected token ';'", 2},
		{"let x = 1;\nlet x = 2;", "Identifier 'x' has already been declared", 2},
		{"foo(", "Unexpected end of input", 1},
		{"\n\n'unterminated", "Invalid or unexpected token", 3},
		{"break", "Illegal break statement", 1},
		{"function f() { await 1 }", "Unexpected", 1},
		{"x = 08", "Octal literals are not allowed in strict mode.", 1},
		{"({a = 1})", "Invalid shorthand property initializer", 1},
		{"a + = 1", "Unexpected token '='", 1},
		{"/(/", "Invalid regular expression", 1},
		{"while (x) let y = 1", "Lexical declaration cannot appear in a single-statement context", 1},
	}
	for _, c := range cases {
		vm := New(Options{})
		_, err := vm.RunCell("cell.js", c.src)
		var ex *Exception
		if !errors.As(err, &ex) {
			t.Errorf("%q: want SyntaxError, got %v", c.src, err)
			continue
		}
		if ex.Name != "SyntaxError" || !strings.Contains(ex.Message, c.msg) || ex.Line != c.line {
			t.Errorf("%q: got %s: %s (line %d), want %q at line %d", c.src, ex.Name, ex.Message, ex.Line, c.msg, c.line)
		}
	}
}

func TestUnsupported(t *testing.T) {
	cases := map[string]string{
		"class A {}":                  "classes",
		"x = 1;\nfunction* g() {}":    "generators",
		"({ get a() { return 1 } })":  "getters/setters",
		"outer: for (;;) break outer": "labels",
		"tag`x`":                      "tagged templates other than String.raw",
		"async function f() { for await (const x of y) {} }": "for await",
		"function f() { return arguments }":                  "arguments",
		"1n":                                                 "BigInt",
		"import x from 'y'":                                  "modules (import)",
		"await import('fs')":                                 "modules (import)",
		"with (o) {}":                                        "with",
		"eval('1')":                                          "eval",
		"o.#x":                                               "classes (private names)",
		"using r = f()":                                      "using declarations",
	}
	for src, what := range cases {
		vm := New(Options{})
		_, err := vm.RunCell("cell.js", src)
		var ex *Exception
		if !errors.As(err, &ex) || ex.Name != "SyntaxError" || ex.Message != "unsupported: "+what || ex.Line == 0 {
			t.Errorf("%q: got %v, want SyntaxError: unsupported: %s", src, err, what)
		}
	}
	vm := New(Options{})
	for src, want := range map[string]string{
		"new Date().toLocaleString()":                "TypeError: unsupported: Date.prototype.toLocaleString",
		"Symbol('x')":                                "ReferenceError: Symbol is not defined",
		"new WeakMap()":                              "ReferenceError: WeakMap is not defined",
		"Object.defineProperty({}, 'x', {get() {}})": "TypeError: unsupported: accessor properties",
		"new Function('return 1')":                   "EvalError: unsupported: Function constructor",
	} {
		_, err := vm.RunCell("cell.js", src)
		if err == nil || !strings.HasPrefix(err.Error(), want) {
			t.Errorf("%q: got %v, want %s", src, err, want)
		}
	}
}

func TestStacks(t *testing.T) {
	vm := New(Options{})
	_, err := vm.RunCell("cell.js", "function g(){ undefinedFn() }\nfunction h(){ g() }\nh()")
	var ex *Exception
	if !errors.As(err, &ex) {
		t.Fatalf("got %v", err)
	}
	want := "ReferenceError: undefinedFn is not defined\n    at g (cell.js:1:15)\n    at h (cell.js:2:15)\n    at cell.js:3:1"
	if ex.Stack != want || ex.Line != 1 || ex.Column != 15 || ex.Name != "ReferenceError" {
		t.Errorf("stack %q line %d col %d", ex.Stack, ex.Line, ex.Column)
	}
	v, err := vm.RunCell("cell.js", "const a = 1;\nconst b = 2;\ntry { null.x } catch (e) { return e.stack }")
	if err != nil || ToString(v) != "TypeError: Cannot read properties of null (reading 'x')\n    at cell.js:3:12" {
		t.Errorf("got %q %v", ToString(v), err)
	}
	v, err = vm.RunCell("cell.js", "const f = () => new Error('m');\nf().stack.split('\\n').filter(l => /^\\s+at /.test(l)).length")
	if err != nil || ToString(v) != "2" {
		t.Errorf("filtered frames %q %v", ToString(v), err)
	}
	_, err = vm.RunCell("cell.js", "throw 'plain'")
	if err == nil || err.Error() != "Uncaught plain" {
		t.Errorf("non-error throw: %v", err)
	}
}

func TestCellSemantics(t *testing.T) {
	vm := New(Options{})
	steps := []struct{ src, want string }{
		{"const a = 1; let b = 2; var c = 3; function f() { return a + b } const {d, e: [g]} = {d: 4, e: [5]}", "undefined"},
		{"[a, b, c, f(), d, g, typeof globalThis.a]", `[1,2,3,3,4,5,"number"]`},
		{"const a = 100; f()", "102"},
		{"let b = 'x'; b + f()", `"x100x"`},
		{"if (a > 0) { return 'early' }\nreturn 'late'", `"early"`},
		{"implicit = 7; implicit", "7"},
		{"const kept = 1; null.boom", ""},
		{"kept", "1"},
		{"a = 5", ""},
		{"1; 2; 'last'", `"last"`},
		{"const z = 1;", "undefined"},
	}
	for i, s := range steps {
		got, err := evalJSON(t, vm, s.src)
		if s.want == "" {
			if err == nil {
				t.Errorf("step %d: want error", i)
			}
			continue
		}
		if err != nil || got != s.want {
			t.Errorf("step %d %q: got %s %v, want %s", i, s.src, got, err, s.want)
		}
	}
}

func TestInterrupt(t *testing.T) {
	vm := New(Options{})
	for _, src := range []string{
		"while (true) {}",
		"try { while (true) {} } catch (e) { return 'caught' } finally { globalThis.fin = 1 }",
		"function f() { return f() } for (;;) { try { f() } catch (e) {} }",
		"const a = []; for (;;) a.sort()",
	} {
		timer := time.AfterFunc(30*time.Millisecond, vm.Interrupt)
		_, err := vm.RunCell("cell.js", src)
		timer.Stop()
		vm.ClearInterrupt()
		if !errors.Is(err, ErrInterrupted) {
			t.Errorf("%q: got %v", src, err)
		}
	}
	v, err := vm.RunCell("cell.js", "typeof fin + (1 + 1)")
	if err != nil || ToString(v) != "undefined2" {
		t.Errorf("after interrupt: %v %v", ToString(v), err)
	}
	p, err := vm.RunCell("cell.js", "await null; while (true) {}")
	if err != nil {
		t.Fatal(err)
	}
	timer := time.AfterFunc(30*time.Millisecond, vm.Interrupt)
	err = vm.RunJobs()
	timer.Stop()
	vm.ClearInterrupt()
	if !errors.Is(err, ErrInterrupted) {
		t.Errorf("async interrupt: %v", err)
	}
	if st, _ := vm.PromiseResult(p); st != PromisePending {
		t.Errorf("interrupted cell promise state %v", st)
	}
	if v, err := vm.RunCell("cell.js", "2 + 2"); err != nil || ToString(v) != "4" {
		t.Errorf("after async interrupt: %v %v", ToString(v), err)
	}
}

func TestLimits(t *testing.T) {
	vm := New(Options{MaxDepth: 500, MemoryLimit: 128 << 20})
	v, err := vm.RunCell("cell.js", "function f(n) { return f(n + 1) + 1 } try { f(0) } catch (e) { return e.name + ': ' + e.message }")
	if err != nil || ToString(v) != "RangeError: Maximum call stack size exceeded" {
		t.Errorf("depth: %v %v", ToString(v), err)
	}
	v, err = vm.RunCell("cell.js", "let a = []; try { while (true) a.push(new Array(1e6).fill(1.5)) } catch (e) { const n = a.length; a = null; return e.name + ' ' + (n > 1) }")
	if err != nil || ToString(v) != "RangeError true" {
		t.Errorf("memory: %v %v", ToString(v), err)
	}
	if v, err := vm.RunCell("cell.js", "[1, 2].length"); err != nil || ToString(v) != "2" {
		t.Errorf("after limits: %v %v", ToString(v), err)
	}
}

func TestHostAsync(t *testing.T) {
	vm := New(Options{})
	type done struct {
		resolve func(Value)
		n       float64
	}
	results := make(chan done, 16)
	vm.SetFunc("delay", func(vm *VM, args []Value) (Value, error) {
		p, resolve, _ := vm.NewPromise()
		n := args[0].Float()
		go func() {
			time.Sleep(50 * time.Millisecond)
			results <- done{resolve, n}
		}()
		return p, nil
	})
	vm.SetFunc("boom", func(vm *VM, args []Value) (Value, error) {
		return Undefined(), errors.New("host failed")
	})
	start := time.Now()
	res, err := vm.RunCell("cell.js", "const r = await Promise.all([1,2,3,4,5,6,7,8].map(n => delay(n)));\nlet msg; try { boom() } catch (e) { msg = e.message }\nreturn r.join() + ' ' + msg")
	if err != nil {
		t.Fatal(err)
	}
	pending := 8
	for {
		if err := vm.RunJobs(); err != nil {
			t.Fatal(err)
		}
		if st, v := vm.PromiseResult(res); st != PromisePending {
			if st != PromiseFulfilled || ToString(v) != "1,2,3,4,5,6,7,8 host failed" {
				t.Fatalf("got %v %v", st, ToString(v))
			}
			break
		}
		if pending == 0 {
			t.Fatal("stuck")
		}
		d := <-results
		pending--
		d.resolve(Number(d.n))
	}
	if el := time.Since(start); el > 400*time.Millisecond {
		t.Errorf("parallel host calls took %v", el)
	}
}

func TestPromiseOrdering(t *testing.T) {
	vm := New(Options{})
	got, err := evalJSON(t, vm, "const log = []; const p = (async () => { log.push(1); await null; log.push(4) })(); Promise.resolve().then(() => log.push(5)); log.push(2); queueMicrotask(() => log.push(6)); log.push(3); log")
	if err != nil || got != "[1,2,3]" {
		t.Errorf("sync part: %s %v", got, err)
	}
	if err := vm.RunJobs(); err != nil {
		t.Fatal(err)
	}
	if got, _ := evalJSON(t, vm, "log"); got != "[1,2,3,4,5,6]" {
		t.Errorf("after jobs: %s", got)
	}
}

func TestAPI(t *testing.T) {
	var lines []string
	vm := New(Options{Stdout: func(level, text string) { lines = append(lines, level+":"+text) }})
	if _, err := vm.RunCell("cell.js", "console.log('a', 1, {b: [2]}); console.error(undefined)"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(lines, "|") != "log:a 1 {\n  \"b\": [\n    2\n  ]\n}|error:undefined" {
		t.Errorf("console: %q", lines)
	}
	v, err := vm.JSONParse(`{"x": [1, "y"]}`)
	if err != nil {
		t.Fatal(err)
	}
	vm.Set("data", v)
	r, err := vm.RunCell("cell.js", "data.x[1] + data.x.length")
	if err != nil || ToString(r) != "y2" {
		t.Errorf("Set/JSONParse: %v %v", ToString(r), err)
	}
	if _, err := vm.RunCell("cell.js", "function add(a, b) { return a + b }"); err != nil {
		t.Fatal(err)
	}
	if r, err := vm.Call("add", Number(2), Str("x")); err != nil || ToString(r) != "2x" {
		t.Errorf("Call: %v %v", ToString(r), err)
	}
	if _, err := vm.Call("missing"); err == nil {
		t.Error("calling undefined must fail")
	}
	e := vm.NewError("ToolError", "bad")
	vm.Set("e", e)
	if r, _ := vm.RunCell("cell.js", "[e instanceof Error, e.name, e.message].join()"); ToString(r) != "true,ToolError,bad" {
		t.Errorf("NewError: %s", ToString(r))
	}
	s, err := vm.JSONStringify(vm.NewArray(Number(1), Str("a"), Null(), Bool(true)))
	if err != nil || s != `[1,"a",null,true]` {
		t.Errorf("JSONStringify: %s %v", s, err)
	}
	obj := vm.NewObject()
	if err := vm.SetProp(obj, "k", Str("v")); err != nil {
		t.Fatal(err)
	}
	if g, _ := vm.GetProp(obj, "k"); ToString(g) != "v" {
		t.Errorf("GetProp: %s", ToString(g))
	}
	if _, err := vm.GetProp(Undefined(), "k"); err == nil {
		t.Error("GetProp on undefined must fail")
	}
}

func TestZeroAllocLoop(t *testing.T) {
	vm := New(Options{})
	count := func(n int) float64 {
		src := "function f(a, b) { return a + b } let s = 0; for (let i = 0; i < " + itoa(n) + "; i++) s = f(s, i % 7); return s"
		vm.RunCell("cell.js", src)
		return testing.AllocsPerRun(3, func() { vm.RunCell("cell.js", src) })
	}
	if small, big := count(1000), count(200000); big > small+5 {
		t.Errorf("allocations grow with iterations: %v vs %v", small, big)
	}
}

func TestConcurrentVMs(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			vm := New(Options{})
			v, err := vm.RunCell("cell.js", "const o = JSON.parse('[{\"a\":1},{\"a\":2}]'); const r = await Promise.all(o.map(async x => x.a * 2)); r.join()")
			if err != nil {
				t.Error(err)
				return
			}
			vm.RunJobs()
			if st, r := vm.PromiseResult(v); st != PromiseFulfilled || ToString(r) != "2,4" {
				t.Errorf("got %v %s", st, ToString(r))
			}
			vm.Close()
		}()
	}
	wg.Wait()
}

func TestClose(t *testing.T) {
	vm := New(Options{})
	if _, err := vm.RunCell("cell.js", "await new Promise(() => {}); globalThis.after = 1"); err != nil {
		t.Fatal(err)
	}
	vm.RunJobs()
	if len(vm.coros) != 1 {
		t.Fatalf("want 1 suspended activation, got %d", len(vm.coros))
	}
	vm.AbandonPending()
	if len(vm.coros) != 0 {
		t.Errorf("AbandonPending left %d", len(vm.coros))
	}
	if v, _ := vm.RunCell("cell.js", "typeof after"); ToString(v) != "undefined" {
		t.Errorf("abandoned cell continued: %s", ToString(v))
	}
}

func TestNativeGlobals(t *testing.T) {
	vm := New(Options{})
	if _, err := vm.RunCell("cell.js", "globalThis.log = []; queueMicrotask(() => log.push('a')); queueMicrotask(() => { throw new Error('ignored') }); queueMicrotask(() => log.push('b')); log.push('sync')"); err != nil {
		t.Fatal(err)
	}
	if err := vm.RunJobs(); err != nil {
		t.Fatal(err)
	}
	got, err := evalJSON(t, vm, "const src = {a: [1, {b: 2}], m: new Map([[1, new Set(['x'])]]), e: new TypeError('t')}; src.self = src; const c = structuredClone(src); [log, c.a[1].b, c.a !== src.a, c.self === c, c.m.get(1).has('x'), c.e.message, typeof structuredClone(() => 1)]")
	if err == nil {
		t.Errorf("cloning a function must fail, got %s", got)
	}
	got, err = evalJSON(t, vm, "const src = {a: [1, {b: 2}], m: new Map([[1, new Set(['x'])]]), e: new TypeError('t')}; src.self = src; const c = structuredClone(src); [log, c.a[1].b, c.a !== src.a, c.self === c, c.m.get(1).has('x'), c.e.message]")
	if err != nil || got != `[["sync","a","b"],2,true,true,true,"t"]` {
		t.Errorf("got %s %v", got, err)
	}
}
