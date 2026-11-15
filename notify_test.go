package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/paul-nameless/qi/internal/agent"
	"github.com/paul-nameless/qi/internal/config"
	"github.com/paul-nameless/qi/internal/tools"
	"github.com/paul-nameless/qi/internal/ui"
)

func waitFile(t *testing.T, path string) string {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
			return string(data)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return ""
}

func TestNotifyTurn(t *testing.T) {
	out := filepath.Join(t.TempDir(), "n")
	cfg := config.Config{NotifyCmd: "printf '%s|%s\\n' {title} {msg} >> " + out}
	r := &repl{agent: newTestAgent(t, cfg), display: devNullPrinter(t)}
	r.notifyTurn("no terminal", nil, time.Minute)
	notifyAsync(cfg, r.agent.Cwd, turnMessage("it's done\nsecond line", nil, 2*time.Second), r.display)
	got := waitFile(t, out)
	want := "qi · " + filepath.Base(r.agent.Cwd) + "|done in 2s: it's done second line\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestNotifyErrors(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	display := ui.NewPrinter(f)
	notifyAsync(config.Config{NotifyCmd: "echo nope >&2; false"}, "/x", "m", display)
	if got := waitFile(t, f.Name()); got != "notify_cmd: nope\n" {
		t.Errorf("got %q", got)
	}
	out := filepath.Join(t.TempDir(), "n")
	notifyAsync(config.Config{NotifyCmd: "off", Picker: "touch " + out}, "/x", "m", display)
	notifyAsync(config.Config{NotifyCmd: "no-such-notifier-xyz " + out}, "/x", "m", display)
	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(out); err == nil {
		t.Error("disabled notifier ran")
	}
}

func TestTurnMessage(t *testing.T) {
	if got := turnMessage("", errors.New("boom"), 61*time.Second); got != "failed after 1m1s: boom" {
		t.Error(got)
	}
	if got := turnMessage(" ", nil, 3*time.Second); got != "done in 3s" {
		t.Error(got)
	}
	if got := turnMessage(strings.Repeat("x", 300), nil, time.Second); len([]rune(got)) > 140 {
		t.Errorf("not shortened: %d", len(got))
	}
}

func newTestAgent(t *testing.T, cfg config.Config) *agent.Agent {
	if cfg.Picker == "" {
		cfg.Picker = "cat"
	}
	a, _, toolset := buildAgent(cfg, options{}, t.TempDir(), nopProvider{}, devNullPrinter(t), true, true)
	a.Registry = tools.NewRegistry(toolset...)
	return a
}
