package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/paul-nameless/qi/internal/config"
	"github.com/paul-nameless/qi/internal/llm"
	"github.com/paul-nameless/qi/internal/mcp"
)

type cellKit struct {
	t    *testing.T
	dir  string
	env  *Env
	run  *Tool
	reg  *Registry
	ctx  context.Context
	tick atomic.Int32
}

func newCellKit(t *testing.T, extra ...*Tool) *cellKit {
	k := &cellKit{t: t, dir: t.TempDir(), ctx: context.Background()}
	k.env = &Env{Cwd: k.dir, SpillDir: t.TempDir()}
	inner := append([]*Tool{Read(), Write(), Edit(), Bash(), JobsTool(), k.tickTool()}, extra...)
	k.run = Run(NewRegistry(inner...))
	k.reg = NewRegistry(k.run)
	return k
}

func (k *cellKit) tickTool() *Tool {
	return &Tool{
		Def:  llm.ToolDef{Name: "tick", Schema: json.RawMessage(`{"type":"object","properties":{"n":{"type":"integer"}}}`)},
		Code: &CodeSpec{Name: "tick", Params: []string{"n"}, Doc: "tick(n) → number"},
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
	in, _ := json.Marshal(runArgs{Code: code, Timeout: timeout})
	return k.run.Run(ctx, k.env, in, nil)
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
	k := newCellKit(t)
	start := time.Now()
	k.want("await Promise.all([1,2,3,4,5,6,7,8].map(() => sleep(200))); 'ok'", "ok")
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Errorf("8 sleeps took %v", d)
	}
}

func TestCellReadRawAndStaleGuard(t *testing.T) {
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
	k := newCellKit(t)
	k.want(`JSON.stringify(await bash("echo hi"))`, `{"output":"hi","exit_code":0,"timed_out":false}`)
	k.want(`JSON.stringify(await bash("echo oops; exit 3"))`, `{"output":"oops","exit_code":3,"timed_out":false}`)
	k.want(`JSON.stringify(await bash("echo start; sleep 5", {timeout: 1}))`, `{"output":"start","exit_code":-1,"timed_out":true}`)
	k.want(`JSON.stringify(await bash({command: "printf 'a\\n\\nb\\n\\n'"}))`, `{"output":"a\n\nb","exit_code":0,"timed_out":false}`)
}

