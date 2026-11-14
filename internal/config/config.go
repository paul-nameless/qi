package config

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"
)

type SubagentModel struct {
	Name     string `yaml:"name"`
	Model    string `yaml:"model"`
	Provider string `yaml:"provider"`
	UseFor   string `yaml:"use_for"`
}

type Provider struct {
	API          string `yaml:"api"`
	BaseURL      string `yaml:"base_url"`
	APIKey       string `yaml:"api_key"`
	APIKeyShell  string `yaml:"api_key_shell"`
	CacheControl *bool  `yaml:"cache_control"`
}

type MCPServer struct {
	Command     string            `yaml:"command"`
	Args        []string          `yaml:"args"`
	Env         map[string]string `yaml:"env"`
	Description string            `yaml:"description"`
}

type Config struct {
	Model           string               `yaml:"model"`
	API             string               `yaml:"api"`
	Provider        string               `yaml:"provider"`
	BaseURL         string               `yaml:"base_url"`
	APIKey          string               `yaml:"api_key"`
	APIKeyShell     string               `yaml:"api_key_shell"`
	ReasoningEffort string               `yaml:"reasoning_effort"`
	MaxTokens       int                  `yaml:"max_tokens"`
	CacheControl    *bool                `yaml:"cache_control"`
	CacheTTL        string               `yaml:"cache_ttl"`
	CompactAt       int                  `yaml:"compact_at"`
	CompactIdleAt   int                  `yaml:"compact_idle_at"`
	ShowReasoning   *bool                `yaml:"show_reasoning"`
	ReadOnly        bool                 `yaml:"read_only"`
	ReadOnlyAllow   []string             `yaml:"read_only_allow"`
	BashTimeout     int                  `yaml:"bash_timeout"`
	SubagentModels  []SubagentModel      `yaml:"subagent_models"`
	SubagentEfforts []string             `yaml:"subagent_efforts"`
	InstructionFile []string             `yaml:"instruction_files"`
	SkillDirs       []string             `yaml:"skill_dirs"`
	HookFiles       []string             `yaml:"hook_files"`
	MCP             map[string]MCPServer `yaml:"mcp"`
	Providers       map[string]Provider  `yaml:"providers"`
	Profiles        map[string]any       `yaml:"profiles"`
	DefaultProfile  string               `yaml:"default_profile"`
	Profile         string               `yaml:"-"`
	profileRaw      []byte
}

func Dir() string {
	if d := os.Getenv("AICODE_CONFIG_DIR"); d != "" {
		return d
	}
	return filepath.Join(Home(), ".config", "aicode")
}

func Home() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}

func ExpandHome(p string) string {
	if p == "~" {
		return Home()
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(Home(), p[2:])
	}
	return p
}

func Load(path, profile string) (Config, error) {
	var c Config
	if path == "" {
		path = filepath.Join(Dir(), "config.yml")
	}
	if err := mergeFile(&c, ExpandHome(path), path != filepath.Join(Dir(), "config.yml")); err != nil {
		return c, err
	}
	if profile == "" {
		profile = c.DefaultProfile
	}
	if profile != "" {
		if err := c.applyProfile(profile); err != nil {
			return c, err
		}
	}
	if err := c.applyProvider(c.Provider); err != nil {
		return c, err
	}
	if c.profileRaw != nil {
		if err := yaml.Unmarshal(c.profileRaw, &c); err != nil {
			return c, err
		}
	}
	c.applyDefaults()
	return c, nil
}

func (c *Config) applyProfile(name string) error {
	var raw []byte
	if p, ok := c.Profiles[name]; ok {
		data, err := yaml.Marshal(p)
		if err != nil {
			return fmt.Errorf("profile %s: %w", name, err)
		}
		raw = data
	} else {
		path := name
		if !strings.Contains(path, "/") && !strings.HasSuffix(path, ".yml") {
			path = filepath.Join(Dir(), path+".yml")
		}
		data, err := os.ReadFile(ExpandHome(path))
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("unknown profile %q (not in profiles: and no %s)", name, path)
		}
		if err != nil {
			return err
		}
		raw = data
	}
	if err := yaml.Unmarshal(raw, c); err != nil {
		return fmt.Errorf("profile %s: %w", name, err)
	}
	c.Profile, c.profileRaw = name, raw
	return nil
}

