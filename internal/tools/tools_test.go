package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/paul-nameless/qi/internal/llm"
	"github.com/paul-nameless/qi/internal/mcp"
)

type nopUI struct{}

func (nopUI) ToolStart(string, string)      {}
func (nopUI) ToolProgress(string, string)   {}
func (nopUI) ToolDone(string, bool, string) {}

type funcSet map[string]*Function

func newFuncSet(fs ...*Function) funcSet {
	s := funcSet{}
	for _, f := range fs {
		s[f.Name] = f
	}
	return s
}

func (s funcSet) Execute(ctx context.Context, env *Env, calls []llm.Block, ui UI) []llm.Block {
	out := make([]llm.Block, len(calls))
	for i, c := range calls {
		res := errResult("unknown function %q", c.Name)
		ui.ToolStart(c.ID, c.Name)
		if f := s[c.Name]; f != nil {
			var extra string
			res, extra = callFunction(ctx, env, f, c, ui)
			if extra != "" {
				res.Text += "\n" + extra
			}
		}
		ui.ToolDone(c.ID, !res.IsError, res.Summary)
		out[i] = llm.Block{Type: llm.BlockToolResult, ToolUseID: c.ID, Text: res.Text, IsError: res.IsError, Images: res.Images}
	}
	return out
}

func TestReadOnlyRefusesWrites(t *testing.T) {
	dir := t.TempDir()
	env := &Env{Cwd: dir, ReadOnly: func() bool { return true }}
	r := newFuncSet(Write(), Edit())
	res := r.Execute(context.Background(), env, []llm.Block{
		{ID: "w", Name: "write", Input: json.RawMessage(`{"file_path":"x.txt","content":"hi"}`)},
		{ID: "e", Name: "edit", Input: json.RawMessage(`{"file_path":"x.txt","old_string":"a","new_string":"b"}`)},
	}, nopUI{})
	for _, b := range res {
		if !b.IsError || !strings.Contains(b.Text, "read-only") {
			t.Errorf("expected refusal, got %+v", b)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "x.txt")); err == nil {
		t.Error("file was written in read-only mode")
	}
}

func TestCapOutput(t *testing.T) {
	env := &Env{SpillDir: t.TempDir()}
	short := strings.Repeat("line\n", 300)
	if env.capOutput(short) != short {
		t.Error("short output changed")
	}
	var b strings.Builder
	for i := 0; i < 1000; i++ {
		fmt.Fprintf(&b, "line %d é\n", i)
	}
	long := b.String()
	out := env.capOutput(long)
	if len(out) > maxOutBytes+500 || !strings.Contains(out, "line 0 ") || !strings.Contains(out, "line 999") || !utf8.ValidString(out) {
		t.Fatalf("bad cap: %d bytes", len(out))
	}
	i := strings.Index(out, "Full output: ")
	path := strings.Fields(out[i+len("Full output: "):])[0]
	data, err := os.ReadFile(path)
	if err != nil || string(data) != long {
		t.Errorf("spill file mismatch: %v", err)
	}
	wide := strings.Repeat(strings.Repeat("é", 5000)+"\n", 10)
	if out := env.capOutput(wide); len(out) > maxOutBytes+500 || !utf8.ValidString(out) {
		t.Errorf("wide lines: %d bytes, valid=%v", len(out), utf8.ValidString(out))
	}
}

func TestBash(t *testing.T) {
	env := &Env{Cwd: t.TempDir(), SpillDir: t.TempDir()}
	res := runBash(context.Background(), env, json.RawMessage(`{"command":"echo hi; exit 3"}`), nil)
	if !res.IsError || !strings.Contains(res.Text, "hi") || !strings.Contains(res.Text, "[exit code 3]") {
		t.Errorf("exit code: %+v", res)
	}
	res = runBash(context.Background(), env, json.RawMessage(`{"command":"sleep 30 & echo $!; wait","timeout":1}`), nil)
	if !strings.Contains(res.Text, "timed out") {
		t.Fatalf("timeout: %+v", res)
	}
	var pid int
	fmt.Sscanf(res.Text, "%d", &pid)
	time.Sleep(100 * time.Millisecond)
	if pid == 0 || alive(pid) {
		t.Errorf("background child %d survived the timeout", pid)
	}
	if res := runBash(context.Background(), env, json.RawMessage(`{"command":"  "}`), nil); !res.IsError {
		t.Error("empty command accepted")
	}
}

func alive(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	return err != nil || !strings.Contains(string(stat), ") Z ")
}

func TestReadFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	os.WriteFile(p, []byte("a\nb\nc\nd\n"), 0o600)
	res, _ := readFile(p, 2, 2, nil)
	if res.Text != "b\nc\n" || res.Summary != "2 lines" {
		t.Errorf("got %+v", res)
	}
	if res, _ := readFile(p, 9, 0, nil); !res.IsError {
		t.Error("offset past end accepted")
	}
	os.WriteFile(filepath.Join(dir, "bin"), []byte{0, 1, 2}, 0o600)
	if res, _ := readFile(filepath.Join(dir, "bin"), 0, 0, nil); !res.IsError || !strings.Contains(res.Text, "binary file") {
		t.Errorf("binary: %+v", res)
	}
	if res, _ := readFile("/dev/null", 0, 0, nil); !res.IsError {
		t.Error("device file accepted")
	}
}

