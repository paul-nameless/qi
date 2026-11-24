package jsvm

import (
	"strings"
	"testing"
	"time"
)

type gate struct {
	code    string
	want    string
	wantErr string
	timeout time.Duration
}

func runGates(t *testing.T, s *session, gates []gate) {
	t.Helper()
	for _, g := range gates {
		if g.timeout == 0 {
			g.timeout = hangGuard
		}
		out, err := s.run(g.code, g.timeout)
		if g.wantErr != "" {
			if err == nil || !strings.HasPrefix(err.Error(), g.wantErr) {
				t.Errorf("%q: got %q %v, want error %q", g.code, out, err, g.wantErr)
			}
			continue
		}
		if err != nil || out != g.want {
			t.Errorf("%q: got %q %v, want %q", g.code, out, err, g.want)
		}
	}
}

func TestGatesState(t *testing.T) {
	t.Parallel()
	s := newSession(t)
	runGates(t, s, []gate{
		{code: "globalThis.a = 1; var b = 2; const c = 3; function f(){ return 4 } ; e2 = 5", want: "5"},
		{code: "return [typeof a, typeof b, typeof c, typeof f, typeof e2].join()", want: "number,number,number,function,number"},
		{code: "const c = 5; console.log('redeclared const', c)", want: "redeclared const 5\n"},
		{code: "let d = 6; console.log('redeclared let', d)", want: "redeclared let 6\n"},
		{code: "const r = await sleep(20); console.log('tla', r)", want: "tla 20\n"},
		{code: "console.log(typeof r, typeof c)", want: "number number\n"},
		{code: "const q = await Promise.all([sleep(10), sleep(10)]); console.log(q)", want: "[\n 10,\n 10\n]\n"},
		{code: "const z = 1;\nnull.x;", wantErr: "TypeError: Cannot read properties of null (reading 'x')\n    at cell.js:2:6"},
		{code: "await fail('x')", wantErr: "ToolError: boom: x"},
		{code: "const x = 1;\nawait fail('nope');\nreturn 1", wantErr: "ToolError: boom: nope\n    at cell.js:2:7"},
		{code: "try { await fail('nope') } catch (e) { return e.name + '|' + e.message }", want: "ToolError|boom: nope"},
		{code: "function g(){ undefinedFn() }\nfunction h(){ g() }\nh()", wantErr: "ReferenceError: undefinedFn is not defined\n    at g (cell.js:1:15)\n    at h (cell.js:2:15)\n    at cell.js:3:1"},
		{code: "sleep(50).then(() => { throw new Error('late') }); fail('unhandled'); return 'ok'", want: "ok"},
		{code: "function f(){ return f() + 1 }\ntry { f() } catch (e) { return 'caught ' + e.name }", want: "caught RangeError"},
		{code: "const a = 1;\nconst b = ;\nreturn 1", wantErr: "SyntaxError: Unexpected token ';'\n    at cell.js:2:11"},
	})
}

func TestGatesCancel(t *testing.T) {
	t.Parallel()
	s := newSession(t)
	runGates(t, s, []gate{
		{code: "globalThis.keep = 42", want: "42"},
		{code: "while (true) {}", wantErr: "interrupted", timeout: 100 * time.Millisecond},
		{code: "return keep + 1", want: "43"},
		{code: "console.log('before'); await sleep(5000); return 'late'", wantErr: "timed out", timeout: 100 * time.Millisecond},
		{code: "return keep + 2", want: "44"},
		{code: "await new Promise(() => {})", wantErr: "the script is waiting on a promise that can never settle"},
	})
}

func TestRegexGateTwice(t *testing.T) {
	t.Parallel()
	s := newSession(t)
	g := gate{code: "const t = 'lorem ipsum dolor '.repeat(50000); return t.match(/\\b\\w+\\b/g).length + ' ' + t.replace(/o(?=r)/g, '0').length + ' ' + t.split(' ').length", want: "150000 900000 150001"}
	runGates(t, s, []gate{g, g})
}

func TestGatesPerf(t *testing.T) {
	if testing.Short() {
		t.Skip("short")
	}
	s := newSession(t)
	runGates(t, s, []gate{{code: "const r = await Promise.all([1,2,3,4,5,6,7,8].map(() => sleep(200))); return r.length", want: "8"}})
	if s.maxPend != 8 {
		t.Errorf("8 parallel host calls: at most %d were in flight at once", s.maxPend)
	}
	runGates(t, s, []gate{
		{code: "let s = 0; for (let i = 0; i < 1e7; i++) s += i % 7; return s", want: "29999994"},
		{code: "const words = []; for (let i = 0; i < 200000; i++) words.push('w' + (i % 5000)); const m = new Map(); for (const w of words) m.set(w, (m.get(w) || 0) + 1); return [...m.entries()].sort((a, b) => b[1] - a[1]).slice(0, 2).join()", want: "w0,40,w1,40"},
		{code: "const r = await mcp('linear', 'list_issues', {n: 8000}); return JSON.stringify(r).length + ' ' + r.nodes.filter(i => i.state.name === 'Todo').length", want: "6218002 1600"},
		{code: "const t = 'lorem ipsum dolor '.repeat(50000); return t.match(/\\b\\w+\\b/g).length + ' ' + t.replace(/o(?=r)/g, '0').length + ' ' + t.split(' ').length", want: "150000 900000 150001"},
	})
}
