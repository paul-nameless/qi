package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/paul-nameless/qi/internal/jsvm"
	"github.com/paul-nameless/qi/internal/llm"
	"github.com/paul-nameless/qi/internal/strutil"
)

const (
	defaultCellTimeout = 300
	maxCellTimeout     = 1800
	maxCellCalls       = 500
	maxCellSubagents   = 20
	maxCellImages      = 8
	cellParallel       = 8
	cellOutLines       = 2000
	cellOutBytes       = 60 << 10
	maxConsoleBytes    = 8 << 20
	maxRecordInput     = 2048
	maxTimerDelay      = 24 * time.Hour
	formatTimeout      = 5 * time.Second
	newVMNote          = "Note: this is a new VM; variables and functions from earlier run calls are gone."
)

type runArgs struct {
	Code    string `json:"code"`
	Timeout int    `json:"timeout"`
}

type runner struct {
	inner     *Registry
	mu        sync.Mutex
	vm        *jsvm.VM
	stringify jsvm.Value
	json      jsvm.Value
	keys      jsvm.Value
	object    jsvm.Value
	global    jsvm.Value
	note      bool
	cur       *cell
	pathMu    sync.Mutex
	paths     map[string]*sync.Mutex
}

func Run(inner *Registry) *Tool {
	r := &runner{inner: inner, paths: map[string]*sync.Mutex{}}
	return &Tool{
		Def: llm.ToolDef{
			Name:        "run",
			Description: runDescription(inner),
			Schema:      json.RawMessage(`{"type":"object","properties":{"code":{"type":"string","description":"JavaScript to run"},"timeout":{"type":"integer","description":"Seconds before the code is stopped (default 300, max 1800)"}},"required":["code"]}`),
		},
		Mutates:  true,
		Inner:    inner,
		Reset:    r.reset,
		describe: runDescription,
		Label: func(env *Env, in json.RawMessage) string {
			a, _ := decode[runArgs](in)
			return "run " + OneLine(a.Code, 70)
		},
		Run: r.run,
	}
}

func (r *runner) reset(hadCells bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.vm != nil {
		r.vm.Close()
		r.vm = nil
	}
	r.note = hadCells
}

func cellTimeout(requested int) time.Duration {
	if requested <= 0 {
		requested = defaultCellTimeout
	}
	return time.Duration(min(requested, maxCellTimeout)) * time.Second
}

