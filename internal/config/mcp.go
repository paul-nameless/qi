package config

import (
	"encoding/json/v2"
	"fmt"
	"os"
	"path"
	"strings"
)

const MCPEnv = "AICODE_MCP"

const slashStandIn = "\x00"

func (s MCPServer) ToolEnabled(name string) bool {
	return (len(s.Tools) == 0 || matchAny(s.Tools, name)) && !matchAny(s.ExcludeTools, name)
}

func matchAny(patterns []string, name string) bool {
	for _, p := range patterns {
		if ok, _ := globMatch(p, name); ok {
			return true
		}
	}
	return false
}

func globMatch(pattern, name string) (bool, error) {
	return path.Match(strings.ReplaceAll(pattern, "/", slashStandIn), strings.ReplaceAll(name, "/", slashStandIn))
}

func checkPatterns(key string, patterns []string) error {
	for i, p := range patterns {
		k := fmt.Sprintf("%s.%d", key, i)
		if p == "" {
			return fmt.Errorf("key %q: empty pattern", k)
		}
		if _, err := globMatch(p, ""); err != nil {
			return fmt.Errorf("key %q: invalid glob pattern %q", k, p)
		}
	}
	return nil
}

func (s MCPServer) validate(key string) error {
	if strings.TrimSpace(s.Command) == "" {
		return fmt.Errorf("key %q: command is required", key)
	}
	if s.Tools != nil && len(s.Tools) == 0 {
		return fmt.Errorf("key %q: empty list; omit the key to allow all tools", key+".tools")
	}
	if err := checkPatterns(key+".tools", s.Tools); err != nil {
		return err
	}
	return checkPatterns(key+".exclude_tools", s.ExcludeTools)
}

func EncodeMCP(servers map[string]MCPServer) (string, error) {
	if servers == nil {
		servers = map[string]MCPServer{}
	}
	data, err := json.Marshal(servers, json.Deterministic(true), json.FormatNilSliceAsNull(true), json.FormatNilMapAsNull(true))
	if err != nil {
		return "", fmt.Errorf("encode mcp servers: %w", err)
	}
	return string(data), nil
}

func MCPFromEnv() (map[string]MCPServer, bool, error) {
	raw, ok := os.LookupEnv(MCPEnv)
	if !ok {
		return nil, false, nil
	}
	_ = os.Unsetenv(MCPEnv)
	var servers map[string]MCPServer
	if err := json.Unmarshal([]byte(raw), &servers, json.RejectUnknownMembers(true)); err != nil {
		return nil, true, fmt.Errorf("%s from the parent agent: %w", MCPEnv, err)
	}
	for _, name := range sortedKeys(servers) {
		if err := servers[name].validate("mcp." + name); err != nil {
			return nil, true, fmt.Errorf("%s from the parent agent: %w", MCPEnv, err)
		}
	}
	return servers, true, nil
}
