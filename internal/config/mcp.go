package config

import (
	"fmt"
	"path"
	"strings"
)

func (s MCPServer) ToolEnabled(name string) bool {
	return (len(s.Tools) == 0 || matchAny(s.Tools, name)) && !matchAny(s.ExcludeTools, name)
}

func (s MCPServer) ExactTools() []string {
	var names []string
	for _, t := range s.Tools {
		if strings.ContainsAny(t, `*?[\`) {
			return nil
		}
		if s.ToolEnabled(t) {
			names = append(names, t)
		}
	}
	return names
}

func matchAny(patterns []string, name string) bool {
	for _, p := range patterns {
		if ok, _ := path.Match(p, name); ok {
			return true
		}
	}
	return false
}

func checkPatterns(key string, patterns []string) error {
	for i, p := range patterns {
		k := fmt.Sprintf("%s.%d", key, i)
		if p == "" {
			return fmt.Errorf("key %q: empty pattern", k)
		}
		if _, err := path.Match(p, ""); err != nil {
			return fmt.Errorf("key %q: invalid glob pattern %q", k, p)
		}
	}
	return nil
}

func (s MCPServer) validate(key string) error {
	if strings.TrimSpace(s.Command) == "" {
		return fmt.Errorf("key %q: command is required", key)
	}
	if err := checkPatterns(key+".tools", s.Tools); err != nil {
		return err
	}
	return checkPatterns(key+".exclude_tools", s.ExcludeTools)
}
