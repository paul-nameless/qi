package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paul-nameless/qi/internal/config"
	"github.com/paul-nameless/qi/internal/llm"
	"github.com/paul-nameless/qi/internal/session"
	"github.com/paul-nameless/qi/internal/ui"
)

func TestPager(t *testing.T) {
	t.Setenv("QI_SESSION_DIR", t.TempDir())
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

func TestRenderColor(t *testing.T) {
	msgs := []llm.Message{
		llm.UserText("hi"),
		{Role: "assistant", Blocks: []llm.Block{{Type: llm.BlockText, Text: "a **bold** word"}, {Type: llm.BlockToolUse, Name: "bash", Input: json.RawMessage(`{"command":"ls"}`)}}},
		{Role: "user", Blocks: []llm.Block{{Type: llm.BlockToolResult, Text: "one\n\x1b[31mtwo"}}},
	}
	got := renderTranscript(msgs, 0, 80)
	for _, want := range []string{ui.Cyan + "❯ " + ui.Reset + "hi\n", ui.Bold + "bold", ui.Dim + "· bash {\"command\":\"ls\"}" + ui.Reset, ui.Dim + "    two" + ui.Reset} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
	if strings.Contains(got, ui.Red) {
		t.Errorf("tool output escapes kept: %q", got)
	}
	if plain := renderTranscript(msgs, 0, 0); strings.Contains(plain, "\x1b") {
		t.Errorf("plain render has escapes: %q", plain)
	}
}
