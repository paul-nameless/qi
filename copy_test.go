package main

import (
	"os"
	"path/filepath"
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
	r.copyAnswer("code")
	if data, _ := os.ReadFile(out); string(data) != "last ``` inside" {
		t.Errorf("code from an earlier answer: %q", data)
	}
	if err := r.copyAnswer("bogus"); err == nil {
		t.Error("bad argument accepted")
	}
}
