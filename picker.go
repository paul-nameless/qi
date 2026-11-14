package main

import (
	"bytes"
	"path/filepath"
	"sort"
	"strings"
)

type pickItem struct {
	name string
	desc string
}

func pick(run runner, command, picker, prompt string, items []pickItem) (string, bool) {
	if len(items) == 0 {
		return "", false
	}
	var in bytes.Buffer
	for _, it := range items {
		in.WriteString(it.name + "\t" + strings.Join(strings.Fields(it.desc), " ") + "\n")
	}
	lines, ok := choose(run, command, picker, prompt, &in)
	if !ok {
		return "", false
	}
	name, _, _ := strings.Cut(lines[len(lines)-1], "\t")
	return name, name != ""
}

func (r *repl) pick(prompt string, items []pickItem) (string, bool) {
	return r.pickWith(r.agent.Cfg.Picker, prompt, items)
}

func (r *repl) pickWith(command, prompt string, items []pickItem) (string, bool) {
	return pick(r.runTTY, command, r.agent.Cfg.Picker, prompt, items)
}

func (r *repl) filePicker() (string, bool) {
	lines, ok := choose(r.runTTY, r.agent.Cfg.FilePicker, r.agent.Cfg.Picker, "@ ", nil)
	if !ok {
		return "", false
	}
	parts := make([]string, len(lines))
	for i, l := range lines {
		name, _, _ := strings.Cut(l, "\t")
		parts[i] = quotePath(relPath(r.agent.Cwd, name))
	}
	return strings.Join(parts, " ") + " ", true
}

func (r *repl) historyPicker(entries []string) (string, bool) {
	items, back := historyItems(entries)
	name, ok := r.pickWith(r.agent.Cfg.HistoryPicker, "history ", items)
	if !ok {
		return "", false
	}
	text, ok := back[name]
	return text, ok
}

func historyItems(entries []string) ([]pickItem, map[string]string) {
	shown := strings.NewReplacer("\r\n", "⏎", "\n", "⏎", "\r", "⏎", "\t", " ")
	back := map[string]string{}
	var items []pickItem
	for i := len(entries) - 1; i >= 0; i-- {
		name := shown.Replace(entries[i])
		if _, dup := back[name]; dup || strings.TrimSpace(name) == "" {
			continue
		}
		back[name] = entries[i]
		items = append(items, pickItem{name: name})
	}
	return items, back
}

func relPath(cwd, p string) string {
	if rel, err := filepath.Rel(cwd, p); err == nil && filepath.IsAbs(p) && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return p
}

func quotePath(p string) string {
	if !strings.ContainsAny(p, " \t'\"") {
		return p
	}
	if strings.Contains(p, "'") {
		return `"` + p + `"`
	}
	return "'" + p + "'"
}

var builtinCommands = []pickItem{
	{"help", "show commands and keys"},
	{"clear", "start a new session"},
	{"context", "show what takes space in the context"},
	{"reload", "re-read skills, commands and CLAUDE.md files"},
	{"compact", "summarize the conversation to free context"},
	{"resume", "switch to another session"},
	{"history", "open the conversation in the pager"},
	{"out", "open the last cut-off tool output in the pager"},
	{"view", "open the last answer in the viewer"},
	{"copy", "copy the last answer to the clipboard"},
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
