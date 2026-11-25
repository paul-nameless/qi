package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/paul-nameless/qi/internal/llm"
	"github.com/paul-nameless/qi/internal/mcp"
	"github.com/paul-nameless/qi/internal/session"
	"github.com/paul-nameless/qi/internal/tools"
)

func loadHooks(projectDir string, files []string) (*Hooks, error) {
	parsed, err := ParseHookFiles(files)
	if err != nil {
		return nil, err
	}
	return NewHooks(projectDir, parsed), nil
}

func writeHooks(t *testing.T, hooks string) *Hooks {
	dir := t.TempDir()
	p := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(p, []byte(`{"hooks":`+hooks+`}`), 0o600); err != nil {
		t.Fatal(err)
	}
	h, err := loadHooks(dir, []string{p, filepath.Join(dir, "missing.json")})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestMatches(t *testing.T) {
	t.Parallel()
	cases := []struct {
		matcher, tool string
		want          bool
	}{
		{"", "bash", true},
		{"*", "edit", true},
		{"Bash", "bash", true},
		{"Edit|Write", "write", true},
		{"Edit|Write", "bash", false},
		{"Task", "subagent", true},
		{"Bash", "", true},
		{"[", "bash", false},
		{"Bas", "bash", false},
	}
	for _, c := range cases {
		var names []string
		if c.tool != "" {
			names = hookCall(c.tool, `{}`).Names
		}
		m := hookMatcher{Matcher: c.matcher}
		m.compile()
		if got := m.matches(names); got != c.want {
			t.Errorf("matches(%q, %q) = %v", c.matcher, c.tool, got)
		}
	}
}

var hookTools = map[string]*tools.Function{
	"read": tools.Read(), "write": tools.Write(), "edit": tools.Edit(), "bash": tools.Bash(),
	"subagent": tools.Subagent("m", nil, nil), "mcp_call": tools.MCPFuncs(mcp.NewManager(nil))[1],
}

func hookCall(name, input string) tools.HookCall {
	return hookTools[name].HookCall(llm.Block{ID: "1", Name: name, Input: json.RawMessage(input)})
}

func TestPreToolUse(t *testing.T) {
	t.Parallel()
	h := writeHooks(t, `{"PreToolUse":[
		{"matcher":"Bash","hooks":[{"type":"command","command":"grep -q 'rm -rf' && { echo no rm >&2; exit 2; } || exit 0"}]},
		{"matcher":"Write","hooks":[{"type":"command","command":"echo '{\"hookSpecificOutput\":{\"permissionDecision\":\"deny\",\"permissionDecisionReason\":\"no writes\"}}'"}]}
	]}`)
	call := hookCall
	if block, reason, _ := h.PreToolUse(context.Background(), call("bash", `{"command":"rm -rf x"}`)); !block || reason != "no rm" {
		t.Errorf("exit 2: %v %q", block, reason)
	}
	if block, _, _ := h.PreToolUse(context.Background(), call("bash", `{"command":"ls"}`)); block {
		t.Error("ls blocked")
	}
	if block, reason, _ := h.PreToolUse(context.Background(), call("write", `{"file_path":"a"}`)); !block || reason != "no writes" {
		t.Errorf("json deny: %v %q", block, reason)
	}
	if block, _, _ := h.PreToolUse(context.Background(), call("read", `{"file_path":"a"}`)); block {
		t.Error("read blocked")
	}
}

func TestStopPromptAndPostHooks(t *testing.T) {
	t.Parallel()
	h := writeHooks(t, `{
		"Stop":[{"hooks":[{"type":"command","command":"grep -q '\"stop_hook_active\":true' && exit 0; echo keep going >&2; exit 2"}]}],
		"UserPromptSubmit":[{"hooks":[{"type":"command","command":"grep -q secret && { echo blocked >&2; exit 2; }; echo extra context"}]}],
		"PostToolUse":[{"hooks":[{"type":"command","command":"echo lint failed >&2; exit 2"}]}]
	}`)
	ctx := context.Background()
	if cont, reason := h.Stop(ctx, "done", false); !cont || reason != "keep going" {
		t.Errorf("stop: %v %q", cont, reason)
	}
	if cont, _ := h.Stop(ctx, "done", true); cont {
		t.Error("stop hook did not respect stop_hook_active")
	}
	if blocked, _, extra := h.UserPromptSubmit(ctx, "hello"); blocked || extra != "extra context" {
		t.Errorf("prompt: %v %q", blocked, extra)
	}
	if blocked, reason, _ := h.UserPromptSubmit(ctx, "my secret"); !blocked || reason != "blocked" {
		t.Errorf("prompt block: %v %q", blocked, reason)
	}
	note, _ := h.PostToolUse(ctx, hookCall("edit", `{}`), tools.Result{Text: "ok"})
	if !strings.Contains(note, "lint failed") {
		t.Errorf("post: %q", note)
	}
}

func TestLegacyDecisionBlock(t *testing.T) {
	t.Parallel()
	h := writeHooks(t, `{"PreToolUse":[{"hooks":[{"type":"command","command":"echo '{\"decision\":\"block\",\"reason\":\"r\"}'"}]}]}`)
	if block, reason, _ := h.PreToolUse(context.Background(), hookCall("bash", `{}`)); !block || reason != "r" {
		t.Errorf("legacy block: %v %q", block, reason)
	}
}

func TestHookJSONIgnoredOnNonZeroExit(t *testing.T) {
	t.Parallel()
	h := writeHooks(t, `{"PreToolUse":[{"hooks":[{"type":"command","command":"echo '{\"decision\":\"block\",\"reason\":\"x\"}'; exit 1"}]}]}`)
	if block, _, _ := h.PreToolUse(context.Background(), hookCall("bash", `{}`)); block {
		t.Error("block JSON honored on exit 1")
	}
}

func TestPreToolUseAskAndUpdatedInput(t *testing.T) {
	t.Parallel()
	h := writeHooks(t, `{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"grep -q danger && echo '{\"hookSpecificOutput\":{\"permissionDecision\":\"ask\"}}' || echo '{\"hookSpecificOutput\":{\"updatedInput\":{\"command\":\"echo safe\"}}}'"}]}]}`)
	if block, _, _ := h.PreToolUse(context.Background(), hookCall("bash", `{"command":"danger"}`)); !block {
		t.Error("ask not treated as block")
	}
	_, _, in := h.PreToolUse(context.Background(), hookCall("bash", `{"command":"ls"}`))
	if string(in) != `{"command":"echo safe"}` {
		t.Errorf("updated input %s", in)
	}
}

func TestMCPHookNaming(t *testing.T) {
	t.Parallel()
	h := writeHooks(t, `{"PreToolUse":[{"matcher":"mcp__github__.*","hooks":[{"type":"command","command":"in=$(cat); echo \"$in\" | grep -q mcp__github__create_issue && echo \"$in\" | grep -q title && { echo denied >&2; exit 2; }; exit 0"}]}]}`)
	call := hookCall("mcp_call", `{"server":"github","tool":"create_issue","arguments":{"title":"x"}}`)
	if block, reason, _ := h.PreToolUse(context.Background(), call); !block || reason != "denied" {
		t.Errorf("mcp hook: %v %q", block, reason)
	}
	other := hookCall("mcp_call", `{"server":"slack","tool":"post","arguments":{}}`)
	if block, _, _ := h.PreToolUse(context.Background(), other); block {
		t.Error("matcher applied to another server")
	}
}

func TestEmptyHooks(t *testing.T) {
	t.Parallel()
	var h *Hooks
	if !h.Empty() {
		t.Error("nil hooks not empty")
	}
	if blocked, _, _ := NewHooks(t.TempDir(), nil).UserPromptSubmit(context.Background(), "x"); blocked {
		t.Error("no hooks blocked a prompt")
	}
}

func TestHookFileErrors(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for name, body := range map[string]string{
		"syntax.json": "{\n  \"hooks\": {\n    \"Stop\": [,]\n  }\n}",
		"shape.json":  `{"hooks": {"Stop": [{"hooks": [{"command": "x", "timeout": "60"}]}]}}`,
		"top.json":    `{"hooks": []}`,
		"fail.json":   `{"hooks": {"Stop": [{"hooks": [{"type": "command", "command": "x", "onFailure": "deny"}]}]}}`,
	} {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(body), 0o600)
		_, err := loadHooks(dir, []string{p})
		if err == nil || !strings.Contains(err.Error(), "hooks: "+p+": ") || !strings.HasSuffix(err.Error(), "; fix it or remove it from hook_files") {
			t.Errorf("%s: %v", name, err)
		}
	}
	_, err := loadHooks(dir, []string{filepath.Join(dir, "syntax.json")})
	if err == nil || !strings.Contains(err.Error(), "invalid JSON at line 3") {
		t.Errorf("syntax error line: %v", err)
	}
	_, err = loadHooks(dir, []string{filepath.Join(dir, "shape.json")})
	if err == nil || !strings.Contains(err.Error(), `"hooks.Stop.0.hooks.0.timeout" has the wrong shape at line 1: string where number is expected`) {
		t.Errorf("shape error: %v", err)
	}
	other := filepath.Join(dir, "other.json")
	os.WriteFile(other, []byte(`{"model": "x", "permissions": {"allow": ["Bash"]}, "hooks": {"Stop": [{"hooks": [{"type": "command", "command": "true", "extra": 1}]}]}}`), 0o600)
	h, err := loadHooks(dir, []string{filepath.Join(dir, "missing.json"), other})
	if err != nil || h.Empty() {
		t.Errorf("unknown keys or a missing file broke loading: %v", err)
	}
	unreadable := filepath.Join(dir, "unreadable.json")
	os.WriteFile(unreadable, []byte(`{}`), 0o000)
	if f, openErr := os.Open(unreadable); openErr == nil {
		f.Close()
		t.Skip("running as root, permissions are not enforced")
	}
	if _, err := loadHooks(dir, []string{unreadable}); err == nil || !strings.Contains(err.Error(), "cannot read: permission denied") {
		t.Errorf("unreadable: %v", err)
	}
}

