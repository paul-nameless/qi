package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paul-nameless/aicode/internal/config"
	"github.com/paul-nameless/aicode/internal/llm"
	"github.com/paul-nameless/aicode/internal/session"
)

func toolResult(text string) llm.Message {
	return llm.Message{Role: "user", Blocks: []llm.Block{{Type: llm.BlockToolResult, ToolUseID: "x", Text: text}}}
}

func TestLastSpill(t *testing.T) {
	dir := "/cache/aicode/out"
	msgs := []llm.Message{
		toolResult("head\n… 9 lines omitted. Full output: /cache/aicode/out/1.txt\ntail"),
		llm.UserText("Full output: /cache/aicode/out/text.txt"),
		toolResult("Full output: /etc/passwd\n"),
		toolResult("Full output: /cache/aicode/out/../../x.txt\n"),
	}
	if got := lastSpill(msgs, dir); got != "/cache/aicode/out/1.txt" {
		t.Errorf("got %q", got)
	}
	if got := lastSpill(msgs[1:], dir); got != "" {
		t.Errorf("paths outside the spill dir accepted: %q", got)
	}
}

func TestPager(t *testing.T) {
	t.Setenv("AICODE_SESSION_DIR", t.TempDir())
	out := filepath.Join(t.TempDir(), "paged")
	a := newTestAgent(t, config.Config{Pager: "cat > " + out})
	a.Env.SpillDir = t.TempDir()
	s := writeSession(t, a.Cwd, "hello pager")
	cur, ld, err := session.Open(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	a.Load(cur, ld)
	defer cur.Close()
	r := &repl{agent: a, display: devNullPrinter(t)}
	if err := r.showHistory(); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(out); !strings.Contains(string(data), "❯ hello pager") || !strings.Contains(string(data), "── compacted ──") {
		t.Errorf("history: %q", data)
	}
	if err := r.showOut(); err == nil {
		t.Error("expected no cut-off output")
	}
	spill := filepath.Join(a.Env.SpillDir, "1.txt")
	os.WriteFile(spill, []byte("full output"), 0o600)
	a.Messages = append(a.Messages, toolResult("x\nFull output: "+spill+"\ny"))
	a.Cfg.Pager = "cp {file} " + out
	if err := r.showOut(); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(out); string(data) != "full output" {
		t.Errorf("out: %q", data)
	}
	a.Cfg.Pager = "no-such-pager-xyz"
	if err := r.showOut(); err == nil || !strings.Contains(err.Error(), "pager is off") {
		t.Errorf("missing pager: %v", err)
	}
}
