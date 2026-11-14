package session

import (
	"os"
	"testing"
	"time"

	"github.com/paul-nameless/aicode/internal/llm"
)

func answered(t *testing.T, cwd, kind string, prompts ...string) *Session {
	s, err := Create(cwd, "", "m", kind, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range prompts {
		_ = s.Message(llm.UserText(p), "m", nil)
		_ = s.Message(llm.Message{Role: "assistant", Blocks: []llm.Block{{Type: llm.BlockText, Text: "a"}}}, "m", nil)
	}
	s.Close()
	return s
}

func TestList(t *testing.T) {
	t.Setenv("AICODE_SESSION_DIR", t.TempDir())
	cwd := "/tmp/proj"
	old := answered(t, cwd, "interactive", "<hook-context>x</hook-context>", "first real", "second")
	newer := answered(t, cwd, "interactive", "newest")
	answered(t, cwd, "headless", "headless one")
	answered(t, cwd, "interactive")
	past := time.Now().Add(-time.Hour)
	os.Chtimes(old.Path, past, past)

	got := List(cwd, true)
	if len(got) != 2 || got[0].ID != newer.ID || got[1].ID != old.ID {
		t.Fatalf("got %+v", got)
	}
	if got[1].First != "first real" || got[1].Messages != 6 || got[0].Messages != 2 {
		t.Errorf("summary %+v", got[1])
	}
	if len(List(cwd, false)) != 3 {
		t.Error("non-interactive listing should include headless sessions")
	}
}

func TestLoadDoesNotWrite(t *testing.T) {
	t.Setenv("AICODE_SESSION_DIR", t.TempDir())
	s := answered(t, "/tmp/proj", "interactive", "hi")
	f, _ := os.OpenFile(s.Path, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString(`{"type":"message"`)
	f.Close()
	before, _ := os.ReadFile(s.Path)
	ld, err := Load(s.Path)
	after, _ := os.ReadFile(s.Path)
	if err != nil || len(ld.Messages) != 2 || string(before) != string(after) {
		t.Errorf("Load changed the file or failed: %v %d", err, len(ld.Messages))
	}
}
