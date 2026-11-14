package tools

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/paul-nameless/aicode/internal/config"
	"github.com/paul-nameless/aicode/internal/llm"
)

type Result struct {
	Text    string
	IsError bool
	Images  []llm.Block
	Summary string
	Diff    string
}

func errResult(format string, a ...any) Result {
	msg := fmt.Sprintf(format, a...)
	return Result{Text: msg, IsError: true, Summary: msg}
}

type Hook func(ctx context.Context, call llm.Block) (block bool, reason string, input json.RawMessage)
type PostHook func(ctx context.Context, call llm.Block, res Result) string

type Env struct {
	Cwd         string
	ReadOnly    func() bool
	Allow       []string
	BashTimeout int
	SpillDir    string
	Subagent    func(ctx context.Context, prompt, model, provider, effort string, progress func(string)) (string, error)
	PreHook     Hook
	PostHook    PostHook
	OnDiff      func(diff string)
	files       fileTimes
}

func (e *Env) diff(path, old, new string) string {
	if e.OnDiff == nil {
		return ""
	}
	rel := e.rel(path)
	return "--- a/" + rel + "\n+++ b/" + rel + "\n" + unifiedDiff(old, new)
}

func (e *Env) readOnly() bool {
	return e.ReadOnly != nil && e.ReadOnly()
}

func (e *Env) resolve(p string) string {
	p = config.ExpandHome(p)
	if !filepath.IsAbs(p) {
		p = filepath.Join(e.Cwd, p)
	}
	return filepath.Clean(p)
}

func (e *Env) rel(p string) string {
	if r, err := filepath.Rel(e.Cwd, p); err == nil && !strings.HasPrefix(r, "..") {
		return r
	}
	return p
}

func rewrapInput(call llm.Block, input json.RawMessage) json.RawMessage {
	if call.Name != "mcp_call" {
		return input
	}
	var orig map[string]json.RawMessage
	if json.Unmarshal(call.Input, &orig) != nil {
		return input
	}
	orig["arguments"] = input
	out, err := json.Marshal(orig)
	if err != nil {
		return input
	}
	return out
}

type Tool struct {
	Def   llm.ToolDef
	Run   func(ctx context.Context, env *Env, input json.RawMessage, progress func(string)) Result
	Label func(env *Env, input json.RawMessage) string
	Path  func(env *Env, input json.RawMessage) string
}

type Registry struct {
	tools []*Tool
	byKey map[string]*Tool
}

func NewRegistry(ts ...*Tool) *Registry {
	r := &Registry{byKey: map[string]*Tool{}}
	for _, t := range ts {
		if t == nil {
			continue
		}
		r.tools = append(r.tools, t)
		r.byKey[t.Def.Name] = t
	}
	return r
}

func (r *Registry) Replace(t *Tool) {
	for i, old := range r.tools {
		if old.Def.Name == t.Def.Name {
			r.tools[i] = t
			r.byKey[t.Def.Name] = t
			return
		}
	}
}

func (r *Registry) Defs() []llm.ToolDef {
	defs := make([]llm.ToolDef, 0, len(r.tools))
	for _, t := range r.tools {
		defs = append(defs, t.Def)
	}
	return defs
}

type UI interface {
	ToolStart(id, label string)
	ToolProgress(id, progress string)
	ToolDone(id string, ok bool, line string)
}

func (r *Registry) Execute(ctx context.Context, env *Env, calls []llm.Block, ui UI) []llm.Block {
	results := make([]llm.Block, len(calls))
	groups := map[string][]int{}
	var order []string
	writes := false
	for _, c := range calls {
		if c.Name == "write" || c.Name == "edit" {
			writes = true
		}
	}
	for i, c := range calls {
		key := fmt.Sprintf("#%d", i)
		if t, ok := r.byKey[c.Name]; ok && t.Path != nil {
			if p := t.Path(env, c.Input); p != "" {
				key = "path:" + p
			}
		}
		if writes && c.Name != "read" && c.Name != "tool_search" && c.Name != "subagent" {
			key = "serial"
		}
		if _, seen := groups[key]; !seen {
			order = append(order, key)
		}
		groups[key] = append(groups[key], i)
	}
	var wg sync.WaitGroup
	for _, key := range order {
		idxs := groups[key]
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, i := range idxs {
				results[i] = r.runOne(ctx, env, calls[i], ui)
			}
		}()
	}
	wg.Wait()
	return results
}

