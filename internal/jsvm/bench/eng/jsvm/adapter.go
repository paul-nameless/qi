package main

import (
	"errors"
	"strings"

	"github.com/paul-nameless/qi/internal/jsvm"
	"github.com/paul-nameless/qi/internal/jsvm/bench/harness"
)

const engineName = "jsvm"

type jvm struct {
	vm    *jsvm.VM
	evals int
}

func newVM() (harness.VM, error) {
	return &jvm{vm: jsvm.New(jsvm.Options{MemoryLimit: 512 << 20, MaxDepth: 2000})}, nil
}

func arg(args []jsvm.Value, i int) string {
	if i < len(args) {
		return jsvm.ToString(args[i])
	}
	return ""
}

func (j *jvm) Register(name string, f func(a, b, c, d string)) error {
	j.vm.SetFunc(name, func(_ *jsvm.VM, args []jsvm.Value) (jsvm.Value, error) {
		f(arg(args, 0), arg(args, 1), arg(args, 2), arg(args, 3))
		return jsvm.Undefined(), nil
	})
	return nil
}

func (j *jvm) file() string {
	j.evals++
	if j.evals == 1 {
		return "prelude.js"
	}
	return "cell.js"
}

func (j *jvm) errText(err error) error {
	var ex *jsvm.Exception
	if err == nil || !errors.As(err, &ex) {
		return err
	}
	if ex.Name == "" {
		return errors.New("Thrown: " + strings.TrimPrefix(ex.Message, "Uncaught "))
	}
	if ex.Name == "SyntaxError" && strings.HasPrefix(ex.Stack, "SyntaxError: "+ex.Message+"\n    at cell.js:") {
		return errors.New(ex.Error())
	}
	s, ferr := j.vm.Call("__err", ex.Value)
	if ferr != nil {
		return errors.New(ex.Error())
	}
	return errors.New(jsvm.ToString(s))
}

func (j *jvm) Eval(src string, async bool) error {
	res, err := j.vm.RunCell(j.file(), src)
	if err != nil {
		return j.errText(err)
	}
	if !async {
		return nil
	}
	j.vm.Set("__last", res)
	_, err = j.vm.RunCell("glue.js", "Promise.resolve(__last).then(v => __host('done', '1', __fmt(v), ''), e => __host('done', '0', __err(e), ''))")
	return j.errText(err)
}

func (j *jvm) Call(fn string, args ...string) error {
	vals := make([]jsvm.Value, len(args))
	for i, a := range args {
		vals[i] = jsvm.Str(a)
	}
	_, err := j.vm.Call(fn, vals...)
	return j.errText(err)
}

func (j *jvm) Drain() error      { return j.errText(j.vm.RunJobs()) }
func (j *jvm) NativeAsync() bool { return true }
func (j *jvm) Interrupt()        { j.vm.Interrupt() }
func (j *jvm) Reset()            { j.vm.ClearInterrupt() }
func (j *jvm) Close()            { j.vm.Close() }

func (j *jvm) Prepare(code string) (string, bool, string) { return code, true, "" }