func (r *runner) run(ctx context.Context, env *Env, in json.RawMessage, progress func(string)) Result {
	a, err := decode[runArgs](in)
	if err != nil {
		return errResult("%v", err)
	}
	if strings.TrimSpace(a.Code) == "" {
		return errResult("code is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.vm == nil {
		if err := r.newVM(); err != nil {
			return errResult("start the JavaScript VM: %v", err)
		}
	}
	c := newCell(ctx, r, env, progress, cellTimeout(a.Timeout))
	if r.note {
		c.note, r.note = newVMNote, false
	}
	r.cur = c
	defer func() { r.cur = nil }()
	return c.execute(a.Code)
}

func (r *runner) newVM() error {
	vm := jsvm.New(jsvm.Options{MemoryLimit: 512 << 20, MaxDepth: 2000, Stdout: r.stdout})
	for _, t := range r.inner.tools {
		if t.Code != nil {
			vm.SetFunc(t.Code.Name, r.toolFunc(t.Def.Name))
		}
	}
	vm.SetFunc("sleep", r.sleepFunc)
	vm.SetFunc("setTimeout", r.setTimeoutFunc)
	vm.SetFunc("clearTimeout", r.clearTimeoutFunc)
	if err := r.bindHelpers(vm); err != nil {
		vm.Close()
		return err
	}
	r.vm = vm
	return nil
}

func (r *runner) bindHelpers(vm *jsvm.VM) error {
	var err error
	r.json, r.object, r.global = vm.Get("JSON"), vm.Get("Object"), vm.Get("globalThis")
	if r.stringify, err = vm.GetProp(r.json, "stringify"); err != nil {
		return err
	}
	if r.keys, err = vm.GetProp(r.object, "keys"); err != nil {
		return err
	}
	console := vm.Get("console")
	log, err := vm.GetProp(console, "log")
	if err != nil {
		return err
	}
	vm.SetFunc("print", func(vm *jsvm.VM, args []jsvm.Value) (jsvm.Value, error) {
		_, err := vm.CallValue(log, console, args...)
		return jsvm.Undefined(), err
	})
	return nil
}

func (r *runner) stdout(_, text string) {
	if c := r.cur; c != nil {
		c.print(text)
	}
}

func (r *runner) lockPath(t *Tool, env *Env, input json.RawMessage) func() {
	if t.Path == nil {
		return func() {}
	}
	p := t.Path(env, input)
	if p == "" {
		return func() {}
	}
	r.pathMu.Lock()
	m := r.paths[p]
	if m == nil {
		m = &sync.Mutex{}
		r.paths[p] = m
	}
	r.pathMu.Unlock()
	m.Lock()
	return m.Unlock
}

func (r *runner) toolFunc(name string) jsvm.HostFunc {
	return func(vm *jsvm.VM, args []jsvm.Value) (jsvm.Value, error) {
		site := callSite(vm)
		c, t := r.cur, r.inner.byKey[name]
		if c == nil || t == nil || t.Code == nil {
			return jsvm.Undefined(), throwToolError(vm, name+" is not available", false, site)
		}
		if err := c.admit(t); err != nil {
			return jsvm.Undefined(), throwToolError(vm, err.Error(), false, site)
		}
		input, err := codeInput(vm, t, args)
		if err != nil {
			return jsvm.Undefined(), throwToolError(vm, err.Error(), false, site)
		}
		p, resolve, reject := vm.NewPromise()
		c.startTool(t, input, &pendingCall{kind: toolCall, resolve: resolve, reject: reject, site: site})
		return p, nil
	}
}

func (r *runner) sleepFunc(vm *jsvm.VM, args []jsvm.Value) (jsvm.Value, error) {
	c := r.cur
	if c == nil {
		return jsvm.Undefined(), throwToolError(vm, "sleep is not available", false, "")
	}
	p, resolve, _ := vm.NewPromise()
	c.startTimer(&pendingCall{kind: sleepCall, resolve: resolve}, delayArg(args, 0))
	return p, nil
}

func (r *runner) setTimeoutFunc(vm *jsvm.VM, args []jsvm.Value) (jsvm.Value, error) {
	c := r.cur
	if c == nil || len(args) == 0 || args[0].Kind() != jsvm.KindFunction {
		return jsvm.Undefined(), vm.Exception(vm.NewError("TypeError", "setTimeout needs a function as its first argument"))
	}
	var rest []jsvm.Value
	if len(args) > 2 {
		rest = args[2:]
	}
	id := c.startTimer(&pendingCall{kind: timerCall, fn: args[0], args: rest}, delayArg(args, 1))
	return jsvm.Number(float64(id)), nil
}

func (r *runner) clearTimeoutFunc(_ *jsvm.VM, args []jsvm.Value) (jsvm.Value, error) {
	if c := r.cur; c != nil && len(args) > 0 {
		c.clearTimer(int(args[0].Float()))
	}
	return jsvm.Undefined(), nil
}

func delayArg(args []jsvm.Value, i int) time.Duration {
	if i >= len(args) {
		return 0
	}
	ms := args[i].Float()
	if ms != ms || ms <= 0 {
		return 0
	}
	if ms >= float64(maxTimerDelay/time.Millisecond) {
		return maxTimerDelay
	}
	return time.Duration(ms * float64(time.Millisecond))
}

func callSite(vm *jsvm.VM) string {
	st, err := vm.GetProp(vm.NewError("Error", ""), "stack")
	if err != nil {
		return ""
	}
	var lines []string
	for _, l := range strings.Split(jsvm.ToString(st), "\n") {
		if strings.HasPrefix(l, "    at") {
			lines = append(lines, l)
		}
	}
	return strings.Join(lines, "\n")
}

func toolError(vm *jsvm.VM, msg string, denied bool, site string) jsvm.Value {
	e := vm.NewError("ToolError", msg)
	stack := "ToolError: " + msg
	if site != "" {
		stack += "\n" + site
	}
	_ = vm.SetProp(e, "denied", jsvm.Bool(denied))
	_ = vm.SetProp(e, "stack", jsvm.Str(stack))
	return e
}

func throwToolError(vm *jsvm.VM, msg string, denied bool, site string) error {
	return vm.Exception(toolError(vm, msg, denied, site))
}

func recordInput(input json.RawMessage) json.RawMessage {
	if len(input) <= maxRecordInput {
		return input
	}
	cut, err := json.Marshal(strutil.CutStart(string(input), maxRecordInput) + "…")
	if err != nil {
		return nil
	}
	return cut
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}
