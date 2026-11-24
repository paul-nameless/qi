package memtest

import (
	"strings"
	"testing"
	"time"

	"github.com/paul-nameless/qi/internal/jsvm"
)

func TestAllocationCaps(t *testing.T) {
	cases := map[string]string{
		"new Array 1e9":    "try { new Array(1e9) } catch (e) { return e.name }",
		"new Array 1e7":    "try { const a = []; for (;;) a.push(new Array(1e7)) } catch (e) { return e.name }",
		"repeat":           "try { 'x'.repeat(1e9) } catch (e) { return e.name }",
		"repeat big":       "try { 'abcdefgh'.repeat(6e7) } catch (e) { return e.name }",
		"padStart":         "try { 'x'.padStart(1e9) } catch (e) { return e.name }",
		"fill":             "try { const k = []; for (;;) k.push(Array(1e6).fill(0)) } catch (e) { return e.name }",
		"join":             "const a = new Array(4e6).fill('x'.repeat(40)); try { const out = []; for (;;) out.push(a.join('')) } catch (e) { return e.name }",
		"rope flatten":     "const keep = []; try { for (let i = 0; ; i++) { const s = 'x'.repeat(1e6) + i; s.charCodeAt(5); keep.push(s) } } catch (e) { return e.name }",
		"push":             "try { const a = []; for (;;) a.push(1, 2, 3, 4, 5, 6, 7, 8) } catch (e) { return e.name }",
		"concat":           "let a = new Array(1e6).fill(1); try { const keep = []; for (;;) keep.push(a.concat(a, a)) } catch (e) { return e.name }",
		"spread":           "let a = new Array(1e6).fill(1); try { const keep = []; for (;;) keep.push([...a, ...a]) } catch (e) { return e.name }",
		"stringify indent": "const o = {a: new Array(1e5).fill({b: [1, 2, 3]})}; try { const keep = []; for (;;) keep.push(JSON.stringify(o, null, 10)) } catch (e) { return e.name }",
		"template":         "let s = 'x'.repeat(1e6); try { const keep = []; for (;;) { s = `${s}${s}`; keep.push(s) } } catch (e) { return e.name }",
		"replace":          "const s = 'x'.repeat(1e6); try { const keep = []; for (;;) keep.push(s.replaceAll('x', 'yy')) } catch (e) { return e.name }",
		"split":            "const s = 'x,'.repeat(5e6); try { const keep = []; for (;;) keep.push(s.split(',')) } catch (e) { return e.name }",
		"regex source":     "try { new RegExp('('.repeat(1e5)) } catch (e) { return e.name }",
	}
	headroom := map[string]int64{"join": 128 << 20}
	for name, src := range cases {
		h := int64(48 << 20)
		if n, ok := headroom[name]; ok {
			h = n
		}
		got, err := runGuarded(limitAbove(h), src)
		if err != nil || (got != "RangeError" && got != "SyntaxError") {
			t.Errorf("%s: got %q %v", name, got, err)
		}
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
	headroom := map[string]int64{"encodeURI": 96 << 20, "split capture": 96 << 20}
	for name, src := range cases {
		h := int64(48 << 20)
		if n, ok := headroom[name]; ok {
			h = n
		}
		got, err := runGuarded(limitAbove(h), src)
		if err != nil || got != "RangeError" {
			t.Errorf("%s: got %q %v", name, got, err)
		}
	}
}

func TestReviewRegressionsMemory(t *testing.T) {
	cases := []struct{ src, want string }{
		{"let s = 'ab'; try { for (let i = 0; i < 40; i++) s += s } catch (e) { return e.name + ' ' + e.message }", "RangeError Invalid string length"},
		{"let s = 'ab'; try { for (let i = 0; i < 40; i++) s = s.concat(s) } catch (e) { return e.name }", "RangeError"},
		{"try { 'x'.repeat(1e6).replaceAll('x', 'y'.repeat(1e6)) } catch (e) { return e.name + ' ' + ''.repeat(1e300).length }", "RangeError 0"},
	}
	for _, c := range cases {
		if got, err := runGuarded(512<<20, c.src); err != nil || got != c.want {
			t.Errorf("%s\n  got %q %v want %s", c.src, got, err, c.want)
		}
	}
}

func TestReviewRound5Memory(t *testing.T) {
	if got, err := runGuarded(limitAbove(32<<20), "const big = 'a' + 'é'.repeat(1e6); const keep = []; for (let i = 0; i < 200; i++) keep.push(/(é+)/.exec(big)); keep.length"); err != nil || got != "200" {
		t.Errorf("shared non-ASCII substrings: %v %v", got, err)
	}
	mem := map[string]string{
		"toUpperCase":   "try { 'ΐ'.repeat(5e7).toUpperCase() } catch (e) { return e.name }",
		"padEnd":        "try { 'a'.padEnd(2e8, '€') } catch (e) { return e.name }",
		"stringify":     "try { JSON.stringify('\\x01'.repeat(1e8)) } catch (e) { return e.name }",
		"flatten ropes": "try { let s = 'x'.repeat(1 << 20); for (let i = 0; i < 7; i++) s += s; const a = []; for (let i = 0; i < 64; i++) a.push(s + i); for (const r of a) r.indexOf('y') } catch (e) { return e.name }",
		"split pieces":  "try { const big = 'é'.repeat(1e7) + ',' + 'é'.repeat(1e7); const keep = []; for (;;) keep.push(big.split(/,/)) } catch (e) { return e.name }",
	}
	for name, src := range mem {
		start := time.Now()
		got, err := runGuarded(96<<20, src)
		if err != nil || got != "RangeError" {
			t.Errorf("%s: got %q %v after %v", name, got, err, time.Since(start))
		}
	}
}

func TestReviewRound6Memory(t *testing.T) {
	mem := map[string]string{
		"strictEquals ropes": "try { let s = 'x'.repeat(1 << 20); for (let i = 0; i < 7; i++) s += s; const a = []; for (let i = 0; i < 64; i++) a.push(s + i); let n = 0; for (const r of a) if (r === s + 'y') n++; return 'eq ' + n } catch (e) { return e.name }",
		"keys big array":     "try { const k = []; for (;;) k.push(Object.keys(new Array(1.6e7))) } catch (e) { return e.name }",
		"regex oom":          "const s = 'ab'.repeat(5e6); try { const k = []; for (;;) k.push(s.match(/a/g)) } catch (e) { return e.name }",
	}
	for name, src := range mem {
		l := int64(256 << 20)
		if name == "regex oom" {
			l = limitAbove(64 << 20)
		}
		got, err := runGuarded(l, src)
		if err != nil || (got != "RangeError" && !strings.HasPrefix(got, "eq")) {
			t.Errorf("%s: got %q %v", name, got, err)
		}
	}
}

func TestMicrotaskFloodHitsLimit(t *testing.T) {
	vm := jsvm.New(jsvm.Options{MemoryLimit: limitAbove(64 << 20)})
	_, err := vm.RunCell("cell.js", "const f = () => {}; while (true) queueMicrotask(f)")
	if errName(err) != "RangeError" {
		t.Errorf("want RangeError, got %v", err)
	}
	vm.AbandonPending()
}
