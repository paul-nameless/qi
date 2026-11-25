package tools

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
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
	raw     string
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
type PostHook func(ctx context.Context, call HookCall, res Result) (note string, updated *ToolOutput)

type ToolOutput struct {
	Output  string
	IsError *bool
}

type SubagentTask struct {
	Prompt   string
	Model    string
	Provider string
	Effort   string
}

type SubagentFunc func(ctx context.Context, task SubagentTask, progress func(string)) (string, error)

type Env struct {
	Cwd         string
	Mode        func() config.Mode
	Scratch     func() string
	Writable    []string
	Allow       []string
	BashTimeout int
	SpillDir    string
	Subagent    SubagentFunc
	Jobs        *Jobs
	PreHook     Hook
	PostHook    PostHook
	files       fileTimes
	gitOnce     sync.Once
	git         []string
	dotGit      string
}

func (e *Env) mode() config.Mode {
	if e.Mode == nil {
		return config.ModeYolo
	}
	return e.Mode()
}

func (e *Env) readOnly() bool { return e.mode() == config.ModeReadOnly }

func (e *Env) project() bool { return e.mode() == config.ModeProject }

func (e *Env) resolve(p string) string {
	if strings.HasPrefix(p, EmbeddedPrefix) {
		return p
	}
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

type Function struct {
	Name      string
	Code      CodeSpec
	Run       func(ctx context.Context, env *Env, input json.RawMessage, progress func(string)) Result
	Label     func(env *Env, input json.RawMessage) string
	Path      func(env *Env, input json.RawMessage) string
	HookNames []string
	Unwrap    func(input json.RawMessage) (name string, args json.RawMessage)
	Rewrap    func(input, args json.RawMessage) json.RawMessage
}

func (f *Function) HookCall(call llm.Block) HookCall {
	hc := HookCall{ID: call.ID, Tool: call.Name, Names: []string{call.Name}, Input: call.Input}
	if len(f.HookNames) > 0 {
		hc.Tool = f.HookNames[0]
		hc.Names = append(hc.Names, f.HookNames...)
	}
	if f.Unwrap == nil {
		return hc
	}
	name, args := f.Unwrap(call.Input)
	if name != "" {
		hc.Tool, hc.Names = name, []string{name}
	}
	if args != nil {
		hc.Input = args
	}
	return hc
}

func (f *Function) rewrap(input, args json.RawMessage) json.RawMessage {
	if f.Rewrap == nil {
		return args
	}
	return f.Rewrap(input, args)
}

func (f *Function) label(env *Env, call llm.Block) (label string) {
	label = call.Name
	defer func() {
		if recover() != nil {
			label = call.Name
		}
	}()
	if f.Label != nil {
		label = f.Label(env, call.Input)
	}
	return label
}

type UI interface {
	ToolStart(id, label string)
	ToolProgress(id, progress string)
	ToolDone(id string, ok bool, line string)
}

type SourceUI interface {
	ToolStartCode(id, label, code string)
}

func (t *Tool) Execute(ctx context.Context, env *Env, calls []llm.Block, ui UI) []llm.Block {
	results := make([]llm.Block, len(calls))
	for i, c := range calls {
		results[i] = t.runOne(ctx, env, c, ui)
	}
	return results
}

func (t *Tool) runOne(ctx context.Context, env *Env, call llm.Block, ui UI) llm.Block {
	label := t.Label(env, call)
	if s, ok := ui.(SourceUI); ok && call.Name == t.Def.Name {
		a, _ := decode[runArgs](call.Input)
		s.ToolStartCode(call.ID, label, a.Code)
	} else {
		ui.ToolStart(call.ID, label)
	}
	res := t.call(withCall(ctx, call.ID, ui), env, call, ui)
	ui.ToolDone(call.ID, !res.IsError, DoneLine(label, res.Summary))
	return llm.Block{Type: llm.BlockToolResult, ToolUseID: call.ID, Text: res.Text, IsError: res.IsError, Images: res.Images, Calls: res.Calls}
}

func (t *Tool) call(ctx context.Context, env *Env, call llm.Block, ui UI) (res Result) {
	defer func() {
		if p := recover(); p != nil {
			res = errResult("tool %s panicked: %v at %s", call.Name, p, panicSite())
		}
	}()
	switch {
	case ctx.Err() != nil:
		return errResult("interrupted by user")
	case call.Name != t.Def.Name:
		if f := t.r.funcs[call.Name]; f != nil {
			return errResult("unknown tool %q: call await %s(...) inside %s", call.Name, f.Code.Name, t.Def.Name)
		}
		return errResult("unknown tool %q", call.Name)
	case llm.InvalidArgs(call.Input):
		return errResult("invalid JSON arguments: %s", string(call.Input))
	}
	return t.r.run(ctx, env, call.Input, func(s string) { ui.ToolProgress(call.ID, s) })
}

func (t *Tool) Label(env *Env, call llm.Block) string {
	if call.Name == t.Def.Name {
		a, _ := decode[runArgs](call.Input)
		return "run " + OneLine(a.Code, 70)
	}
	if f := t.r.funcs[call.Name]; f != nil {
		return f.label(env, call)
	}
	return call.Name
}

func DoneLine(label, summary string) string {
	if summary == "" {
		return label
	}
	return label + " · " + OneLine(summary, 80)
}

func callFunction(ctx context.Context, env *Env, f *Function, call llm.Block, ui UI) (res Result, extra string) {
	defer func() {
		if p := recover(); p != nil {
			res = errResult("tool %s panicked: %v at %s", call.Name, p, panicSite())
		}
	}()
	if ctx.Err() != nil {
		return errResult("interrupted by user"), ""
	}
	if env.PreHook != nil {
		block, reason, input := env.PreHook(ctx, f.HookCall(call))
		if block {
			return deniedResult("blocked by hook: %s", reason), ""
		}
		if input != nil {
			call.Input = f.rewrap(call.Input, input)
		}
	}
	res = f.Run(ctx, env, call.Input, func(s string) { ui.ToolProgress(call.ID, s) })
	if env.PostHook != nil {
		var updated *ToolOutput
		extra, updated = env.PostHook(ctx, f.HookCall(call), res)
		if updated != nil {
			res = replaceOutput(res, *updated)
		}
	}
	return res, extra
}

func replaceOutput(res Result, u ToolOutput) Result {
	raw := rawOutput(res, u.Output)
	res.Text = u.Output
	if u.IsError != nil {
		res.IsError = *u.IsError
	}
	if res.Value != nil {
		res.Value = jsonValue(raw)
	}
	if js := res.JS; js != nil {
		res.JS = func(vm *jsvm.VM) jsvm.Value {
			v := js(vm)
			_ = vm.SetProp(v, "output", jsvm.Str(raw))
			return v
		}
	}
	return res
}

func rawOutput(res Result, text string) string {
	if res.JS == nil || !strings.HasPrefix(res.Text, res.raw) {
		return text
	}
	return strings.TrimSuffix(text, res.Text[len(res.raw):])
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
	path, err := e.Spill(out)
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

func (e *Env) outDir() (string, error) {
	if e.SpillDir != "" {
		return e.SpillDir, os.MkdirAll(e.SpillDir, 0o700)
	}
	s := e.scratch()
	if s == "" {
		return "", fmt.Errorf("no private scratch dir under %s", os.TempDir())
	}
	dir := filepath.Join(s, ".qi-out")
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return "", err
	}
	if !PrivateDir(dir) {
		return "", fmt.Errorf("%s is not a private directory owned by you", dir)
	}
	return dir, nil
}

func PrivateDir(path string) bool {
	st, err := os.Lstat(path)
	if err != nil || !st.IsDir() || st.Mode().Perm() != 0o700 {
		return false
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	return ok && int(sys.Uid) == os.Getuid()
}

func (e *Env) Spill(out string) (string, error) {
	dir, err := e.outDir()
	if err != nil {
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
