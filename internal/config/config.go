package config

import (
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

type SubagentModel struct {
	Name     string `json:"name"`
	Model    string `json:"model"`
	Provider string `json:"provider"`
	UseFor   string `json:"use_for"`
}

type Provider struct {
	API          string `json:"api"`
	BaseURL      string `json:"base_url"`
	APIKey       string `json:"api_key"`
	APIKeyShell  string `json:"api_key_shell"`
	CacheControl *bool  `json:"cache_control"`
}

type MCPServer struct {
	Command      string            `json:"command"`
	Args         []string          `json:"args"`
	Env          map[string]string `json:"env"`
	Description  string            `json:"description"`
	Tools        []string          `json:"tools"`
	ExcludeTools []string          `json:"exclude_tools"`
}

const DefaultPicker = `fzf --height=40% --reverse --no-multi --bind=ctrl-z:ignore --delimiter='\t' --nth=1 --tabstop=24 --prompt={prompt}`

type Config struct {
	Schema          string                    `json:"$schema"`
	Model           string                    `json:"model"`
	API             string                    `json:"api"`
	Provider        string                    `json:"provider"`
	BaseURL         string                    `json:"base_url"`
	APIKey          string                    `json:"api_key"`
	APIKeyShell     string                    `json:"api_key_shell"`
	ReasoningEffort string                    `json:"reasoning_effort"`
	MaxTokens       int                       `json:"max_tokens"`
	CacheControl    *bool                     `json:"cache_control"`
	CacheTTL        string                    `json:"cache_ttl"`
	CompactAt       Threshold                 `json:"compact_at"`
	CompactIdleAt   Threshold                 `json:"compact_idle_at"`
	ContextWindow   Tokens                    `json:"context_window"`
	MaxRounds       int                       `json:"max_rounds"`
	ShowReasoning   *bool                     `json:"show_reasoning"`
	ReadOnly        bool                      `json:"read_only"`
	ReadOnlyAllow   []string                  `json:"read_only_allow"`
	BashTimeout     int                       `json:"bash_timeout"`
	SubagentModels  []SubagentModel           `json:"subagent_models"`
	SubagentEfforts []string                  `json:"subagent_efforts"`
	InstructionFile []string                  `json:"instruction_files"`
	SkillDirs       []string                  `json:"skill_dirs"`
	HookFiles       []string                  `json:"hook_files"`
	MCP             map[string]MCPServer      `json:"mcp"`
	Picker          string                    `json:"picker"`
	NotifyCmd       string                    `json:"notify_cmd"`
	FilePicker      string                    `json:"file_picker"`
	HistoryPicker   string                    `json:"history_picker"`
	SessionPicker   string                    `json:"session_picker"`
	Pager           string                    `json:"pager"`
	CopyCmd         string                    `json:"copy_cmd"`
	BuiltinSkills   *bool                     `json:"builtin_skills"`
	Providers       map[string]Provider       `json:"providers"`
	Profiles        map[string]jsontext.Value `json:"profiles"`
	DefaultProfile  string                    `json:"default_profile"`
	Profile         string                    `json:"-"`
	profileRaw      []byte
	globalMCP       map[string]MCPServer
	source          string
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

func ShortHome(p string) string {
	if rel, err := filepath.Rel(Home(), p); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.Join("~", rel)
	}
	return p
}

func Load(path, profile string) (Config, error) {
	return Resolve(path, profile, "", "", "")
}

func DefaultPath() string {
	return filepath.Join(Dir(), "config.json")
}

func Resolve(path, profile, provider, model, effort string) (Config, error) {
	var c Config
	if path == "" {
		path = DefaultPath()
	}
	path = ExpandHome(path)
	c.source = "config " + path
	if err := mergeFile(&c, path, path != DefaultPath()); err != nil {
		return c, err
	}
	c.globalMCP = c.MCP
	if profile == "" {
		profile = c.DefaultProfile
	}
	if profile != "" {
		if err := c.applyProfile(profile); err != nil {
			return c, err
		}
	}
	if err := c.loadProvider(c.Provider); err != nil {
		return c, fmt.Errorf("%s: key \"provider\": %w", c.source, err)
	}
	if c.profileRaw != nil {
		if err := c.overlay(c.profileRaw); err != nil {
			return c, fmt.Errorf("%s: %w", c.source, err)
		}
	}
	if provider != "" {
		if err := c.UseProvider(provider); err != nil {
			return c, fmt.Errorf("-provider: %w", err)
		}
	}
	if model != "" {
		c.SetModel(model)
	}
	if effort != "" {
		c.ReasoningEffort = effort
	}
	c.applyDefaults()
	if err := c.validate(); err != nil {
		return c, fmt.Errorf("%s: %w", c.source, err)
	}
	return c, nil
}

func Check(path, profile string) error {
	c, err := Resolve(path, profile, "", "", "")
	if err != nil {
		return err
	}
	names := c.ProfileNames()
	files, err := profileFiles(ExpandHome(path))
	if err != nil {
		return err
	}
	for _, name := range files {
		if _, inline := c.Profiles[name]; !inline {
			names = append(names, name)
		}
	}
	for _, name := range names {
		if _, err := Resolve(path, name, "", "", ""); err != nil {
			return err
		}
	}
	return nil
}

func profileFiles(configPath string) ([]string, error) {
	paths, err := filepath.Glob(filepath.Join(Dir(), "*.json"))
	if err != nil {
		return nil, err
	}
	var names []string
	for _, p := range paths {
		base := filepath.Base(p)
		if p == DefaultPath() || p == configPath || base == "settings.json" {
			continue
		}
		names = append(names, strings.TrimSuffix(base, ".json"))
	}
	return names, nil
}

func (c *Config) applyProfile(name string) error {
	raw, source, err := c.profileData(name)
	if err != nil {
		return err
	}
	if err := c.overlay(raw); err != nil {
		return fmt.Errorf("%s: %w", source, err)
	}
	c.Profile, c.profileRaw, c.source = name, raw, source
	return nil
}

func (c *Config) profileData(name string) ([]byte, string, error) {
	if raw, ok := c.Profiles[name]; ok {
		return raw, fmt.Sprintf("%s: profile %q", c.source, name), nil
	}
	path := profilePath(name)
	if err := rejectYAML(path); err != nil {
		return nil, "", err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := legacyYAML(path); err != nil {
			return nil, "", err
		}
		return nil, "", fmt.Errorf("unknown profile %q (not in profiles and no %s)", name, ShortHome(path))
	}
	if err != nil {
		return nil, "", fmt.Errorf("read profile %s: %w", path, err)
	}
	source := "profile file " + path
	if err := checkProfile(data); err != nil {
		return nil, "", fmt.Errorf("%s: %w", source, err)
	}
	return data, source, nil
}

func profilePath(name string) string {
	if strings.Contains(name, "/") {
		return ExpandHome(name)
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".json", ".yml", ".yaml":
		return filepath.Join(Dir(), name)
	}
	return filepath.Join(Dir(), name+".json")
}

func (c *Config) overlay(raw []byte) error {
	if err := clearKeys(c, raw); err != nil {
		return err
	}
	if err := decodeStrict(raw, c); err != nil {
		return err
	}
	return c.mergeMCP(raw)
}

var builtinProviders = map[string]Provider{
	"claude":    {API: "anthropic"},
	"anthropic": {API: "anthropic"},
	"openai":    {API: "openai"},
}

func (c *Config) UseProvider(name string) error {
	if name == "" || (name == c.Provider && c.API != "") {
		return nil
	}
	p, err := c.provider(name)
	if err != nil {
		return err
	}
	c.Provider = name
	c.API, c.BaseURL, c.APIKey, c.APIKeyShell, c.CacheControl = p.API, p.BaseURL, p.APIKey, p.APIKeyShell, p.CacheControl
	return nil
}

func (c *Config) loadProvider(name string) error {
	if name == "" {
		return nil
	}
	if _, named := c.Providers[name]; named {
		c.API = ""
		return c.UseProvider(name)
	}
	p, err := c.provider(name)
	if err != nil {
		return err
	}
	c.API = p.API
	return nil
}

func (c *Config) provider(name string) (Provider, error) {
	if p, ok := c.Providers[name]; ok {
		return p, nil
	}
	if p, ok := builtinProviders[strings.ToLower(name)]; ok {
		return p, nil
	}
	return Provider{}, fmt.Errorf("unknown provider %q", name)
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
	if err := rejectYAML(path); err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := legacyYAML(path); err != nil || !required {
			return err
		}
	}
	if err != nil {
		return fmt.Errorf("read config %s: %w", path, err)
	}
	if err := decodeStrict(data, c); err != nil {
		return fmt.Errorf("%s: %w", c.source, err)
	}
	if err := rejectMCPNulls(data); err != nil {
		return fmt.Errorf("%s: %w", c.source, err)
	}
	for _, name := range c.ProfileNames() {
		if err := checkProfile(c.Profiles[name]); err != nil {
			return fmt.Errorf("%s: profile %q: %w", c.source, name, err)
		}
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
	if c.CompactAt == (Threshold{}) {
		c.CompactAt = Threshold{Tokens: 150000}
	}
	if c.CompactIdleAt == (Threshold{}) {
		c.CompactIdleAt = Threshold{Tokens: 100000}
	}
	if c.Picker == "" {
		c.Picker = DefaultPicker
	}
	c.toolDefaults()
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

func (c *Config) toolDefaults() {
	mac := runtime.GOOS == "darwin"
	setDefault(&c.NotifyCmd, pick(mac, "terminal-notifier -title {title} -message {msg}", "notify-send {title} {msg}"))
	setDefault(&c.HistoryPicker, "{picker}")
	setDefault(&c.SessionPicker, `{picker} --with-nth=2.. --nth=.. --preview '"$AICODE_BIN" -show {1}' --preview-window=right,60%`)
	setDefault(&c.Pager, os.Getenv("PAGER"))
	setDefault(&c.Pager, "less -R")
	setDefault(&c.CopyCmd, pick(mac, "pbcopy", firstOnPath("wl-copy", "xclip -selection clipboard")))
	setDefault(&c.FilePicker, "{picker}")
}

func firstOnPath(commands ...string) string {
	for _, c := range commands {
		if _, err := exec.LookPath(strings.Fields(c)[0]); err == nil {
			return c
		}
	}
	return commands[0]
}

func setDefault(field *string, value string) {
	if *field == "" {
		*field = value
	}
}

func pick(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}

func (c Config) ShowReasoningOn() bool {
	return c.ShowReasoning == nil || *c.ShowReasoning
}

func (c Config) BuiltinSkillsOn() bool {
	return c.BuiltinSkills == nil || *c.BuiltinSkills
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
	return "", fmt.Errorf("no API key: set api_key_shell in %s or %s", ShortHome(DefaultPath()), strings.Join(envs, "/"))
}

func (c *Config) DefaultCacheTTL(headless bool) {
	switch {
	case c.CacheTTL != "":
	case headless:
		c.CacheTTL = "5m"
	default:
		c.CacheTTL = "1h"
	}
}
