package jsvm

import (
	"strings"
	"testing"
	"time"
)

func TestReviewRound4(t *testing.T) {
	cases := map[string]string{
		"try { String({toString: Object.prototype.toLocaleString}) } catch (e) { return e.name }":                          "RangeError",
		"try { return ({toString: String.prototype.trim}) + '' } catch (e) { return e.name }":                              "RangeError",
		"const e = {toString: Error.prototype.toString}; e.message = e; try { String(e) } catch (x) { return x.name }":     "RangeError",
		"try { ({toISOString: Date.prototype.toJSON}).toISOString() } catch (e) { return e.name }":                         "RangeError",
		"let f = () => 0; const a = Array(1000).fill(0); for (let i = 0; i < 300; i++) f = f.bind(null, ...a); return f()": "0",
		"const x = 1; function f(a = x) { let x = 2; return a } return f()":                                                "1",
		"const x = 1; function f(a = x) { var x = 2; return a } return f()":                                                "1",
		"function f(a, b = () => a) { let c = b(); return c } return f(5)":                                                 "5",
		"return ['file10'.localeCompare('File9', undefined, {numeric: true}), 'A1'.localeCompare('a2', undefined, {numeric: true}), ['File10', 'file9', 'File2'].sort((x, y) => x.localeCompare(y, undefined, {numeric: true})).join()].join()": "1,-1,File2,file9,File10",
		"return decodeURIComponent('%EF%BF%BD') === '\\uFFFD' && decodeURIComponent(encodeURIComponent('\\uFFFD')) === '\\uFFFD'":                                                                                                               "true",
		"let D = 0; for (let i = 0; i < 1990; i++) D = [D]; function r(k, v) { return v === 0 ? JSON.stringify(D, r) : v } try { JSON.stringify(D, r) } catch (e) { return e.name }":                                                            "RangeError",
		"try { new Date(0).toLocaleDateString('en-GB') } catch (e) { return e.message }":                                                                                                                                                        "unsupported: toLocaleDateString locale en-GB",
		"return new Date(Date.UTC(2024, 2, 4)).toLocaleDateString(undefined, {timeZone: undefined}).length":                                                                                                                                     "10",
	}
	for src, want := range cases {
		s := newSession(t)
		out, err := s.run(src, 20*time.Second)
		if err != nil || out != want {
			t.Errorf("%s\n  got %q %v want %q", src, out, err, want)
		}
	}
	nested := "function f(n) { return n ? " + strings.Repeat("(", 200) + "f(n - 1)" + strings.Repeat(strings.Repeat("+1", 190)+")", 200) + " : 0 } f(1500)"
	if _, err := New(Options{}).RunCell("cell.js", nested); errName(err) != "SyntaxError" {
		t.Errorf("multiplied nesting: %v", err)
	}
}

func TestDST(t *testing.T) {
	withLocal(t, "Europe/Berlin")
	vm := New(Options{})
	if v, err := vm.RunCell("cell.js", "[new Date(2024, 9, 27, 2, 30).toISOString(), new Date(2024, 9, 27, 3, 30).toISOString(), new Date('2024-10-27T02:30:00').toISOString()].join()"); err != nil || ToString(v) != "2024-10-27T00:30:00.000Z,2024-10-27T02:30:00.000Z,2024-10-27T00:30:00.000Z" {
		t.Errorf("overlap: %v %v", ToString(v), err)
	}
	withLocal(t, "America/New_York")
	if v, err := vm.RunCell("cell.js", "const d = new Date(2024, 2, 10, 2, 30); [d.getHours(), d.toISOString(), new Date(2024, 2, 10, 1, 59).getHours()].join()"); err != nil || ToString(v) != "3,2024-03-10T07:30:00.000Z,1" {
		t.Errorf("gap: %v %v", ToString(v), err)
	}
}

func TestUntrackedAllocations(t *testing.T) {
	cases := map[string]string{
		"slice non-ascii": "const big = 'é'.repeat(5e6), keep = []; try { for (;;) keep.push(big.slice(1)) } catch (e) { return e.name }",
		"set union":       "const a = new Set(Array.from({length: 2e5}, (_, i) => i)); const keep = []; try { for (;;) keep.push(a.union(a)) } catch (e) { return e.name }",
		"clone map":       "const m = new Map(Array.from({length: 2e5}, (_, i) => [i, i])); const keep = []; try { for (;;) keep.push(structuredClone(m)) } catch (e) { return e.name }",
		"encodeURI":       "try { encodeURIComponent('é'.repeat(3e7)) } catch (e) { return e.name }",
		"escape":          "const s = 'é'.repeat(1e6), keep = []; try { for (;;) keep.push(escape(s)) } catch (e) { return e.name }",
		"split capture":   "try { 'a'.repeat(5e7).split(/()/) } catch (e) { return e.name }",
		"match global":    "try { const k = []; for (;;) k.push('ab'.repeat(1e6).match(/ab/g)) } catch (e) { return e.name }",
		"trimEnd":         "const b = ' ' + 'x'.repeat(1e6), k = []; try { for (;;) k.push(b.trimEnd() + '!') } catch (e) { return e.name }",
		"replace $'":      "try { 'a'.repeat(3e4).replace(/a/g, \"$'\") } catch (e) { return e.name }",
		"rope doubling":   "let s = 'x'.repeat(300); try { for (let i = 0; i < 22; i++) s = s + s; s.indexOf('y') } catch (e) { return e.name }",
		"sparse index":    "try { const k = []; for (;;) { const a = []; a[1.6e7] = 0; k.push(a) } } catch (e) { return e.name }",
	}
	for name, src := range cases {
		vm := New(Options{MemoryLimit: 96 << 20})
		done := make(chan struct{})
		go func() {
			select {
			case <-done:
			case <-time.After(20 * time.Second):
				vm.Interrupt()
			}
		}()
		v, err := vm.RunCell("cell.js", src)
		close(done)
		if err != nil || ToString(v) != "RangeError" {
			t.Errorf("%s: got %q %v", name, ToString(v), err)
		}
		vm.Close()
		forceGC()
	}
}

