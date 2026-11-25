package app

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/paul-nameless/qi/internal/config"
	"github.com/paul-nameless/qi/internal/tools"
)

func (r *repl) resolve(profile string) (config.Config, error) {
	return config.Resolve(config.Options{Path: r.opts.configPath, Profile: profile, Provider: r.opts.provider, Effort: r.opts.effort, SystemPromptFile: r.opts.systemPromptFile})
}

func (r *repl) switchProfile(name string) error {
	r.opts.provider = ""
	cfg, err := r.resolve(name)
	if err != nil {
		return err
	}
	before := subagentsMenu(r.agent.Config().SubagentChoices())
	if err := r.apply(cfg); err != nil {
		return err
	}
	if menu := subagentsMenu(cfg.SubagentChoices()); r.runner != nil && menu != before {
		r.display.Info(menu)
	}
	if cfg.ProfileSetsMode() {
		m := cfg.StartMode()
		r.agent.SetMode(m)
		r.warnMode(m)
	}
	r.report(r.loadPrompt())
	if r.runner != nil && !sameMCP(cfg.MCP, r.runner.MCP) {
		r.display.Info("MCP servers unchanged; restart qi to apply the new profile's MCP setup")
	}
	if _, err := config.SetValue(r.opts.configPath, "", "default_profile", cfg.Profile); err != nil {
		r.display.Error("couldn't save the default profile: " + err.Error())
	}
	return nil
}

func sameMCP(a, b map[string]config.MCPServer) bool {
	return len(a) == 0 && len(b) == 0 || reflect.DeepEqual(a, b)
}

func effortNames() []string {
	return append([]string{"default"}, config.Efforts...)
}

func effortName(e string) string {
	if e == "" {
		return "default"
	}
	return e
}

func (r *repl) setEffort(level string) error {
	if !slices.Contains(effortNames(), level) {
		return fmt.Errorf("unknown effort %q", level)
	}
	if level == "default" {
		level = ""
	}
	r.opts.effort = level
	r.agent.EditConfig(func(c *config.Config) { c.ReasoningEffort = level })
	return nil
}

func (r *repl) apply(cfg config.Config) error {
	cfg.DefaultCacheTTL(false)
	key, err := cfg.ResolveAPIKey()
	if err != nil {
		return err
	}
	r.agent.Reconfigure(cfg, newProvider(cfg, key))
	if r.runner != nil {
		r.runner.Profile, r.runner.Provider = cfg.Profile, cfg.Provider
		r.runner.Access.ProjectWritable, r.runner.Access.ReadOnlyAllow = cfg.ProjectWritable, cfg.ReadOnlyAllow
		r.agent.ReplaceFunction(tools.Subagent(cfg.SubagentChoices()))
	}
	return nil
}

func (r *repl) profileInfo() string {
	cfg := r.agent.Config()
	var b strings.Builder
	fmt.Fprintf(&b, "model: %s", cfg.Model)
	if cfg.Provider != "" {
		fmt.Fprintf(&b, " (provider %s)", cfg.Provider)
	}
	fmt.Fprintf(&b, " · effort %s", effortName(cfg.ReasoningEffort))
	if cfg.Profile != "" {
		fmt.Fprintf(&b, " · profile %s", cfg.Profile)
	}
	for _, p := range cfg.ProfileNames() {
		fmt.Fprintf(&b, "\n  /profile %s", p)
	}
	return b.String()
}
