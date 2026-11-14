package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paul-nameless/aicode/internal/llm"
	"github.com/paul-nameless/aicode/internal/tools"
)

func writeHooks(t *testing.T, hooks string) *Hooks {
	dir := t.TempDir()
	p := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(p, []byte(`{"hooks":`+hooks+`}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return LoadHooks(dir, []string{p, filepath.Join(dir, "missing.json")})
}

func TestMatches(t *testing.T) {
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
		if got := matches(c.matcher, c.tool); got != c.want {
			t.Errorf("matches(%q, %q) = %v", c.matcher, c.tool, got)
		}
	}
}

func TestPreToolUse(t *testing.T) {
	h := writeHooks(t, `{"PreToolUse":[
		{"matcher":"Bash","hooks":[{"type":"command","command":"grep -q 'rm -rf' && { echo no rm >&2; exit 2; } || exit 0"}]},
		{"matcher":"Write","hooks":[{"type":"command","command":"echo '{\"hookSpecificOutput\":{\"permissionDecision\":\"deny\",\"permissionDecisionReason\":\"no writes\"}}'"}]}
	]}`)
	call := func(name, input string) llm.Block {
		return llm.Block{ID: "1", Name: name, Input: json.RawMessage(input)}
	}
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
	note := h.PostToolUse(ctx, llm.Block{Name: "edit", Input: json.RawMessage(`{}`)}, tools.Result{Text: "ok"})
	if !strings.Contains(note, "lint failed") {
		t.Errorf("post: %q", note)
	}
}

func TestHookJSONIgnoredOnNonZeroExit(t *testing.T) {
	h := writeHooks(t, `{"PreToolUse":[{"hooks":[{"type":"command","command":"echo '{\"decision\":\"block\",\"reason\":\"x\"}'; exit 1"}]}]}`)
	if block, _, _ := h.PreToolUse(context.Background(), llm.Block{Name: "bash", Input: json.RawMessage(`{}`)}); block {
		t.Error("block JSON honored on exit 1")
	}
}

func TestPreToolUseAskAndUpdatedInput(t *testing.T) {
	h := writeHooks(t, `{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"grep -q danger && echo '{\"hookSpecificOutput\":{\"permissionDecision\":\"ask\"}}' || echo '{\"hookSpecificOutput\":{\"updatedInput\":{\"command\":\"echo safe\"}}}'"}]}]}`)
	if block, _, _ := h.PreToolUse(context.Background(), llm.Block{Name: "bash", Input: json.RawMessage(`{"command":"danger"}`)}); !block {
		t.Error("ask not treated as block")
	}
	_, _, in := h.PreToolUse(context.Background(), llm.Block{Name: "bash", Input: json.RawMessage(`{"command":"ls"}`)})
	if string(in) != `{"command":"echo safe"}` {
		t.Errorf("updated input %s", in)
	}
}

func TestMCPHookNaming(t *testing.T) {
	h := writeHooks(t, `{"PreToolUse":[{"matcher":"mcp__github__.*","hooks":[{"type":"command","command":"in=$(cat); echo \"$in\" | grep -q mcp__github__create_issue && echo \"$in\" | grep -q title && { echo denied >&2; exit 2; }; exit 0"}]}]}`)
	call := llm.Block{Name: "mcp_call", Input: json.RawMessage(`{"server":"github","tool":"create_issue","arguments":{"title":"x"}}`)}
	if block, reason, _ := h.PreToolUse(context.Background(), call); !block || reason != "denied" {
		t.Errorf("mcp hook: %v %q", block, reason)
	}
	other := llm.Block{Name: "mcp_call", Input: json.RawMessage(`{"server":"slack","tool":"post","arguments":{}}`)}
	if block, _, _ := h.PreToolUse(context.Background(), other); block {
		t.Error("matcher applied to another server")
	}
}

func TestEmptyHooks(t *testing.T) {
	var h *Hooks
	if !h.Empty() {
		t.Error("nil hooks not empty")
	}
	if blocked, _, _ := LoadHooks(t.TempDir(), nil).UserPromptSubmit(context.Background(), "x"); blocked {
		t.Error("no hooks blocked a prompt")
	}
}
