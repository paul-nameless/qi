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

func TestExecuteOrderAndPathSerialization(t *testing.T) {
	var mu sync.Mutex
	var events []string
	active := map[string]int{}
	overlap := false
	slow := &Tool{
		Def:  llm.ToolDef{Name: "slow"},
		Path: pathKey,
		Run: func(ctx context.Context, env *Env, in json.RawMessage, _ func(string)) Result {
			p := pathKey(env, in)
			mu.Lock()
			active[p]++
			if active[p] > 1 {
				overlap = true
			}
			events = append(events, "start "+filepath.Base(p))
			mu.Unlock()
			time.Sleep(30 * time.Millisecond)
			mu.Lock()
			active[p]--
			mu.Unlock()
			return Result{Text: "done " + filepath.Base(p)}
		},
	}
	r := NewRegistry(slow)
	env := &Env{Cwd: t.TempDir()}
	calls := []llm.Block{
		{ID: "1", Name: "slow", Input: json.RawMessage(`{"file_path":"a.txt"}`)},
		{ID: "2", Name: "slow", Input: json.RawMessage(`{"file_path":"b.txt"}`)},
		{ID: "3", Name: "slow", Input: json.RawMessage(`{"file_path":"./a.txt"}`)},
		{ID: "4", Name: "missing", Input: json.RawMessage(`{}`)},
		{ID: "5", Name: "slow", Input: json.RawMessage(`{"_invalid_json":"x"}`)},
	}
	start := time.Now()
	res := r.Execute(context.Background(), env, calls, nopUI{})
	for i, b := range res {
		if b.ToolUseID != calls[i].ID {
			t.Errorf("result %d has id %s", i, b.ToolUseID)
		}
	}
	if overlap {
		t.Error("calls on the same path overlapped")
	}
	if !res[3].IsError || !strings.Contains(res[3].Text, "unknown tool") || !res[4].IsError {
		t.Errorf("error results: %+v %+v", res[3], res[4])
	}
	if d := time.Since(start); d > 85*time.Millisecond {
		t.Errorf("different paths did not run in parallel: %v", d)
	}
}

