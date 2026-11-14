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
