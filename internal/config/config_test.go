package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const sample = `{
  "providers": {
    "openrouter": {"base_url": "https://openrouter.ai/api/v1", "api_key": "or-key"},
    "anthropic": {"api": "anthropic", "api_key": "ant-key"}
  },
  "model": "base-model",
  "reasoning_effort": "low",
  "default_profile": "smart",
  "profiles": {
    "smart": {
      "provider": "openrouter",
      "model": "openai/gpt-6.1-sol",
      "reasoning_effort": "medium",
      "subagent_profiles": ["glm", "local"]
    },
    "glm": {"provider": "openrouter", "model": "z-ai/glm-5.3"},
    "local": {"provider": "openrouter", "base_url": "http://localhost:8080/v1", "model": "qwen"}
  }
}
`

func TestProfiles(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("QI_CONFIG_DIR", dir)
	path := filepath.Join(dir, "config.json")
	os.WriteFile(path, []byte(sample), 0o600)
	os.WriteFile(filepath.Join(dir, "legacy.json"), []byte(`{"model": "legacy-model", "provider": "claude"}`), 0o600)
	v1 := filepath.Join(dir, "v1.json")
	os.WriteFile(v1, []byte(`{"api_key_shell": "pass show x", "provider": "claude", "model": "claude-sonnet-4-20250514"}`), 0o600)
	if c, err := Load(v1, ""); err != nil || c.API != "anthropic" || c.APIKeyShell != "pass show x" {
		t.Errorf("v1 config must keep its api_key_shell: %+v %v", c, err)
	}

	c, err := Load(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if c.Profile != "smart" || c.Model != "openai/gpt-6.1-sol" || c.ReasoningEffort != "medium" || c.BaseURL != "https://openrouter.ai/api/v1" || c.APIKey != "or-key" || len(c.SubagentChoices()) != 2 {
		t.Errorf("default profile: %+v", c)
	}
	c, err = Load(path, "glm")
	if err != nil {
		t.Fatal(err)
	}
	if c.Model != "z-ai/glm-5.3" || c.ReasoningEffort != "low" || c.API != "openai" {
		t.Errorf("glm profile: %+v", c)
	}
	if err := c.UseProvider("anthropic"); err != nil || c.API != "anthropic" || c.APIKey != "ant-key" || c.BaseURL != "" {
		t.Errorf("switch provider: %+v %v", c, err)
	}
	_, err = Load(path, "legacy")
	wantErr(t, err, `unknown profile "legacy"`, "profiles: glm, local, smart")
	local, err := Load(path, "local")
	if err != nil || local.BaseURL != "http://localhost:8080/v1" || local.APIKey != "or-key" {
		t.Errorf("profile base_url should win over its provider: %+v %v", local, err)
	}
	if err := local.UseProvider("openrouter"); err != nil || local.BaseURL != "http://localhost:8080/v1" {
		t.Errorf("re-selecting the current provider must keep the profile url: %+v", local)
	}
	if names := c.ProfileNames(); len(names) != 3 || names[0] != "glm" {
		t.Errorf("names %v", names)
	}
}

func TestSubagentChoices(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("QI_CONFIG_DIR", dir)
	path := writeConfig(t, dir, "config.json", `{
  "providers": {"anth": {"api": "anthropic", "api_key": "k"}},
  "reasoning_effort": "low",
  "default_profile": "main",
  "profiles": {
    "main": {"model": "main-model", "reasoning_effort": "high", "use_for": "general", "subagent_profiles": ["fast", "main", "claude"]},
    "fast": {"model": "fast-model", "use_for": "search"},
    "claude": {"provider": "anth", "reasoning_effort": "max"},
    "solo": {"model": "solo-model"}
  }
}`)
	c, err := Resolve(Options{Path: path, Model: "cli-model", Effort: "minimal"})
	if err != nil {
		t.Fatal(err)
	}
	want := []SubagentChoice{
		{Name: "fast", Model: "fast-model", Effort: "low", UseFor: "search"},
		{Name: "main", Model: "main-model", Effort: "high", UseFor: "general"},
		{Name: "claude", Model: "claude-sonnet-5-5", Effort: "max"},
	}
	if got := c.SubagentChoices(); !slices.Equal(got, want) {
		t.Errorf("choices %+v", got)
	}
	c, err = Resolve(Options{Path: path, Profile: "solo", Model: "cli-model", Effort: "minimal"})
	if err != nil {
		t.Fatal(err)
	}
	if got := c.SubagentChoices(); !slices.Equal(got, []SubagentChoice{{Name: SelfChoice, Model: "cli-model", Effort: "low", Self: true}}) {
		t.Errorf("self choice %+v", got)
	}
	if got := (Config{Model: "m", ReasoningEffort: "high"}).SubagentChoices(); !slices.Equal(got, []SubagentChoice{{Name: SelfChoice, Model: "m", Effort: "high", Self: true}}) {
		t.Errorf("self choice of an unresolved config %+v", got)
	}
	if s := (SubagentChoice{Name: "a", Model: "m"}).String() + " " + want[0].String(); s != "a (m) fast (fast-model, effort low)" {
		t.Errorf("String %q", s)
	}
}

func TestSubagentProfilesMustExist(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("QI_CONFIG_DIR", dir)
	path := writeConfig(t, dir, "config.json", `{"subagent_profiles": ["a", "haiko"], "profiles": {"a": {}, "b": {}}}`)
	_, err := Load(path, "")
	wantErr(t, err, path+`: key "subagent_profiles.1": unknown profile "haiko" (profiles: a, b)`)
	path = writeConfig(t, dir, "config.json", `{"profiles": {"a": {}, "b": {"subagent_profiles": ["zz"]}}}`)
	_, err = Load(path, "b")
	wantErr(t, err, path+`: profile "b": key "subagent_profiles.0": unknown profile "zz" (profiles: a, b)`)
	path = writeConfig(t, dir, "config.json", `{"subagent_profiles": ["x"]}`)
	_, err = Load(path, "")
	wantErr(t, err, `unknown profile "x" (profiles: none defined)`)
	path = writeConfig(t, dir, "config.json", `{"subagent_profiles": ["bad"], "profiles": {"bad": {"provider": "nope"}}}`)
	_, err = Load(path, "")
	wantErr(t, err, `profile "bad": key "provider": unknown provider "nope"`)
}

func TestUseProviderBuiltinsResetConnection(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("QI_CONFIG_DIR", dir)
	path := filepath.Join(dir, "config.json")
	os.WriteFile(path, []byte(sample), 0o600)
	c, err := Load(path, "smart")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.UseProvider("claude"); err != nil || c.API != "anthropic" || c.BaseURL != "" || c.APIKey != "" {
		t.Errorf("claude: %+v %v", c, err)
	}
	if err := c.UseProvider("openai"); err != nil || c.API != "openai" {
		t.Errorf("openai: %+v %v", c, err)
	}
	if err := c.UseProvider("nope"); err == nil {
		t.Error("unknown provider accepted")
	}
}

func TestRuntimeBuiltinSwitchDropsOldKey(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("QI_CONFIG_DIR", dir)
	path := filepath.Join(dir, "c.json")
	os.WriteFile(path, []byte(`{"api": "anthropic", "api_key": "sk-ant"}`), 0o600)
	c, err := Resolve(Options{Path: path, Provider: "openai"})
	if err != nil || c.API != "openai" || c.APIKey != "" || c.Model != "gpt-6-luna" {
		t.Errorf("openai override kept anthropic settings: %+v %v", c, err)
	}
	c, err = Resolve(Options{Path: path, Provider: "claude", Effort: "high"})
	if err != nil || c.API != "anthropic" || c.Model != "claude-sonnet-5-5" || c.ReasoningEffort != "high" {
		t.Errorf("claude override: %+v %v", c, err)
	}
}

func TestDefaultCacheTTL(t *testing.T) {
	for _, c := range []struct {
		set      string
		headless bool
		want     string
	}{{"", true, "5m"}, {"", false, "1h"}, {"1h", true, "1h"}, {"5m", false, "5m"}} {
		cfg := Config{CacheTTL: c.set}
		cfg.DefaultCacheTTL(c.headless)
		if cfg.CacheTTL != c.want {
			t.Errorf("set %q headless %v: got %q, want %q", c.set, c.headless, cfg.CacheTTL, c.want)
		}
	}
}

func TestXDGDirs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cases := []struct {
		qi, xdgConfig, xdgData string
		dir, data              string
	}{
		{"", "", "", filepath.Join(home, ".config", "qi"), filepath.Join(home, ".local", "share", "qi")},
		{"", "/x/conf", "/x/data", "/x/conf/qi", "/x/data/qi"},
		{"", "relative", "relative", filepath.Join(home, ".config", "qi"), filepath.Join(home, ".local", "share", "qi")},
		{"/q", "/x/conf", "/x/data", "/q", "/x/data/qi"},
	}
	for _, c := range cases {
		t.Setenv("QI_CONFIG_DIR", c.qi)
		t.Setenv("XDG_CONFIG_HOME", c.xdgConfig)
		t.Setenv("XDG_DATA_HOME", c.xdgData)
		if got := Dir(); got != c.dir {
			t.Errorf("%+v: Dir() = %q", c, got)
		}
		if got := DataDir(); got != c.data {
			t.Errorf("%+v: DataDir() = %q", c, got)
		}
	}
}

