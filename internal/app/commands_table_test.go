package app

import (
	"testing"
)

const wantHelp = `Commands:
  /help              this help
  /keys              key bindings
  /config            config file and external tools
  /clear             start a new session
  /context           show what takes space in the context
  /reload            re-read skills, commands and CLAUDE.md files (rebuilds the cached prompt)
  /compact [focus]   summarize the conversation to free context (optional focus for the summary)
  /resume [id]       switch to another session of this directory (picker without id)
  /history           open the whole conversation in the pager
  /copy [code]       copy the last answer (or its last code block) to the clipboard
  /model [name]      show or switch the model (switching loses the prompt cache)
  /profile [name]    show or switch the profile
  /effort [level]    show or switch the reasoning effort (default, none, low, medium, high, xhigh, max)
  /skills            list skills (/<skill-name> [args] runs one)
  /mcp               start MCP servers and show their status and enabled tools
  /exit              quit`

var wantPicker = []pickItem{
	{"help", "show commands"},
	{"keys", "show key bindings"},
	{"config", "show the config file and external tools"},
	{"clear", "start a new session"},
	{"context", "show what takes space in the context"},
	{"reload", "re-read skills, commands and CLAUDE.md files"},
	{"compact", "summarize the conversation to free context"},
	{"resume", "switch to another session"},
	{"history", "open the conversation in the pager"},
	{"copy", "copy the last answer to the clipboard"},
	{"model", "switch the model"},
	{"profile", "switch the profile"},
	{"effort", "switch the reasoning effort"},
	{"skills", "pick a skill"},
	{"mcp", "show MCP servers and their enabled tools"},
	{"exit", "quit"},
}

func TestCommandTable(t *testing.T) {
	if got := helpText(); got != wantHelp {
		t.Errorf("help:\n%s\nwant:\n%s", got, wantHelp)
	}
	got := pickerItems()
	if len(got) != len(wantPicker) {
		t.Fatalf("picker items: %+v", got)
	}
	for i := range got {
		if got[i] != wantPicker[i] {
			t.Errorf("picker item %d: %+v, want %+v", i, got[i], wantPicker[i])
		}
	}
	for _, name := range []string{"quit", "exit", "help", "mcp"} {
		if findCommand(name) == nil {
			t.Errorf("/%s not found", name)
		}
	}
	if findCommand("nope") != nil {
		t.Error("unknown command found")
	}
}
