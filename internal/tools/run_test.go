package tools

import (
	"container/heap"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/paul-nameless/qi/internal/config"
	"github.com/paul-nameless/qi/internal/jsvm"
	"github.com/paul-nameless/qi/internal/llm"
	"github.com/paul-nameless/qi/internal/mcp"
)

type cellKit struct {
	t    *testing.T
	dir  string
	env  *Env
	run  *Tool
	ctx  context.Context
	tick atomic.Int32
}

func newCellKit(t *testing.T, extra ...*Function) *cellKit {
	k := &cellKit{t: t, dir: t.TempDir(), ctx: context.Background()}
	k.env = &Env{Cwd: k.dir, SpillDir: t.TempDir()}
	k.run = Run(append([]*Function{Read(), Write(), Edit(), Bash(), JobsFunc(), k.tickTool()}, extra...)...)
	return k
}

func (k *cellKit) tickTool() *Function {
	return &Function{
		Name: "tick",
		Code: CodeSpec{Name: "tick", Params: []string{"n"}, Doc: "tick(n) → number"},
		Run: func(ctx context.Context, env *Env, in json.RawMessage, _ func(string)) Result {
			k.tick.Add(1)
			return Result{Text: "tick", Value: json.RawMessage(`{"ticks":1}`)}
		},
	}
}

func (k *cellKit) cell(code string) Result {
	k.t.Helper()
	return k.cellCtx(k.ctx, code, 0)
}

func (k *cellKit) cellCtx(ctx context.Context, code string, timeout int) Result {
	return k.cellProgress(ctx, code, timeout, nil)
}

func (k *cellKit) cellProgress(ctx context.Context, code string, timeout int, progress func(string)) Result {
	in, _ := json.Marshal(runArgs{Code: code, Timeout: timeout})
	return k.run.r.run(ctx, k.env, in, progress)
}

func (k *cellKit) second(d time.Duration) {
	k.run.r.second = d
}

func (k *cellKit) want(code, want string) {
	k.t.Helper()
	res := k.cell(code)
	if res.IsError || res.Text != want {
		k.t.Errorf("cell %q:\ngot  %q (error=%v)\nwant %q", code, res.Text, res.IsError, want)
	}
}

func (k *cellKit) wantErr(code string, parts ...string) Result {
	k.t.Helper()
	res := k.cell(code)
	if !res.IsError {
		k.t.Errorf("cell %q: expected an error, got %q", code, res.Text)
	}
	for _, p := range parts {
		if !strings.Contains(res.Text, p) {
			k.t.Errorf("cell %q: %q lacks %q", code, res.Text, p)
		}
	}
	return res
}

func (k *cellKit) file(name, content string) string {
	k.t.Helper()
	p := filepath.Join(k.dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		k.t.Fatal(err)
	}
	return p
}

func TestCellBasics(t *testing.T) {
	t.Parallel()
	k := newCellKit(t)
	cases := []struct{ code, want string }{
		{"const a = 1; let b = [1, 2]", "(no output)"},
		{"const a = 5; a + b.length", "7"},
		{"await sleep(1); return 'done'", "done"},
		{"return {a: [1, 2], s: 'x'}", "{\n \"a\": [\n  1,\n  2\n ],\n \"s\": \"x\"\n}"},
		{"console.log('one'); print('two', {k: 1}); console.error('three'); 'end'", "one\ntwo {\n  \"k\": 1\n}\nthree\nend"},
		{"undefined", "(no output)"},
		{"null", "null"},
		{"const t = []; setTimeout((x) => t.push(x), 5, 'late'); const c = setTimeout(() => t.push('never'), 1); clearTimeout(c); await sleep(30); t", "[\n \"late\"\n]"},
		{"tick(1)", "{\n \"ticks\": 1\n}"},
	}
	for _, c := range cases {
		k.want(c.code, c.want)
	}
}

func TestCellParallelSleeps(t *testing.T) {
	t.Parallel()
	k := newCellKit(t)
	start := time.Now()
	k.want("await Promise.all([1,2,3,4,5,6,7,8].map(() => sleep(200))); 'ok'", "ok")
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Errorf("8 sleeps took %v", d)
	}
}

func TestCellReadRawAndStaleGuard(t *testing.T) {
	t.Parallel()
	k := newCellKit(t)
	p := k.file("f.txt", "one\r\ntwo\nthree\n")
	k.want(`globalThis.s = await read("f.txt"); JSON.stringify(s)`, `"one\r\ntwo\nthree\n"`)
	k.want(`JSON.stringify(await read("f.txt", {offset: 2, limit: 1}))`, `"two\n"`)
	k.want(`JSON.stringify(await read("f.txt", {offset: 3}))`, `"three\n"`)
	k.file("empty.txt", "")
	k.want(`JSON.stringify(await read("empty.txt"))`, `""`)
	k.want(`await write("f.txt", s.toUpperCase()); await read("f.txt")`, "ONE\r\nTWO\nTHREE\n")
	future := time.Now().Add(time.Hour)
	os.Chtimes(p, future, future)
	k.wantErr(`await write("f.txt", "x")`, "ToolError: ", "changed on disk")
	k.want(`await read("f.txt"); (await write("f.txt", "x\n")).split(" ")[0]`, "updated")
	k.wantErr(`await read("f.txt", {lines: 3})`, "read: unknown option lines")
}

func TestCellEdit(t *testing.T) {
	t.Parallel()
	k := newCellKit(t)
	k.file("a.go", "x := 1\ny := x + x\n")
	k.cell(`await edit("a.go", "x", "z", {all: true})`)
	k.cell(`await edit({file_path: "a.go", old_string: "y", new_string: "w"})`)
	data, _ := os.ReadFile(filepath.Join(k.dir, "a.go"))
	if string(data) != "z := 1\nw := z + z\n" {
		t.Errorf("got %q", data)
	}
	k.wantErr(`await edit("a.go", "z", "q")`, "matches 3 places")
	k.wantErr(`await edit("a.go", "z", "q", true)`, "edit takes at most 3 arguments")
}