func TestEnvKeyFollowsHost(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "oa")
	t.Setenv("OPENROUTER_API_KEY", "or")
	t.Setenv("ANTHROPIC_API_KEY", "an")
	cases := []struct {
		cfg  Config
		want string
	}{
		{Config{API: "anthropic", BaseURL: "https://api.anthropic.com"}, "an"},
		{Config{BaseURL: "https://openrouter.ai/api/v1"}, "or"},
		{Config{BaseURL: "https://eu.openrouter.ai/api/v1"}, "or"},
		{Config{BaseURL: "https://evil.example/openrouter.ai/v1"}, "oa"},
		{Config{BaseURL: "https://openrouter.ai.evil.example/v1"}, "oa"},
		{Config{BaseURL: "http://localhost:8080/v1"}, "oa"},
		{Config{}, "oa"},
	}
	for _, c := range cases {
		if got, err := c.cfg.ResolveAPIKey(); err != nil || got != c.want {
			t.Errorf("%+v: %q %v", c.cfg.BaseURL, got, err)
		}
	}
	t.Setenv("OPENAI_API_KEY", "")
	if _, err := (Config{BaseURL: "http://localhost:8080/v1"}).ResolveAPIKey(); err == nil || !strings.Contains(err.Error(), "OPENAI_API_KEY") {
		t.Errorf("OpenRouter key sent to another host: %v", err)
	}
}