func TestFuzzdiffFindings(t *testing.T) {
	withLocal(t, "UTC")
	if v, _ := New(Options{}).RunCell("cell.js", "Object.is(new Date(0).getTimezoneOffset(), 0)"); ToString(v) != "true" {
		t.Error("getTimezoneOffset in UTC must be +0")
	}
	cases := map[string]string{
		"return Object.getOwnPropertyNames('ab').join()":                                                                      "0,1,length",
		"return ['a,b,c'.split(/,/y).join('|'), 'aab abc'.split(new RegExp('[ab]+', 'y')).join('|')].join(';')":               "a|b|c;| |c",
		"return [Math.hypot(1e300, 1), Math.hypot(5e-324), Math.hypot(3, 4), Math.hypot(NaN, Infinity), Math.hypot()].join()": "1e+300,5e-324,5,Infinity,0",
		"const a = [0]; a.x = 1; return Object.values(a).join()":                                                              "0,1",
		"return [(1.5).toString(3), (3.5).toString(3), (2.5).toString(5), (0.3).toString(7)].join()":                          "1.111111111111111111111111111111111,10.111111111111111111111111111111111,2.2222222222222222222222,0.2046204620462046205",
		"return String(parseInt('-undefineda0a', 36))":                                                                        "-4034103750424256000",
	}
	for src, want := range cases {
		s := newSession(t)
		out, err := s.run(src, 5*time.Second)
		if err != nil || out != want {
			t.Errorf("%s\n  got %q %v want %q", src, out, err, want)
		}
	}
}

func TestReviewRound5(t *testing.T) {
	cases := map[string]string{
		"function f(a = 1) { var a; return a } return f(5)":                                                                                                                        "5",
		"function f(o = {}) { var o = o || {d: 1}; return JSON.stringify(o) } return f({x: 2})":                                                                                    "{\"x\":2}",
		"return ['Émile', 'Zoe', 'Eve', 'eve', 'zoe', 'émile'].sort((a, b) => a.localeCompare(b)).join()":                                                                          "émile,Émile,eve,Eve,zoe,Zoe",
		"return ['é'.localeCompare('f'), 'file_1'.localeCompare('file10'), 'a'.localeCompare('B'), 'a'.localeCompare('a')].join()":                                                 "-1,-1,-1,0",
		"try { (1234.5).toLocaleString('de-DE') } catch (e) { return e.message }":                                                                                                  "unsupported: Number.prototype.toLocaleString locale de-DE",
		"try { (0.25).toLocaleString('en-US', {style: 'percent'}) } catch (e) { return e.message }":                                                                                "unsupported: Number.prototype.toLocaleString option style",
		"try { (1).toLocaleString(undefined, {maximumFractionDigits: NaN}) } catch (e) { return e.name }":                                                                          "RangeError",
		"return (1234.5678).toLocaleString('en-US', {maximumFractionDigits: 2})":                                                                                                   "1,234.57",
		"const r = /a/g; r.lastIndex = 2; return [...'aaaa'.matchAll(r)].length":                                                                                                   "2",
		"return ['abc'.startsWith('c', -1), 'abc'.startsWith('a', -1), 'a'.padStart(1e10, '')].join()":                                                                             "false,true,a",
		"const s = 'é' + 'a'.repeat(2e5); return [s.indexOf('a'.repeat(1e5) + 'b'), s.indexOf('aa', 3), s.lastIndexOf('é'), s.lastIndexOf('a', 5), 'xéx'.lastIndexOf('x')].join()": "-1,3,0,5,2",
	}
	for src, want := range cases {
		s := newSession(t)
		out, err := s.run(src, 10*time.Second)
		if err != nil || out != want {
			t.Errorf("%s\n  got %q %v want %q", src, out, err, want)
		}
	}
	if v, err := New(Options{MemoryLimit: 96 << 20}).RunCell("cell.js", "const big = 'a' + 'é'.repeat(3e6); const keep = []; for (let i = 0; i < 200; i++) keep.push(/(é+)/.exec(big)); keep.length"); err != nil || ToString(v) != "200" {
		t.Errorf("shared non-ASCII substrings: %v %v", ToString(v), err)
	}
	if _, err := New(Options{}).RunCell("cell.js", "function f(a = 1) { let a = 2 }"); errName(err) != "SyntaxError" {
		t.Errorf("let shadowing param: %v", err)
	}
	mem := map[string]string{
		"toUpperCase":   "try { 'ΐ'.repeat(5e7).toUpperCase() } catch (e) { return e.name }",
		"padEnd":        "try { 'a'.padEnd(2e8, '€') } catch (e) { return e.name }",
		"stringify":     "try { JSON.stringify('\\x01'.repeat(1e8)) } catch (e) { return e.name }",
		"flatten ropes": "try { let s = 'x'.repeat(1 << 20); for (let i = 0; i < 7; i++) s += s; const a = []; for (let i = 0; i < 64; i++) a.push(s + i); for (const r of a) r.indexOf('y') } catch (e) { return e.name }",
		"split pieces":  "try { const big = 'é'.repeat(1e7) + ',' + 'é'.repeat(1e7); const keep = []; for (;;) keep.push(big.split(/,/)) } catch (e) { return e.name }",
	}
	for name, src := range mem {
		vm := New(Options{MemoryLimit: 96 << 20})
		start := time.Now()
		timer := time.AfterFunc(15*time.Second, vm.Interrupt)
		v, err := vm.RunCell("cell.js", src)
		timer.Stop()
		if err != nil || ToString(v) != "RangeError" {
			t.Errorf("%s: got %q %v after %v", name, ToString(v), err, time.Since(start))
		}
		vm.Close()
		forceGC()
	}
}

