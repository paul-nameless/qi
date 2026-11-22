package main

import (
	"errors"
	"fmt"
	"github.com/paul-nameless/qi/internal/jsvm/bench/harness"
	"os"

	"modernc.org/quickjs"
)

const engineName = "modernc-quickjs"

type mvm struct{ vm *quickjs.VM }

func newVM() (harness.VM, error) {
	vm, err := quickjs.NewVM()
	if err != nil {
		return nil, err
	}
	vm.SetMemoryLimit(512 << 20)
	return &mvm{vm}, nil
}

func (m *mvm) Register(name string, f func(a, b, c, d string)) error {
	return m.vm.RegisterFunc(name, func(a, b, c, d string) { f(a, b, c, d) }, false)
}

const asyncFlag = 128

func withLine(err error) error {
	var e *quickjs.Error
	if errors.As(err, &e) && e.LineNumber > 0 {
		return fmt.Errorf("%s\n    at <eval>:%d:%d", err.Error(), e.LineNumber, e.ColumnNumber)
	}
	return err
}

func (m *mvm) Eval(src string, async bool) error {
	if !async {
		_, err := m.vm.Eval(src, quickjs.EvalGlobal)
		return withLine(err)
	}
	v, err := m.vm.EvalValue(src, quickjs.EvalGlobal|asyncFlag)
	if err != nil {
		return withLine(err)
	}
	defer v.Free()
	g := m.vm.GlobalObject()
	defer g.Free()
	a, err := m.vm.NewAtom("__last")
	if err != nil {
		return err
	}
	if err := m.vm.SetPropertyValue(g, a, v.Dup()); err != nil {
		return err
	}
	_, err = m.vm.Eval("__last.then(v => __host('done', '1', __fmt(v && v.value), ''), e => __host('done', '0', __err(e), '')); undefined", quickjs.EvalGlobal)
	return err
}

func (m *mvm) Call(fn string, args ...string) error {
	a := make([]any, len(args))
	for i, x := range args {
		a[i] = x
	}
	_, err := m.vm.Call(fn, a...)
	return err
}

func (m *mvm) Drain() error {
	_, err := m.vm.ExecutePendingJobs()
	return err
}
func (m *mvm) NativeAsync() bool { return true }
func (m *mvm) Interrupt()        { m.vm.Interrupt() }
func (m *mvm) Reset()            {}
func (m *mvm) Close()            { m.vm.Close() }

func (m *mvm) Prepare(code string) (string, bool, string) {
	src := globalize(code)
	if os.Getenv("DEBUG_PREP") != "" {
		fmt.Fprintln(os.Stderr, src)
	}
	fb, _ := harness.Wrap(code, "iife")
	return src, true, fb
}