func TestCellBashShape(t *testing.T) {
	t.Parallel()
	k := newCellKit(t)
	k.want(`JSON.stringify(await bash("echo hi"))`, `{"output":"hi","exit_code":0,"timed_out":false,"truncated":false}`)
	k.want(`JSON.stringify(await bash("echo oops; exit 3"))`, `{"output":"oops","exit_code":3,"timed_out":false,"truncated":false}`)
	k.want(`JSON.stringify(await bash("echo start; sleep 5", {timeout: 1}))`, `{"output":"start","exit_code":-1,"timed_out":true,"truncated":false}`)
	k.want(`JSON.stringify(await bash({command: "printf 'a\\n\\nb\\n\\n'"}))`, `{"output":"a\n\nb","exit_code":0,"timed_out":false,"truncated":false}`)
	k.want("(await bash(String.raw`echo ab.c | sed -E 's/(b)\\.c/\\1x/'; printf 'p\\nq'`)).output", "abx\np\nq")
}

func TestCellToolError(t *testing.T) {
	t.Parallel()
	k := newCellKit(t)
	k.env.ReadOnly = func() bool { return true }
	k.wantErr("\n\nawait write('x.txt', 'y')", "ToolError: read-only mode: write is not allowed\n    at cell.js:3:")
	k.want(`try { await write("x.txt", "y") } catch (e) { return [e.name, e.denied, e.message].join("|") }`, "ToolError|true|read-only mode: write is not allowed\nFailed calls:\n✗ write x.txt: read-only mode: write is not allowed")
	k.want(`try { await bash("touch x.txt") } catch (e) { return e.denied }`, "true\nFailed calls:\n✗ bash touch x.txt: read-only mode: \"touch x.txt\" is not in the read-only allowlist")
	if res := k.cell(`try { await read("nope.txt") } catch (e) { return e.denied }`); res.IsError || !strings.HasPrefix(res.Text, "false\nFailed calls:\n✗ read nope.txt: stat ") {
		t.Errorf("caught read failure: %+v", res)
	}
	k.env.ReadOnly = nil
	k.env.PreHook = func(ctx context.Context, c HookCall) (bool, string, json.RawMessage) {
		if c.Tool == "Write" && strings.Contains(string(c.Input), "blocked") {
			return true, "no writes there", nil
		}
		if c.Tool == "Write" {
			return false, "", json.RawMessage(`{"file_path":"rewritten.txt","content":"hooked"}`)
		}
		return false, "", nil
	}
	k.want(`try { await write("blocked.txt", "y") } catch (e) { return e.denied + " " + e.message }`, "true blocked by hook: no writes there\nFailed calls:\n✗ write blocked.txt: blocked by hook: no writes there")
	k.cell(`await write("orig.txt", "y")`)
	if data, err := os.ReadFile(filepath.Join(k.dir, "rewritten.txt")); err != nil || string(data) != "hooked" {
		t.Errorf("rewritten input not applied: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(k.dir, "orig.txt")); err == nil {
		t.Error("original input ran")
	}
}

func TestCellSerializesCallsOnOnePath(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	active, overlap := map[string]int{}, false
	running, peak := 0, 0
	slow := &Function{
		Name: "slow",
		Code: CodeSpec{Name: "slow", Params: []string{"file_path"}, Required: 1},
		Path: pathKey,
		Run: func(ctx context.Context, env *Env, in json.RawMessage, _ func(string)) Result {
			p := pathKey(env, in)
			mu.Lock()
			active[p]++
			running++
			peak = max(peak, running)
			overlap = overlap || active[p] > 1
			mu.Unlock()
			time.Sleep(100 * time.Millisecond)
			mu.Lock()
			active[p]--
			running--
			mu.Unlock()
			return Result{Text: "done"}
		},
	}
	k := newCellKit(t, slow)
	if res := k.cell(`await Promise.all([slow("a.txt"), slow("b.txt"), slow("./a.txt")])`); res.IsError {
		t.Fatal(res.Text)
	}
	if overlap {
		t.Error("calls on the same path overlapped")
	}
	if peak < 2 {
		t.Error("calls on different paths did not run in parallel")
	}
}

func TestRunFiresHooksOnlyForInnerCalls(t *testing.T) {
	t.Parallel()
	k := newCellKit(t)
	k.file("f.txt", "content\n")
	var mu sync.Mutex
	var seen []string
	record := func(event, tool string) {
		mu.Lock()
		seen = append(seen, event+" "+tool)
		mu.Unlock()
	}
	k.env.PreHook = func(ctx context.Context, c HookCall) (bool, string, json.RawMessage) {
		record("pre", c.Tool)
		return false, "", nil
	}
	k.env.PostHook = func(ctx context.Context, c HookCall, r Result) string {
		record("post", c.Tool)
		return ""
	}
	in, _ := json.Marshal(runArgs{Code: `await read("f.txt")`})
	res := k.run.Execute(k.ctx, k.env, []llm.Block{{ID: "1", Name: "run", Input: in}}, nopUI{})
	if res[0].IsError || strings.Join(seen, ",") != "pre Read,post Read" {
		t.Errorf("hooks saw %v, result %+v", seen, res[0])
	}
}

func TestCellPostHookStaysOutOfValues(t *testing.T) {
	t.Parallel()
	k := newCellKit(t)
	k.file("f.txt", "content\n")
	var calls []HookCall
	var mu sync.Mutex
	k.env.PostHook = func(ctx context.Context, c HookCall, r Result) string {
		mu.Lock()
		calls = append(calls, c)
		mu.Unlock()
		return "POST " + c.Tool
	}
	res := k.cell(`const r = await read("f.txt"); JSON.stringify(r)`)
	if res.Text != "\"content\\n\"\nPOST Read" {
		t.Errorf("got %q", res.Text)
	}
	if len(calls) != 1 || string(calls[0].Input) != `{"file_path":"f.txt"}` || calls[0].Names[0] != "read" {
		t.Errorf("hook calls %+v", calls)
	}
}

func TestCellUnawaitedCalls(t *testing.T) {
	t.Parallel()
	k := newCellKit(t)
	res := k.cell(`bash("sleep 0.3; echo done > out.txt"); edit("missing.txt", "a", "b"); "returned"`)
	if _, err := os.Stat(filepath.Join(k.dir, "out.txt")); err != nil {
		t.Errorf("unawaited bash was not waited for: %v", err)
	}
	for _, want := range []string{"returned\n", "Note: 2 calls were still running", "Failed:\nedit missing.txt: "} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("%q lacks %q", res.Text, want)
		}
	}
	if res.IsError {
		t.Error("unawaited calls made the cell fail")
	}
}

