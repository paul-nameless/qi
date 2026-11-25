package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paul-nameless/qi/internal/agent"
	"github.com/paul-nameless/qi/internal/config"
	"github.com/paul-nameless/qi/internal/llm"
	"github.com/paul-nameless/qi/internal/ui"
)

func TestCopy(t *testing.T) {
	out := filepath.Join(t.TempDir(), "clip")
	r := &repl{agent: newTestAgent(t, config.Config{CopyCmd: "cat > " + out}), display: devNullPrinter(t)}
	if err := r.copyAnswer(""); err == nil {
		t.Error("nothing to copy")
	}
	setMessages(r.agent, []llm.Message{
		answer("old\n```sh\nold code\n```"),
		llm.UserText("```\nuser code\n```"),
		answer("text\n```go\nfunc a() {}\n\n```\nmid\n~~~\nlast ``` inside\n~~~\nend"),
	})
	cases := []struct{ args, want string }{
		{"", "text\n```go\nfunc a() {}\n\n```\nmid\n~~~\nlast ``` inside\n~~~\nend"},
		{"answer", "text\n```go\nfunc a() {}\n\n```\nmid\n~~~\nlast ``` inside\n~~~\nend"},
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
	setMessages(r.agent, append(r.agent.Messages(), answer("no code here")))
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
	editConfig(r.agent, func(c *config.Config) { c.CopyCmd = "no-such-tool-xyz" })
	if err := r.copyAnswer(""); err == nil || !strings.Contains(err.Error(), "copy_cmd is off") {
		t.Errorf("missing tool: %v", err)
	}
	editConfig(r.agent, func(c *config.Config) { c.CopyCmd = "echo nope >&2; false" })
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
	return llm.Message{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockText, Text: text}}}
}

func TestCopySessionID(t *testing.T) {
	out := filepath.Join(t.TempDir(), "clip")
	r, shown := testRepl(t, config.Config{Model: "m", CopyCmd: "cat > " + out}, scripted())
	r.editor.T.Keys <- ui.Key{Kind: ui.KeyCopyID}
	r.editor.T.Keys <- ui.Key{Kind: ui.KeyEnter}
	r.editor.OnCopyID = func() { r.report(r.copyAnswer("id")) }
	if _, err := r.editor.ReadLine("❯ ", ""); err != nil {
		t.Fatal(err)
	}
	id := r.agent.Session().ID
	if data, _ := os.ReadFile(out); string(data) != id || !strings.Contains(shown.String(), "copied session id "+id) {
		t.Errorf("alt+c copied %q, printed %q", data, shown.String())
	}
}

func TestTabCompletesInsteadOfRunning(t *testing.T) {
	r, _ := testRepl(t, config.Config{Model: "m", Picker: "builtin"}, scripted())
	keys := func(s string, last ui.KeyKind) {
		for _, c := range s {
			r.editor.T.Keys <- ui.Key{Kind: ui.KeyRune, Rune: c}
		}
		r.editor.T.Keys <- ui.Key{Kind: last}
	}
	cases := []struct {
		typed  string
		last   ui.KeyKind
		text   string
		submit bool
	}{
		{"cfg", ui.KeyTab, "/config ", false},
		{"cfg", ui.KeyEnter, "/config", true},
		{"copy", ui.KeyTab, "/copy ", false},
	}
	for _, c := range cases {
		keys(c.typed, c.last)
		if text, submit := r.slashPicker(); text != c.text || submit != c.submit {
			t.Errorf("%q then %v: got %q %v", c.typed, c.last, text, submit)
		}
	}
	keys("copy", ui.KeyEnter)
	keys("id", ui.KeyTab)
	if text, submit := r.slashPicker(); text != "/copy id" || submit {
		t.Errorf("tab in the argument picker: %q %v", text, submit)
	}
	keys("copy", ui.KeyEnter)
	keys("id", ui.KeyEnter)
	if text, submit := r.slashPicker(); text != "/copy id" || !submit {
		t.Errorf("enter in the argument picker: %q %v", text, submit)
	}
	keys("c", ui.KeyBackspace)
	r.editor.T.Keys <- ui.Key{Kind: ui.KeyBackspace}
	if text, submit := r.slashPicker(); text != "" || submit {
		t.Errorf("backspace on an empty filter removes the slash: %q %v", text, submit)
	}
	keys("copy", ui.KeyEnter)
	r.editor.T.Keys <- ui.Key{Kind: ui.KeyBackspace}
	if text, submit := r.slashPicker(); text != "/copy" || submit {
		t.Errorf("backspace in the argument picker removes the space: %q %v", text, submit)
	}
	r.skills = []agent.Skill{{Name: "deploy", Path: "/x/SKILL.md"}}
	keys("skills", ui.KeyEnter)
	r.editor.T.Keys <- ui.Key{Kind: ui.KeyBackspace}
	if text, submit := r.slashPicker(); text != "/skills" || submit {
		t.Errorf("backspace in the skills picker removes the space: %q %v", text, submit)
	}
	keys("", ui.KeyEsc)
	if text, _ := r.slashPicker(); text != "/" {
		t.Errorf("esc keeps the slash: %q", text)
	}
}
