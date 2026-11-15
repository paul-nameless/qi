package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paul-nameless/aicode/internal/config"
	"github.com/paul-nameless/aicode/internal/llm"
)

func TestCopy(t *testing.T) {
	out := filepath.Join(t.TempDir(), "clip")
	r := &repl{agent: newTestAgent(t, config.Config{CopyCmd: "cat > " + out}), display: devNullPrinter(t)}
	if err := r.copyAnswer(""); err == nil {
		t.Error("nothing to copy")
	}
	r.agent.Messages = []llm.Message{
		answer("old\n```sh\nold code\n```"),
		llm.UserText("```\nuser code\n```"),
		answer("text\n```go\nfunc a() {}\n\n```\nmid\n~~~\nlast ``` inside\n~~~\nend"),
	}
	cases := []struct{ args, want string }{
		{"", "text\n```go\nfunc a() {}\n\n```\nmid\n~~~\nlast ``` inside\n~~~\nend"},
		{"code", "last ``` inside"},
	}
	for _, c := range cases {
		if err := r.copyAnswer(c.args); err != nil {
			t.Fatal(err)
		}
		if data, _ := os.ReadFile(out); string(data) != c.want {
			t.Errorf("/copy %s: %q", c.args, data)
		}
	}
	r.agent.Messages = append(r.agent.Messages, answer("no code here"))
	os.Remove(out)
	if err := r.copyAnswer("code"); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(out); string(data) != "last ``` inside" {
		t.Errorf("code from an earlier answer: %q", data)
	}
	if err := r.copyAnswer("bogus"); err == nil {
		t.Error("bad argument accepted")
	}
	r.agent.Cfg.CopyCmd = "no-such-tool-xyz"
	if err := r.copyAnswer(""); err == nil || !strings.Contains(err.Error(), "copy_cmd is off") {
		t.Errorf("missing tool: %v", err)
	}
	r.agent.Cfg.CopyCmd = "echo nope >&2; false"
	if err := r.copyAnswer(""); err == nil || err.Error() != "copy_cmd: nope" {
		t.Errorf("failing tool: %v", err)
	}
}

func TestLastFence(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"```a\nx\n```\n```\nopen", "x", true},
		{"````md\n```go\ninner\n```\n````", "```go\ninner\n```", true},
		{"no fence", "", false},
	}
	for _, c := range cases {
		if got, ok := lastFence(c.in); got != c.want || ok != c.ok {
			t.Errorf("%q: got %q, %v", c.in, got, ok)
		}
	}
}

func answer(text string) llm.Message {
	return llm.Message{Role: "assistant", Blocks: []llm.Block{{Type: llm.BlockText, Text: text}}}
}