func TestCellNeverSettles(t *testing.T) {
	t.Parallel()
	k := newCellKit(t)
	start := time.Now()
	k.wantErr(`await new Promise(() => {})`, "can never settle")
	if time.Since(start) > time.Second {
		t.Error("never-settling promise was not detected fast")
	}
}

func TestCellBusyLoopStops(t *testing.T) {
	t.Parallel()
	k := newCellKit(t)
	k.second(50 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	res := k.cellProgress(ctx, `globalThis.kept = 1; print("looping"); let i = 0; while (true) { i++ }`, 0, func(string) { cancel() })
	if !res.IsError || !strings.Contains(res.Text, "interrupted by user") {
		t.Errorf("cancel: %+v", res)
	}
	res = k.cellCtx(context.Background(), "kept = 2; while (true) {}", 1)
	if !res.IsError || !strings.Contains(res.Text, "timed out after 1s") {
		t.Errorf("timeout: %+v", res)
	}
	k.want("kept + 1", "3")
}

func TestCellStopsCallingAfterCancel(t *testing.T) {
	t.Parallel()
	k := newCellKit(t)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for deadline := time.Now().Add(5 * time.Second); k.tick.Load() < 3 && time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		}
		cancel()
	}()
	res := k.cellCtx(ctx, `globalThis.n = 0; while (true) { try { n++; await tick(); await bash("sleep 0.02") } catch {} }`, 0)
	if !strings.Contains(res.Text, "interrupted by user") {
		t.Fatalf("got %q", res.Text)
	}
	after := k.tick.Load()
	time.Sleep(100 * time.Millisecond)
	if k.tick.Load() != after || after == 0 {
		t.Errorf("ticks %d then %d", after, k.tick.Load())
	}
	if n := k.cell("n").Text; n != fmt.Sprint(after) && n != fmt.Sprint(after+1) {
		t.Errorf("n = %s, ticks %d", n, after)
	}
}

func TestCellFiresTimersInDeadlineOrder(t *testing.T) {
	t.Parallel()
	k := newCellKit(t)
	c := newCell(context.Background(), k.run.r, k.env, nil, time.Hour)
	defer c.clock.stop()
	var got []string
	timer := func(name string, due time.Time) int {
		p := &pendingCall{kind: sleepCall, resolve: func(jsvm.Value) { got = append(got, name) }, stop: make(chan struct{}), due: due}
		id := c.add(p)
		heap.Push(&c.timers, timerAt{due: due, id: id})
		return id
	}
	now := time.Now()
	timer("early", now.Add(-time.Second))
	late := timer("late", now)
	c.settle(outcome{id: late})
	if !slices.Equal(got, []string{"early"}) {
		t.Fatalf("the late timer reported first and timers fired as %v, want the early one first", got)
	}
	if !c.next() {
		t.Fatal("the cell stopped")
	}
	c.wg.Wait()
	if !slices.Equal(got, []string{"early", "late"}) {
		t.Errorf("timers fired as %v, want early then late", got)
	}
}

func TestCellTimeoutSparesFormatting(t *testing.T) {
	t.Parallel()
	t.Run("in time", func(t *testing.T) {
		t.Parallel()
		k := newCellKit(t)
		k.second(300 * time.Millisecond)
		res := k.cellCtx(context.Background(), `return {toJSON() { const t = Date.now(); while (Date.now() - t < 600) {} return "slow" }}`, 1)
		if res.IsError || res.Text != `"slow"` {
			t.Errorf("a value finished in time was cut by the cell timeout: %+v", res)
		}
		k.want("1 + 1", "2")
	})
	t.Run("endless", func(t *testing.T) {
		t.Parallel()
		testEndlessFormatStopped(t)
	})
}

func testEndlessFormatStopped(t *testing.T) {
	k := newCellKit(t)
	k.second(100 * time.Millisecond)
	done := make(chan Result, 1)
	start := time.Now()
	go func() { done <- k.cellCtx(context.Background(), `return {toJSON() { while (true) {} }}`, 0) }()
	select {
	case res := <-done:
		if d, bound := time.Since(start), formatSeconds*k.run.r.unit(); d < bound {
			t.Errorf("an endless toJSON was stopped after %v, before the %v format bound", d, bound)
		}
		if !strings.Contains(res.Text, "the value could not be shown") {
			t.Errorf("endless toJSON: %q", res.Text)
		}
	case <-time.After(formatSeconds*k.run.r.unit() + 3*time.Second):
		t.Fatal("formatting an endless toJSON was not stopped")
	}
	k.second(0)
	k.want("1 + 1", "2")
}

func TestCellJobsDrainAfterThrow(t *testing.T) {
	t.Parallel()
	k := newCellKit(t)
	k.wantErr(`Promise.resolve().then(() => console.log("leak")); null.x`, "leak\nTypeError")
	k.want("1", "1")
}

