package harness

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func fresh() *Session {
	vm, err := NewVM()
	if err != nil {
		fmt.Println("newVM:", err)
		os.Exit(1)
	}
	s, err := NewSession(vm)
	if err != nil {
		fmt.Println("session:", err)
		os.Exit(1)
	}
	return s
}

func one(s *Session, code string, d time.Duration) (string, error, time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	t := time.Now()
	out, err := s.Run(ctx, code)
	showOff = s.Off
	return out, err, time.Since(t)
}

func show(name string, out string, err error, d time.Duration) {
	full := out
	e := ""
	if err != nil {
		e = " ERR: " + strings.ReplaceAll(fixLines(err.Error(), showOff), "\n", " ⏎ ")
	}
	if len(out) > 300 {
		out = out[:300] + "…"
	}
	record(Rec{Name: name, Ms: d.Milliseconds(), Out: full, Err: errText(err)})
	fmt.Printf("%-22s %6dms  out=%q%s\n", name, d.Milliseconds(), out, cut(e, 420))
}

func cut(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

var showOff int

func rssMB() float64 {
	var ru syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	return float64(ru.Maxrss) / (1 << 20)
}

func gates() {
	s := fresh()
	o, e, d := one(s, "const t = Date.now(); const r = await Promise.all([1,2,3,4,5,6,7,8].map(() => sleep(200))); return (Date.now() - t) + ' ' + r.length", 5*time.Second)
	show("parallel8x200", o, e, d)

	s = fresh()
	o, e, d = one(s, "globalThis.a = 1; var b = 2; const c = 3; function f(){ return 4 } ; e2 = 5", time.Second)
	show("iife-state-set", o, e, d)
	o, e, d = one(s, "return [typeof a, typeof b, typeof c, typeof f, typeof e2].join()", time.Second)
	show("iife-state-get", o, e, d)

	s = fresh()
	s.Mode = "native"
	o, e, d = one(s, "const c = 3; let d = 4; var b = 2; function f(){ return 4 }; class K {}", time.Second)
	show("native-state-set", o, e, d)
	o, e, d = one(s, "console.log([typeof c, typeof d, typeof b, typeof f, typeof K].join())", time.Second)
	show("native-state-get", o, e, d)
	o, e, d = one(s, "const c = 5; console.log('redeclared const', c)", time.Second)
	show("native-redeclare-const", o, e, d)
	o, e, d = one(s, "let d = 6; console.log('redeclared let', d)", time.Second)
	show("native-redeclare-let", o, e, d)
	if os.Getenv("SKIP_TLA") == "" {
		o, e, d = one(s, "const r = await sleep(20); console.log('tla', r)", 2*time.Second)
		show("native-tla", o, e, d)
		o, e, d = one(s, "console.log(typeof r, typeof c)", time.Second)
		show("native-tla-state", o, e, d)
		o, e, d = one(s, "const q = await Promise.all([sleep(100), sleep(100)]); console.log(q)", 2*time.Second)
		show("native-tla-par", o, e, d)
	}
	o, e, d = one(s, "const z = 1;\nnull.x;", time.Second)
	show("native-throw", o, e, d)
	if os.Getenv("SKIP_TLA") == "" {
		o, e, d = one(s, "await fail('x')", time.Second)
		show("native-reject", o, e, d)
	}

	s = fresh()
	_, _, _ = one(s, "globalThis.keep = 42", time.Second)
	o, e, d = one(s, "while (true) {}", 300*time.Millisecond)
	show("cancel-busy", o, e, d)
	o, e, d = one(s, "return keep + 1", time.Second)
	show("after-cancel-busy", o, e, d)
	o, e, d = one(s, "console.log('before'); await sleep(5000); return 'late'", 300*time.Millisecond)
	show("cancel-pending", o, e, d)
	o, e, d = one(s, "return keep + 2", time.Second)
	show("after-cancel-pending", o, e, d)
	o, e, d = one(s, "await new Promise(() => {})", time.Second)
	show("never-settles", o, e, d)

	s = fresh()
	o, e, d = one(s, "const a = 1;\nconst b = 2;\nnull.x;\nreturn 1", time.Second)
	show("err-throw-line3", o, e, d)
	o, e, d = one(s, "const a = 1;\nconst b = ;\nreturn 1", time.Second)
	show("err-syntax-line2", o, e, d)
	o, e, d = one(s, "const x = 1;\nawait fail('nope');\nreturn 1", time.Second)
	show("err-tool-uncaught", o, e, d)
	o, e, d = one(s, "try { await fail('nope') } catch (e) { return e.name + '|' + e.message }", time.Second)
	show("err-tool-caught", o, e, d)
	o, e, d = one(s, "function g(){ undefinedFn() }\nfunction h(){ g() }\nh()", time.Second)
	show("err-stack", o, e, d)
	o, e, d = one(s, "sleep(50).then(() => { throw new Error('late') }); fail('unhandled'); return 'ok'", time.Second)
	show("unawaited", o, e, d)
	o, e, d = one(s, "function f(){ return f() + 1 }\ntry { f() } catch (e) { return 'caught ' + e.name }", 5*time.Second)
	show("recursion", o, e, d)

	s = fresh()
	o, e, d = one(s, perfItems["loop1e7"].code, perfItems["loop1e7"].timeout)
	show("loop1e7", o, e, d)
	o, e, d = one(s, perfItems["map-sort"].code, perfItems["map-sort"].timeout)
	show("map-sort", o, e, d)
	o, e, d = one(s, perfItems["regex"].code, perfItems["regex"].timeout)
	show("regex", o, e, d)
	before := rssMB()
	o, e, d = one(s, perfItems["mcp8000"].code, perfItems["mcp8000"].timeout)
	show("mcp8000", o, e, d)
	record(Rec{Name: "rss-mcp", Vals: map[string]float64{"before": before, "after": rssMB()}})
	fmt.Printf("%-22s before=%.0fMB after=%.0fMB\n", "rss-mcp", before, rssMB())
}

func memtest() {
	s := fresh()
	o, e, d := one(s, "const a = []; try { while (true) a.push(new Array(1e6).fill(1.5)) } catch (e) { return 'caught ' + e.name + ' ' + a.length }", 4*time.Second)
	show("memory-runaway", o, e, d)
	record(Rec{Name: "rss-peak", Vals: map[string]float64{"mb": rssMB()}})
	fmt.Printf("%-22s %.0fMB\n", "rss-peak", rssMB())
	o, e, d = one(s, "return 1 + 1", time.Second)
	show("after-memory", o, e, d)
}

func startup() {
	t := time.Now()
	s := fresh()
	first := time.Since(t)
	o, _, _ := one(s, "return 1", time.Second)
	firstRun := time.Since(t)
	idle := rssMB()
	t = time.Now()
	const n = 20
	for i := 0; i < n; i++ {
		x := fresh()
		_, _, _ = one(x, "return 1", time.Second)
		x.vm.Close()
	}
	record(Rec{Name: "startup", Out: o, Vals: map[string]float64{"firstVM_us": float64(first.Microseconds()), "firstRun_us": float64(firstRun.Microseconds()), "nextVM_us": float64(time.Since(t).Microseconds()) / n, "idleRSS": idle, "rssAfter": rssMB()}})
	fmt.Printf("startup: firstVM=%dms firstRun=%dms (%s) nextVM+run=%.1fms idleRSS=%.0fMB rssAfter%d=%.0fMB\n", first.Milliseconds(), firstRun.Milliseconds(), o, float64(time.Since(t).Microseconds())/1000/n, idle, n, rssMB())
}

type probe struct {
	Name string `json:"name"`
	Code string `json:"code"`
}

func feat(file string) {
	b, err := os.ReadFile(file)
	if err != nil {
		panic(err)
	}
	var ps []probe
	if err := json.Unmarshal(b, &ps); err != nil {
		panic(err)
	}
	pass := 0
	for _, p := range ps {
		s := fresh()
		o, e, _ := one(s, p.Code, 3*time.Second)
		s.vm.Close()
		if e == nil && strings.TrimSpace(o) == "true" {
			pass++
			continue
		}
		msg := o
		if e != nil {
			msg = strings.SplitN(e.Error(), "\n", 2)[0]
		}
		record(Rec{Name: p.Name, Err: msg})
		fmt.Printf("FAIL %-28s %s\n", p.Name, cut(msg, 110))
	}
	record(Rec{Name: "features", Vals: map[string]float64{"pass": float64(pass), "total": float64(len(ps))}})
	fmt.Printf("features: %d/%d\n", pass, len(ps))
}

func runFile(file, dir, mode string) {
	b, err := os.ReadFile(file)
	if err != nil {
		panic(err)
	}
	toolDir = dir
	s := fresh()
	s.Mode = mode
	o, e, d := one(s, string(b), 60*time.Second)
	res := map[string]any{"out": o, "ms": d.Milliseconds(), "calls": s.calls}
	if e != nil {
		res["err"] = e.Error()
	}
	j, _ := json.Marshal(res)
	fmt.Println(string(j))
}

var lineRe = regexp.MustCompile(`(cell\.js|<eval>):(\d+)`)

func fixLines(msg string, off int) string {
	if off == 0 {
		return msg
	}
	return lineRe.ReplaceAllStringFunc(msg, func(m string) string {
		sub := lineRe.FindStringSubmatch(m)
		n, _ := strconv.Atoi(sub[2])
		return sub[1] + ":" + strconv.Itoa(n-off)
	})
}

func capOut(s string) string {
	const limit = 8000
	if len(s) <= limit {
		return s
	}
	return s[:limit/2] + fmt.Sprintf("\n[... %d chars truncated ...]\n", len(s)-limit) + s[len(s)-limit/2:]
}

func serve(dir string) {
	toolDir = dir
	s := fresh()
	s.Mode = "repl"
	in := bufio.NewReaderSize(os.Stdin, 1<<20)
	enc := json.NewEncoder(os.Stdout)
	for {
		line, err := in.ReadBytes('\n')
		if len(line) == 0 && err != nil {
			return
		}
		var req struct{ Code string }
		_ = json.Unmarshal(line, &req)
		s.names = nil
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		t := time.Now()
		out, e := s.Run(ctx, req.Code)
		cancel()
		res := map[string]any{"calls": s.names, "ms": time.Since(t).Milliseconds(), "failed": e != nil}
		if e != nil {
			msg := fixLines(e.Error(), s.Off)
			if e == errTimeout {
				msg = "Error: timed out after 120s"
			}
			out += "Error: " + strings.TrimPrefix(msg, "Error: ")
			res["err"] = msg
		}
		if out == "" {
			out = "(no output)"
		}
		res["out"] = capOut(out)
		_ = enc.Encode(res)
	}
}

func stateTests() {
	defer stateTests2()
	s := fresh()
	s.Mode = "repl"
	steps := []string{
		"const a = await sleep(5);\nconst {b, c} = {b: 2, c: 3};\nclass K { m() { return 9 } }\nfunction f() { return a + b }\na",
		"let count = 0;\nfunction inc() { count++ }\nconsole.log(a, b, c, new K().m(), f())",
		"inc(); inc();\ncount",
		"const a = 100;\nf()",
		"class K { m() { return 1 } }\nfunction f() { return 'new' }\n[new K().m(), f()].join()",
		"const r = [1,2,3].map(x => x * 2)\nreturn r.length",
		"if (a > 0) { return 'early' }\nreturn 'late'",
		"const [p, q] = await Promise.all([sleep(50), sleep(60)]);\n({p, q})",
		"const items = (await mcp('linear', 'list', {n: 50})).nodes;\nitems.length",
		"const todo = items.filter(i => i.state.name === 'Todo');\nlet n;\nn = todo.length;\nn + p",
		"const x = 1;\nundefinedThing.x",
		"const y = 1;\nawait fail('bad')",
		"const z = ;",
		"for (const i of [1,2]) { console.log(i) }\nawait new Promise(r => setTimeout(r, 20));\n'done ' + typeof i",
		"var v1 = 1; let v2 = 2\nv1 + v2",
		"v1 + v2 + n",
	}
	for i, c := range steps {
		o, e, d := one(s, c, 2*time.Second)
		show(fmt.Sprintf("repl-%d", i), o, e, d)
	}
}

func stateTests2() {
	s := fresh()
	s.Mode = "repl"
	steps := []string{
		"const {a} = (await mcp('x', 'y', {n: 3})); // note\na.nodes",
		"// lead\nconst {nodes: [first, ...rest]} = (await mcp('x', 'y', {n: 3}))\n// trailing comment\n(async () => { return first.identifier + rest.length })() // c",
		"const fmt = (x) => `<${x}>`;\n[1, 2].map(fmt).join('') // done",
		"const big = await mcp('x', 'y', {n: 3}); big.nodes.map(n => ({ id: n.identifier, s: n.state.name }));",
		"const s = `const x = 1;\nlet y = 2;\nclass Z {}`\ns.length",
		"await fail('with line')",
		"const o = {a: 1,\n  b: 2}\nfunction helper() {\n  const inner = 1\n  return inner + o.b\n}\nhelper()",
		"helper() + o.a",
	}
	for i, c := range steps {
		o, e, d := one(s, c, 2*time.Second)
		show(fmt.Sprintf("repl2-%d", i), o, e, d)
	}
}
