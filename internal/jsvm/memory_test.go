package jsvm

import (
	"runtime"
	"testing"
)

const chargeLimit = 8 << 20

var chargeCases = map[string]string{
	"new Array":        "try { new Array(1e6) } catch (e) { return e.message }",
	"Array.from":       "try { Array.from({length: 1e6}) } catch (e) { return e.message }",
	"repeat":           "try { 'x'.repeat(1e7) } catch (e) { return e.message }",
	"repeat multibyte": "try { 'abcdefgh'.repeat(6e7) } catch (e) { return e.message }",
	"padStart":         "try { 'x'.padStart(1e7) } catch (e) { return e.message }",
	"padEnd":           "try { 'a'.padEnd(4e6, '€') } catch (e) { return e.message }",
	"toUpperCase":      "const s = 'ΐ'.repeat(1.2e6); try { s.toUpperCase() } catch (e) { return e.message }",
	"stringify escape": "const s = '\\x01'.repeat(2e6); try { JSON.stringify(s) } catch (e) { return e.message }",
	"encodeURI":        "const s = 'é'.repeat(2e6); try { encodeURIComponent(s) } catch (e) { return e.message }",
	"split capture":    "const s = 'a'.repeat(3e6); try { s.split(/()/) } catch (e) { return e.message }",
	"replace $'":       "try { 'a'.repeat(3e4).replace(/a/g, \"$'\") } catch (e) { return e.message }",
	"replaceAll grow":  "const x = 'x'.repeat(1e6), y = 'y'.repeat(1e6); try { x.replaceAll('x', y) } catch (e) { return e.message }",
	"sparse index":     "const a = []; try { a[1e6] = 0 } catch (e) { return e.message }",
	"keys big array":   "const a = new Array(2e5); try { Object.keys(a) } catch (e) { return e.message }",
	"rope doubling":    "let s = 'x'.repeat(300); try { for (let i = 0; i < 22; i++) s = s + s; s.indexOf('y') } catch (e) { return e.message }",
	"new Array loop":   "const a = []; try { for (let i = 0; i < 64; i++) a.push(new Array(5e4)) } catch (e) { return e.message }",
	"fill":             "const k = []; try { for (let i = 0; i < 64; i++) k.push(Array(5e4).fill(0)) } catch (e) { return e.message }",
	"join":             "const a = new Array(1e5).fill('x'.repeat(40)), out = []; try { for (let i = 0; i < 16; i++) out.push(a.join('')) } catch (e) { return e.message }",
	"rope flatten":     "const keep = []; try { for (let i = 0; i < 640; i++) { const s = 'x'.repeat(1e5) + i; s.charCodeAt(5); keep.push(s) } } catch (e) { return e.message }",
	"flatten ropes":    "let s = 'x'.repeat(1 << 14); for (let i = 0; i < 7; i++) s += s; const a = []; for (let i = 0; i < 32; i++) a.push(s + i); try { for (const r of a) r.indexOf('y') } catch (e) { return e.message }",
	"push":             "const a = []; try { for (let i = 0; i < 3e5; i++) a.push(1, 2, 3, 4, 5, 6, 7, 8) } catch (e) { return e.message }",
	"concat":           "const a = new Array(5e4).fill(1), keep = []; try { for (let i = 0; i < 16; i++) keep.push(a.concat(a, a)) } catch (e) { return e.message }",
	"spread":           "const a = new Array(5e4).fill(1), keep = []; try { for (let i = 0; i < 24; i++) keep.push([...a, ...a]) } catch (e) { return e.message }",
	"stringify indent": "const o = {a: new Array(5e3).fill({b: [1, 2, 3]})}, keep = []; try { for (let i = 0; i < 160; i++) keep.push(JSON.stringify(o, null, 10)) } catch (e) { return e.message }",
	"template":         "let s = 'x'.repeat(1e5); const keep = []; try { for (let i = 0; i < 12; i++) { s = `${s}${s}`; keep.push(s) } } catch (e) { return e.message }",
	"replaceAll":       "const s = ('x' + 'a'.repeat(99)).repeat(1e3), keep = []; try { for (let i = 0; i < 640; i++) keep.push(s.replaceAll('x', 'yy')) } catch (e) { return e.message }",
	"split":            "const s = 'x,'.repeat(5e4), keep = []; try { for (let i = 0; i < 64; i++) keep.push(s.split(',')) } catch (e) { return e.message }",
	"split pieces":     "const big = 'é'.repeat(1e5) + ',' + 'é'.repeat(1e5), keep = []; try { for (let i = 0; i < 160; i++) keep.push(big.split(/,/)) } catch (e) { return e.message }",
	"slice non-ascii":  "const big = 'é'.repeat(1e5), keep = []; try { for (let i = 0; i < 160; i++) keep.push(big.slice(1)) } catch (e) { return e.message }",
	"set union":        "const a = new Set(Array.from({length: 2e4}, (_, i) => i)), keep = []; try { for (let i = 0; i < 32; i++) keep.push(a.union(a)) } catch (e) { return e.message }",
	"clone map":        "const m = new Map(Array.from({length: 2e4}, (_, i) => [i, i])), keep = []; try { for (let i = 0; i < 32; i++) keep.push(structuredClone(m)) } catch (e) { return e.message }",
	"escape":           "const s = 'é'.repeat(1e5), keep = []; try { for (let i = 0; i < 200; i++) keep.push(escape(s)) } catch (e) { return e.message }",
	"match global":     "const s = 'ab'.repeat(2e4), k = []; try { for (let i = 0; i < 32; i++) k.push(s.match(/ab/g)) } catch (e) { return e.message }",
	"trimEnd":          "const b = ' ' + 'x'.repeat(1e5), k = []; try { for (let i = 0; i < 640; i++) k.push((b + i).trimEnd()) } catch (e) { return e.message }",
	"microtasks":       "const f = () => {}; try { for (let i = 0; i < 1e6; i++) queueMicrotask(f) } catch (e) { return e.message }",
}

