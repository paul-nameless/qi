package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paul-nameless/aicode/internal/config"
	"github.com/paul-nameless/aicode/internal/session"
)

func TestPager(t *testing.T) {
	t.Setenv("AICODE_SESSION_DIR", t.TempDir())
	out := filepath.Join(t.TempDir(), "paged")
	a := newTestAgent(t, config.Config{Pager: "cat > " + out})
	s := writeSession(t, a.Cwd, "hello pager")
	cur, ld, err := session.Open(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer cur.Close()
	a.Load(cur, ld)
	r := &repl{agent: a, display: devNullPrinter(t)}
	if err := r.showHistory(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(out)
	for _, want := range []string{"❯ hello pager", "── compacted ──", "    8\n"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("history misses %q: %q", want, data)
		}
	}
	if strings.Contains(string(data), "more lines") {
		t.Errorf("history cut a tool result: %q", data)
	}
	a.Cfg.Pager = "no-such-pager-xyz"
	if err := r.showHistory(); err == nil || !strings.Contains(err.Error(), "pager is off") {
		t.Errorf("missing pager: %v", err)
	}
}
