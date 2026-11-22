package jsvm

import (
	"encoding/json"
	"testing"
)

func benchCell(b *testing.B, setup func(vm *VM), src string) {
	vm := New(Options{MemoryLimit: 1 << 30})
	if setup != nil {
		setup(vm)
	}
	if _, err := vm.RunCell("cell.js", src); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := vm.RunCell("cell.js", src); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLoopGate(b *testing.B) {
	benchCell(b, nil, "let s = 0; for (let i = 0; i < 1e7; i++) s += i % 7; return s")
}

func BenchmarkCallGate(b *testing.B) {
	benchCell(b, nil, "function f(a,b){return a+b} let s=0; for (let i=0;i<3e6;i++) s=f(s,i); return s")
}

func BenchmarkMapSortGate(b *testing.B) {
	benchCell(b, nil, "const words = []; for (let i = 0; i < 200000; i++) words.push('w' + (i % 5000)); const m = new Map(); for (const w of words) m.set(w, (m.get(w) || 0) + 1); return [...m.entries()].sort((a, b) => b[1] - a[1]).slice(0, 2).join()")
}

func BenchmarkStringConcat(b *testing.B) {
	benchCell(b, nil, "let s = ''; for (let i = 0; i < 100000; i++) { s += 'line ' + i + '\\n'; if (s.length < 0) break } return s.length")
}

func jsonDoc(b *testing.B) string {
	j, err := json.Marshal(bigIssues(8000))
	if err != nil {
		b.Fatal(err)
	}
	return string(j)
}

func BenchmarkJSONParseStringify(b *testing.B) {
	doc := jsonDoc(b)
	b.SetBytes(int64(len(doc)))
	benchCell(b, func(vm *VM) { vm.Set("doc", Str(doc)) }, "const r = JSON.parse(doc); return JSON.stringify(r).length + ' ' + r.nodes.filter(i => i.state.name === 'Todo').length")
}

func BenchmarkJSONParse(b *testing.B) {
	doc := jsonDoc(b)
	b.SetBytes(int64(len(doc)))
	benchCell(b, func(vm *VM) { vm.Set("doc", Str(doc)) }, "return JSON.parse(doc).nodes.length")
}

func BenchmarkJSONStringify(b *testing.B) {
	doc := jsonDoc(b)
	b.SetBytes(int64(len(doc)))
	benchCell(b, func(vm *VM) {
		vm.Set("doc", Str(doc))
		if _, err := vm.RunCell("setup.js", "globalThis.parsed = JSON.parse(doc)"); err != nil {
			b.Fatal(err)
		}
	}, "return JSON.stringify(parsed).length")
}

func BenchmarkPropertyAccess(b *testing.B) {
	benchCell(b, func(vm *VM) {
		if _, err := vm.RunCell("setup.js", "globalThis.items = Array.from({length: 10000}, (_, i) => ({id: i, priority: i % 5, estimate: i % 8, state: {name: i % 3 ? 'Todo' : 'Done'}}))"); err != nil {
			b.Fatal(err)
		}
	}, "let s = 0; for (let k = 0; k < 20; k++) for (let i = 0; i < items.length; i++) { const o = items[i]; s += o.priority * o.estimate + (o.state.name === 'Todo' ? 1 : 0) } return s")
}

func BenchmarkArrayMethods(b *testing.B) {
	benchCell(b, func(vm *VM) {
		if _, err := vm.RunCell("setup.js", "globalThis.items = Array.from({length: 20000}, (_, i) => ({id: 'SER-' + i, priority: (i * 7) % 5, estimate: i % 8}))"); err != nil {
			b.Fatal(err)
		}
	}, "const top = items.filter(i => i.priority > 1).map(i => ({id: i.id, score: i.priority * 10 + i.estimate})).sort((a, b) => b.score - a.score || a.id.localeCompare(b.id)); return top.slice(0, 3).map(t => t.id).join()")
}

func BenchmarkNewVM(b *testing.B) {
	for i := 0; i < b.N; i++ {
		New(Options{})
	}
}

func BenchmarkRegexGate(b *testing.B) {
	benchCell(b, nil, "const t = 'lorem ipsum dolor '.repeat(50000); return t.match(/\\b\\w+\\b/g).length + ' ' + t.replace(/o(?=r)/g, '0').length + ' ' + t.split(' ').length")
}

func BenchmarkRegexShortLines(b *testing.B) {
	benchCell(b, func(vm *VM) {
		if _, err := vm.RunCell("setup.js", "globalThis.lines = Array.from({length: 100000}, (_, i) => 'line ' + i + (i % 7 ? ' ok' : ' TODO fix'))"); err != nil {
			b.Fatal(err)
		}
	}, "return lines.filter(l => /TODO/.test(l)).length")
}
