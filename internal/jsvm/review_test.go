package jsvm

import (
	"strings"
	"testing"
)

func TestReviewRegressions(t *testing.T) {
	t.Parallel()
	cases := []struct{ src, want string }{
		{"function show(a, b) { return a + '=' + b }; return show('x', await Promise.resolve(1))", "x=1"},
		{"function pair(a, b) { return a + ',' + b } async function f() { return pair('A', await 1) } const p = f(); const q = pair('X', 'Y'); return [await p, q]", `["A,1","X,Y"]`},
		{"const r = []; async function g(i) { r.push(i, await i, i) } await Promise.all([g(1), g(2), g(3)]); return r", "[1,1,1,2,2,2,3,3,3]"},
		{"try { await Promise.reject(new Error('x')) } catch (e) {} function h(a, b, c) { return a + b + c } return h(1, await 2, 3)", "6"},
		{"return [(-2) ** 2, (-1) ** 3, 2 ** -1]", "[4,-1,0.5]"},
		{"const q = [1]; q.forEach(x => { if (x < 5) q.push(x + 1) }); const a = [1, 2]; a.filter(x => a.push(x)); return [q, a.length]", "[[1,2],4]"},
		{"function set(o) { o.a = 1 } set({}); const f = Object.preventExtensions({}); let e = 'none'; try { set(f) } catch (x) { e = x.name } set(globalThis); return [e, f.a === undefined, a]", `["TypeError",true,1]`},
		{"let log = ''; function s(x) { log += 's'; return x } log += 'r' + s(1); let n = 1; n += (n = 10); return [log, n]", `["r1",11]`},
		{"const read = p => 'outer:' + p; const tools = { read(p) { return read(p) } }; const count = 5; return [tools.read('x'), ({ count() { return count } }).count()]", `["outer:x",5]`},
		{"let s = 'ab'; try { for (let i = 0; i < 40; i++) s += s } catch (e) { return e.name + ' ' + e.message }", "RangeError Invalid string length"},
		{"const a = [1]; a.push(a); try { a.flat(Infinity) } catch (e) { return e.name }", "RangeError"},
		{"const o = {}; try { Object.setPrototypeOf(Object.prototype.constructor.prototype, o); Object.setPrototypeOf(o, o) } catch (e) { return e.name }", "TypeError"},
		{"try { Array.from({length: 1e10}) } catch (e) { return e.name }", "RangeError"},
		{"return [(1.005).toFixed(2), (2.5).toFixed(0), (-0.0001).toFixed(2), (1234.5678).toFixed(2), (0.125).toFixed(2), (-1.5).toFixed(0)]", `["1.00","3","-0.00","1234.57","0.13","-2"]`},
		{"const t = 'é'.repeat(3) + ' ab cd'; return [...t.matchAll(/\\w+/g)].map(m => m.index)", "[4,7]"},
		{"const r = /b/g; const s = 'éb éb'; r.exec(s); const i1 = r.lastIndex; r.exec(s); return [i1, r.lastIndex]", "[2,5]"},
		{"let s = ''; for (let i = 0; i < 2000; i++) { s += 'é'; if (s.length !== i + 1) return 'bad' } return s.length", "2000"},
		{"const a = [1, 2, 3]; return a.reduceRight((acc, x) => { a.length = 1; return acc + x }, 0)", "4"},
		{"try { (1).toLocaleString(undefined, {maximumFractionDigits: -1}) } catch (e) { return e.name }", "RangeError"},
		{"let s = 'ab'; try { for (let i = 0; i < 40; i++) s = s.concat(s) } catch (e) { return e.name }", "RangeError"},
		{"let a = []; for (let i = 0; i < 100000; i++) a = [a]; try { String(a) } catch (e) { return e.name }", "RangeError"},
		{"let f = () => 1; for (let i = 0; i < 5000; i++) f = f.bind(null, i); return [f(), f.name.length > 1000]", "[1,true]"},
		{"const s = 'é'.repeat(2e5) + ' a'.repeat(1e5); const re = /a/g; let n = 0, last = 0; while (re.exec(s)) { n++; last = re.lastIndex } return [n, last]", "[100000,400000]"},
		{"return [`a\\r\\nb`.length, (-0.0001).toLocaleString(), Object.is(-0 % 5, -0)]", `[4,"-0",true]`},
		{"const o = Object.seal({a: 1}); try { delete o.a } catch (e) { return [e.name, o.a] }", `["TypeError",1]`},
		{"const m = new Map([[1, 1]]), seen = []; for (const [k] of m) { seen.push(k); if (k === 1) { m.delete(1); m.set(2, 2) } } const s = new Set([1]); const s2 = []; for (const v of s) { s2.push(v); if (v === 1) { s.clear(); s.add(3) } } return [seen, s2]", "[[1,2],[1,3]]"},
		{"let a, b; [a, [b] = [2]] = [1]; let p; ({x: {p} = {p: 3}} = {}); return [a, b, p]", "[1,2,3]"},
		{"const s = new Set([1]), seen = []; for (const x of s) { seen.push(x); s.delete(x); if (x < 3) s.add(x + 1) } return seen", "[1,2,3]"},
		{"async function f(a = 1, b) { await null; return b } return await f(undefined, 'kept')", "kept"},
		{"const a = [3, 1, 2]; try { a.sort((x, y) => { if (x === 2) throw 1; return x - y }) } catch (e) {} return a", "[3,1,2]"},
	}
	for _, c := range cases {
		s := newSession(t)
		out, err := s.run(c.src, 5e9)
		if err != nil {
			t.Errorf("%s\n  error %v", c.src, err)
		} else if got := jsonOut(t, s, out); got != c.want {
			t.Errorf("%s\n  got %s want %s", c.src, got, c.want)
		}
		s.vm.Close()
	}
}

