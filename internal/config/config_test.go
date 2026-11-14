package config

import (
	"os"
	"path/filepath"
	"testing"
)

const sample = `
providers:
  openrouter:
    base_url: https://openrouter.ai/api/v1
    api_key: or-key
  anthropic:
    api: anthropic
    api_key: ant-key
model: base-model
reasoning_effort: low
default_profile: smart
profiles:
  smart:
    provider: openrouter
    model: openai/gpt-6.1-sol
    reasoning_effort: medium
    subagent_models:
      - name: luna
        model: openai/gpt-6-luna
        use_for: cheap
      - name: haiku
        provider: anthropic
        model: claude-haiku-5-5
        use_for: claude
  glm:
    provider: openrouter
    model: z-ai/glm-5.3
  local:
    provider: openrouter
    base_url: http://localhost:8080/v1
    model: qwen
`

func TestProfiles(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AICODE_CONFIG_DIR", dir)
	path := filepath.Join(dir, "config.yml")
	os.WriteFile(path, []byte(sample), 0o600)
	os.WriteFile(filepath.Join(dir, "legacy.yml"), []byte("model: legacy-model\nprovider: claude\n"), 0o600)
	v1 := filepath.Join(dir, "v1.yml")
	os.WriteFile(v1, []byte("api_key_shell: \"pass show x\"\nprovider: \"claude\"\nmodel: claude-sonnet-4-20250514\n"), 0o600)
	if c, err := Load(v1, ""); err != nil || c.API != "anthropic" || c.APIKeyShell != "pass show x" {
		t.Errorf("v1 config must keep its api_key_shell: %+v %v", c, err)
	}

	c, err := Load(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if c.Profile != "smart" || c.Model != "openai/gpt-6.1-sol" || c.ReasoningEffort != "medium" || c.BaseURL != "https://openrouter.ai/api/v1" || c.APIKey != "or-key" || len(c.SubagentModels) != 2 {
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
	c, err = Load(path, "legacy")
	if err != nil || c.Model != "legacy-model" || c.API != "anthropic" {
		t.Errorf("file profile: %+v %v", c, err)
	}
	if _, err := Load(path, "nope"); err == nil {
		t.Error("unknown profile accepted")
	}
	if c, _ := Load(path, "local"); c.BaseURL != "http://localhost:8080/v1" || c.APIKey != "or-key" {
		t.Errorf("profile base_url should win over its provider: %+v", c)
	}
	os.WriteFile(filepath.Join(dir, "filelocal.yml"), []byte("provider: openrouter\nbase_url: http://localhost:9/v1\nmodel: q\n"), 0o600)
	fl, err := Load(path, "filelocal")
	if err != nil || fl.BaseURL != "http://localhost:9/v1" || fl.APIKey != "or-key" {
		t.Errorf("file profile base_url should win: %+v %v", fl, err)
	}
	if err := fl.UseProvider("openrouter"); err != nil || fl.BaseURL != "http://localhost:9/v1" {
		t.Errorf("re-selecting the current provider must keep the profile url: %+v", fl)
	}
	if names := c.ProfileNames(); len(names) != 3 || names[0] != "glm" {
		t.Errorf("names %v", names)
	}
}

func TestUseProviderBuiltinsResetConnection(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AICODE_CONFIG_DIR", dir)
	path := filepath.Join(dir, "config.yml")
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
	t.Setenv("AICODE_CONFIG_DIR", dir)
	path := filepath.Join(dir, "c.yml")
	os.WriteFile(path, []byte("api: anthropic\napi_key: sk-ant\n"), 0o600)
	c, err := Resolve(path, "", "openai", "", "")
	if err != nil || c.API != "openai" || c.APIKey != "" || c.Model != "gpt-6-luna" {
		t.Errorf("openai override kept anthropic settings: %+v %v", c, err)
	}
	c, err = Resolve(path, "", "claude", "", "high")
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
