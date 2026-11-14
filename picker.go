package main

import (
	"bytes"
	"os"
	"os/exec"
	"sort"
	"strings"
)

type pickItem struct {
	name string
	desc string
}

func pickerAvailable(command string) bool {
	f := strings.Fields(command)
	if len(f) == 0 {
		return false
	}
	_, err := exec.LookPath(f[0])
	return err == nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func pick(command, prompt string, items []pickItem) (string, bool) {
	if len(items) == 0 {
		return "", false
	}
	var in bytes.Buffer
	for _, it := range items {
		in.WriteString(it.name + "\t" + strings.Join(strings.Fields(it.desc), " ") + "\n")
	}
	cmd := exec.Command("sh", "-c", strings.ReplaceAll(command, "{prompt}", shellQuote(prompt)))
	cmd.Env = append(os.Environ(), "FZF_DEFAULT_OPTS=", "FZF_DEFAULT_OPTS_FILE=", "AICODE_PICK_PROMPT="+prompt)
	cmd.Stdin = &in
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	name, _, _ := strings.Cut(lines[len(lines)-1], "\t")
	return name, name != ""
}

func (r *repl) pick(prompt string, items []pickItem) (string, bool) {
	return pick(r.agent.Cfg.Picker, prompt, items)
}

var builtinCommands = []pickItem{
	{"help", "show commands and keys"},
	{"clear", "start a new session"},
	{"compact", "summarize the conversation to free context"},
	{"model", "switch the model"},
	{"profile", "switch the profile"},
	{"effort", "switch the reasoning effort"},
	{"skills", "pick a skill"},
	{"exit", "quit"},
}

func (r *repl) slashPicker() (string, bool) {
	name, ok := r.pick("/", builtinCommands)
	if !ok {
		return "/", false
	}
	switch name {
	case "model":
		return r.pickArg("model", r.modelItems())
	case "effort":
		return r.pickArg("effort", r.effortItems())
	case "profile":
		items := r.profileItems()
		if len(items) == 0 {
			r.display.Info("no profiles: add a profiles: section to " + r.configFile() + " (see readme)")
			return "", false
		}
		return r.pickArg("profile", items)
	case "skills":
		items := r.skillItems()
		if len(items) == 0 {
			r.display.Info("no skills found in ~/.claude/skills or .claude/skills")
			return "", false
		}
		if s, ok := r.pick("skill ", items); ok {
			return "/" + s + " ", false
		}
		return "/", false
	}
	return "/" + name, true
}

func (r *repl) pickArg(cmd string, items []pickItem) (string, bool) {
	if v, ok := r.pick(cmd+" ", items); ok {
		return "/" + cmd + " " + v, true
	}
	return "/" + cmd + " ", false
}

func (r *repl) configFile() string {
	if r.opts.configPath != "" {
		return r.opts.configPath
	}
	return "~/.config/aicode/config.yml"
}

func (r *repl) skillItems() []pickItem {
	var items []pickItem
	for _, s := range r.skills {
		items = append(items, pickItem{s.Name, s.Description})
	}
	for _, c := range r.commands {
		items = append(items, pickItem{c.Name, "command"})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].name < items[j].name })
	return items
}

func (r *repl) modelItems() []pickItem {
	cfg := r.agent.Cfg
	items := []pickItem{{cfg.Model, "current"}}
	seen := map[string]bool{cfg.Model: true}
	add := func(name, desc string) {
		if !seen[name] {
			seen[name] = true
			items = append(items, pickItem{name, desc})
		}
	}
	for _, m := range cfg.SubagentModels {
		add(m.Name, m.Model+" · "+m.UseFor)
	}
	for _, p := range cfg.ProfileNames() {
		if pc, err := r.resolve(p); err == nil {
			for _, m := range pc.SubagentModels {
				add(m.Name, m.Model+" · "+m.UseFor)
			}
		}
	}
	return items
}

func (r *repl) profileItems() []pickItem {
	var items []pickItem
	for _, p := range r.agent.Cfg.ProfileNames() {
		desc := ""
		if pc, err := r.resolve(p); err == nil {
			desc = pc.Model + " via " + pc.Provider
		}
		if p == r.agent.Cfg.Profile {
			desc += " (current)"
		}
		items = append(items, pickItem{p, desc})
	}
	return items
}

func (r *repl) effortItems() []pickItem {
	current := effortName(r.agent.Cfg.ReasoningEffort)
	items := make([]pickItem, len(efforts))
	for i, e := range efforts {
		if e.name == current {
			e.desc = strings.TrimSpace("current " + e.desc)
		}
		items[i] = e
	}
	return items
}
