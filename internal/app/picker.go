package app

import (
	"bytes"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/paul-nameless/qi/internal/config"
	"github.com/paul-nameless/qi/internal/ui"
)

type pickItem struct {
	name string
	desc string
}

func isBuiltin(command, picker string) bool {
	return firstCommand(strings.ReplaceAll(command, "{picker}", picker)) == config.DefaultPicker
}

func uiItems(items []pickItem) []ui.PickItem {
	out := make([]ui.PickItem, len(items))
	for i, it := range items {
		out[i] = ui.PickItem{Name: it.name, Desc: it.desc}
	}
	return out
}

func pick(run runner, ed *ui.Editor, command, picker, prompt string, items []pickItem, searchDesc bool) (string, bool) {
	name, ok, _ := pickOrComplete(run, ed, command, picker, prompt, items, ui.PickOptions{SearchDesc: searchDesc})
	return name, ok
}

func pickOrComplete(run runner, ed *ui.Editor, command, picker, prompt string, items []pickItem, opts ui.PickOptions) (string, bool, bool) {
	if len(items) == 0 {
		return "", false, false
	}
	if isBuiltin(command, picker) {
		if ed == nil {
			return "", false, false
		}
		i, ok, completed := ed.Pick(ui.PickList{Prompt: prompt, Items: uiItems(items)}, opts)
		if !ok {
			return "", false, false
		}
		return items[i].name, true, completed
	}
	var in bytes.Buffer
	for _, it := range items {
		in.WriteString(it.name + "\t" + strings.Join(strings.Fields(it.desc), " ") + "\n")
	}
	lines, ok := choose(run, command, picker, prompt, &in)
	if !ok {
		return "", false, false
	}
	name, _, _ := strings.Cut(lines[len(lines)-1], "\t")
	return name, name != "", false
}

func (r *repl) pick(prompt string, items []pickItem) (string, bool) {
	return r.pickWith(r.agent.Config().Picker, prompt, items)
}

func (r *repl) pickWith(command, prompt string, items []pickItem) (string, bool) {
	return pick(r.runTTY, r.editor, command, r.agent.Config().Picker, prompt, items, false)
}

func (r *repl) filePicker() (string, bool) {
	if cfg := r.agent.Config(); isBuiltin(cfg.FilePicker, cfg.Picker) && r.editor != nil {
		path, ok := browseFiles(r.editor, r.agent.Cwd())
		if !ok {
			return "", false
		}
		return quotePath(path) + " ", true
	}
	files := strings.Join(listFiles(r.agent.Cwd(), fileListLimit), "\n") + "\n"
	lines, ok := choose(r.runTTY, r.agent.Config().FilePicker, r.agent.Config().Picker, "@ ", strings.NewReader(files))
	if !ok {
		return "", false
	}
	parts := make([]string, len(lines))
	for i, l := range lines {
		name, _, _ := strings.Cut(l, "\t")
		parts[i] = quotePath(relPath(r.agent.Cwd(), name))
	}
	return strings.Join(parts, " ") + " ", true
}

func (r *repl) historyPicker(entries []string) (string, bool) {
	items, back := historyItems(entries)
	name, ok := r.pickWith(r.agent.Config().HistoryPicker, "history ", items)
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
	for _, entry := range slices.Backward(entries) {
		name := shown.Replace(entry)
		if _, dup := back[name]; dup || strings.TrimSpace(name) == "" {
			continue
		}
		back[name] = entry
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

func (r *repl) pickComplete(prompt string, items []pickItem, erased *bool) (string, bool, bool) {
	cfg := r.agent.Config()
	return pickOrComplete(r.runTTY, r.editor, cfg.Picker, cfg.Picker, prompt, items, ui.PickOptions{OnErase: func() { *erased = true }})
}

func (r *repl) slashPicker() (string, bool) {
	erased := false
	name, ok, completed := r.pickComplete("/", pickerItems(), &erased)
	switch {
	case erased:
		return "", false
	case !ok:
		return "/", false
	}
	if completed {
		return "/" + name + " ", false
	}
	if c := findCommand(name); c != nil && c.pickArg != nil {
		return c.pickArg(r)
	}
	return "/" + name, true
}

func (r *repl) pickProfile() (string, bool) {
	items := r.profileItems()
	if len(items) == 0 {
		r.display.Info("no profiles: add a \"profiles\" object to " + r.configFile() + " (see readme)")
		return "", false
	}
	return r.pickArg("profile", items)
}

func (r *repl) pickSkill() (string, bool) {
	items := r.skillItems()
	if len(items) == 0 {
		r.display.Info("no skills found in ~/.claude/skills or .claude/skills")
		return "", false
	}
	erased := false
	s, ok, _ := r.pickComplete("skill ", items, &erased)
	switch {
	case erased:
		return "/skills", false
	case ok:
		return "/" + s + " ", false
	}
	return "/", false
}

func (r *repl) pickArg(cmd string, items []pickItem) (string, bool) {
	erased := false
	v, ok, completed := r.pickComplete(cmd+" ", items, &erased)
	switch {
	case erased:
		return "/" + cmd, false
	case ok:
		return "/" + cmd + " " + v, !completed
	}
	return "/" + cmd + " ", false
}

func (r *repl) configFile() string {
	if r.opts.configPath != "" {
		return r.opts.configPath
	}
	return config.ShortHome(config.DefaultPath())
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

func (r *repl) profileItems() []pickItem {
	var items []pickItem
	for _, p := range r.agent.Config().ProfileNames() {
		desc := ""
		if pc, err := r.resolve(p); err == nil {
			desc = pc.Model + " via " + pc.Provider
		}
		if p == r.agent.Config().Profile {
			desc += " (current)"
		}
		items = append(items, pickItem{p, desc})
	}
	return items
}

func (r *repl) effortItems() []pickItem {
	current := effortName(r.agent.Config().ReasoningEffort)
	var items []pickItem
	for _, name := range effortNames() {
		item := pickItem{name: name}
		if name == current {
			item.desc = "current"
		}
		items = append(items, item)
	}
	return items
}