func (r *Registry) runOne(ctx context.Context, env *Env, call llm.Block, ui UI) llm.Block {
	t, ok := r.byKey[call.Name]
	label := call.Name
	if ok && t.Label != nil {
		label = t.Label(env, call.Input)
	}
	ui.ToolStart(call.ID, label)
	var res Result
	switch {
	case ctx.Err() != nil:
		res = errResult("interrupted by user")
	case !ok:
		res = errResult("unknown tool %q", call.Name)
	case llm.InvalidArgs(call.Input):
		res = errResult("invalid JSON arguments: %s", string(call.Input))
	default:
		if env.PreHook != nil {
			block, reason, input := env.PreHook(ctx, call)
			if block {
				res = errResult("blocked by hook: %s", reason)
				break
			}
			if input != nil {
				call.Input = rewrapInput(call, input)
			}
		}
		res = t.Run(ctx, env, call.Input, func(s string) { ui.ToolProgress(call.ID, s) })
		if ctx.Err() != nil && !res.IsError {
			res.Text += "\n[interrupted by user]"
		}
		if env.PostHook != nil {
			if extra := env.PostHook(ctx, call, res); extra != "" {
				res.Text += "\n" + extra
			}
		}
	}
	line := label
	if res.Summary != "" {
		line += " · " + OneLine(res.Summary, 80)
	}
	ui.ToolDone(call.ID, !res.IsError, line)
	if res.Diff != "" && env.OnDiff != nil {
		env.OnDiff(res.Diff)
	}
	return llm.Block{Type: llm.BlockToolResult, ToolUseID: call.ID, Text: res.Text, IsError: res.IsError, Images: res.Images}
}

func OneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return s
}

func decode[T any](raw json.RawMessage) (T, error) {
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		return v, fmt.Errorf("bad arguments: %w", err)
	}
	return v, nil
}

const (
	maxOutBytes = 30000
	maxOutLines = 400
	headLines   = 150
	tailLines   = 100
)

func (e *Env) capOutput(out string) string {
	lines := strings.Split(out, "\n")
	if len(out) <= maxOutBytes && len(lines) <= maxOutLines {
		return out
	}
	path := e.spill(out)
	half := maxOutBytes / 2
	if len(lines) <= headLines+tailLines {
		head, tail := cutStart(out, half), cutEnd(out, half)
		return head + fmt.Sprintf("\n… %d bytes omitted. Full output: %s\n", len(out)-len(head)-len(tail), path) + tail
	}
	head := cutStart(strings.Join(lines[:headLines], "\n"), half)
	tail := cutEnd(strings.Join(lines[len(lines)-tailLines:], "\n"), half)
	return head + fmt.Sprintf("\n… %d lines omitted. Full output: %s\n", len(lines)-headLines-tailLines, path) + tail
}

func cutStart(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func cutEnd(s string, n int) string {
	if len(s) <= n {
		return s
	}
	i := len(s) - n
	for i < len(s) && !utf8.RuneStart(s[i]) {
		i++
	}
	return s[i:]
}

func (e *Env) SpillPath() string {
	if e.SpillDir != "" {
		return e.SpillDir
	}
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	return filepath.Join(base, "aicode", "out")
}

func (e *Env) spill(out string) string {
	dir := e.SpillPath()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "(not saved: " + err.Error() + ")"
	}
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	path := filepath.Join(dir, time.Now().Format("20060102-150405")+"-"+hex.EncodeToString(b)+".txt")
	if err := os.WriteFile(path, []byte(out), 0o600); err != nil {
		return "(not saved: " + err.Error() + ")"
	}
	return path
}