func TestHookTimeoutAndStartFailureAreReported(t *testing.T) {
	t.Parallel()
	var lines []string
	var mu sync.Mutex
	h := writeHooks(t, `{"Stop":[{"hooks":[{"type":"command","command":"echo slow >&2; sleep 5","timeout":1},{"type":"command","command":"no-such-hook-binary"}]}]}`)
	h.Info = func(s string) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, s)
	}
	if blocked, _ := h.Stop(context.Background(), "", false); blocked {
		t.Error("a failing hook blocked")
	}
	slices.Sort(lines)
	if len(lines) != 2 || !strings.HasPrefix(lines[0], `hook "echo slow >&2; sleep 5" timed out after 1s: slow`) || !strings.HasPrefix(lines[1], `hook "no-such-hook-binary" failed to start (exit 127): `) {
		t.Errorf("lines %q", lines)
	}
}

func TestAsyncHookRunsInBackground(t *testing.T) {
	t.Parallel()
	marker := filepath.Join(t.TempDir(), "ran")
	h := writeHooks(t, `{"PreToolUse":[{"hooks":[{"type":"command","async":true,"command":"sleep 0.5; echo no >&2; touch `+marker+`; exit 2"}]}]}`)
	start := time.Now()
	if block, _, _ := h.PreToolUse(context.Background(), hookCall("bash", `{"command":"ls"}`)); block {
		t.Error("an async hook blocked the call")
	}
	if waited := time.Since(start); waited > 300*time.Millisecond {
		t.Errorf("waited %v for an async hook", waited)
	}
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if _, err := os.Stat(marker); err == nil {
			return
		}
	}
	t.Error("the async hook never ran to the end")
}

