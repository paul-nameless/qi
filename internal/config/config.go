package config

import (
	"bytes"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"net/url"
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

const DefaultPicker = "builtin"

type Config struct {
	Schema           string    `json:"$schema"`
	Model            string    `json:"model"`
	API              string    `json:"api"`
	Provider         string    `json:"provider"`
	BaseURL          string    `json:"base_url"`
	APIKey           string    `json:"api_key"`
	APIKeyShell      string    `json:"api_key_shell"`
	ReasoningEffort  string    `json:"reasoning_effort"`
	MaxTokens        int       `json:"max_tokens"`
	CacheControl     *bool     `json:"cache_control"`
	CacheTTL         string    `json:"cache_ttl"`
	CompactAt        Threshold `json:"compact_at"`
	CompactIdleAt    Threshold `json:"compact_idle_at"`
	ContextWindow    Tokens    `json:"context_window"`
	MaxRounds        int       `json:"max_rounds"`
	ShowReasoning    *bool     `json:"show_reasoning"`
	TerminalImages   *bool     `json:"terminal_images"`
	RunCode          *bool     `json:"run_code"`
	ReadOnly         bool      `json:"read_only"`
	ReadOnlyAllow    []string  `json:"read_only_allow"`
	ProjectWritable  []string  `json:"project_writable"`
	Mode             string    `json:"mode"`
	BashTimeout      int       `json:"bash_timeout"`
	SystemPromptFile string    `json:"system_prompt_file"`
	SystemPrompt     string    `json:"-"`
	systemPromptPath string
	SubagentModels   []SubagentModel           `json:"subagent_models"`
	SubagentEfforts  []string                  `json:"subagent_efforts"`
	InstructionFile  []string                  `json:"instruction_files"`
	SkillDirs        []string                  `json:"skill_dirs"`
	HookFiles        []string                  `json:"hook_files"`
	MCP              map[string]MCPServer      `json:"mcp"`
	Picker           string                    `json:"picker"`
	NotifyCmd        string                    `json:"notify_cmd"`
	FilePicker       string                    `json:"file_picker"`
	HistoryPicker    string                    `json:"history_picker"`
	SessionPicker    string                    `json:"session_picker"`
	Pager            string                    `json:"pager"`
	CopyCmd          string                    `json:"copy_cmd"`
	BuiltinSkills    *bool                     `json:"builtin_skills"`
	Providers        map[string]Provider       `json:"providers"`
	Profiles         map[string]jsontext.Value `json:"profiles"`
	DefaultProfile   string                    `json:"default_profile"`
	Profile          string                    `json:"-"`
	profileRaw       []byte
	globalMCP        map[string]MCPServer
	source           string
}

func Dir() string {
	if d := os.Getenv("QI_CONFIG_DIR"); d != "" {
		return d
	}
	return filepath.Join(xdgDir("XDG_CONFIG_HOME", ".config"), "qi")
}

func DataDir() string {
	return filepath.Join(xdgDir("XDG_DATA_HOME", filepath.Join(".local", "share")), "qi")
}

func xdgDir(env, fallback string) string {
	if d := os.Getenv(env); filepath.IsAbs(d) {
		return d
	}
	return filepath.Join(Home(), fallback)
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

type Options struct {
	Path             string
	Profile          string
	Provider         string
	Model            string
	Effort           string
	SystemPromptFile string
}

func Load(path, profile string) (Config, error) {
	return Resolve(Options{Path: path, Profile: profile})
}

func DefaultPath() string {
	return filepath.Join(Dir(), "config.json")
}

func Resolve(o Options) (Config, error) {
	var c Config
	path, profile := o.Path, o.Profile
	if path == "" {
		path = DefaultPath()
	}
	path = ExpandHome(path)
	c.source = "config " + path
	if err := mergeFile(&c, path, path != DefaultPath()); err != nil {
		return c, err
	}
	c.globalMCP = c.MCP
	if err := c.validateGlobal(); err != nil {
		return c, fmt.Errorf("%s: %w", c.source, err)
	}
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
	if o.Provider != "" {
		if err := c.UseProvider(o.Provider); err != nil {
			return c, fmt.Errorf("-provider: %w", err)
		}
	}
	if o.Model != "" {
		c.SetModel(o.Model)
	}
	if o.Effort != "" {
		c.ReasoningEffort = o.Effort
	}
	where := c.source + `: key "system_prompt_file"`
	if o.SystemPromptFile != "" {
		c.SystemPromptFile, where = o.SystemPromptFile, "-system-prompt-file"
	}
	if err := c.loadSystemPrompt(filepath.Dir(path)); err != nil {
		return c, fmt.Errorf("%s: %w", where, err)
	}
	c.applyDefaults()
	if err := c.validate(); err != nil {
		return c, fmt.Errorf("%s: %w", c.source, err)
	}
	return c, nil
}

func Check(path, profile string) error {
	c, err := Load(path, profile)
	if err != nil {
		return err
	}
	for _, name := range c.ProfileNames() {
		if _, err := Load(path, name); err != nil {
			return err
		}
	}
	return nil
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
	raw, ok := c.Profiles[name]
	if !ok {
		known := "none defined"
		if names := c.ProfileNames(); len(names) > 0 {
			known = strings.Join(names, ", ")
		}
		return nil, "", fmt.Errorf("%s: unknown profile %q (profiles: %s)", c.source, name, known)
	}
	return raw, fmt.Sprintf("%s: profile %q", c.source, name), nil
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
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) && !required {
		return nil
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

func (c Config) validateGlobal() error {
	c.applyDefaults()
	return c.validate()
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
	setDefault(&c.SessionPicker, pick(c.Picker == DefaultPicker, "{picker}", `{picker} --with-nth=2.. --nth=.. --preview '"$QI_BIN" -show {1}' --preview-window=right,60%`))
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

func (c Config) TerminalImagesOn() bool {
	return c.TerminalImages == nil || *c.TerminalImages
}

func (c Config) RunCodeOn() bool {
	return c.RunCode == nil || *c.RunCode
}

func (c Config) BuiltinSkillsOn() bool {
	return c.BuiltinSkills == nil || *c.BuiltinSkills
}

func (c Config) ResolveAPIKey() (string, error) {
	if c.APIKey != "" {
		return c.APIKey, nil
	}
	if c.APIKeyShell != "" {
		cmd := exec.Command("sh", "-c", c.APIKeyShell)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			msg = ": " + msg
		}
		if err != nil {
			return "", fmt.Errorf("api_key_shell failed: %w%s", err, msg)
		}
		key := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
		if key == "" {
			return "", errors.New("api_key_shell returned an empty key" + msg)
		}
		return key, nil
	}
	env := c.keyEnv()
	if v := os.Getenv(env); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("no API key: set api_key_shell in %s or %s", ShortHome(DefaultPath()), env)
}

func (c Config) keyEnv() string {
	switch {
	case c.API == "anthropic":
		return "ANTHROPIC_API_KEY"
	case isOpenRouter(c.BaseURL):
		return "OPENROUTER_API_KEY"
	}
	return "OPENAI_API_KEY"
}

func isOpenRouter(baseURL string) bool {
	u, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "openrouter.ai" || strings.HasSuffix(host, ".openrouter.ai")
}

func (c Config) ProfileSetsMode() bool {
	if c.profileRaw == nil {
		return false
	}
	var p struct {
		Mode     string `json:"mode"`
		ReadOnly bool   `json:"read_only"`
	}
	if json.Unmarshal(c.profileRaw, &p) != nil {
		return false
	}
	return p.ReadOnly || p.Mode != ""
}

func (c Config) StartMode() Mode {
	if c.ReadOnly {
		return ModeReadOnly
	}
	return ParseMode(c.Mode)
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

func (c *Config) loadSystemPrompt(dir string) error {
	c.systemPromptPath = ""
	if c.SystemPromptFile == "" {
		return nil
	}
	c.systemPromptPath = ExpandHome(c.SystemPromptFile)
	if !filepath.IsAbs(c.systemPromptPath) {
		c.systemPromptPath = filepath.Join(dir, c.systemPromptPath)
	}
	return c.ReadSystemPrompt()
}

func (c *Config) ReadSystemPrompt() error {
	if c.systemPromptPath == "" {
		return nil
	}
	data, err := os.ReadFile(c.systemPromptPath)
	if err != nil {
		return err
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return fmt.Errorf("%s is empty", c.systemPromptPath)
	}
	c.SystemPrompt = text
	return nil
}