func TestCellToolError(t *testing.T) {
	k := newCellKit(t)
	k.env.ReadOnly = func() bool { return true }
	k.wantErr("\n\nawait write('x.txt', 'y')", "ToolError: read-only mode: write is not allowed\n    at cell.js:3:")
	k.want(`try { await write("x.txt", "y") } catch (e) { return [e.name, e.denied, e.message].join("|") }`, "ToolError|true|read-only mode: write is not allowed")
	k.want(`try { await bash("touch x.txt") } catch (e) { return e.denied }`, "true")
	k.want(`try { await read("nope.txt") } catch (e) { return e.denied }`, "false")
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
	k.want(`try { await write("blocked.txt", "y") } catch (e) { return e.denied + " " + e.message }`, "true blocked by hook: no writes there")
	k.cell(`await write("orig.txt", "y")`)
	if data, err := os.ReadFile(filepath.Join(k.dir, "rewritten.txt")); err != nil || string(data) != "hooked" {
		t.Errorf("rewritten input not applied: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(k.dir, "orig.txt")); err == nil {
		t.Error("original input ran")
	}
}

func TestCellPostHookStaysOutOfValues(t *testing.T) {
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
	k := newCellKit(t)
	start := time.Now()
	k.wantErr(`await new Promise(() => {})`, "can never settle")
	if time.Since(start) > time.Second {
		t.Error("never-settling promise was not detected fast")
	}
}

func TestCellBusyLoopStops(t *testing.T) {
	k := newCellKit(t)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	res := k.cellCtx(ctx, "globalThis.kept = 1; let i = 0; while (true) { i++ }", 0)
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
	k := newCellKit(t)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	res := k.cellCtx(ctx, `globalThis.n = 0; while (true) { try { n++; await tick(); await bash("sleep 0.02") } catch {} }`, 0)
	if !strings.Contains(res.Text, "interrupted by user") {
		t.Fatalf("got %q", res.Text)
	}
	after := k.tick.Load()
	time.Sleep(200 * time.Millisecond)
	if k.tick.Load() != after || after == 0 {
		t.Errorf("ticks %d then %d", after, k.tick.Load())
	}
	if n := k.cell("n").Text; n != fmt.Sprint(after) && n != fmt.Sprint(after+1) {
		t.Errorf("n = %s, ticks %d", n, after)
	}
}

func TestCellTimeoutSparesFormatting(t *testing.T) {
	k := newCellKit(t)
	res := k.cellCtx(context.Background(), `return {toJSON() { const t = Date.now(); while (Date.now() - t < 1500) {} return "slow" }}`, 1)
	if res.IsError || res.Text != `"slow"` {
		t.Errorf("a value finished in time was cut by the cell timeout: %+v", res)
	}
	done := make(chan Result, 1)
	go func() { done <- k.cellCtx(context.Background(), `return {toJSON() { while (true) {} }}`, 1) }()
	select {
	case res := <-done:
		if !strings.Contains(res.Text, "the value could not be shown") {
			t.Errorf("endless toJSON: %q", res.Text)
		}
	case <-time.After(formatTimeout + 5*time.Second):
		t.Fatal("formatting an endless toJSON was not stopped")
	}
	k.want("1 + 1", "2")
}

func TestCellJobsDrainAfterThrow(t *testing.T) {
	k := newCellKit(t)
	k.wantErr(`Promise.resolve().then(() => console.log("leak")); null.x`, "leak\nTypeError")
	k.want("1", "1")
}

func TestCellResultsNotCut(t *testing.T) {
	k := newCellKit(t)
	k.want(`(await bash("seq 1 5000")).output.split("\n").length`, "5000")
	content := strings.Repeat("short line\n", 2500) + strings.Repeat("x", 5000) + "\n"
	k.file("big.txt", content)
	k.want(`(await read("big.txt")).length`, fmt.Sprint(len(content)))
}

func TestCellInterruptThroughHostCalls(t *testing.T) {
	k := newCellKit(t, Subagent("m", nil, nil))
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
	k := newCellKit(t)
	k.want(`let i = 0; try { for (; i < 600; i++) await tick() } catch (e) { return i + " " + e.name + " " + e.message }`,
		"500 ToolError too many tool calls in one run call (limit 500); split the work across several run calls")
	k.want(`await tick(); "next cell ok"`, "next cell ok")
}

func TestCellReset(t *testing.T) {
	k := newCellKit(t)
	k.cell("globalThis.z = 1")
	k.run.Reset(true)
	k.want("typeof z", newVMNote+"\nundefined")
	k.want("typeof z", "undefined")
	NewRegistry(k.run).Reset(false)
	k.want("1", "1")
}

func TestRunReplaceAndHint(t *testing.T) {
	sub := func(model string) *Tool { return Subagent(model, nil, []string{"low"}) }
	build := func() *Registry {
		return NewRegistry(Run(NewRegistry(Read(), Write(), Edit(), Bash(), JobsTool(), sub("m1"))))
	}
	r := build()
	if len(r.Defs()) != 1 || r.Defs()[0].Name != "run" {
		t.Fatalf("defs %+v", r.Defs())
	}
	if !strings.Contains(r.Defs()[0].Description, "- m1: the current model") {
		t.Error("description lacks the subagent menu")
	}
	r.Replace(sub("m2"))
	desc := r.Defs()[0].Description
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
	a, _ := json.Marshal(build().Defs())
	b, _ := json.Marshal(build().Defs())
	if string(a) != string(b) {
		t.Error("run description is not byte-stable")
	}
}

func TestCellErrorsText(t *testing.T) {
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
}

func TestCellMCP(t *testing.T) {
	m := mcp.NewManager(map[string]config.MCPServer{"fake": fakeMCP(config.MCPServer{})})
	defer m.Close()
	k := newCellKit(t, MCPTools(m)...)
	k.want(`await mcp("fake", "echo", {text: "hi"})`, "echo:hi")
	k.want(`(await tool_search("echo", "fake")).includes("tool: echo")`, "true")
	k.env.ReadOnly = func() bool { return true }
	k.want(`try { await mcp({server: "fake", tool: "echo", arguments: {}}) } catch (e) { return e.denied }`, "true")
	for in, want := range map[string]string{`{"a": 1}`: `{"a": 1}`, ` [1] `: `[1]`, `{bad`: ``, `echo:{}`: ``, `"s"`: ``} {
		if got := string(jsonValue(in)); got != want {
			t.Errorf("jsonValue(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCellRecordsCalls(t *testing.T) {
	k := newCellKit(t)
	big := strings.Repeat("é", 3000)
	code := fmt.Sprintf(`await write("big.txt", %q); await tick(2)`, big)
	blocks := k.reg.Execute(context.Background(), k.env, []llm.Block{{ID: "c1", Name: "run", Input: mustJSON(t, runArgs{Code: code})}}, nopUI{})
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
	k := newCellKit(t)
	ui := &recordUI{}
	k.reg.Execute(context.Background(), k.env, []llm.Block{{ID: "r9", Name: "run", Input: mustJSON(t, runArgs{Code: "await tick(); await tick()"})}}, ui)
	want := []string{"start r9 run await tick(); await tick()", "start r9.1 tick", "done r9.1 tick", "start r9.2 tick", "done r9.2 tick", "done r9 run await tick(); await tick() · 2 calls · 3 lines"}
	if strings.Join(ui.events, "\n") != strings.Join(want, "\n") {
		t.Errorf("ui events:\n%s", strings.Join(ui.events, "\n"))
	}
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
	menu := []config.SubagentModel{{Name: "fast", Model: "m-fast", Provider: "p1"}, {Name: "deep", Model: "m-deep"}}
	k := newCellKit(t, Subagent("main", menu, []string{"low", "high"}))
	var mu sync.Mutex
	var tasks []SubagentTask
	k.env.Subagent = func(ctx context.Context, task SubagentTask, progress func(string)) (string, error) {
		mu.Lock()
		tasks = append(tasks, task)
		mu.Unlock()
		if task.Prompt == "slow" {
			select {
			case <-time.After(1500 * time.Millisecond):
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
	if tasks[0].Model != "m-fast" || tasks[0].Provider != "p1" || tasks[0].Effort != "" || tasks[1].Model != "m-deep" || tasks[1].Effort != "high" {
		t.Errorf("tasks %+v", tasks)
	}
	k.want(`let n = 0; try { for (; n < 25; n++) await subagent("q" + n) } catch (e) { return n + " " + e.message }`, "20 too many subagent calls in one run call (limit 20)")
}

func TestSubagentLabelDefaults(t *testing.T) {
	menu := []config.SubagentModel{{Name: "fast", Model: "m-fast"}}
	sub := Subagent("main", menu, []string{"low"})
	for in, want := range map[string]string{`{"prompt":"x"}`: "subagent fast/default x", `{"prompt":"x","model":"m","effort":"low"}`: "subagent m/low x"} {
		if got := Label(sub, &Env{}, llm.Block{Name: "subagent", Input: json.RawMessage(in)}); got != want {
			t.Errorf("%s: %q, want %q", in, got, want)
		}
	}
}

func TestCellParallelLimit(t *testing.T) {
	var active, peak atomic.Int32
	nap := &Tool{
		Def:  llm.ToolDef{Name: "nap"},
		Code: &CodeSpec{Name: "nap"},
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
