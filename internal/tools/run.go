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
	formatSeconds      = 5
	codeFinished       = "the code has finished"
	newVMNote          = "Note: this is a new VM; variables and functions from earlier run calls are gone."
)

type runArgs struct {
	Code    string `json:"code"`
	Timeout int    `json:"timeout"`
}

type Tool struct {
	Def llm.ToolDef
	r   *runner
}

type runner struct {
	order     []*Function
	funcs     map[string]*Function
	mu        sync.Mutex
	vm        *jsvm.VM
	stringify jsvm.Value
	json      jsvm.Value
	keys      jsvm.Value
	object    jsvm.Value
	global    jsvm.Value
	console   jsvm.Value
	log       jsvm.Value
	note      bool
	cur       *cell
	second    time.Duration
}

func Run(funcs ...*Function) *Tool {
	r := &runner{funcs: map[string]*Function{}}
	for _, f := range funcs {
		if f != nil {
			r.order = append(r.order, f)
			r.funcs[f.Name] = f
		}
	}
	return &Tool{
		Def: llm.ToolDef{
			Name:        "run",
			Description: runDescription(r.order),
			Schema:      json.RawMessage(fmt.Sprintf(`{"type":"object","properties":{"code":{"type":"string","description":"JavaScript to run"},"timeout":{"type":"integer","description":"Seconds before the code is stopped (default %d, max %d)"}},"required":["code"]}`, defaultCellTimeout, maxCellTimeout)),
		},
		r: r,
	}
}

func (t *Tool) Replace(f *Function) {
	for i, old := range t.r.order {
		if old.Name == f.Name {
			t.r.order[i] = f
			t.r.funcs[f.Name] = f
			t.Def.Description = runDescription(t.r.order)
			return
		}
	}
}

func (t *Tool) Reset(hadCells bool) {
	r := t.r
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.vm != nil {
		r.vm.Close()
		r.vm = nil
	}
	r.note = hadCells
}

func (r *runner) unit() time.Duration {
	if r.second > 0 {
		return r.second
	}
	return time.Second
}

func (r *runner) cellTimeout(requested int) time.Duration {
	if requested <= 0 {
		requested = defaultCellTimeout
	}
	return time.Duration(min(requested, maxCellTimeout)) * r.unit()
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
	r.bindFuncs()
	c := newCell(ctx, r, env, progress, r.cellTimeout(a.Timeout))
	if r.note {
		c.note, r.note = newVMNote, false
	}
	r.cur = c
	defer func() { r.cur = nil }()
	return c.execute(a.Code)
}

func (r *runner) newVM() error {
	vm := jsvm.New(jsvm.Options{MemoryLimit: 512 << 20, MaxDepth: 2000, Stdout: r.stdout})
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
	r.console = vm.Get("console")
	r.log, err = vm.GetProp(r.console, "log")
	return err
}

func (r *runner) bindFuncs() {
	for _, f := range r.order {
		r.vm.SetFunc(f.Code.Name, r.toolFunc(f.Name))
	}
	r.vm.SetFunc("sleep", r.sleepFunc)
	r.vm.SetFunc("print", r.printFunc)
}

func (r *runner) printFunc(vm *jsvm.VM, args []jsvm.Value) (jsvm.Value, error) {
	_, err := vm.CallValue(r.log, r.console, args...)
	return jsvm.Undefined(), err
}

func (r *runner) stdout(_, text string) {
	if c := r.cur; c != nil {
		c.print(text)
	}
}

func (r *runner) toolFunc(name string) jsvm.HostFunc {
	return func(vm *jsvm.VM, args []jsvm.Value) (jsvm.Value, error) {
		site := callSite(vm)
		c, t := r.cur, r.funcs[name]
		if c == nil || t == nil {
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
	if c.settled {
		return jsvm.Undefined(), vm.Exception(vm.NewError("Error", "sleep: "+codeFinished))
	}
	p, resolve, _ := vm.NewPromise()
	c.startTimer(&pendingCall{kind: sleepCall, resolve: resolve}, delayArg(args))
	return p, nil
}

func delayArg(args []jsvm.Value) time.Duration {
	if len(args) == 0 {
		return 0
	}
	ms := args[0].Float()
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
	for l := range strings.SplitSeq(jsvm.ToString(st), "\n") {
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
