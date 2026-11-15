package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/paul-nameless/qi/internal/config"
	"github.com/paul-nameless/qi/internal/tools"
	"github.com/paul-nameless/qi/internal/ui"
)

type hookCmd struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
}

type hookMatcher struct {
	Matcher string    `json:"matcher"`
	Hooks   []hookCmd `json:"hooks"`
	re      *regexp.Regexp
}

func (m *hookMatcher) compile() {
	m.re, _ = regexp.Compile("^(?:" + m.Matcher + ")$")
}

type Hooks struct {
	events     map[string][]hookMatcher
	Cwd        string
	SessionID  string
	Session    string
	ReadOnly   func() bool
	SkipPrompt bool
	Info       func(string)
}

type HookFile struct {
	Path   string
	events map[string][]hookMatcher
}

func LoadHooks(projectDir string, files []string) (*Hooks, error) {
	parsed, err := ParseHookFiles(files)
	if err != nil {
		return nil, err
	}
	return NewHooks(projectDir, parsed), nil
}

func NewHooks(projectDir string, files []HookFile) *Hooks {
	h := &Hooks{events: map[string][]hookMatcher{}, Cwd: projectDir}
	for _, f := range files {
		for ev, ms := range f.events {
			for _, m := range ms {
				m.compile()
				h.events[ev] = append(h.events[ev], m)
			}
		}
	}
	return h
}

func ParseHookFiles(files []string) ([]HookFile, error) {
	var out []HookFile
	seen := map[string]bool{}
	for _, f := range files {
		events, real, err := parseHookFile(f)
		if errors.Is(err, fs.ErrNotExist) || seen[real] {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("hooks: %s: %s; fix it or remove it from hook_files", config.ShortHome(f), err)
		}
		seen[real] = true
		out = append(out, HookFile{Path: f, events: events})
	}
	return out, nil
}

func parseHookFile(path string) (map[string][]hookMatcher, string, error) {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, "", pathProblem(err)
	}
	data, err := os.ReadFile(real)
	if err != nil {
		return nil, real, pathProblem(err)
	}
	var s struct {
		Hooks map[string][]hookMatcher `json:"hooks"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, real, jsonProblem(data, err)
	}
	return s.Hooks, real, nil
}

func pathProblem(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("cannot read: %w", pe.Err)
	}
	return err
}

func jsonProblem(data []byte, err error) error {
	var syn *json.SyntaxError
	var typ *json.UnmarshalTypeError
	switch {
	case errors.As(err, &syn):
		return fmt.Errorf("invalid JSON at line %d", lineAt(data, syn.Offset))
	case errors.As(err, &typ):
		return fmt.Errorf("%q has the wrong shape at line %d: %s where %s is expected", typ.Field, lineAt(data, typ.Offset), typ.Value, jsonKind(typ.Type))
	}
	return fmt.Errorf("invalid JSON: %w", err)
}

func jsonKind(t reflect.Type) string {
	switch t.Kind() {
	case reflect.Slice, reflect.Array:
		return "array"
	case reflect.Map, reflect.Struct:
		return "object"
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	}
	return "number"
}

func lineAt(data []byte, offset int64) int {
	return bytes.Count(data[:min(int(offset), len(data))], []byte("\n")) + 1
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

func (m hookMatcher) matches(names []string) bool {
	if m.Matcher == "" || m.Matcher == "*" || len(names) == 0 {
		return true
	}
	for _, n := range names {
		if m.re != nil && m.re.MatchString(n) || m.re == nil && m.Matcher == n {
			return true
		}
	}
	return false
}

func (h *Hooks) run(ctx context.Context, event string, names []string, payload map[string]any) []hookOutcome {
	if h.Empty() {
		return nil
	}
	var cmds []hookCmd
	dup := map[string]bool{}
	for _, m := range h.events[event] {
		if m.matches(names) {
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
			defer ui.CleanupOnPanic()
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
	cmd.Env = append(os.Environ(), "CLAUDE_PROJECT_DIR="+h.Cwd, "QI=1")
	cmd.Stdin = bytes.NewReader(input)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	out := hookOutcome{code: exitCode(err), stdout: stdout.String(), stderr: stderr.String()}
	if ctx.Err() == nil {
		h.reportFailure(c, timeout, tctx.Err(), err, out)
	}
	return out
}

func exitCode(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	if err != nil {
		return -1
	}
	return 0
}

func (h *Hooks) reportFailure(c hookCmd, timeout int, deadline, err error, o hookOutcome) {
	var exitErr *exec.ExitError
	var msg string
	switch {
	case errors.Is(deadline, context.DeadlineExceeded):
		msg = fmt.Sprintf("timed out after %ds", timeout)
	case err != nil && !errors.As(err, &exitErr):
		msg = "failed to start: " + err.Error()
	case o.code == 126 || o.code == 127:
		msg = fmt.Sprintf("failed to start (exit %d)", o.code)
	default:
		return
	}
	if last := lastLine(o.stderr); last != "" {
		msg += ": " + last
	}
	if h.Info != nil {
		h.Info("hook " + strconv.Quote(c.Command) + " " + msg)
	}
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
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

func toolInput(call tools.HookCall) any {
	var v any
	if json.Unmarshal(call.Input, &v) != nil {
		return map[string]any{}
	}
	return v
}

func (h *Hooks) PreToolUse(ctx context.Context, call tools.HookCall) (bool, string, json.RawMessage) {
	var updated json.RawMessage
	for _, o := range h.run(ctx, "PreToolUse", call.Names, map[string]any{"tool_name": call.Tool, "tool_input": toolInput(call), "tool_use_id": call.ID}) {
		if o.code == 2 {
			return true, strings.TrimSpace(o.stderr), nil
		}
		j := o.json()
		switch str(j, "hookSpecificOutput", "permissionDecision") {
		case "deny":
			return true, str(j, "hookSpecificOutput", "permissionDecisionReason"), nil
		case "ask":
			return true, "a hook asked for confirmation, which qi cannot prompt for: " + str(j, "hookSpecificOutput", "permissionDecisionReason"), nil
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

func (h *Hooks) PostToolUse(ctx context.Context, call tools.HookCall, res tools.Result) string {
	var notes []string
	payload := map[string]any{"tool_name": call.Tool, "tool_input": toolInput(call), "tool_use_id": call.ID, "tool_response": map[string]any{"output": res.Text, "is_error": res.IsError}}
	for _, o := range h.run(ctx, "PostToolUse", call.Names, payload) {
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
	for _, o := range h.run(ctx, "UserPromptSubmit", nil, map[string]any{"prompt": prompt}) {
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
	for _, o := range h.run(ctx, "Stop", nil, map[string]any{"stop_hook_active": active, "last_assistant_message": lastText}) {
		if o.code == 2 {
			return true, strings.TrimSpace(o.stderr)
		}
		if j := o.json(); str(j, "decision") == "block" {
			return true, str(j, "reason")
		}
	}
	return false, ""
}
