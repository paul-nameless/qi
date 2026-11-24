package app

import (
	"fmt"
	"strings"

	"github.com/paul-nameless/qi/internal/config"
	"github.com/paul-nameless/qi/internal/ui"
)

type setting struct {
	key     string
	on, off string
	desc    string
	get     func(config.Config) bool
	set     func(*config.Config, bool)
}

var settings = []setting{
	{key: "run_code", on: "full", off: "compact", desc: "the code of each run call: all of it, or one line", get: config.Config.RunCodeOn, set: func(c *config.Config, v bool) { c.RunCode = &v }},
	{key: "show_reasoning", on: "on", off: "off", desc: "print the model's reasoning", get: config.Config.ShowReasoningOn, set: func(c *config.Config, v bool) { c.ShowReasoning = &v }},
	{key: "terminal_images", on: "on", off: "off", desc: "draw images named in the answer (Ghostty, kitty)", get: config.Config.TerminalImagesOn, set: func(c *config.Config, v bool) { c.TerminalImages = &v }},
}

func findSetting(key string) *setting {
	for i := range settings {
		if settings[i].key == key {
			return &settings[i]
		}
	}
	return nil
}

func settingKeys() string {
	keys := make([]string, len(settings))
	for i, s := range settings {
		keys[i] = s.key
	}
	return strings.Join(keys, ", ")
}

func (s setting) value(on bool) string {
	if on {
		return s.on
	}
	return s.off
}

func (r *repl) configCmd(args string) {
	if args != "" {
		r.report(r.toggleSetting(args))
		return
	}
	r.display.Info(r.configInfo())
	cfg := r.agent.Config()
	if !r.toolOn(cfg.Picker) {
		return
	}
	if !isBuiltin(cfg.Picker, cfg.Picker) || r.editor == nil {
		if key, ok := r.pick("config ", r.settingItems()); ok {
			r.report(r.toggleSetting(key))
		}
		return
	}
	list := ui.PickList{Prompt: "config ", Items: uiItems(r.settingItems())}
	r.editor.Pick(list, ui.PickOptions{OnPick: func(i int) *ui.PickList {
		r.report(r.toggleSetting(settings[i].key))
		list.Items = uiItems(r.settingItems())
		return &list
	}})
}

func (r *repl) toggleSetting(key string) error {
	s := findSetting(key)
	if s == nil {
		return fmt.Errorf("unknown setting %q (switchable: %s)", key, settingKeys())
	}
	cfg := r.agent.Config()
	v := !s.get(cfg)
	r.agent.EditConfig(func(c *config.Config) { s.set(c, v) })
	inProfile, err := config.SetValue(r.opts.configPath, cfg.Profile, s.key, v)
	switch {
	case err != nil:
		r.display.Info(fmt.Sprintf("%s: %s (not saved)", s.key, s.value(v)))
		return fmt.Errorf("not saved: %w", err)
	case inProfile:
		r.display.Info(fmt.Sprintf("%s: %s (saved in profile %q)", s.key, s.value(v), cfg.Profile))
	default:
		r.display.Info(fmt.Sprintf("%s: %s (saved)", s.key, s.value(v)))
	}
	return nil
}

func (r *repl) settingItems() []pickItem {
	cfg := r.agent.Config()
	items := make([]pickItem, len(settings))
	for i, s := range settings {
		items[i] = pickItem{s.key, fmt.Sprintf("%-8s %s", s.value(s.get(cfg)), s.desc)}
	}
	return items
}

func (r *repl) settingsInfo() string {
	cfg := r.agent.Config()
	var b strings.Builder
	b.WriteString("Settings (/config <name> switches one and saves it):")
	for _, s := range settings {
		fmt.Fprintf(&b, "\n  %-15s %-8s %s", s.key, s.value(s.get(cfg)), s.desc)
	}
	return b.String()
}
