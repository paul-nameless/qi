package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/paul-nameless/aicode/internal/llm"
	"github.com/paul-nameless/aicode/internal/tools"
)

type hookCmd struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
}

type hookMatcher struct {
	Matcher string    `json:"matcher"`
	Hooks   []hookCmd `json:"hooks"`
}

type Hooks struct {
	events     map[string][]hookMatcher
	Cwd        string
	SessionID  string
	Session    string
	ReadOnly   func() bool
	SkipPrompt bool
}

var claudeToolNames = map[string][]string{
	"read":     {"Read"},
	"write":    {"Write"},
	"edit":     {"Edit"},
	"bash":     {"Bash"},
	"subagent": {"Agent", "Task"},
}

func LoadHooks(projectDir string, files []string) *Hooks {
	h := &Hooks{events: map[string][]hookMatcher{}, Cwd: projectDir}
	seen := map[string]bool{}
	for _, f := range files {
		real, err := filepath.EvalSymlinks(f)
		if err != nil || seen[real] {
			continue
		}
		seen[real] = true
		data, err := os.ReadFile(real)
		if err != nil {
			continue
		}
		var s struct {
			Hooks map[string][]hookMatcher `json:"hooks"`
		}
		if json.Unmarshal(data, &s) != nil {
			continue
		}
		for ev, ms := range s.Hooks {
			h.events[ev] = append(h.events[ev], ms...)
		}
	}
	return h
}

func (h *Hooks) Empty() bool {
	return h == nil || len(h.events) == 0
}

type hookOutcome struct {
	code   int
	stdout string
	stderr string
}

func (o hookOutcome) json() map[string]any {
	var m map[string]any
	if s := strings.TrimSpace(o.stdout); o.code == 0 && strings.HasPrefix(s, "{") {
		_ = json.Unmarshal([]byte(s), &m)
	}
	return m
}

func matches(matcher, tool string) bool {
	if matcher == "" || matcher == "*" || tool == "" {
		return true
	}
	names := append([]string{tool}, claudeToolNames[tool]...)
	re, err := regexp.Compile("^(?:" + matcher + ")$")
	for _, n := range names {
		if err == nil && re.MatchString(n) || err != nil && matcher == n {
			return true
		}
	}
	return false
}

func (h *Hooks) run(ctx context.Context, event, tool string, payload map[string]any) []hookOutcome {
	if h.Empty() {
		return nil
	}
	var cmds []hookCmd
	dup := map[string]bool{}
	for _, m := range h.events[event] {
		if matches(m.Matcher, tool) {
			for _, c := range m.Hooks {
				if c.Type == "command" && c.Command != "" && !dup[c.Command] {
					dup[c.Command] = true
					cmds = append(cmds, c)
				}
			}
		}
	}
	if len(cmds) == 0 {
		return nil
	}
	mode := "bypassPermissions"
	if h.ReadOnly != nil && h.ReadOnly() {
		mode = "plan"
	}
	payload["session_id"] = h.SessionID
	payload["transcript_path"] = h.Session
	payload["cwd"] = h.Cwd
	payload["hook_event_name"] = event
	payload["permission_mode"] = mode
	input, _ := json.Marshal(payload)
	out := make([]hookOutcome, len(cmds))
	var wg sync.WaitGroup
	for i, c := range cmds {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i] = h.exec(ctx, c, input)
		}()
	}
	wg.Wait()
	return out
}

