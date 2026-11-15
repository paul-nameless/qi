package main

import (
	"context"
	"os"
	"testing"

	"github.com/paul-nameless/aicode/internal/config"
	"github.com/paul-nameless/aicode/internal/llm"
	"github.com/paul-nameless/aicode/internal/tools"
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
	a3, _, _ := buildAgent(config.Config{Model: "m", ReadOnly: true}, options{}, t.TempDir(), nopProvider{}, display, true, false)
	if !a3.Env.ReadOnly() {
		t.Error("read_only: true in config not applied")
	}
}

func TestModelSwitchKeepsSubagentsOnProfileProvider(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AICODE_CONFIG_DIR", dir)
	path := dir + "/config.yml"
	os.WriteFile(path, []byte(`providers:
  or:
    base_url: https://openrouter.ai/api/v1
    api_key: k1
  anth:
    api: anthropic
    api_key: k2
default_profile: p
profiles:
  p:
    provider: or
    model: openai/gpt-6-luna
    subagent_models:
      - {name: luna, model: openai/gpt-6-luna, use_for: x}
      - {name: haiku, provider: anth, model: claude-haiku-5-5, use_for: y}
  q:
    provider: anth
    model: claude-haiku-5-5
`), 0o600)
	o := options{configPath: path, provider: "anth"}
	cfg, err := config.Resolve(path, "", o.provider, "", "")
	if err != nil {
		t.Fatal(err)
	}
	a, runner, _ := buildAgent(cfg, o, dir, nopProvider{}, devNullPrinter(t), true, false)
	a.Registry = tools.NewRegistry()
	r := &repl{agent: a, display: devNullPrinter(t), runner: runner, opts: o}
	if err := r.switchProfile("p"); err != nil {
		t.Fatal(err)
	}
	if err := r.switchModel("haiku"); err != nil {
		t.Fatal(err)
	}
	if a.Cfg.Provider != "anth" || runner.Provider != "or" {
		t.Errorf("after /model haiku: agent provider %q, subagent default %q", a.Cfg.Provider, runner.Provider)
	}
	if err := r.switchModel("some-raw-model"); err != nil {
		t.Fatal(err)
	}
	if a.Cfg.Provider != "or" {
		t.Errorf("CLI -provider came back after /profile: %q", a.Cfg.Provider)
	}
}

func TestSubagentModeIsFixedAtLaunch(t *testing.T) {
	display := devNullPrinter(t)
	a, runner, _ := buildAgent(config.Config{Model: "m"}, options{}, t.TempDir(), nopProvider{}, display, true, false)
	r := &repl{agent: a, display: display, runner: runner}
	r.toggleMode()
	if !runner.ReadOnly() {
		t.Error("subagents launched after switching to read-only would not get -read-only")
	}
}
