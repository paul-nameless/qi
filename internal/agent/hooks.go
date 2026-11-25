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
	"time"
	"unicode/utf8"

	"github.com/paul-nameless/qi/internal/config"
	"github.com/paul-nameless/qi/internal/proc"
	"github.com/paul-nameless/qi/internal/session"
	"github.com/paul-nameless/qi/internal/tools"
	"github.com/paul-nameless/qi/internal/ui"
)

type hookCmd struct {
	Type      string `json:"type"`
	Command   string `json:"command"`
	Timeout   int    `json:"timeout"`
	Async     bool   `json:"async"`
	OnFailure string `json:"onFailure"`
}

func (c hookCmd) blocksOnFailure() bool {
	return c.OnFailure == "block" && !c.Async
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
	Session    func() *session.Session
	Mode       func() Mode
	SkipPrompt bool
	Info       func(string)
	Spill      func(string) (string, error)
}

func (h *Hooks) Attach(a *Agent) {
	h.Session, h.Mode, h.Spill = a.Session, a.Mode, a.Spill
}

func (h *Hooks) sessionRef() (id, path string) {
	if h.Session == nil {
		return "", ""
	}
	if s := h.Session(); s != nil {
		return s.ID, s.Path
	}
	return "", ""
}

const (
	hookTextCap     = 10000
	hookPreviewSize = 2000
)

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
	for ev, ms := range s.Hooks {
		for _, m := range ms {
			for _, c := range m.Hooks {
				if c.OnFailure != "" && c.OnFailure != "continue" && c.OnFailure != "block" {
					return nil, real, fmt.Errorf("%s hook %q: onFailure must be \"continue\" or \"block\", not %q", ev, c.Command, c.OnFailure)
				}
			}
		}
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
	cmd     hookCmd
	code    int
	stdout  string
	stderr  string
	failure string
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
	seen := map[string]int{}
	for _, m := range h.events[event] {
		if m.matches(names) {
			for _, c := range m.Hooks {
				if c.Type != "command" || c.Command == "" {
					continue
				}
				if i, ok := seen[c.Command]; ok {
					cmds[i].Async = cmds[i].Async && c.Async
					if c.OnFailure == "block" {
						cmds[i].OnFailure = "block"
					}
					continue
				}
				seen[c.Command] = len(cmds)
				cmds = append(cmds, c)
			}
		}
	}
	if len(cmds) == 0 {
		return nil
	}
	mode := "bypassPermissions"
	if h.Mode != nil {
		switch h.Mode() {
		case ModeReadOnly:
			mode = "plan"
		case ModeProject:
			mode = "acceptEdits"
		}
	}
	payload["session_id"], payload["transcript_path"] = h.sessionRef()
	payload["cwd"] = h.Cwd
	payload["hook_event_name"] = event
	payload["permission_mode"] = mode
	input, _ := json.Marshal(payload)
	var waited []hookCmd
	for _, c := range cmds {
		if !c.Async {
			waited = append(waited, c)
			continue
		}
		h.start(c, input)
	}
	out := make([]hookOutcome, len(waited))
	var wg sync.WaitGroup
	for i, c := range waited {
		wg.Go(func() {
			defer ui.CleanupOnPanic()
			out[i] = h.exec(ctx, c, input)
		})
	}
	wg.Wait()
	return out
}

func (h *Hooks) start(c hookCmd, input []byte) {
	ctx, cancel := context.WithTimeout(context.Background(), hookTimeout(c))
	cmd := h.command(ctx, c, input)
	if cmd.Start() != nil {
		cancel()
		return
	}
	go func() {
		_ = cmd.Wait()
		cancel()
	}()
}

func hookTimeout(c hookCmd) time.Duration {
	if c.Timeout <= 0 {
		return 60 * time.Second
	}
	return time.Duration(c.Timeout) * time.Second
}

func (h *Hooks) command(ctx context.Context, c hookCmd, input []byte) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "sh", "-c", c.Command)
	cmd.Dir = h.Cwd
	cmd.Env = append(os.Environ(), "CLAUDE_PROJECT_DIR="+h.Cwd, "QI=1")
	cmd.Stdin = bytes.NewReader(input)
	proc.Setup(cmd, proc.Session, proc.Grace)
	return cmd
}