var builtinProviders = map[string]Provider{
	"claude":    {API: "anthropic"},
	"anthropic": {API: "anthropic"},
	"openai":    {API: "openai"},
}

func (c *Config) UseProvider(name string) error {
	if name == c.Provider && c.API != "" {
		return nil
	}
	return c.applyProvider(name)
}

func (c *Config) applyProvider(name string) error {
	if name == "" {
		return nil
	}
	p, ok := c.Providers[name]
	if !ok {
		b, isBuiltin := builtinProviders[strings.ToLower(name)]
		if !isBuiltin {
			return fmt.Errorf("unknown provider %q", name)
		}
		if _, wasNamed := c.Providers[c.Provider]; !wasNamed {
			c.Provider, c.API, c.BaseURL = name, b.API, ""
			return nil
		}
		p = b
	}
	c.Provider = name
	c.API, c.BaseURL, c.APIKey, c.APIKeyShell, c.CacheControl = p.API, p.BaseURL, p.APIKey, p.APIKeyShell, p.CacheControl
	return nil
}

func (c Config) ProfileNames() []string {
	var names []string
	for n := range c.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func mergeFile(c *Config, path string, required bool) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && !required {
			return nil
		}
		return fmt.Errorf("read config %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, c); err != nil {
		return fmt.Errorf("parse config %s: %w", path, err)
	}
	return nil
}

func (c *Config) applyDefaults() {
	if c.API == "" {
		c.API = "openai"
	}
	if c.API == "openai" && c.BaseURL == "" {
		if os.Getenv("OPENAI_API_KEY") == "" && os.Getenv("OPENROUTER_API_KEY") != "" {
			c.BaseURL = "https://openrouter.ai/api/v1"
		}
	}
	if c.Model == "" {
		switch {
		case c.API == "anthropic":
			c.Model = "claude-sonnet-5-5"
		case strings.Contains(c.BaseURL, "openrouter"):
			c.Model = "openai/gpt-6-luna"
		default:
			c.Model = "gpt-6-luna"
		}
	}
	if c.MaxTokens == 0 {
		c.MaxTokens = 32000
	}
	if c.CompactAt == 0 {
		c.CompactAt = 150000
	}
	if c.CompactIdleAt == 0 {
		c.CompactIdleAt = 40000
	}
	if c.CacheTTL == "" {
		c.CacheTTL = "1h"
	}
	if c.BashTimeout == 0 {
		c.BashTimeout = 120
	}
	if len(c.SubagentEfforts) == 0 {
		c.SubagentEfforts = []string{"low", "medium", "high"}
	}
	if len(c.InstructionFile) == 0 {
		c.InstructionFile = []string{"~/.claude/CLAUDE.md", "CLAUDE.md", "AGENTS.md", "CLAUDE.local.md"}
	}
	if len(c.SkillDirs) == 0 {
		c.SkillDirs = []string{"~/.claude/skills", ".claude/skills", filepath.Join(Dir(), "skills")}
	}
	if len(c.HookFiles) == 0 {
		c.HookFiles = []string{"~/.claude/settings.json", ".claude/settings.json", ".claude/settings.local.json", filepath.Join(Dir(), "settings.json")}
	}
}

func (c Config) ShowReasoningOn() bool {
	return c.ShowReasoning == nil || *c.ShowReasoning
}

func (c Config) ResolveAPIKey() (string, error) {
	if c.APIKey != "" {
		return c.APIKey, nil
	}
	if c.APIKeyShell != "" {
		out, err := exec.Command("sh", "-c", c.APIKeyShell).Output()
		if err != nil {
			return "", fmt.Errorf("api_key_shell failed: %w", err)
		}
		key := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
		if key == "" {
			return "", errors.New("api_key_shell returned an empty key")
		}
		return key, nil
	}
	var envs []string
	switch {
	case c.API == "anthropic":
		envs = []string{"ANTHROPIC_API_KEY"}
	case strings.Contains(c.BaseURL, "openrouter"):
		envs = []string{"OPENROUTER_API_KEY", "OPENAI_API_KEY"}
	default:
		envs = []string{"OPENAI_API_KEY", "OPENROUTER_API_KEY"}
	}
	for _, e := range envs {
		if v := os.Getenv(e); v != "" {
			return v, nil
		}
	}
	return "", fmt.Errorf("no API key: set api_key_shell in %s or %s", filepath.Join(Dir(), "config.yml"), strings.Join(envs, "/"))
}
