package main

import (
	"os"
	"strings"
	"testing"

	"github.com/paul-nameless/aicode/internal/config"
	"github.com/paul-nameless/aicode/internal/ui"
)

func TestShowDiff(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	r := &repl{agent: newTestAgent(t, config.Config{DiffCmd: "sed 's/^/> /'"}), display: ui.NewPrinter(f)}
	r.showDiff("a\nb\n")
	r.agent.Cfg.DiffCmd = "seq 100"
	r.showDiff("x")
	r.agent.Cfg.DiffCmd = "echo bad >&2; false"
	r.showDiff("x")
	data, _ := os.ReadFile(f.Name())
	got := string(data)
	if !strings.HasPrefix(got, "> a\n> b\n1\n") || !strings.Contains(got, "40\n… 60 more lines\n") || strings.Contains(got, "\n41\n") || !strings.HasSuffix(got, "diff_cmd: bad\n") {
		t.Errorf("got %q", got)
	}
}
