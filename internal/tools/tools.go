package tools

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/paul-nameless/qi/internal/config"
	"github.com/paul-nameless/qi/internal/jsvm"
	"github.com/paul-nameless/qi/internal/llm"
	"github.com/paul-nameless/qi/internal/strutil"
)

type Result struct {
	Text    string
	IsError bool
	Images  []llm.Block
	Summary string
	Value   json.RawMessage
	JS      func(vm *jsvm.VM) jsvm.Value
	Denied  bool
	Calls   []llm.InnerCall
}

func errResult(format string, a ...any) Result {
	msg := fmt.Sprintf(format, a...)
	return Result{Text: msg, IsError: true, Summary: msg}
}

func deniedResult(format string, a ...any) Result {
	res := errResult(format, a...)
	res.Denied = true
	return res
}

type HookCall struct {
	ID    string
	Tool  string
	Names []string
	Input json.RawMessage
}

type Hook func(ctx context.Context, call HookCall) (block bool, reason string, input json.RawMessage)
type PostHook func(ctx context.Context, call HookCall, res Result) string

type SubagentTask struct {
	Prompt   string
	Model    string
	Provider string
	Effort   string
}

type SubagentFunc func(ctx context.Context, task SubagentTask, progress func(string)) (string, error)

type Env struct {
	Cwd         string
	ReadOnly    func() bool
	Allow       []string
	BashTimeout int
	SpillDir    string
	Subagent    SubagentFunc
	Jobs        *Jobs
	PreHook     Hook
	PostHook    PostHook
	files       fileTimes
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

type Tool struct {
	Def          llm.ToolDef
	Run          func(ctx context.Context, env *Env, input json.RawMessage, progress func(string)) Result
	Label        func(env *Env, input json.RawMessage) string
	Path         func(env *Env, input json.RawMessage) string
	Mutates      bool
	ParallelSafe bool
	HookNames    []string
	Unwrap       func(input json.RawMessage) (name string, args json.RawMessage)
	Rewrap       func(input, args json.RawMessage) json.RawMessage
	Code         *CodeSpec
	Source       func(input json.RawMessage) string
	Inner        *Registry
	Reset        func(hadCells bool)
	describe     func(inner *Registry) string
}

func (t *Tool) HookCall(call llm.Block) HookCall {
	hc := HookCall{ID: call.ID, Tool: call.Name, Names: []string{call.Name}, Input: call.Input}
	if len(t.HookNames) > 0 {
		hc.Tool = t.HookNames[0]
		hc.Names = append(hc.Names, t.HookNames...)
	}
	if t.Unwrap == nil {
		return hc
	}
	name, args := t.Unwrap(call.Input)
	if name != "" {
		hc.Tool, hc.Names = name, []string{name}
	}
	if args != nil {
		hc.Input = args
	}
	return hc
}

func (t *Tool) rewrap(input, args json.RawMessage) json.RawMessage {
	if t.Rewrap == nil {
		return args
	}
	return t.Rewrap(input, args)
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
	if r.replaceTop(t) {
		return
	}
	for _, outer := range r.tools {
		if outer.Inner == nil || !outer.Inner.replaceTop(t) {
			continue
		}
		if outer.describe != nil {
			outer.Def.Description = outer.describe(outer.Inner)
		}
	}
}

func (r *Registry) replaceTop(t *Tool) bool {
	for i, old := range r.tools {
		if old.Def.Name == t.Def.Name {
			r.tools[i] = t
			r.byKey[t.Def.Name] = t
			return true
		}
	}
	return false
}

func (r *Registry) Reset(hadCells bool) {
	for _, t := range r.tools {
		if t.Reset != nil {
			t.Reset(hadCells)
		}
	}
}

func (r *Registry) innerHint(name string) (string, bool) {
	for _, outer := range r.tools {
		if outer.Inner == nil {
			continue
		}
		if t := outer.Inner.byKey[name]; t != nil && t.Code != nil {
			return fmt.Sprintf("unknown tool %q: call await %s(...) inside %s", name, t.Code.Name, outer.Def.Name), true
		}
	}
	return "", false
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

type SourceUI interface {
	ToolStartCode(id, label, code string)
}

func startCall(ui UI, t *Tool, call llm.Block, label string) {
	if s, ok := ui.(SourceUI); ok && t != nil && t.Source != nil {
		s.ToolStartCode(call.ID, label, t.Source(call.Input))
		return
	}
	ui.ToolStart(call.ID, label)
}

func (r *Registry) Execute(ctx context.Context, env *Env, calls []llm.Block, ui UI) []llm.Block {
	results := make([]llm.Block, len(calls))
	groups := map[string][]int{}
	var order []string
	writes := false
	for _, c := range calls {
		if t := r.byKey[c.Name]; t != nil && t.Mutates {
			writes = true
		}
	}
	for i, c := range calls {
		t := r.byKey[c.Name]
		key := fmt.Sprintf("#%d", i)
		if t != nil && t.Path != nil {
			if p := t.Path(env, c.Input); p != "" {
				key = "path:" + p
			}
		}
		if writes && (t == nil || !t.ParallelSafe) {
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
	t := r.byKey[call.Name]
	label := Label(t, env, call)
	startCall(ui, t, call, label)
	res := r.invoke(withCall(ctx, call.ID, ui), env, t, call, ui)
	ui.ToolDone(call.ID, !res.IsError, DoneLine(label, res.Summary))
	return llm.Block{Type: llm.BlockToolResult, ToolUseID: call.ID, Text: res.Text, IsError: res.IsError, Images: res.Images, Calls: res.Calls}
}

func DoneLine(label, summary string) string {
	if summary == "" {
		return label
	}
	return label + " · " + OneLine(summary, 80)
}

func Label(t *Tool, env *Env, call llm.Block) (label string) {
	label = call.Name
	defer func() {
		if recover() != nil {
			label = call.Name
		}
	}()
	if t != nil && t.Label != nil {
		label = t.Label(env, call.Input)
	}
	return label
}

func (r *Registry) invoke(ctx context.Context, env *Env, t *Tool, call llm.Block, ui UI) Result {
	res, extra := r.invokeSplit(ctx, env, t, call, ui)
	if extra != "" {
		res.Text += "\n" + extra
	}
	return res
}

func (r *Registry) invokeSplit(ctx context.Context, env *Env, t *Tool, call llm.Block, ui UI) (res Result, extra string) {
	defer func() {
		if p := recover(); p != nil {
			res = errResult("tool %s panicked: %v at %s", call.Name, p, panicSite())
		}
	}()
	switch {
	case ctx.Err() != nil:
		return errResult("interrupted by user"), ""
	case t == nil:
		if hint, ok := r.innerHint(call.Name); ok {
			return errResult("%s", hint), ""
		}
		return errResult("unknown tool %q", call.Name), ""
	case llm.InvalidArgs(call.Input):
		return errResult("invalid JSON arguments: %s", string(call.Input)), ""
	}
	if env.PreHook != nil {
		block, reason, input := env.PreHook(ctx, t.HookCall(call))
		if block {
			return deniedResult("blocked by hook: %s", reason), ""
		}
		if input != nil {
			call.Input = t.rewrap(call.Input, input)
		}
	}
	res = t.Run(ctx, env, call.Input, func(s string) { ui.ToolProgress(call.ID, s) })
	if ctx.Err() != nil && !res.IsError && !inCell(ctx) {
		res.Text += "\n[interrupted by user]"
	}
	if env.PostHook != nil {
		extra = env.PostHook(ctx, t.HookCall(call), res)
	}
	return res, extra
}

func panicSite() string {
	pcs := make([]uintptr, 32)
	frames := runtime.CallersFrames(pcs[:runtime.Callers(3, pcs)])
	for {
		f, more := frames.Next()
		if !strings.HasPrefix(f.Function, "runtime.") {
			return fmt.Sprintf("%s (%s:%d)", f.Function, filepath.Base(f.File), f.Line)
		}
		if !more {
			return "unknown location"
		}
	}
}

func OneLine(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > limit {
		return string(r[:limit-1]) + "…"
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
)

func (e *Env) capOutput(out string) string {
	return e.capText(out, maxOutLines, maxOutBytes)
}

func (e *Env) capText(out string, maxLines, maxBytes int) string {
	if len(out) <= maxBytes && strings.Count(out, "\n") < maxLines {
		return out
	}
	lines := strings.Split(out, "\n")
	path, err := e.spill(out)
	if err != nil {
		path = "(not saved: " + err.Error() + ")"
	}
	half := maxBytes / 2
	headLines, tailLines := maxLines*3/8, maxLines/4
	if len(lines) <= headLines+tailLines {
		head, tail := strutil.CutStart(out, half), strutil.CutEnd(out, half)
		return head + fmt.Sprintf("\n… %d bytes omitted. Full output: %s\n", len(out)-len(head)-len(tail), path) + tail
	}
	head := strutil.CutStart(strings.Join(lines[:headLines], "\n"), half)
	tail := strutil.CutEnd(strings.Join(lines[len(lines)-tailLines:], "\n"), half)
	return head + fmt.Sprintf("\n… %d lines omitted. Full output: %s\n", len(lines)-headLines-tailLines, path) + tail
}

func (e *Env) outDir() string {
	if e.SpillDir != "" {
		return e.SpillDir
	}
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	return filepath.Join(base, "qi", "out")
}

func (e *Env) spill(out string) (string, error) {
	dir := e.outDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	path := filepath.Join(dir, time.Now().Format("20060102-150405")+"-"+hex.EncodeToString(b)+".txt")
	if err := os.WriteFile(path, []byte(out), 0o600); err != nil {
		return "", err
	}
	return path, nil
}