func TestReadOnlyRefusesWrites(t *testing.T) {
	dir := t.TempDir()
	env := &Env{Cwd: dir, ReadOnly: func() bool { return true }}
	r := NewRegistry(Write(), Edit())
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

func TestReadPath(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	os.WriteFile(p, []byte("a\nb\nc\nd\n"), 0o600)
	res := readPath(p, 2, 2, nil)
	if res.Text != "2\tb\n3\tc\n(lines 2-3 of 4; continue with offset=4)\n" {
		t.Errorf("got %q", res.Text)
	}
	if res := readPath(p, 9, 0, nil); !res.IsError {
		t.Error("offset past end accepted")
	}
	os.WriteFile(filepath.Join(dir, "bin"), []byte{0, 1, 2}, 0o600)
	if res := readPath(filepath.Join(dir, "bin"), 0, 0, nil); res.Summary != "binary" {
		t.Errorf("binary: %+v", res)
	}
	if res := readPath("/dev/null", 0, 0, nil); !res.IsError {
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
	res := NewRegistry(Write()).Execute(context.Background(), env, []llm.Block{
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

func TestSamePathEditsRunInOrder(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("one\n"), 0o600)
	res := NewRegistry(Edit()).Execute(context.Background(), &Env{Cwd: dir}, []llm.Block{
		{ID: "1", Name: "edit", Input: json.RawMessage(`{"file_path":"f.txt","old_string":"one","new_string":"two"}`)},
		{ID: "2", Name: "edit", Input: json.RawMessage(`{"file_path":"./f.txt","old_string":"two","new_string":"three"}`)},
	}, nopUI{})
	data, _ := os.ReadFile(filepath.Join(dir, "f.txt"))
	if string(data) != "three\n" || res[0].IsError || res[1].IsError {
		t.Fatalf("got %q, results %+v", data, res)
	}
}

func TestBashTimeoutUnits(t *testing.T) {
	cases := map[[2]int]int{{120000, 0}: 120, {30, 0}: 30, {0, 0}: 120, {0, 45}: 45, {900000, 0}: 600, {1500, 0}: 2, {600, 0}: 600, {601, 0}: 600, {900, 0}: 600}
	for in, want := range cases {
		if got := bashTimeout(in[0], in[1]); got != want {
			t.Errorf("bashTimeout(%d, %d) = %d, want %d", in[0], in[1], got, want)
		}
	}
}

func TestReadCapsOutputBytes(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "big.jsonl")
	line := strings.Repeat("x", 1999) + "\n"
	os.WriteFile(p, []byte(strings.Repeat(line, 2000)), 0o600)
	res := readPath(p, 0, 0, nil)
	if len(res.Text) > maxReadOutput+5000 || !strings.Contains(res.Text, "of 2000; continue with offset=") {
		t.Errorf("read returned %d bytes", len(res.Text))
	}
}

func TestEditsSerializeDependentBash(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("old\n"), 0o600)
	res := NewRegistry(Edit(), Bash()).Execute(context.Background(), &Env{Cwd: dir, SpillDir: t.TempDir()}, []llm.Block{
		{ID: "1", Name: "edit", Input: json.RawMessage(`{"file_path":"f.txt","old_string":"old","new_string":"new"}`)},
		{ID: "2", Name: "bash", Input: json.RawMessage(`{"command":"cat f.txt"}`)},
	}, nopUI{})
	if !strings.Contains(res[1].Text, "new") {
		t.Errorf("bash ran before the edit: %q", res[1].Text)
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
	r := NewRegistry(Read(), Write(), Edit())
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
	ok := func(name string) *Tool {
		return &Tool{Def: llm.ToolDef{Name: name}, Run: func(context.Context, *Env, json.RawMessage, func(string)) Result {
			return Result{Text: name + " ran"}
		}}
	}
	boom := &Tool{Def: llm.ToolDef{Name: "boom"}, Run: func(context.Context, *Env, json.RawMessage, func(string)) Result {
		panic(errors.New("boom"))
	}}
	r := NewRegistry(ok("fine"), ok("write"), boom)
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

func TestExecuteSchedulesByMetadata(t *testing.T) {
	var mu sync.Mutex
	running, maxSerial, lookOverlapped := map[string]bool{}, 0, false
	tool := func(name string, mutates, parallel bool) *Tool {
		return &Tool{
			Def:          llm.ToolDef{Name: name},
			Mutates:      mutates,
			ParallelSafe: parallel,
			Run: func(ctx context.Context, env *Env, in json.RawMessage, _ func(string)) Result {
				mu.Lock()
				running[name] = true
				if running["change"] && running["shell"] {
					maxSerial = 2
				}
				if running["look"] && (running["change"] || running["shell"]) {
					lookOverlapped = true
				}
				mu.Unlock()
				time.Sleep(30 * time.Millisecond)
				mu.Lock()
				running[name] = false
				mu.Unlock()
				return Result{Text: name}
			},
		}
	}
	r := NewRegistry(tool("change", true, false), tool("shell", false, false), tool("look", false, true))
	calls := []llm.Block{{ID: "1", Name: "change", Input: json.RawMessage(`{}`)}, {ID: "2", Name: "shell", Input: json.RawMessage(`{}`)}, {ID: "3", Name: "look", Input: json.RawMessage(`{}`)}}
	r.Execute(context.Background(), &Env{Cwd: t.TempDir()}, calls, nopUI{})
	if maxSerial != 0 {
		t.Error("a mutating call ran alongside a call that is not parallel-safe")
	}
	if !lookOverlapped {
		t.Error("parallel-safe call did not run alongside the serial ones")
	}
	running, maxSerial = map[string]bool{}, 0
	r = NewRegistry(tool("change", false, false), tool("shell", false, false))
	r.Execute(context.Background(), &Env{Cwd: t.TempDir()}, calls[:2], nopUI{})
	if maxSerial != 2 {
		t.Error("calls ran serially without a mutating tool")
	}
}

func TestMCPCallHookUnwrapAndRewrap(t *testing.T) {
	call := MCPTools(mcp.NewManager(nil))[1]
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