func TestCellResultsNotCut(t *testing.T) {
	t.Parallel()
	k := newCellKit(t)
	k.want(`(await bash("seq 1 5000")).output.split("\n").length`, "5000")
	content := strings.Repeat("short line\n", 2500) + strings.Repeat("x", 5000) + "\n"
	k.file("big.txt", content)
	k.want(`(await read("big.txt")).length`, fmt.Sprint(len(content)))
}

func TestCellInterruptThroughHostCalls(t *testing.T) {
	t.Parallel()
	k := newCellKit(t, Subagent("m", nil, nil))
	k.second(300 * time.Millisecond)
	cases := []string{
		`while (true) { try { print({toJSON() { while (true) {} }}) } catch (e) {} }`,
		`while (true) { try { await tick({toJSON() { while (true) {} }}) } catch (e) {} }`,
		`subagent("quick"); while (true) {}`,
	}
	k.env.Subagent = func(ctx context.Context, task SubagentTask, progress func(string)) (string, error) { return "ok", nil }
	for _, code := range cases {
		done := make(chan Result, 1)
		go func() { done <- k.cellCtx(context.Background(), code, 1) }()
		select {
		case res := <-done:
			if !strings.Contains(res.Text, "timed out after 1s") {
				t.Errorf("%s: %q", code, res.Text)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("%s: not stopped by the timeout", code)
		}
	}
}

func TestCellBackgroundJobSurvives(t *testing.T) {
	t.Parallel()
	k := newCellKit(t)
	k.env.Jobs = NewJobs()
	defer k.env.Jobs.KillAll()
	res := k.cell(`await bash("sleep 0.3; echo ok", {background: true})`)
	if res.IsError || !strings.Contains(res.Text, "Started background job 1") {
		t.Fatalf("got %+v", res)
	}
	select {
	case <-k.env.Jobs.Changed():
	case <-time.After(5 * time.Second):
		t.Fatal("job never finished")
	}
	if n := k.env.Jobs.TakeNotices(); len(n) != 1 || n[0].Status != "exit 0" {
		t.Errorf("notices %+v", n)
	}
}

func TestCellCallLimit(t *testing.T) {
	t.Parallel()
	k := newCellKit(t)
	k.want(`let i = 0; try { for (; i < 600; i++) await tick() } catch (e) { return i + " " + e.name + " " + e.message }`,
		"500 ToolError too many tool calls in one run call (limit 500); split the work across several run calls")
	k.want(`await tick(); "next cell ok"`, "next cell ok")
}

func TestCellReset(t *testing.T) {
	t.Parallel()
	k := newCellKit(t)
	k.cell("globalThis.z = 1")
	k.run.Reset(true)
	k.want("typeof z", newVMNote+"\nundefined")
	k.want("typeof z", "undefined")
	k.run.Reset(false)
	k.want("1", "1")
}

func TestRunPanicAndReplaceThroughExecute(t *testing.T) {
	t.Parallel()
	boom := &Function{Name: "boom", Code: CodeSpec{Name: "boom"}, Run: func(context.Context, *Env, json.RawMessage, func(string)) Result {
		panic("boom")
	}}
	k := newCellKit(t, boom)
	exec := func(code string) llm.Block {
		return k.run.Execute(k.ctx, k.env, []llm.Block{{ID: "1", Name: "run", Input: mustJSON(t, runArgs{Code: code})}}, nopUI{})[0]
	}
	if b := exec(`try { await boom() } catch (e) { return e.message }`); b.IsError || !strings.Contains(b.Text, "tool boom panicked: boom") {
		t.Errorf("panicking function: %+v", b)
	}
	k.run.Replace(&Function{Name: "tick", Code: CodeSpec{Name: "tick"}, Run: func(context.Context, *Env, json.RawMessage, func(string)) Result {
		return Result{Text: "tock", Value: json.RawMessage(`"tock"`)}
	}})
	if b := exec(`return await tick()`); b.IsError || !strings.HasPrefix(b.Text, "tock") {
		t.Errorf("replaced function not called: %+v", b)
	}
}

func TestRunReplaceAndHint(t *testing.T) {
	t.Parallel()
	sub := func(model string) *Function { return Subagent(model, nil, []string{"low"}) }
	build := func() *Tool {
		return Run(Read(), Write(), Edit(), Bash(), JobsFunc(), sub("m1"))
	}
	r := build()
	if r.Def.Name != "run" {
		t.Fatalf("def %+v", r.Def)
	}
	if !strings.Contains(r.Def.Description, "- m1: the current model") {
		t.Error("description lacks the subagent menu")
	}
	r.Replace(sub("m2"))
	desc := r.Def.Description
	if !strings.Contains(desc, "- m2: the current model") || strings.Contains(desc, "m1") {
		t.Errorf("description after Replace:\n%s", desc)
	}
	res := r.Execute(context.Background(), &Env{Cwd: t.TempDir()}, []llm.Block{
		{ID: "1", Name: "edit", Input: json.RawMessage(`{}`)},
		{ID: "2", Name: "nope", Input: json.RawMessage(`{}`)},
	}, nopUI{})
	if res[0].Text != `unknown tool "edit": call await edit(...) inside run` || res[1].Text != `unknown tool "nope"` {
		t.Errorf("hints: %q %q", res[0].Text, res[1].Text)
	}
	a, _ := json.Marshal(build().Def)
	b, _ := json.Marshal(build().Def)
	if string(a) != string(b) {
		t.Error("run description is not byte-stable")
	}
}

func TestCellErrorsText(t *testing.T) {
	t.Parallel()
	k := newCellKit(t)
	k.wantErr("let x = ;", "SyntaxError: Unexpected token ';'\n    at cell.js:1:9")
	k.wantErr("1\nclass A {}", "SyntaxError: unsupported: classes\n    at cell.js:2:")
	k.wantErr("missing", "ReferenceError: missing is not defined", "No variables are defined in this VM yet.")
	k.cell("const alpha = 1; function beta() {}")
	k.wantErr("gamma", "ReferenceError: gamma is not defined\n    at cell.js:1:1", "Variables defined in this VM: ", "alpha", "beta")
	k.file("f.txt", "x\n")
	res := k.wantErr(`await read("f.txt"); await tick(); null.x`, "TypeError", "Calls that completed:\n✓ read f.txt\n✓ tick")
	if res.Summary != "2 calls · TypeError: Cannot read properties of null (reading 'x')" {
		t.Errorf("summary %q", res.Summary)
	}
	k.wantErr("throw 5", "Uncaught 5")
}

func TestCellOutputCap(t *testing.T) {
	t.Parallel()
	k := newCellKit(t)
	res := k.cell(`for (let i = 0; i < 5000; i++) console.log("line " + i)`)
	for _, want := range []string{"line 0\n", "line 4999", "lines omitted. Full output: "} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("capped output lacks %q", want)
		}
	}
	if len(res.Text) > cellOutBytes+500 || strings.Count(res.Text, "\n") > cellOutLines {
		t.Errorf("output not capped: %d bytes", len(res.Text))
	}
}