func (h *Hooks) exec(ctx context.Context, c hookCmd, input []byte) hookOutcome {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 60
	}
	tctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(tctx, "sh", "-c", c.Command)
	cmd.Dir = h.Cwd
	cmd.Env = append(os.Environ(), "CLAUDE_PROJECT_DIR="+h.Cwd, "AICODE=1")
	cmd.Stdin = bytes.NewReader(input)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code = exitErr.ExitCode()
	} else if err != nil {
		code = 1
	}
	return hookOutcome{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

func str(m map[string]any, keys ...string) string {
	for i, k := range keys {
		v, ok := m[k]
		if !ok {
			return ""
		}
		if i == len(keys)-1 {
			s, _ := v.(string)
			return s
		}
		m, ok = v.(map[string]any)
		if !ok {
			return ""
		}
	}
	return ""
}

func toolInput(call llm.Block) any {
	var v any
	if json.Unmarshal(call.Input, &v) != nil {
		return map[string]any{}
	}
	if call.Name == "mcp_call" {
		if m, ok := v.(map[string]any); ok && m["arguments"] != nil {
			return m["arguments"]
		}
	}
	return v
}

func hookToolName(call llm.Block) string {
	if call.Name == "mcp_call" {
		var a struct{ Server, Tool string }
		if json.Unmarshal(call.Input, &a) == nil && a.Server != "" {
			return "mcp__" + a.Server + "__" + a.Tool
		}
	}
	return claudeName(call.Name)
}

func (h *Hooks) PreToolUse(ctx context.Context, call llm.Block) (bool, string, json.RawMessage) {
	var updated json.RawMessage
	for _, o := range h.run(ctx, "PreToolUse", hookMatchName(call), map[string]any{"tool_name": hookToolName(call), "tool_input": toolInput(call), "tool_use_id": call.ID}) {
		if o.code == 2 {
			return true, strings.TrimSpace(o.stderr), nil
		}
		j := o.json()
		switch str(j, "hookSpecificOutput", "permissionDecision") {
		case "deny":
			return true, str(j, "hookSpecificOutput", "permissionDecisionReason"), nil
		case "ask":
			return true, "a hook asked for confirmation, which aicode cannot prompt for: " + str(j, "hookSpecificOutput", "permissionDecisionReason"), nil
		}
		if str(j, "decision") == "block" {
			return true, str(j, "reason"), nil
		}
		if hso, ok := j["hookSpecificOutput"].(map[string]any); ok && hso["updatedInput"] != nil {
			updated, _ = json.Marshal(hso["updatedInput"])
		}
	}
	return false, "", updated
}

func (h *Hooks) PostToolUse(ctx context.Context, call llm.Block, res tools.Result) string {
	var notes []string
	payload := map[string]any{"tool_name": hookToolName(call), "tool_input": toolInput(call), "tool_use_id": call.ID, "tool_response": map[string]any{"output": res.Text, "is_error": res.IsError}}
	for _, o := range h.run(ctx, "PostToolUse", hookMatchName(call), payload) {
		j := o.json()
		switch {
		case o.code == 2:
			notes = append(notes, strings.TrimSpace(o.stderr))
		case str(j, "decision") == "block":
			notes = append(notes, str(j, "reason"))
		case str(j, "hookSpecificOutput", "additionalContext") != "":
			notes = append(notes, str(j, "hookSpecificOutput", "additionalContext"))
		}
	}
	if len(notes) == 0 {
		return ""
	}
	return "<hook>\n" + strings.Join(notes, "\n") + "\n</hook>"
}

func (h *Hooks) UserPromptSubmit(ctx context.Context, prompt string) (blocked bool, reason, extra string) {
	if h.SkipPrompt {
		return false, "", ""
	}
	var ctxs []string
	for _, o := range h.run(ctx, "UserPromptSubmit", "", map[string]any{"prompt": prompt}) {
		j := o.json()
		switch {
		case o.code == 2:
			return true, strings.TrimSpace(o.stderr), ""
		case str(j, "decision") == "block":
			return true, str(j, "reason"), ""
		case j != nil:
			if c := str(j, "hookSpecificOutput", "additionalContext"); c != "" {
				ctxs = append(ctxs, c)
			}
		case o.code == 0 && strings.TrimSpace(o.stdout) != "":
			ctxs = append(ctxs, strings.TrimSpace(o.stdout))
		}
	}
	return false, "", strings.Join(ctxs, "\n")
}

func (h *Hooks) Stop(ctx context.Context, lastText string, active bool) (bool, string) {
	for _, o := range h.run(ctx, "Stop", "", map[string]any{"stop_hook_active": active, "last_assistant_message": lastText}) {
		if o.code == 2 {
			return true, strings.TrimSpace(o.stderr)
		}
		if j := o.json(); str(j, "decision") == "block" {
			return true, str(j, "reason")
		}
	}
	return false, ""
}

func hookMatchName(call llm.Block) string {
	if call.Name == "mcp_call" {
		return hookToolName(call)
	}
	return call.Name
}

func claudeName(tool string) string {
	if names, ok := claudeToolNames[tool]; ok {
		return names[0]
	}
	return tool
}
