package jsvm

import (
	"errors"
	"strings"
	"testing"
)

func errName(err error) string {
	if ex, ok := errors.AsType[*Exception](err); ok {
		return ex.Name
	}
	if err == nil {
		return ""
	}
	return "go:" + err.Error()
}

func TestNestingCaps(t *testing.T) {
	t.Parallel()
	r := strings.Repeat
	syntax := map[string]string{
		"parens":       r("(", 5000) + "1" + r(")", 5000),
		"arrays":       r("[", 5000) + r("]", 5000),
		"objects":      r("{a:", 5000) + "1" + r("}", 5000),
		"blocks":       r("{", 5000) + r("}", 5000),
		"unary":        r("!", 100000) + "1",
		"new":          r("new ", 5000) + "F",
		"await":        r("await ", 5000) + "1",
		"ternary":      r("1 ? 1 : ", 5000) + "1",
		"assign":       r("a = ", 5000) + "1",
		"plus chain":   "1" + r(" + 1", 100000),
		"member chain": "a" + r(".b", 100000),
		"call chain":   "f" + r("()", 100000),
		"else if":      "if (a) {}" + r(" else if (a) {}", 5000),
		"functions":    r("function f() {", 3000) + r("}", 3000),
		"arrows":       r("() => ", 5000) + "1",
		"templates":    r("`${", 3000) + "1" + r("}`", 3000),
		"patterns":     "const " + r("[", 5000) + "a" + r("]", 5000) + " = []",
		"obj patterns": "const " + r("{a:", 5000) + "b" + r("}", 5000) + " = {}",
	}
	for name, src := range syntax {
		_, err := New(Options{}).RunCell("cell.js", src)
		if errName(err) != "SyntaxError" {
			t.Errorf("%s: got %v", name, err)
		}
	}
	vm := New(Options{})
	ok := map[string]string{
		"plus 300":    "return 1" + r(" + 1", 300),
		"else if 100": "let a = 0; if (a) {}" + r(" else if (a) {}", 100) + " else { a = 7 } return a",
		"parens 60":   "return " + r("(", 60) + "2" + r(")", 60),
		"json depth":  "return JSON.parse(" + "'" + r("[", 1000) + r("]", 1000) + "'" + ").length",
	}
	for name, src := range ok {
		if _, err := vm.RunCell("cell.js", src); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestRuntimeRecursionCaps(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"join":            "let a = []; for (let i = 0; i < 1e4; i++) a = [a]; try { a.join() } catch (e) { return e.name }",
		"toString":        "let a = []; for (let i = 0; i < 1e4; i++) a = [a]; try { String(a) } catch (e) { return e.name }",
		"flat":            "let a = []; for (let i = 0; i < 1e4; i++) a = [a]; try { a.flat(Infinity) } catch (e) { return e.name }",
		"json stringify":  "let a = []; for (let i = 0; i < 1e4; i++) a = [a]; try { JSON.stringify(a) } catch (e) { return e.name }",
		"json parse":      "try { JSON.parse('['.repeat(1e4)) } catch (e) { return e.name }",
		"structuredClone": "let a = {}; for (let i = 0; i < 1e4; i++) a = {a}; try { structuredClone(a) } catch (e) { return e.name }",
		"bind call":       "let f = () => 1; for (let i = 0; i < 1e4; i++) f = f.bind(null); return f()",
		"bind construct":  "let f = function () {}; for (let i = 0; i < 1e4; i++) f = f.bind(null); return typeof new f()",
		"bind instanceof": "let f = function () {}; for (let i = 0; i < 1e4; i++) f = f.bind(null); return [{} instanceof f, f.name.length > 1000].join()",
		"iter helpers":    "let it = [1, 2].values(); for (let i = 0; i < 1e4; i++) it = it.map(x => x); try { it.next() } catch (e) { return e.name }",
		"comparator":      "function s(a) { return a.sort((x, y) => { s([1, 2]); return x - y }) } try { s([2, 1]) } catch (e) { return e.name }",
		"proto chain":     "let o = {}; for (let i = 0; i < 1e4; i++) o = Object.create(o); return [o.toString === Object.prototype.toString, 'x' in o].join()",
	}
	want := map[string]string{"bind instanceof": "false,true", "proto chain": "true,false", "bind call": "1", "bind construct": "object"}
	for name, src := range cases {
		s := newSession(t)
		out, err := s.run(src, hangGuard)
		w := want[name]
		if w == "" {
			w = "RangeError"
		}
		if err != nil || out != w {
			t.Errorf("%s: got %q %v, want %s", name, out, err, w)
		}
	}
}

func TestInternalErrorsCatchable(t *testing.T) {
	t.Parallel()
	vm := New(Options{})
	vm.SetFunc("crash", func(vm *VM, args []Value) (Value, error) {
		var m map[string]*object
		return objV(m["x"].proto()), nil
	})
	vm.SetFunc("oob", func(vm *VM, args []Value) (Value, error) {
		return args[5], nil
	})
	v, err := vm.RunCell("cell.js", "const r = []; for (const f of [crash, oob]) { try { f() } catch (e) { r.push(e.name + ': ' + e.message.split(':')[0]) } } r.join('|')")
	if err != nil || ToString(v) != "InternalError: internal error|InternalError: internal error" {
		t.Errorf("caught: %v %v", ToString(v), err)
	}
	_, err = vm.RunCell("cell.js", "crash()")
	if errName(err) != "InternalError" {
		t.Errorf("uncaught: %v", err)
	}
	p, err := vm.RunCell("cell.js", "await null; oob()")
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.RunJobs(); err != nil {
		t.Fatal(err)
	}
	if st, r := vm.PromiseResult(p); st != PromiseRejected || vm.Exception(r).Name != "InternalError" {
		t.Errorf("async: %v %v", st, ToString(r))
	}
	p, err = vm.RunCell("cell.js", "await Promise.resolve().then(() => crash()).catch(e => e.name)")
	if err != nil {
		t.Fatal(err)
	}
	vm.RunJobs()
	if st, r := vm.PromiseResult(p); st != PromiseFulfilled || ToString(r) != "InternalError" {
		t.Errorf("reaction: %v %v", st, ToString(r))
	}
	if v, err := vm.RunCell("cell.js", "[1, 2, 3].map(x => x * 2).join()"); err != nil || ToString(v) != "2,4,6" {
		t.Errorf("after: %v %v", ToString(v), err)
	}
}