func TestCellImages(t *testing.T) {
	t.Parallel()
	k := newCellKit(t)
	k.file("a.png", string(testPNG(t, 4, 4, func(x, y int) color.RGBA { return color.RGBA{255, 0, 0, 255} })))
	res := k.cell(`const note = await read("a.png"); typeof note`)
	if res.IsError || len(res.Images) != 1 || res.Text != "string" {
		t.Errorf("image read: %q, %d images", res.Text, len(res.Images))
	}
	res = k.cell(`for (let i = 0; i < 10; i++) await read("a.png")`)
	if len(res.Images) != maxCellImages || !strings.Contains(res.Text, "2 more were dropped") {
		t.Errorf("image cap: %q, %d images", res.Text, len(res.Images))
	}
	want := fmt.Sprintf("so you can see it (at most %d images per run call); the user does not see it, so to show an image to the user, put its full path in your answer.", maxCellImages)
	if !strings.Contains(k.run.Def.Description, want) {
		t.Errorf("read doc lacks %q", want)
	}
}

func TestCellMCP(t *testing.T) {
	t.Parallel()
	m := mcp.NewManager(map[string]config.MCPServer{"fake": fakeMCP(config.MCPServer{})})
	defer m.Close()
	k := newCellKit(t, MCPFuncs(m)...)
	k.want(`await mcp("fake", "echo", {text: "hi"})`, "echo:hi")
	k.want(`(await tool_search("echo", "fake")).includes("tool: echo")`, "true")
	var hookArgs []string
	k.env.PreHook = func(ctx context.Context, c HookCall) (bool, string, json.RawMessage) {
		hookArgs = append(hookArgs, string(c.Input))
		return false, "", nil
	}
	k.want(`[await mcp("fake", "echo"), await mcp("fake", "echo", undefined), await mcp("fake", "echo", null)].join(" ")`, "echo:<nil> echo:<nil> echo:<nil>")
	if strings.Join(hookArgs, " ") != "{} {} {}" {
		t.Errorf("missing args sent as %q, want {}", hookArgs)
	}
	k.env.PreHook = nil
	k.wantErr(`await mcp("fake")`, "mcp: tool is undefined")
	k.env.ReadOnly = func() bool { return true }
	k.want(`try { await mcp({server: "fake", tool: "echo", arguments: {}}) } catch (e) { return e.denied }`, "true\nFailed calls:\n✗ mcp fake.echo: read-only mode: MCP tools may have side effects and are disabled")
	for in, want := range map[string]string{`{"a": 1}`: `{"a": 1}`, ` [1] `: `[1]`, `{bad`: ``, `echo:{}`: ``, `"s"`: ``} {
		if got := string(jsonValue(in)); got != want {
			t.Errorf("jsonValue(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCellRecordsCalls(t *testing.T) {
	t.Parallel()
	k := newCellKit(t)
	big := strings.Repeat("é", 3000)
	code := fmt.Sprintf(`await write("big.txt", %q); await tick(2)`, big)
	blocks := k.run.Execute(context.Background(), k.env, []llm.Block{{ID: "c1", Name: "run", Input: mustJSON(t, runArgs{Code: code})}}, nopUI{})
	b := blocks[0]
	if b.IsError || len(b.Calls) != 2 {
		t.Fatalf("block %+v", b)
	}
	var cut string
	if err := json.Unmarshal(b.Calls[0].Input, &cut); err != nil || !strings.HasSuffix(cut, "…") || len(cut) > maxRecordInput+10 {
		t.Errorf("cut input %q: %v", b.Calls[0].Input, err)
	}
	if b.Calls[0].Tool != "write" || b.Calls[0].Label != "write big.txt" || !b.Calls[0].OK || string(b.Calls[1].Input) != `{"n":2}` {
		t.Errorf("calls %+v", b.Calls)
	}
	if _, err := json.Marshal(b); err != nil {
		t.Errorf("block does not marshal: %v", err)
	}
	if b.Text != "{\n \"ticks\": 1\n}" {
		t.Errorf("text %q", b.Text)
	}
}

func TestCellInnerUI(t *testing.T) {
	t.Parallel()
	k := newCellKit(t)
	ui := &recordUI{}
	k.run.Execute(context.Background(), k.env, []llm.Block{{ID: "r9", Name: "run", Input: mustJSON(t, runArgs{Code: "await tick(); await tick()"})}}, ui)
	want := []string{"start r9 run await tick(); await tick()", "start r9.1 tick", "done r9.1 tick", "start r9.2 tick", "done r9.2 tick", "done r9 run await tick(); await tick() · 2 calls · 3 lines"}
	if strings.Join(ui.events, "\n") != strings.Join(want, "\n") {
		t.Errorf("ui events:\n%s", strings.Join(ui.events, "\n"))
	}
}

func TestCellSourceUI(t *testing.T) {
	t.Parallel()
	k := newCellKit(t)
	ui := &sourceUI{}
	code := "await tick()\nreturn 1"
	k.run.Execute(context.Background(), k.env, []llm.Block{{ID: "r1", Name: "run", Input: mustJSON(t, runArgs{Code: code})}}, ui)
	want := []string{"code r1 run await tick() return 1 | " + code, "start r1.1 tick", "done r1.1 tick", "done r1 run await tick() return 1 · 1 call · 1 line"}
	if strings.Join(ui.events, "\n") != strings.Join(want, "\n") {
		t.Errorf("ui events:\n%s", strings.Join(ui.events, "\n"))
	}
}

type sourceUI struct{ recordUI }

func (u *sourceUI) ToolStartCode(id, label, code string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.events = append(u.events, "code "+id+" "+label+" | "+code)
}

type recordUI struct {
	mu     sync.Mutex
	events []string
}

func (u *recordUI) ToolStart(id, label string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.events = append(u.events, "start "+id+" "+label)
}

func (u *recordUI) ToolProgress(string, string) {}

func (u *recordUI) ToolDone(id string, _ bool, line string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.events = append(u.events, "done "+id+" "+line)
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCellSubagents(t *testing.T) {
	t.Parallel()
	menu := []config.SubagentModel{{Name: "fast", Model: "m-fast", Provider: "p1"}, {Name: "deep", Model: "m-deep"}}
	k := newCellKit(t, Subagent("main", menu, []string{"low", "high"}))
	k.second(300 * time.Millisecond)
	var mu sync.Mutex
	var tasks []SubagentTask
	k.env.Subagent = func(ctx context.Context, task SubagentTask, progress func(string)) (string, error) {
		mu.Lock()
		tasks = append(tasks, task)
		mu.Unlock()
		if task.Prompt == "slow" {
			select {
			case <-time.After(300 * time.Millisecond):
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		return "answer to " + task.Prompt, nil
	}
	res := k.cellCtx(context.Background(), `await subagent("slow")`, 1)
	if res.IsError || res.Text != "answer to slow" {
		t.Errorf("the timeout did not pause for a subagent: %+v", res)
	}
	res = k.cellCtx(context.Background(), `await subagent("quick", {model: "deep", effort: "high"}); while (true) {}`, 1)
	if !strings.Contains(res.Text, "timed out after 1s") {
		t.Errorf("the timeout was not re-armed after the subagent: %q", res.Text)
	}
	if len(tasks) < 2 {
		t.Fatalf("tasks %+v", tasks)
	}
	if tasks[0].Model != "m-fast" || tasks[0].Provider != "p1" || tasks[0].Effort != "" || tasks[1].Model != "m-deep" || tasks[1].Effort != "high" {
		t.Errorf("tasks %+v", tasks)
	}
	k.want(`let n = 0; try { for (; n < 25; n++) await subagent("q" + n) } catch (e) { return n + " " + e.message }`, "20 too many subagent calls in one run call (limit 20)")
}

func TestCellFailureSavesSubagentAnswers(t *testing.T) {
	t.Parallel()
	k := newCellKit(t, Subagent("m", nil, nil))
	k.second(300 * time.Millisecond)
	k.env.Subagent = func(ctx context.Context, task SubagentTask, progress func(string)) (string, error) {
		switch task.Prompt {
		case "fail":
			time.Sleep(50 * time.Millisecond)
			return "", errors.New("api error 402")
		case "slow":
			time.Sleep(300 * time.Millisecond)
		}
		return "report for " + task.Prompt, nil
	}
	res := k.wantErr(`await Promise.all([subagent("quick"), subagent("slow"), subagent("fail")])`, "ToolError: subagent failed: api error 402", "Note: the code failed while 1 call was still running; it was waited for.")
	if strings.Contains(res.Text, "await every call") {
		t.Errorf("failed cell blames unawaited calls: %q", res.Text)
	}
	for _, name := range []string{"quick", "slow"} {
		m := regexp.MustCompile(`✓ subagent m/default ` + name + ` → answer saved to (\S+)`).FindStringSubmatch(res.Text)
		if m == nil {
			t.Fatalf("%s: no saved answer in %q", name, res.Text)
		}
		if data, err := os.ReadFile(m[1]); err != nil || string(data) != "report for "+name {
			t.Errorf("%s: saved %q, %v", name, data, err)
		}
	}
	k.wantErr(`await subagent("first"); for (let i = 0; i < 35; i++) await tick(); null.x`, "(last 30 of 36):\n✓ subagent m/default first → answer saved to ")
	if res = k.cellCtx(context.Background(), `await subagent("quick"); while (true) {}`, 1); !strings.Contains(res.Text, "timed out after 1s") || !strings.Contains(res.Text, "✓ subagent m/default quick → answer saved to ") {
		t.Errorf("timeout: %q", res.Text)
	}
	k.env.SpillDir = t.TempDir()
	if res = k.cell(`const r = await Promise.allSettled([subagent("quick"), subagent("fail")]); r.map(x => x.status).join(" ")`); res.IsError || !strings.HasPrefix(res.Text, "fulfilled rejected\n") {
		t.Errorf("allSettled: %+v", res)
	}
	k.want(`(await Promise.all([subagent("a"), subagent("b")])).join(", ")`, "report for a, report for b")
	if files, _ := os.ReadDir(k.env.SpillDir); len(files) != 0 {
		t.Errorf("a successful cell saved %d answers", len(files))
	}
}

func TestSubagentLabelDefaults(t *testing.T) {
	t.Parallel()
	menu := []config.SubagentModel{{Name: "fast", Model: "m-fast"}}
	sub := Subagent("main", menu, []string{"low"})
	for in, want := range map[string]string{`{"prompt":"x"}`: "subagent fast/default x", `{"prompt":"x","model":"m","effort":"low"}`: "subagent m/low x"} {
		if got := sub.label(&Env{}, llm.Block{Name: "subagent", Input: json.RawMessage(in)}); got != want {
			t.Errorf("%s: %q, want %q", in, got, want)
		}
	}
}

func TestCellParallelLimit(t *testing.T) {
	t.Parallel()
	var active, peak atomic.Int32
	nap := &Function{
		Name: "nap",
		Code: CodeSpec{Name: "nap"},
		Run: func(ctx context.Context, env *Env, in json.RawMessage, _ func(string)) Result {
			n := active.Add(1)
			for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
			}
			time.Sleep(50 * time.Millisecond)
			active.Add(-1)
			return Result{Text: "ok"}
		},
	}
	k := newCellKit(t, nap)
	k.want(`(await Promise.all(Array(20).fill(0).map(() => nap()))).length`, "20")
	if peak.Load() != cellParallel {
		t.Errorf("peak concurrency %d, want %d", peak.Load(), cellParallel)
	}
}

func TestCellRequiredArgs(t *testing.T) {
	t.Parallel()
	k := newCellKit(t, Subagent("m", nil, nil))
	k.env.Subagent = func(ctx context.Context, task SubagentTask, progress func(string)) (string, error) { return "ok", nil }
	k.file("out.txt", "keep\n")
	res := k.wantErr(`const obj = {}; await write("out.txt", obj.typo)`, "ToolError: write: content is undefined\n    at cell.js:1:")
	if len(res.Calls) != 0 {
		t.Errorf("a call with a missing argument ran: %+v", res.Calls)
	}
	k.wantErr(`await edit("out.txt", "keep", undefined)`, "edit: new (new_string) is undefined")
	k.wantErr(`await edit("out.txt", "keep", null)`, "edit: new (new_string) is null")
	k.wantErr(`await write({file_path: "out.txt"})`, "write: content is undefined")
	k.wantErr(`await read()`, "read: path (file_path) is undefined")
	k.wantErr(`await edit("out.txt", "keep", "x", {all: true, replace_all: false})`, "edit: both all and replace_all given")
	if data, _ := os.ReadFile(filepath.Join(k.dir, "out.txt")); string(data) != "keep\n" {
		t.Errorf("out.txt changed: %q", data)
	}
	k.want(`await write("empty.txt", ""); JSON.stringify(await read("empty.txt"))`, `""`)
	k.want(`await subagent("quick")`, "ok")
}

func TestCellFailedCallsNote(t *testing.T) {
	t.Parallel()
	k := newCellKit(t)
	k.file("a.go", "package a\n")
	res := k.cell(`edit("a.go", "x", "y"); await bash("sleep 0.3"); "done"`)
	if res.IsError || !strings.HasPrefix(res.Text, "done\nFailed calls:\n✗ edit a.go: old not found") || strings.Contains(res.Text, "still running") {
		t.Errorf("unawaited failure: %+v", res)
	}
	res = k.cell(`for (let i = 0; i < 12; i++) { try { await read("missing" + i) } catch {} } "ok"`)
	if !strings.HasSuffix(res.Text, "\nand 2 more") || strings.Count(res.Text, "✗ read missing") != 10 {
		t.Errorf("failed list cap: %q", res.Text)
	}
	k.want(`(await bash("exit 3")).exit_code`, "3")
	res = k.cell(`edit("missing.txt", "a", "b"); "r"`)
	if strings.Count(res.Text, "edit missing.txt") != 1 || !strings.Contains(res.Text, "still running") {
		t.Errorf("late failure listed twice or not at all: %q", res.Text)
	}
}

func TestCellBashErrorResolves(t *testing.T) {
	t.Parallel()
	k := newCellKit(t)
	var mu sync.Mutex
	var hookErrs []bool
	k.env.PostHook = func(ctx context.Context, c HookCall, r Result) string {
		mu.Lock()
		if c.Tool == "Bash" {
			hookErrs = append(hookErrs, r.IsError)
		}
		mu.Unlock()
		return ""
	}
	code := `const a = await bash("echo x; exit 3"); const b = await bash("sleep 5", {timeout: 1}); return [a.exit_code, b.timed_out].join(" ")`
	b := k.run.Execute(context.Background(), k.env, []llm.Block{{ID: "c1", Name: "run", Input: mustJSON(t, runArgs{Code: code})}}, nopUI{})[0]
	if b.IsError || b.Text != "3 true" {
		t.Errorf("bash errors did not resolve: %+v", b)
	}
	if len(b.Calls) != 2 || b.Calls[0].OK || b.Calls[1].OK {
		t.Errorf("calls %+v", b.Calls)
	}
	if len(hookErrs) != 2 || !hookErrs[0] || !hookErrs[1] {
		t.Errorf("post hook is_error %v", hookErrs)
	}
}

func TestCellNoCallsAfterFinish(t *testing.T) {
	t.Parallel()
	k := newCellKit(t)
	k.file("f.txt", "x\n")
	for _, code := range []string{
		`return {toJSON() { sleep(1); return 1 }}`,
		`return {toJSON() { read("f.txt"); return 1 }}`,
		`return {toJSON() { setTimeout(() => {}, 1); return 1 }}`,
	} {
		done := make(chan Result, 1)
		go func() { done <- k.cell(code) }()
		select {
		case res := <-done:
			if len(res.Calls) != 0 {
				t.Errorf("%s: a call ran after the code finished: %+v", code, res.Calls)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: hung", code)
		}
	}
	k.want("1 + 1", "2")
}

func TestCellTimersCancelledAfterSettle(t *testing.T) {
	t.Parallel()
	k := newCellKit(t)
	k.second(300 * time.Millisecond)
	start := time.Now()
	k.want(`return (await Promise.race([bash("echo hi"), new Promise((_, rej) => setTimeout(() => rej(new Error("timeout")), 60000))])).output`, "hi")
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("a pending guard timer held the result for %v", d)
	}
	k.want(`sleep(50).then(() => write("late.txt", "x")); "ok"`, "ok")
	time.Sleep(150 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(k.dir, "late.txt")); err == nil {
		t.Error("a sleep fired after the code finished")
	}
	res := k.cellCtx(context.Background(), `bash("sleep 5"); "kept"`, 1)
	if res.IsError || !strings.HasPrefix(res.Text, "kept\nNote: 1 call was still running when the code finished; await") || !strings.Contains(res.Text, "waiting for the calls still running stopped: timed out after 1s") {
		t.Errorf("timeout while waiting for a late call: %+v", res)
	}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	res = k.cellCtx(ctx, `bash("sleep 5"); ({a: 1})`, 0)
	if res.IsError || !strings.HasPrefix(res.Text, "{\n \"a\": 1\n}\n") || !strings.Contains(res.Text, "stopped: interrupted by user") {
		t.Errorf("interrupt while waiting for a late call: %+v", res)
	}
}

func TestCellReadRejectsNonText(t *testing.T) {
	t.Parallel()
	k := newCellKit(t)
	k.file("bin", "a\x00b")
	k.file("latin.txt", "caf\xe9\n")
	k.wantErr(`await read("bin")`, "ToolError: binary file, 3 bytes", "use bash")
	k.wantErr(`await read("latin.txt")`, "ToolError: not valid UTF-8", "use bash")
}

func TestCellBashTruncated(t *testing.T) {
	t.Parallel()
	k := newCellKit(t)
	k.want(`const r = await bash("seq 1 300000"); [r.truncated, r.output.includes("bytes dropped")].join(" ")`, "true true")
	k.want(`(await bash("seq 1 3")).truncated`, "false")
}

func TestCellRedeclaredFunctions(t *testing.T) {
	t.Parallel()
	k := newCellKit(t)
	k.file("f.txt", "hi\n")
	k.want(`const read = 1; const jobs = 2; var print = 3; let sleep = 4`, "(no output)")
	k.want(`await sleep(1); print("p"); [await read("f.txt"), typeof jobs].join("")`, "p\nhi\nfunction")
}

func TestCellErrorKeptWhenWaitStops(t *testing.T) {
	t.Parallel()
	k := newCellKit(t)
	k.second(500 * time.Millisecond)
	res := k.cellCtx(context.Background(), `await Promise.all([bash("sleep 100", {timeout: 600}), read("nope")])`, 1)
	for _, want := range []string{"ToolError: stat ", "Calls that completed:\n✗ read nope\nNote: the code failed while 1 call was still running. Failed:\nbash sleep 100: ", "Note: waiting for the calls still running stopped: timed out after 1s"} {
		if !res.IsError || !strings.Contains(res.Text, want) {
			t.Errorf("timeout: %q lacks %q", res.Text, want)
		}
	}
	if !strings.HasPrefix(res.Text, "ToolError: stat ") || strings.Contains(res.Text, "waited for") || strings.Contains(res.Text, "await every call") || strings.Contains(res.Text, "✗ bash") {
		t.Errorf("timeout: %q", res.Text)
	}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	res = k.cellCtx(ctx, `bash("sleep 100", {timeout: 600}); undefinedName`, 0)
	if !res.IsError || !strings.HasPrefix(res.Text, "ReferenceError: undefinedName is not defined") || !strings.Contains(res.Text, "No variables are defined in this VM yet.") || !strings.HasSuffix(res.Text, "Note: waiting for the calls still running stopped: interrupted by user") {
		t.Errorf("interrupt: %q", res.Text)
	}
}

func TestRunDescriptionReservedNames(t *testing.T) {
	t.Parallel()
	k := newCellKit(t)
	want := "- The names read, write, edit, bash, jobs, tick, sleep, setTimeout, clearTimeout and print are set again at the start of every run call: don't use them for your own variables.\n"
	if desc := k.run.Def.Description; !strings.Contains(desc, want) || strings.Contains(desc, "subagent,") {
		t.Errorf("description:\n%s", desc)
	}
}

func TestCellFormatJobsDoNotLeak(t *testing.T) {
	t.Parallel()
	k := newCellKit(t)
	k.want(`return {toJSON() { Promise.resolve().then(() => write("late.txt", "x")); Promise.resolve().then(() => console.log("job ran")); return 1 }}`, "job ran\n1")
	res := k.cell(`await sleep(1); 2`)
	if res.IsError || res.Text != "2" || len(res.Calls) != 0 {
		t.Errorf("next cell: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(k.dir, "late.txt")); err == nil {
		t.Error("a promise job queued while formatting wrote a file")
	}
}

func TestCellEndlessFormatJobsStopped(t *testing.T) {
	k := newCellKit(t)
	k.second(100 * time.Millisecond)
	start := time.Now()
	k.want(`return {toJSON() { const spin = () => Promise.resolve().then(spin); spin(); return 3 }}`, "3")
	bound := formatSeconds * k.run.r.unit()
	if d := time.Since(start); d < bound || d > bound+3*time.Second {
		t.Errorf("an endless job loop held the result for %v, want the %v format bound", d, bound)
	}
	k.second(0)
	k.want(`await sleep(1); 4`, "4")
}

func TestCellParkedFunctionResumesInLaterCell(t *testing.T) {
	t.Parallel()
	k := newCellKit(t)
	k.want(`globalThis.out = []; globalThis.gate = new Promise(r => { globalThis.open = r }); (async () => { await gate; out.push("resumed") })(); "parked"`, "parked")
	k.want(`return {toJSON() { return "formatted" }}`, `"formatted"`)
	k.want(`open(); await sleep(1); out.join()`, "resumed")
}