func TestHooksWiring(t *testing.T) {
	dir := t.TempDir()
	var postCalls atomic.Int32
	env := &Env{
		Cwd: dir,
		PreHook: func(ctx context.Context, c HookCall) (bool, string, json.RawMessage) {
			if c.ID == "rewrite" {
				return false, "", json.RawMessage(`{"file_path":"c.txt","content":"z"}`)
			}
			return c.ID == "blocked", "nope", nil
		},
		PostHook: func(ctx context.Context, c HookCall, r Result) string { postCalls.Add(1); return "x" },
	}
	res := newFuncSet(Write()).Execute(context.Background(), env, []llm.Block{
		{ID: "blocked", Name: "write", Input: json.RawMessage(`{"file_path":"a.txt","content":"1"}`)},
		{ID: "ok", Name: "write", Input: json.RawMessage(`{"file_path":"b.txt","content":"2"}`)},
		{ID: "rewrite", Name: "write", Input: json.RawMessage(`{"file_path":"d.txt","content":"3"}`)},
	}, nopUI{})
	if data, err := os.ReadFile(filepath.Join(dir, "c.txt")); err != nil || string(data) != "z" {
		t.Errorf("updated input not applied: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "d.txt")); err == nil {
		t.Error("original input ran despite updatedInput")
	}
	if !res[0].IsError || res[0].Text != "blocked by hook: nope" {
		t.Errorf("blocked result %+v", res[0])
	}
	if _, err := os.Stat(filepath.Join(dir, "a.txt")); err == nil {
		t.Error("blocked write ran")
	}
	if !strings.HasSuffix(res[1].Text, "\nx") || postCalls.Load() != 2 {
		t.Errorf("post hook: %q calls=%d", res[1].Text, postCalls.Load())
	}
}

func TestBashTimeoutUnits(t *testing.T) {
	cases := map[[2]int]int{{120000, 0}: 120, {30, 0}: 30, {0, 0}: 120, {0, 45}: 45, {900000, 0}: 600, {1500, 0}: 600, {3600, 0}: 600, {10000, 0}: 10, {600, 0}: 600, {601, 0}: 600, {900, 0}: 600}
	for in, want := range cases {
		if got := bashTimeout(in[0], in[1]); got != want {
			t.Errorf("bashTimeout(%d, %d) = %d, want %d", in[0], in[1], got, want)
		}
	}
}

func TestBashTTYReadFailsFast(t *testing.T) {
	start := time.Now()
	runBash(context.Background(), &Env{Cwd: t.TempDir(), SpillDir: t.TempDir()}, json.RawMessage(`{"command":"head -1 </dev/tty; echo done","timeout":20}`), nil)
	if time.Since(start) > 5*time.Second {
		t.Error("reading /dev/tty blocked instead of failing")
	}
}

func TestStaleFileGuard(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	os.WriteFile(p, []byte("one\ntwo\nthree\n"), 0o600)
	env := &Env{Cwd: dir}
	r := newFuncSet(Read(), Write(), Edit())
	run := func(name, input string) llm.Block {
		return r.Execute(context.Background(), env, []llm.Block{{ID: "x", Name: name, Input: json.RawMessage(input)}}, nopUI{})[0]
	}
	both := r.Execute(context.Background(), env, []llm.Block{
		{ID: "r", Name: "read", Input: json.RawMessage(`{"file_path":"f.txt"}`)},
		{ID: "w", Name: "write", Input: json.RawMessage(`{"file_path":"f.txt","content":"one\ntwo\nthree\n"}`)},
	}, nopUI{})
	if both[1].IsError {
		t.Errorf("read then write in one response: %+v", both[1])
	}
	res := run("edit", `{"file_path":"f.txt","old_string":"two","new_string":"TWO"}`)
	if res.IsError || !strings.Contains(res.Text, "2\tTWO") || !strings.Contains(res.Text, "1\tone") {
		t.Errorf("edit snippet: %q", res.Text)
	}
	future := time.Now().Add(time.Hour)
	os.Chtimes(p, future, future)
	if res := run("write", `{"file_path":"f.txt","content":"x\n"}`); !res.IsError || !strings.Contains(res.Text, "changed on disk") {
		t.Errorf("stale write: %+v", res)
	}
	if res := run("edit", `{"file_path":"f.txt","old_string":"one","new_string":"ONE"}`); res.IsError {
		t.Errorf("edit after an outside change should apply: %+v", res)
	}
	run("read", `{"file_path":"f.txt"}`)
	if res := run("write", `{"file_path":"f.txt","content":"new\n"}`); res.IsError {
		t.Errorf("write after read: %+v", res)
	}
	if res := run("write", `{"file_path":"g.txt","content":"new\n"}`); res.IsError {
		t.Errorf("create: %+v", res)
	}
}

type doneUI struct {
	nopUI
	mu   sync.Mutex
	done map[string]bool
}

func (u *doneUI) ToolDone(id string, ok bool, _ string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.done[id] = ok
}

func TestPanickingToolBecomesErrorResult(t *testing.T) {
	ok := func(name string) *Function {
		return &Function{Name: name, Run: func(context.Context, *Env, json.RawMessage, func(string)) Result {
			return Result{Text: name + " ran"}
		}}
	}
	boom := &Function{Name: "boom", Run: func(context.Context, *Env, json.RawMessage, func(string)) Result {
		panic(errors.New("boom"))
	}}
	r := newFuncSet(ok("fine"), ok("write"), boom)
	for _, names := range [][]string{{"boom", "fine"}, {"write", "boom", "fine"}} {
		calls := make([]llm.Block, len(names))
		for i, n := range names {
			calls[i] = llm.Block{ID: fmt.Sprint(i), Name: n, Input: json.RawMessage(`{}`)}
		}
		ui := &doneUI{done: map[string]bool{}}
		res := r.Execute(context.Background(), &Env{Cwd: t.TempDir()}, calls, ui)
		for i, n := range names {
			b := res[i]
			if b.ToolUseID != calls[i].ID || b.Type != llm.BlockToolResult {
				t.Fatalf("%v: result %d missing: %+v", names, i, b)
			}
			if _, seen := ui.done[calls[i].ID]; !seen {
				t.Errorf("%v: %s never reported done", names, n)
			}
			if n == "boom" {
				if !b.IsError || !strings.Contains(b.Text, "panicked: boom") || !strings.Contains(b.Text, "tools_test.go:") {
					t.Errorf("%v: panic result %q", names, b.Text)
				}
			} else if b.IsError || b.Text != n+" ran" {
				t.Errorf("%v: %s after the panic: %+v", names, n, b)
			}
		}
	}
}

func TestMCPCallHookUnwrapAndRewrap(t *testing.T) {
	call := MCPFuncs(mcp.NewManager(nil))[1]
	hc := call.HookCall(llm.Block{ID: "1", Name: "mcp_call", Input: json.RawMessage(`{"server":"gh","tool":"create","arguments":{"title":"x"}}`)})
	if hc.Tool != "mcp__gh__create" || len(hc.Names) != 1 || string(hc.Input) != `{"title":"x"}` {
		t.Errorf("hook call %+v", hc)
	}
	got := call.rewrap(json.RawMessage(`{"server":"gh","tool":"create","arguments":{"title":"x"}}`), json.RawMessage(`{"title":"y"}`))
	if string(got) != `{"arguments":{"title":"y"},"server":"gh","tool":"create"}` {
		t.Errorf("rewrap %s", got)
	}
	read := Read().HookCall(llm.Block{ID: "2", Name: "read", Input: json.RawMessage(`{"file_path":"a"}`)})
	if read.Tool != "Read" || strings.Join(read.Names, ",") != "read,Read" || string(read.Input) != `{"file_path":"a"}` {
		t.Errorf("read hook call %+v", read)
	}
}

func TestSpillFailureIsReported(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	env := &Env{SpillDir: filepath.Join(blocker, "sub")}
	if _, err := env.spill("x"); err == nil {
		t.Error("spill into a file path succeeded")
	}
	if out := env.capOutput(strings.Repeat("line\n", 1000)); !strings.Contains(out, "Full output: (not saved: ") {
		t.Errorf("cap output without a spill file: %q", out[len(out)-200:])
	}
	if p := env.saveImage([]byte("x"), "image/png"); p != "" {
		t.Errorf("saveImage returned %q", p)
	}
}

func TestReadServesEmbeddedFiles(t *testing.T) {
	Embed("qi:test/hello.md", []byte("line one\nline two\n"))
	t.Cleanup(func() { delete(embedded, "qi:test/hello.md") })
	env := &Env{Cwd: t.TempDir()}
	r := newFuncSet(Read())
	res := r.Execute(context.Background(), env, []llm.Block{
		{ID: "ok", Name: "read", Input: json.RawMessage(`{"file_path":"qi:test/hello.md","offset":2}`)},
		{ID: "missing", Name: "read", Input: json.RawMessage(`{"file_path":"qi:test/nope.md"}`)},
	}, nopUI{})
	if res[0].IsError || !strings.Contains(res[0].Text, "line two") || strings.Contains(res[0].Text, "line one") {
		t.Errorf("embedded read: %+v", res[0])
	}
	if !res[1].IsError || !strings.Contains(res[1].Text, "qi:test/hello.md") {
		t.Errorf("an unknown qi: path should list the built-in files: %+v", res[1])
	}
}

func TestSpillGoesToTheSessionScratch(t *testing.T) {
	scratch := t.TempDir()
	env := &Env{Scratch: func() string { return scratch }}
	path, err := env.spill("big output")
	if err != nil || !strings.HasPrefix(path, filepath.Join(scratch, "out")+string(filepath.Separator)) {
		t.Errorf("spill went to %q: %v", path, err)
	}
}
