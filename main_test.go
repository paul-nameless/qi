package main

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/paul-nameless/aicode/internal/agent"
	"github.com/paul-nameless/aicode/internal/config"
	"github.com/paul-nameless/aicode/internal/llm"
	"github.com/paul-nameless/aicode/internal/ui"
)

type nopProvider struct{}

func (nopProvider) Stream(context.Context, llm.Request, func(llm.Delta)) (llm.Message, llm.Usage, error) {
	return llm.Message{}, llm.Usage{}, nil
}

func devNullPrinter(t *testing.T) *ui.Printer {
	f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return ui.NewPrinter(f)
}

func TestReadOnlyWiring(t *testing.T) {
	display := devNullPrinter(t)
	a, runner, _ := buildAgent(config.Config{Model: "m"}, options{}, t.TempDir(), nopProvider{}, display, true, false)
	if a.Env.ReadOnly() || runner.ReadOnly() {
		t.Fatal("read-only on by default")
	}
	a.SetReadOnly(true)
	if !a.Env.ReadOnly() || !runner.ReadOnly() {
		t.Error("env or subagent runner does not follow the agent's read-only mode")
	}
	a2, _, _ := buildAgent(config.Config{Model: "m"}, options{readOnly: true}, t.TempDir(), nopProvider{}, display, true, false)
	if !a2.Env.ReadOnly() {
		t.Error("-read-only flag not applied")
	}
}

func TestToggleModeStopsSubagents(t *testing.T) {
	display := devNullPrinter(t)
	a, runner, _ := buildAgent(config.Config{Model: "m"}, options{}, t.TempDir(), nopProvider{}, display, true, false)
	r := &repl{agent: a, display: display, runner: runner}
	ctx, done := runner.Track(context.Background())
	defer done()
	r.toggleMode()
	if !a.ReadOnly() || !errors.Is(context.Cause(ctx), agent.ErrModeChanged) {
		t.Error("switching to read-only did not stop running subagents")
	}
}