func TestAPIKeyShellErrorsShowStderr(t *testing.T) {
	cases := map[string]string{
		"echo nope >&2 | head -n 1": "empty key: nope",
		"echo boom >&2; exit 3":     "exit status 3: boom",
	}
	for shell, want := range cases {
		_, err := Config{APIKeyShell: shell}.ResolveAPIKey()
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v, want %q", shell, err, want)
		}
	}
	if key, err := (Config{APIKeyShell: "echo warn >&2; echo k1"}).ResolveAPIKey(); err != nil || key != "k1" {
		t.Errorf("stderr with a key: %q %v", key, err)
	}
}

func TestSystemPromptFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("QI_CONFIG_DIR", dir)
	writeConfig(t, dir, "global.md", "global prompt\n")
	writeConfig(t, dir, "fast.md", "fast prompt")
	cli := writeConfig(t, t.TempDir(), "cli.md", "cli prompt")
	path := writeConfig(t, dir, "c.json", `{"api": "anthropic", "api_key": "k", "system_prompt_file": "global.md", "profiles": {"fast": {"system_prompt_file": "fast.md"}, "plain": {}}}`)
	for _, c := range []struct{ profile, file, want string }{
		{"plain", "", "global prompt"},
		{"fast", "", "fast prompt"},
		{"fast", cli, "cli prompt"},
	} {
		got, err := Resolve(Options{Path: path, Profile: c.profile, SystemPromptFile: c.file})
		if err != nil || got.SystemPrompt != c.want {
			t.Errorf("%s %q: got %q, %v", c.profile, c.file, got.SystemPrompt, err)
		}
	}
	missing := writeConfig(t, dir, "m.json", `{"api": "anthropic", "api_key": "k", "system_prompt_file": "nope.md"}`)
	_, err := Resolve(Options{Path: missing})
	wantErr(t, err, "system_prompt_file", "nope.md")
}