func jsonOut(t *testing.T, s *session, out string) string {
	v, err := s.vm.JSONParse(out)
	if err != nil {
		return out
	}
	j, _ := s.vm.JSONStringify(v)
	return j
}

func TestRedeclareConstAsVar(t *testing.T) {
	t.Parallel()
	vm := New(Options{})
	if _, err := vm.RunCell("cell.js", "const c = 1"); err != nil {
		t.Fatal(err)
	}
	if v, err := vm.RunCell("cell.js", "var c = 2; c"); err != nil || ToString(v) != "2" {
		t.Errorf("got %v %v", ToString(v), err)
	}
}

func TestExceptionInterruptSafe(t *testing.T) {
	t.Parallel()
	vm := New(Options{})
	vm.Interrupt()
	_, err := vm.RunCell("cell.js", "1")
	vm.ClearInterrupt()
	if err == nil {
		t.Fatal("interrupt pending must fail the cell")
	}
	v, err := vm.RunCell("cell.js", "const e = new Error('m'); e.name = {toString() { throw 1 }}; e")
	if err != nil {
		t.Fatal(err)
	}
	vm.Interrupt()
	ex := vm.Exception(v)
	vm.ClearInterrupt()
	if ex.Message != "m" {
		t.Errorf("message %q", ex.Message)
	}
}

func TestSharedStringsNoRace(t *testing.T) {
	t.Parallel()
	done := make(chan bool)
	for range 4 {
		go func() {
			vm := New(Options{})
			v, err := vm.RunCell("cell.js", "['a' < '😀', 'b'.lastIndexOf('é'), typeof 1 < '😀', (-0).toFixed(2), JSON.parse('{\"a\":1,\"b\":2}', function (k, v) { if (k === 'a') this.b = 5; return v }).b]")
			if err != nil {
				t.Error(err)
			} else if s, _ := vm.JSONStringify(v); s != `[true,-1,true,"0.00",5]` {
				t.Errorf("got %s", s)
			}
			done <- true
		}()
	}
	for range 4 {
		<-done
	}
}

