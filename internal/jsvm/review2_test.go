package jsvm

import (
	"strings"
	"testing"
)

func TestReviewRound4(t *testing.T) {
	t.Parallel()
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
		out, err := s.run(src, hangGuard)
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
		out, err := s.run(src, hangGuard)
		if err != nil || out != want {
			t.Errorf("%s\n  got %q %v want %q", src, out, err, want)
		}
	}
}

func TestReviewRound5(t *testing.T) {
	t.Parallel()
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
		out, err := s.run(src, hangGuard)
		if err != nil || out != want {
			t.Errorf("%s\n  got %q %v want %q", src, out, err, want)
		}
	}
	if _, err := New(Options{}).RunCell("cell.js", "function f(a = 1) { let a = 2 }"); errName(err) != "SyntaxError" {
		t.Errorf("let shadowing param: %v", err)
	}
}

func TestReviewRound6(t *testing.T) {
	t.Parallel()
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
		out, err := s.run(src, hangGuard)
		if err != nil || out != want {
			t.Errorf("%s\n  got %q %v want %q", src, out, err, want)
		}
	}
}

func TestRev4Round1(t *testing.T) {
	t.Parallel()
	v, err := New(Options{}).RunCell("c.js", "const m = new Map([[1, 2]]); Object.setPrototypeOf(m, null); const c = structuredClone(m); c instanceof Map && c.get(1) === 2")
	if err != nil || ToString(v) != "true" {
		t.Errorf("structuredClone of null-proto Map: %s %v", ToString(v), err)
	}
	cases := map[string]string{
		"return JSON.stringify([1, 2].flatMap(x => x === 1 ? [, 5] : [x]))":                                                               "[5,2]",
		"return [1].flatMap(() => [, ,]).length":                                                                                          "0",
		"const seen = []; JSON.stringify([1, , 3], (k, v) => { seen.push(typeof v); return v }); return seen.join()":                      "object,number,undefined,number",
		"return JSON.stringify([1, , 3], (k, v) => v)":                                                                                    "[1,null,3]",
		"const a = [1, 2, 3]; const [x = a.pop(), y, z = 9] = [undefined, ...a].slice(0, 2).concat([undefined]); return [x, y, z].join()": "3,1,9",
		"const a = [undefined, 2, 3]; const [x = (a[1] = 7), y] = a; return [x, y].join()":                                                "7,7",
		"const a = [undefined, 2]; const [x = a.length = 0, y] = a; return String(y)":                                                     "undefined",
		"const a = [undefined]; const [x = a.push(9), y] = a; return [x, y].join()":                                                       "2,9",
		"const a = [undefined, 2]; const [x = a.pop(), y] = a; return [x, String(y)].join()":                                              "2,undefined",
		"const a = []; const [x = a.push(1, 2), y] = a; return [x, String(y)].join()":                                                     "2,undefined",
		"const a = [undefined, 1]; const [x = a.push(5), ...r] = a; return r.join()":                                                      "1,5",
		"const [p, , q, ...r] = [1, , 3, 4, , 6]; return [p, q, r.length, r[1]].join()":                                                   "1,3,3,",
		"const a = [1, 2, 3, 4]; return a.slice({valueOf() { a.splice(0); return 3 }}).length":                                            "0",
		"return Promise.race(5).then(() => 'ok', e => e.name)":                                                                            "TypeError",
		"return [0x200000000000011 === 144115188075855904, Number('0x200000000000011') === 144115188075855904, 0b100000000000000000000000000000000000000000000000000011 === 9007199254740996].join()": "true,true,true",
		"return parseInt('g0000000002v', 32) === 576460752303423616":   "true",
		"return parseInt('1fffffffffffff1', 16) === 0x1fffffffffffff1": "true",
	}
	for src, want := range cases {
		s := newSession(t)
		out, err := s.run(src, hangGuard)
		if err != nil || out != want {
			t.Errorf("%s\n  got %q %v want %q", src, out, err, want)
		}
	}
}

func TestLocaleCompareSharedPrefix(t *testing.T) {
	t.Parallel()
	pairs := [][2]string{{"abcé", "abcè"}, {"abcE", "abce"}, {"aé", "ae"}, {"aé", "aêb"}, {"x\xe2\x82A", "x\xe2\x82B"}, {"x\xe2\x82", "x\xe2\x82\xac"}, {"path/a-b", "path/a_b"}, {"ab", "abc"}, {"Ab", "ab"}, {"dir/Émile", "dir/emile"}}
	for _, p := range pairs {
		for _, ab := range [][2]string{p, {p[1], p[0]}} {
			want := strings.Compare(ab[0], ab[1])
			for level := 2; level >= 0; level-- {
				if c := compareLevel(ab[0], ab[1], level); c != 0 {
					want = c
				}
			}
			if got := localeCompare(ab[0], ab[1]); got != want {
				t.Errorf("localeCompare(%q, %q) = %d, want %d", ab[0], ab[1], got, want)
			}
		}
	}
}
