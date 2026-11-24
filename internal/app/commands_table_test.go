package app

import (
	"strings"
	"testing"

	"github.com/paul-nameless/qi/internal/agent"
	"github.com/paul-nameless/qi/internal/config"
)

func TestCommandTable(t *testing.T) {
	help := helpText()
	items := map[string]bool{}
	for _, it := range pickerItems() {
		items[it.name] = true
	}
	for _, c := range commandList {
		inHelp := strings.Contains(help, "\n  /"+c.name+" ") || strings.Contains(help, "\n  /"+c.name+"\n")
		if c.alias == (inHelp || items[c.name]) {
			t.Errorf("/%s (alias %v): in help %v, in picker %v", c.name, c.alias, inHelp, items[c.name])
		}
		if findCommand(c.name) == nil || c.quit == (c.run != nil) {
			t.Errorf("/%s: not dispatchable", c.name)
		}
	}
	if !strings.HasPrefix(help, "Commands:\n  /help                   this help\n") || !strings.Contains(help, "\n  /compact [focus]        summarize") || !strings.Contains(help, "\n  /copy [answer|code|id]  copy") {
		t.Errorf("help layout:\n%s", help)
	}
	if findCommand("nope") != nil {
		t.Error("unknown command found")
	}
}

func TestBuiltinSkillExpandsFromMemory(t *testing.T) {
	r, _ := testRepl(t, config.Config{Model: "m"}, scripted())
	r.skills = agent.BuiltinSkills()
	text, err := r.expand("qi-config", "add a profile")
	if err != nil || !strings.Contains(text, "<skill name=\"qi-config\">") || !strings.Contains(text, "qi:skills/qi-config/schema.json") || !strings.Contains(text, "add a profile") {
		t.Errorf("expand: %v\n%s", err, text)
	}
}