func TestBlockingDuplicateOfAsyncHookStillBlocks(t *testing.T) {
	t.Parallel()
	h := writeHooks(t, `{"PreToolUse":[
		{"hooks":[{"type":"command","async":true,"command":"echo no >&2; exit 2"}]},
		{"matcher":"Bash","hooks":[{"type":"command","command":"echo no >&2; exit 2"}]}
	]}`)
	if block, _, _ := h.PreToolUse(context.Background(), hookCall("bash", `{"command":"ls"}`)); !block {
		t.Error("the same command marked blocking elsewhere did not block")
	}
}

func TestOnFailureBlock(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, c := range []struct {
		name, hook string
		block      bool
		reason     string
	}{
		{"missing script", `{"type":"command","command":"no-such-hook-binary","onFailure":"block"}`, true, "PreToolUse:Bash hook error: [no-such-hook-binary]: failed; blocking because onFailure is \"block\"\n"},
		{"missing script unset", `{"type":"command","command":"no-such-hook-binary"}`, false, ""},
		{"missing script continue", `{"type":"command","command":"no-such-hook-binary","onFailure":"continue"}`, false, ""},
		{"exit 1 with allow", `{"type":"command","command":"echo '{\"hookSpecificOutput\":{\"permissionDecision\":\"allow\"}}'; exit 1","onFailure":"block"}`, true, "PreToolUse:Bash hook error: [echo '{\"hookSpecificOutput\":{\"permissionDecision\":\"allow\"}}'; exit 1]: failed; blocking because onFailure is \"block\""},
		{"timeout", `{"type":"command","command":"sleep 5","timeout":1,"onFailure":"block"}`, true, "PreToolUse:Bash hook error: [sleep 5]: timed out; blocking because onFailure is \"block\""},
		{"invalid json", `{"type":"command","command":"echo '{nope}'","onFailure":"block"}`, true, "PreToolUse:Bash hook error: [echo '{nope}']: failed; blocking because onFailure is \"block\""},
		{"plain text", `{"type":"command","command":"echo fine","onFailure":"block"}`, false, ""},
		{"async", `{"type":"command","command":"exit 1","async":true,"onFailure":"block"}`, false, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			h := writeHooks(t, `{"PreToolUse":[{"matcher":"Bash","hooks":[`+c.hook+`]}]}`)
			block, reason, _ := h.PreToolUse(ctx, hookCall("bash", `{"command":"ls"}`))
			if block != c.block || !strings.HasPrefix(reason, c.reason) {
				t.Errorf("got %v %q", block, reason)
			}
		})
	}
}

