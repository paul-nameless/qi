package app

import (
	"fmt"
	"strings"

	"github.com/paul-nameless/qi/internal/config"
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
	if !r.toolOn(r.agent.Config().Picker) {
		return
	}
	if key, ok := r.pick("config ", r.settingItems()); ok {
		r.report(r.toggleSetting(key))
	}
}

func (r *repl) toggleSetting(key string) error {
	s := findSetting(key)
	if s == nil {
		return fmt.Errorf("unknown setting %q (switchable: %s)", key, settingKeys())
	}
	v := !s.get(r.agent.Config())
	if r.overrides == nil {
		r.overrides = map[string]bool{}
	}
	r.overrides[s.key] = v
	r.agent.EditConfig(func(c *config.Config) { s.set(c, v) })
	r.display.Info(fmt.Sprintf("%s: %s for this session", s.key, s.value(v)))
	return nil
}

func (r *repl) keepSettings(cfg *config.Config) {
	for key, v := range r.overrides {
		if s := findSetting(key); s != nil {
			s.set(cfg, v)
		}
	}
}

func (r *repl) settingItems() []pickItem {
	cfg := r.agent.Config()
	items := make([]pickItem, len(settings))
	for i, s := range settings {
		on := s.get(cfg)
		items[i] = pickItem{s.key, fmt.Sprintf("%s · %s · pick for %s", s.value(on), s.desc, s.value(!on))}
	}
	return items
}

func (r *repl) settingsInfo() string {
	cfg := r.agent.Config()
	var b strings.Builder
	b.WriteString("Settings (/config <name> switches one for this session):")
	for _, s := range settings {
		fmt.Fprintf(&b, "\n  %-15s %-8s %s", s.key, s.value(s.get(cfg)), s.desc)
	}
	return b.String()
}
