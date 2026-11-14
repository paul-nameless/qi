package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/paul-nameless/aicode/internal/agent"
	"github.com/paul-nameless/aicode/internal/config"
	"github.com/paul-nameless/aicode/internal/llm"
	"github.com/paul-nameless/aicode/internal/session"
)

func writeSession(t *testing.T, cwd, prompt string) *session.Session {
	s, err := session.Create(cwd, "", "m", "interactive", false)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Message(llm.UserText(prompt), "m", nil)
	_ = s.Message(llm.Message{Role: "assistant", Blocks: []llm.Block{{Type: llm.BlockText, Text: "running"}, {Type: llm.BlockToolUse, ID: "c1", Name: "bash", Input: json.RawMessage(`{"command":"seq 20"}`)}}}, "m", nil)
	_ = s.Message(llm.Message{Role: "user", Blocks: []llm.Block{{Type: llm.BlockToolResult, ToolUseID: "c1", Text: "1\n2\n3\n4\n5\n6\n7\n8\x1b[31m"}}}, "m", nil)
	_ = s.Compaction("the summary", nil, "m")
	_ = s.Message(llm.Message{Role: "assistant", Blocks: []llm.Block{{Type: llm.BlockText, Text: "done"}}}, "m", nil)
	_ = s.Message(llm.UserText("dropped"), "m", nil)
	_ = s.Drop()
	s.Close()
	return s
}

func TestShowSession(t *testing.T) {
	t.Setenv("AICODE_SESSION_DIR", t.TempDir())
	cwd, _ := os.Getwd()
	s := writeSession(t, cwd, "first prompt")
	var out bytes.Buffer
	if err := showSession(&out, s.ID); err != nil {
		t.Fatal(err)
	}
	want := "❯ first prompt\nrunning\n· bash {\"command\":\"seq 20\"}\n  ⎿ 1\n    2\n    3\n    4\n    5\n    6\n    … 2 more lines\n\n── compacted ──\nthe summary\ndone\n"
	if out.String() != want {
		t.Errorf("got\n%s\nwant\n%s", out.String(), want)
	}
	if err := showSession(&out, "../x"); err == nil {
		t.Error("invalid id accepted")
	}
}

func TestResume(t *testing.T) {
	t.Setenv("AICODE_SESSION_DIR", t.TempDir())
	a := newTestAgent(t, config.Config{})
	first := writeSession(t, a.Cwd, "first")
	second := writeSession(t, a.Cwd, "second")
	cur, _, err := session.Open(first.Path)
	if err != nil {
		t.Fatal(err)
	}
	a.Session = cur
	r := &repl{agent: a, display: devNullPrinter(t), hooks: &agent.Hooks{}}
	r.agent.Cfg.SessionPicker = "grep second"
	items := sessionItems(a.Cwd, cur.ID)
	if len(items) != 2 || !strings.Contains(items[0].desc+items[1].desc, "(current)") {
		t.Fatalf("items %+v", items)
	}
	if err := r.resume(""); err != nil {
		t.Fatal(err)
	}
	if a.Session.ID != second.ID || r.hooks.SessionID != second.ID || len(a.Messages) != 2 || a.Messages[0].Text() != "the summary" {
		t.Errorf("not switched: %s %d", a.Session.ID, len(a.Messages))
	}
	if err := r.resume(first.ID); err != nil || a.Session.ID != first.ID {
		t.Errorf("resume by id: %v", err)
	}
	r.agent.Cfg.SessionPicker = "false"
	if err := r.resume(""); err != nil || a.Session.ID != first.ID {
		t.Errorf("cancel should keep the session: %v", err)
	}
	a.Session.Close()
}
