package config

import (
	"fmt"
	"slices"
	"strings"
)

var (
	apis      = []string{"openai", "responses", "anthropic"}
	Efforts   = []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}
	cacheTTLs = []string{"5m", "1h"}
)

func (c Config) validate() error {
	checks := []error{
		oneOf("api", c.API, apis),
		oneOf("cache_ttl", c.CacheTTL, cacheTTLs),
		oneOf("reasoning_effort", c.ReasoningEffort, Efforts),
		oneOf("mode", c.Mode, modeNames),
		atLeast("max_tokens", c.MaxTokens, 1),
		atLeast("bash_timeout", c.BashTimeout, 1),
		atLeast("max_rounds", c.MaxRounds, 0),
	}
	for i, p := range c.ProjectWritable {
		if !strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "~/") {
			checks = append(checks, fmt.Errorf("key \"project_writable.%d\": %q must be an absolute path or start with ~/", i, p))
		}
	}
	for i, name := range c.SubagentProfiles {
		if _, ok := c.Profiles[name]; !ok {
			checks = append(checks, fmt.Errorf("key \"subagent_profiles.%d\": %w", i, c.unknownProfile(name)))
		}
	}
	for _, name := range sortedKeys(c.MCP) {
		checks = append(checks, c.MCP[name].validate("mcp."+name))
	}
	for _, err := range checks {
		if err != nil {
			return err
		}
	}
	return nil
}

func oneOf(key, value string, allowed []string) error {
	if value == "" || slices.Contains(allowed, value) {
		return nil
	}
	return fmt.Errorf("key %q: %q is not one of %s", key, value, strings.Join(allowed, ", "))
}

func atLeast(key string, value, minimum int) error {
	if value >= minimum {
		return nil
	}
	return fmt.Errorf("key %q: must be at least %d, got %d", key, minimum, value)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