func TestOnFailureBlockPerEvent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := writeHooks(t, `{
		"UserPromptSubmit":[{"hooks":[{"type":"command","command":"echo boom >&2; exit 1","onFailure":"block"}]}],
		"PostToolUse":[{"hooks":[{"type":"command","command":"exit 3","onFailure":"block"}]}],
		"Stop":[{"hooks":[{"type":"command","command":"exit 1","onFailure":"block"}]}]
	}`)
	if blocked, reason, _ := h.UserPromptSubmit(ctx, "hi"); !blocked || reason != "UserPromptSubmit hook error: [echo boom >&2; exit 1]: failed; blocking because onFailure is \"block\"\nboom" {
		t.Errorf("prompt: %v %q", blocked, reason)
	}
	if note, _ := h.PostToolUse(ctx, hookCall("edit", `{}`), tools.Result{Text: "ok"}); !strings.Contains(note, "PostToolUse:Edit hook error: [exit 3]: failed") {
		t.Errorf("post: %q", note)
	}
	if cont, _ := h.Stop(ctx, "done", false); cont {
		t.Error("a failing Stop hook kept the turn going")
	}
}

func TestBlockingDuplicateKeepsOnFailure(t *testing.T) {
	t.Parallel()
	h := writeHooks(t, `{"PreToolUse":[
		{"hooks":[{"type":"command","command":"exit 1"}]},
		{"matcher":"Bash","hooks":[{"type":"command","command":"exit 1","onFailure":"block"}]}
	]}`)
	if block, _, _ := h.PreToolUse(context.Background(), hookCall("bash", `{"command":"ls"}`)); !block {
		t.Error("onFailure block on a duplicate listing was lost")
	}
}