func (h *Hooks) exec(ctx context.Context, c hookCmd, input []byte) hookOutcome {
	timeout := int(hookTimeout(c) / time.Second)
	tctx, cancel := context.WithTimeout(ctx, hookTimeout(c))
	defer cancel()
	cmd := h.command(tctx, c, input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	out := hookOutcome{cmd: c, code: exitCode(err), stdout: stdout.String(), stderr: stderr.String()}
	if ctx.Err() == nil {
		h.reportFailure(c, timeout, tctx.Err(), err, out)
	}
	out.failure = failure(tctx.Err(), out)
	return out
}

func failure(deadline error, o hookOutcome) string {
	switch {
	case errors.Is(deadline, context.DeadlineExceeded):
		return "timed out"
	case o.code != 0 && o.code != 2:
		return "failed"
	case o.code == 0 && invalidJSON(o.stdout):
		return "failed"
	}
	return ""
}

func invalidJSON(stdout string) bool {
	s := strings.TrimSpace(stdout)
	return strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") && !json.Valid([]byte(s))
}

func (h *Hooks) failureBlock(event, tool string, o hookOutcome) string {
	if o.failure == "" || !o.cmd.blocksOnFailure() {
		return ""
	}
	if tool != "" {
		event += ":" + tool
	}
	msg := fmt.Sprintf("%s hook error: [%s]: %s; blocking because onFailure is \"block\"", event, o.cmd.Command, o.failure)
	if e := strings.TrimSpace(o.stderr); e != "" {
		msg += "\n" + e
	}
	return h.capText(msg)
}

func (h *Hooks) capText(s string) string {
	n := utf8.RuneCountInString(s)
	if n <= hookTextCap {
		return s
	}
	path := "(not saved: no spill dir)"
	if h.Spill != nil {
		if p, err := h.Spill(s); err != nil {
			path = "(not saved: " + err.Error() + ")"
		} else {
			path = p
		}
	}
	preview := string([]rune(s)[:hookPreviewSize])
	return fmt.Sprintf("%s\n… hook output of %d characters cut to the first %d. Full output: %s", preview, n, hookPreviewSize, path)
}

func exitCode(err error) int {
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
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
		if r := h.failureBlock("PreToolUse", call.Tool, o); r != "" {
			return true, r, nil
		}
		if o.code == 2 {
			return true, h.capText(strings.TrimSpace(o.stderr)), nil
		}
		j := o.json()
		switch str(j, "hookSpecificOutput", "permissionDecision") {
		case "deny":
			return true, h.capText(str(j, "hookSpecificOutput", "permissionDecisionReason")), nil
		case "ask":
			return true, h.capText("a hook asked for confirmation, which qi cannot prompt for: " + str(j, "hookSpecificOutput", "permissionDecisionReason")), nil
		}
		if str(j, "decision") == "block" {
			return true, h.capText(str(j, "reason")), nil
		}
		if hso, ok := j["hookSpecificOutput"].(map[string]any); ok && hso["updatedInput"] != nil {
			updated, _ = json.Marshal(hso["updatedInput"])
		}
	}
	return false, "", updated
}

func (h *Hooks) PostToolUse(ctx context.Context, call tools.HookCall, res tools.Result) (string, *tools.ToolOutput) {
	var notes []string
	var updated *tools.ToolOutput
	payload := map[string]any{"tool_name": call.Tool, "tool_input": toolInput(call), "tool_use_id": call.ID, "tool_response": map[string]any{"output": res.Text, "is_error": res.IsError}}
	for _, o := range h.run(ctx, "PostToolUse", call.Names, payload) {
		if r := h.failureBlock("PostToolUse", call.Tool, o); r != "" {
			notes = append(notes, r)
			continue
		}
		j := o.json()
		if u := h.updatedOutput(call, o, j); u != nil {
			updated = u
		}
		switch {
		case o.code == 2:
			notes = append(notes, h.capText(strings.TrimSpace(o.stderr)))
		case str(j, "decision") == "block":
			notes = append(notes, h.capText(str(j, "reason")))
		case str(j, "hookSpecificOutput", "additionalContext") != "":
			notes = append(notes, h.capText(str(j, "hookSpecificOutput", "additionalContext")))
		}
	}
	if len(notes) == 0 {
		return "", updated
	}
	return "<hook>\n" + strings.Join(notes, "\n") + "\n</hook>", updated
}

func (h *Hooks) updatedOutput(call tools.HookCall, o hookOutcome, j map[string]any) *tools.ToolOutput {
	hso, _ := j["hookSpecificOutput"].(map[string]any)
	key := "updatedToolOutput"
	v, ok := hso[key]
	if !ok && strings.HasPrefix(call.Tool, "mcp__") {
		key = "updatedMCPToolOutput"
		v, ok = hso[key]
	}
	if !ok {
		return nil
	}
	m, _ := v.(map[string]any)
	text, isText := m["output"].(string)
	isErr, errOK := m["is_error"].(bool)
	if _, has := m["is_error"]; !isText || has && !errOK {
		if h.Info != nil {
			h.Info("hook " + strconv.Quote(o.cmd.Command) + ": " + key + " ignored: it must look like tool_response, {\"output\": string, \"is_error\": bool}")
		}
		return nil
	}
	out := &tools.ToolOutput{Output: text}
	if errOK {
		out.IsError = &isErr
	}
	return out
}

func (h *Hooks) UserPromptSubmit(ctx context.Context, prompt string) (blocked bool, reason, extra string) {
	if h.SkipPrompt {
		return false, "", ""
	}
	var ctxs []string
	for _, o := range h.run(ctx, "UserPromptSubmit", nil, map[string]any{"prompt": prompt}) {
		if r := h.failureBlock("UserPromptSubmit", "", o); r != "" {
			return true, r, ""
		}
		j := o.json()
		switch {
		case o.code == 2:
			return true, h.capText(strings.TrimSpace(o.stderr)), ""
		case str(j, "decision") == "block":
			return true, h.capText(str(j, "reason")), ""
		case j != nil:
			if c := str(j, "hookSpecificOutput", "additionalContext"); c != "" {
				ctxs = append(ctxs, h.capText(c))
			}
		case o.code == 0 && strings.TrimSpace(o.stdout) != "" && !invalidJSON(o.stdout):
			ctxs = append(ctxs, h.capText(strings.TrimSpace(o.stdout)))
		}
	}
	return false, "", strings.Join(ctxs, "\n")
}

func (h *Hooks) Stop(ctx context.Context, lastText string, active bool) (bool, string) {
	for _, o := range h.run(ctx, "Stop", nil, map[string]any{"stop_hook_active": active, "last_assistant_message": lastText}) {
		if o.code == 2 {
			return true, h.capText(strings.TrimSpace(o.stderr))
		}
		if j := o.json(); str(j, "decision") == "block" {
			return true, h.capText(str(j, "reason"))
		}
	}
	return false, ""
}