func TestReviewRound6(t *testing.T) {
	cases := map[string]string{
		"return ['file 10'.localeCompare('file2', undefined, {numeric: true}), 'a-2'.localeCompare('a2', undefined, {numeric: true}), 'item1'.localeCompare('item 2', undefined, {numeric: true}), 'file_1'.localeCompare('file10', undefined, {numeric: true})].join()": "-1,-1,1,-1",
		"return ['a😀'.lastIndexOf('😀', 0), '😀😀'.lastIndexOf('😀', 1), 'ab😀'.lastIndexOf('b😀', 0), '😀'.indexOf('', 1)].join()":                                                                                                                                             "-1,0,-1,1",
		"return ['abc'.includes('a', Infinity), 'é'.includes('é', 1e19), 'abc'.includes('c', 2)].join()":                                                                                                                                                                 "false,false,true",
		"try { 'abc'.endsWith(/c/) } catch (e) { return e.name }": "TypeError",
		"return [(1.005).toLocaleString('en-US', {maximumFractionDigits: 2}), (2.675).toLocaleString(undefined, {maximumFractionDigits: 2}), (1.0005).toLocaleString()].join('|')": "1.01|2.68|1.001",
		"return [1234.5, null, 7].toLocaleString()": "1,234.5,,7",
		"const a = Object.freeze([3, 1, 2]); const r = []; for (const f of ['sort', 'pop', 'shift', 'reverse']) { try { a[f](); r.push('no') } catch (e) { r.push(e.name) } } return r.join() + ' ' + a.join()": "TypeError,TypeError,TypeError,TypeError 3,1,2",
		"return [parseInt('f'.repeat(1e6), 16), parseInt('0'.repeat(1e6) + 'ff', 16), parseInt('1'.repeat(20), 16)].join()":                                                                                     "Infinity,255,8.059505464097528e+22",
		"try { 'x'.repeat(1e6).match(/x/g); return 'ok' } catch (e) { return e.name }":                                                                                                                          "ok",
	}
	for src, want := range cases {
		s := newSession(t)
		out, err := s.run(src, 10*time.Second)
		if err != nil || out != want {
			t.Errorf("%s\n  got %q %v want %q", src, out, err, want)
		}
	}
	mem := map[string]string{
		"strictEquals ropes": "try { let s = 'x'.repeat(1 << 20); for (let i = 0; i < 7; i++) s += s; const a = []; for (let i = 0; i < 64; i++) a.push(s + i); let n = 0; for (const r of a) if (r === s + 'y') n++; return 'eq ' + n } catch (e) { return e.name }",
		"keys big array":     "try { const k = []; for (;;) k.push(Object.keys(new Array(1.6e7))) } catch (e) { return e.name }",
		"regex oom":          "const s = 'ab'.repeat(5e6); try { const k = []; for (;;) k.push(s.match(/a/g)) } catch (e) { return e.name }",
	}
	for name, src := range mem {
		vm := New(Options{MemoryLimit: 256 << 20})
		timer := time.AfterFunc(20*time.Second, vm.Interrupt)
		v, err := vm.RunCell("cell.js", src)
		timer.Stop()
		if err != nil || (ToString(v) != "RangeError" && !strings.HasPrefix(ToString(v), "eq")) {
			t.Errorf("%s: got %q %v", name, ToString(v), err)
		}
		vm.Close()
		forceGC()
	}
}