func TestTDZ(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"function f() { try { x; let x = 1 } catch (e) { return e.name } } return f()":                                                        "ReferenceError",
		"function f() { try { return typeof y; let y } catch (e) { return e.name } } return f()":                                              "ReferenceError",
		"let out; { try { out = z } catch (e) { out = e.name } const z = 1 } return out":                                                      "ReferenceError",
		"function f() { let a = a + 1 } try { f() } catch (e) { return e.name }":                                                              "ReferenceError",
		"function f(a = b, b = 1) { return a } try { f() } catch (e) { return e.name }":                                                       "ReferenceError",
		"function f() { const c = 1; try { c = 2 } catch (e) { return e.name } } return f()":                                                  "TypeError",
		"function f() { try { g(); let x = 1; function g() { return x } } catch (e) { return e.name } } return f()":                           "ReferenceError",
		"function f() { try { x = 2; let x } catch (e) { return e.name } } return f()":                                                        "ReferenceError",
		"function f(v) { switch (v) { case 1: let x = 1; break; case 2: try { return x } catch (e) { return e.name } } } return f(2)":         "ReferenceError",
		"function f() { const r = []; for (let i = 0; i < 3; i++) { const g = () => x; let x = i; r.push(g()) } return r.join() } return f()": "0,1,2",
		"function f() { let s = 0; for (let i = 0; i < 5; i++) s += i; const k = s; function h() { return k } return h() } return f()":        "10",
		"function f(a, b = a + 1) { return b } return f(1)":                                                                                   "2",
	}
	for src, want := range cases {
		s := newSession(t)
		out, err := s.run(src, 5e9)
		if err != nil || out != want {
			t.Errorf("%s\n  got %q %v want %q", src, out, err, want)
		}
	}
}

func TestScoreboardFixes(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"return ['ß'.toUpperCase(), 'İ'.toLowerCase().length, 'ﬀ'.toUpperCase(), 'ΑΣ'.toLowerCase(), 'ᾳ'.toUpperCase(), 'ŉ'.toUpperCase().length, 'straße'.toLocaleUpperCase(), 'ABC'.toLowerCase()].join()":                                "SS,2,FF,ας,ΑΙ,2,STRASSE,abc",
		"return [(0.1).toString(3), (0.5).toString(2), (255.5).toString(16), (1/3).toString(36), (-7.25).toString(8), (0.000001).toString(5)].join()":                                                                                       "0.0022002200220022002200220022002201,0.1,ff.8,0.c,-7.2,0.0000000014340322421131001434032",
		"return [Number.parseFloat === parseFloat, Number.parseInt === parseInt].join()":                                                                                                                                                    "true,true",
		"return [[1,2,3,4,5].copyWithin(0, 3), [1,2,3,4,5].copyWithin(-2, 0, 2), [1,2,3,4,5].copyWithin(1, -3, -1)].join('|')":                                                                                                              "4,5,3,4,5|1,2,3,1,2|1,3,4,4,5",
		"return [encodeURIComponent('a b&c/d?é😀'), encodeURI('http://x.y/a b?q=é#f'), decodeURIComponent('%F0%9F%98%80%20x'), decodeURI('%20%2F%3F'), (() => { try { decodeURIComponent('%') } catch (e) { return e.name } })()].join('|')": "a%20b%26c%2Fd%3F%C3%A9%F0%9F%98%80|http://x.y/a%20b?q=%C3%A9#f|😀 x| %2F%3F|URIError",
		"return [escape('a b+é€😀'), unescape('%u20AC%E9%20x%zz'), escape === undefined].join('|')":                                                                                                                                          "a%20b+%E9%u20AC%uD83D%uDE00|€é x%zz|false",
		"return [['a10', 'a9', 'a2b', 'a02c'].sort((x, y) => x.localeCompare(y, undefined, {numeric: true})).join(), (() => { try { 'a'.localeCompare('b', 'en', {sensitivity: 'base'}) } catch (e) { return e.message } })()].join('|')":   "a2b,a02c,a9,a10|unsupported: localeCompare option sensitivity",
		"return [typeof this, this === globalThis, (() => this === globalThis)()].join()":                                                                                                                                                   "object,true,true",
		"const a5 = 1; globalThis.a5 = 2; return a5":                              "2",
		"const b5 = 1; try { b5 = 3 } catch (e) { return e.name }":                "TypeError",
		"return [null ?? (0 || 1), (null ?? 0) || 1, (a => a ?? 2)(null)].join()": "1,1,2",
	}
	for src, want := range cases {
		s := newSession(t)
		out, err := s.run(src, 5e9)
		if err != nil || out != want {
			t.Errorf("%s\n  got %q %v\n want %q", src, out, err, want)
		}
	}
	for _, src := range []string{"const a = null, b = 0, c = 1; a ?? b || c", "a || b ?? c", "a ?? b && c"} {
		_, err := New(Options{}).RunCell("cell.js", src)
		if err == nil || !strings.HasPrefix(err.Error(), "SyntaxError") {
			t.Errorf("%q: want SyntaxError, got %v", src, err)
		}
	}
}