func TestPostToolUseUpdatedOutput(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var info []string
	var mu sync.Mutex
	h := writeHooks(t, `{"PostToolUse":[
		{"matcher":"Bash","hooks":[{"type":"command","command":"echo '{\"hookSpecificOutput\":{\"additionalContext\":\"note\",\"updatedToolOutput\":{\"output\":\"[redacted]\"}}}'"}]},
		{"matcher":"Read","hooks":[{"type":"command","command":"echo '{\"hookSpecificOutput\":{\"updatedToolOutput\":{\"stdout\":\"x\"}}}'"}]},
		{"matcher":"Write","hooks":[{"type":"command","command":"echo '{\"hookSpecificOutput\":{\"updatedMCPToolOutput\":{\"output\":\"x\"}}}'"}]},
		{"matcher":"mcp__.*","hooks":[{"type":"command","command":"echo '{\"hookSpecificOutput\":{\"updatedMCPToolOutput\":{\"output\":\"mcp\",\"is_error\":true}}}'"}]}
	]}`)
	h.Info = func(s string) {
		mu.Lock()
		defer mu.Unlock()
		info = append(info, s)
	}
	note, out := h.PostToolUse(ctx, hookCall("bash", `{"command":"env"}`), tools.Result{Text: "KEY=1"})
	if note != "<hook>\nnote\n</hook>" || out == nil || out.Output != "[redacted]" || out.IsError != nil {
		t.Errorf("bash: %q %+v", note, out)
	}
	if _, out := h.PostToolUse(ctx, hookCall("read", `{"file_path":"a"}`), tools.Result{Text: "a"}); out != nil || len(info) != 1 || !strings.Contains(info[0], "updatedToolOutput ignored") {
		t.Errorf("wrong shape: %+v %q", out, info)
	}
	if _, out := h.PostToolUse(ctx, hookCall("write", `{"file_path":"a","content":"b"}`), tools.Result{Text: "ok"}); out != nil {
		t.Errorf("updatedMCPToolOutput applied to Write: %+v", out)
	}
	_, out = h.PostToolUse(ctx, hookCall("mcp_call", `{"server":"s","tool":"t","arguments":{}}`), tools.Result{Text: "{}"})
	if out == nil || out.Output != "mcp" || out.IsError == nil || !*out.IsError {
		t.Errorf("mcp: %+v", out)
	}
}

func TestHookOutputCap(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := writeHooks(t, `{
		"UserPromptSubmit":[{"hooks":[{"type":"command","command":"awk 'BEGIN{while(i++<12000)printf \"a\"}'"}]}]
	}`)
	var spilled []string
	var mu sync.Mutex
	h.Spill = func(s string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		spilled = append(spilled, s)
		return "/spill/1.txt", nil
	}
	_, _, extra := h.UserPromptSubmit(ctx, "hi")
	want := strings.Repeat("a", 2000) + "\n… hook output of 12000 characters cut to the first 2000. Full output: /spill/1.txt"
	if extra != want || len(spilled) != 1 || len(spilled[0]) != 12000 {
		t.Errorf("extra %d chars, want %d, spilled %d", len(extra), len(want), len(spilled))
	}
	h.Spill = nil
	if got := h.capText(strings.Repeat("é", 10001)); !strings.HasSuffix(got, "Full output: (not saved: no spill dir)") || !strings.HasPrefix(got, strings.Repeat("é", 2000)+"\n") {
		t.Errorf("no spill: %q", got[len(got)-60:])
	}
	if got := h.capText(strings.Repeat("é", 10000)); got != strings.Repeat("é", 10000) {
		t.Error("text at the cap was cut")
	}
}

func TestHookPayloadReadsSessionLive(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "payloads")
	h := writeHooks(t, `{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"cat >> '`+out+`'; echo >> '`+out+`'"}]}]}`)
	h.UserPromptSubmit(context.Background(), "none")
	cur := &session.Session{ID: "one", Path: "/s/one.jsonl"}
	h.Session = func() *session.Session { return cur }
	h.UserPromptSubmit(context.Background(), "first")
	cur = &session.Session{ID: "two", Path: "/s/two.jsonl"}
	h.UserPromptSubmit(context.Background(), "second")
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for line := range strings.Lines(strings.TrimSpace(string(data))) {
		var p struct {
			ID   string `json:"session_id"`
			Path string `json:"transcript_path"`
		}
		if err := json.Unmarshal([]byte(line), &p); err != nil {
			t.Fatal(err)
		}
		got = append(got, p.ID+" "+p.Path)
	}
	if want := []string{" ", "one /s/one.jsonl", "two /s/two.jsonl"}; !slices.Equal(got, want) {
		t.Errorf("payload sessions %q, want %q", got, want)
	}
}
