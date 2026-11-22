package main

import (
	"errors"
	"github.com/paul-nameless/qi/internal/jsvm/bench/harness"

	"github.com/dop251/goja"
)

const engineName = "goja"

type gvm struct{ rt *goja.Runtime }

func newVM() (harness.VM, error) {
	rt := goja.New()
	rt.SetMaxCallStackSize(2000)
	return &gvm{rt}, nil
}

func (g *gvm) Register(name string, f func(a, b, c, d string)) error {
	return g.rt.Set(name, func(a, b, c, d string) { f(a, b, c, d) })
}

func (g *gvm) Eval(src string, async bool) error {
	if async {
		return errors.New("no top-level await in scripts")
	}
	_, err := g.rt.RunScript("cell.js", src)
	return err
}

func (g *gvm) Call(fn string, args ...string) error {
	f, ok := goja.AssertFunction(g.rt.Get(fn))
	if !ok {
		return errors.New(fn + " is not a function")
	}
	vals := make([]goja.Value, len(args))
	for i, a := range args {
		vals[i] = g.rt.ToValue(a)
	}
	_, err := f(goja.Undefined(), vals...)
	return err
}

func (g *gvm) Drain() error      { return nil }
func (g *gvm) NativeAsync() bool { return false }
func (g *gvm) Interrupt()        { g.rt.Interrupt("interrupted") }
func (g *gvm) Reset()            { g.rt.ClearInterrupt() }
func (g *gvm) Close()            {}