const oomMessage = "out of memory: heap limit exceeded"

func TestMemoryCharges(t *testing.T) {
	t.Parallel()
	for name, src := range chargeCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			vm := chargedVM(chargeLimit)
			defer vm.Close()
			v, err := vm.RunCell("cell.js", src)
			if charged := vm.heap(false); err != nil || ToString(v) != oomMessage || charged > 2*chargeLimit {
				t.Errorf("got %q %v after %d bytes charged", ToString(v), err, charged)
			}
			vm.AbandonPending()
			if v, err := vm.RunCell("cell.js", "[1, 2].length"); err != nil || ToString(v) != "2" {
				t.Errorf("after the limit: %q %v", ToString(v), err)
			}
		})
	}
}

func TestSizeCaps(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"try { new Array(1e9) } catch (e) { return e.message }":             "unsupported: array length 1000000000",
		"try { Array.from({length: 1e10}) } catch (e) { return e.message }": "Invalid array length",
		"try { 'x'.repeat(1e9) } catch (e) { return e.message }":            "Invalid string length",
		"try { 'x'.padStart(1e9) } catch (e) { return e.message }":          "Invalid string length",
		"try { 'a'.padEnd(2e8, '€') } catch (e) { return e.message }":       "Invalid string length",
		"''.repeat(1e300).length":                                           "0",
		"try { new RegExp('('.repeat(1e5)) } catch (e) { return e.name }":   "SyntaxError",
	}
	vm := New(Options{})
	for src, want := range cases {
		if v, err := vm.RunCell("cell.js", src); err != nil || ToString(v) != want {
			t.Errorf("%s: got %q %v, want %s", src, ToString(v), err, want)
		}
	}
}

func TestMemoryLimitIsRangeError(t *testing.T) {
	t.Parallel()
	vm := chargedVM(chargeLimit)
	defer vm.Close()
	if v, err := vm.RunCell("cell.js", "try { new Array(1e6) } catch (e) { return e.name }"); err != nil || ToString(v) != "RangeError" {
		t.Errorf("caught: %q %v", ToString(v), err)
	}
	_, err := vm.RunCell("cell.js", "const f = () => {}; for (let i = 0; i < 1e6; i++) queueMicrotask(f)")
	if errName(err) != "RangeError" {
		t.Errorf("uncaught with queued jobs: %v", err)
	}
	vm.AbandonPending()
	if v, err := vm.RunCell("cell.js", "[1, 2].length"); err != nil || ToString(v) != "2" {
		t.Errorf("after an uncaught limit: %q %v", ToString(v), err)
	}
}

func TestNoFalseOutOfMemoryAtProductionLimit(t *testing.T) {
	vm := New(Options{MemoryLimit: 512 << 20})
	defer vm.Close()
	cases := map[string]string{
		"[parseInt('f'.repeat(1e6), 16), parseInt('0'.repeat(1e6) + 'ff', 16)].join()": "Infinity,255",
		"try { 'x'.repeat(1e6).match(/x/g); return 'ok' } catch (e) { return e.name }": "ok",
	}
	for src, want := range cases {
		if v, err := vm.RunCell("cell.js", src); err != nil || ToString(v) != want {
			t.Errorf("%s: got %q %v, want %s", src, ToString(v), err, want)
		}
	}
	forceGC()
}

func TestMemoryChargesUnderLimit(t *testing.T) {
	t.Parallel()
	vm := chargedVM(chargeLimit)
	v, err := vm.RunCell("cell.js", "const a = new Array(1e5).fill(1), s = 'x'.repeat(1e6); [a.length, s.length].join()")
	if err != nil || ToString(v) != "100000,1000000" {
		t.Errorf("got %q %v", ToString(v), err)
	}
}

func TestMemoryLimitProcessHeap(t *testing.T) {
	ballast := make([]byte, 64<<20)
	vm := New(Options{MemoryLimit: limitAbove(32 << 20)})
	steps := []struct{ name, src, want string }{
		{"retained", "let a = []; try { for (let i = 0; i < 100; i++) a.push(new Array(1e5).fill(1.5)) } catch (e) { const n = a.length; a = null; return e.message + ' ' + (n > 1) }", oomMessage + " true"},
		{"usable", "[1, 2].length", "2"},
		{"garbage", "for (let i = 0; i < 100; i++) new Array(1e5).fill(i); 'ok'", "ok"},
		{"shared substrings", "const big = 'a' + 'é'.repeat(3e6), keep = []; for (let i = 0; i < 12; i++) keep.push(/(é+)/.exec(big)); keep.length", "12"},
		{"lazy ropes", "let s = 'x'.repeat(1 << 18); for (let i = 0; i < 5; i++) s += s; const r = []; for (let i = 0; i < 64; i++) r.push(s + i); let n = 0; for (const x of r) if (x === s + 'y') n++; 'eq ' + n", "eq 0"},
		{"flattened ropes", "try { for (const x of r) x.indexOf('y') } catch (e) { return e.message }", oomMessage},
	}
	for _, s := range steps {
		if v, err := vm.RunCell("cell.js", s.src); err != nil || ToString(v) != s.want {
			t.Errorf("%s: got %q %v, want %s", s.name, ToString(v), err, s.want)
		}
	}
	vm.Close()
	runtime.KeepAlive(ballast)
	forceGC()
}
