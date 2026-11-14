package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/paul-nameless/aicode/internal/agent"
	"github.com/paul-nameless/aicode/internal/config"
	"github.com/paul-nameless/aicode/internal/tools"
	"github.com/paul-nameless/aicode/internal/ui"
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
	cfg := config.Config{NotifyCmd: "printf '%s|%s\\n' {title} {msg} >> " + out, NotifyAfter: "1s"}
	r := &repl{agent: newTestAgent(t, cfg), display: devNullPrinter(t)}
	r.notifyTurn("short", nil, 500*time.Millisecond)
	r.notifyTurn("it's done\nsecond line", nil, 2*time.Second)
	got := waitFile(t, out)
	want := "aicode · " + filepath.Base(r.agent.Cwd) + "|done in 2s: it's done second line\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestShouldNotify(t *testing.T) {
	if !shouldNotify(30*time.Second, 20*time.Second, false) || shouldNotify(30*time.Second, 20*time.Second, true) || shouldNotify(time.Second, 20*time.Second, false) {
		t.Error("shouldNotify")
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
	if notifyAfter(config.Config{NotifyAfter: "bad"}) != 20*time.Second {
		t.Error("bad notify_after should fall back to 20s")
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
