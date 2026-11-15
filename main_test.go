package main

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/paul-nameless/aicode/internal/config"
	"github.com/paul-nameless/aicode/internal/llm"
	"github.com/paul-nameless/aicode/internal/session"
	"github.com/paul-nameless/aicode/internal/tools"
	"github.com/paul-nameless/aicode/internal/ui"
)

type nopProvider struct{}

func (nopProvider) Stream(context.Context, llm.Request, func(llm.Delta)) (llm.Message, llm.Usage, error) {
	return llm.Message{}, llm.Usage{}, nil
}

func devNullPrinter(t *testing.T) *ui.Printer {
	f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return ui.NewPrinter(f)
}

func TestReadOnlyWiring(t *testing.T) {
	display := devNullPrinter(t)
	a, runner, _ := buildAgent(config.Config{Model: "m"}, options{}, t.TempDir(), nopProvider{}, display, true, false)
	if a.Env.ReadOnly() || runner.ReadOnly() {
		t.Fatal("read-only on by default")
	}
	a.SetReadOnly(true)
	if !a.Env.ReadOnly() || !runner.ReadOnly() {
		t.Error("env or subagent runner does not follow the agent's read-only mode")
	}
	a2, _, _ := buildAgent(config.Config{Model: "m"}, options{readOnly: true}, t.TempDir(), nopProvider{}, display, true, false)
	if !a2.Env.ReadOnly() {
		t.Error("-read-only flag not applied")
	}
	a3, _, _ := buildAgent(config.Config{Model: "m", ReadOnly: true}, options{}, t.TempDir(), nopProvider{}, display, true, false)
	if !a3.Env.ReadOnly() {
		t.Error("read_only: true in config not applied")
	}
}

func TestModelSwitchKeepsSubagentsOnProfileProvider(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AICODE_CONFIG_DIR", dir)
	path := dir + "/config.json"
	os.WriteFile(path, []byte(`{
  "providers": {
    "or": {"base_url": "https://openrouter.ai/api/v1", "api_key": "k1"},
    "anth": {"api": "anthropic", "api_key": "k2"}
  },
  "default_profile": "p",
  "profiles": {
    "p": {
      "provider": "or",
      "model": "openai/gpt-6-luna",
      "subagent_models": [
        {"name": "luna", "model": "openai/gpt-6-luna", "use_for": "x"},
        {"name": "haiku", "provider": "anth", "model": "claude-haiku-5-5", "use_for": "y"}
      ]
    },
    "q": {"provider": "anth", "model": "claude-haiku-5-5"}
  }
}
`), 0o600)
	o := options{configPath: path, provider: "anth"}
	cfg, err := config.Resolve(path, "", o.provider, "", "")
	if err != nil {
		t.Fatal(err)
	}
	a, runner, _ := buildAgent(cfg, o, dir, nopProvider{}, devNullPrinter(t), true, false)
	a.Registry = tools.NewRegistry()
	r := &repl{agent: a, display: devNullPrinter(t), runner: runner, opts: o}
	if err := r.switchProfile("p"); err != nil {
		t.Fatal(err)
	}
	if err := r.switchModel("haiku"); err != nil {
		t.Fatal(err)
	}
	if a.Cfg.Provider != "anth" || runner.Provider != "or" {
		t.Errorf("after /model haiku: agent provider %q, subagent default %q", a.Cfg.Provider, runner.Provider)
	}
	if err := r.switchModel("some-raw-model"); err != nil {
		t.Fatal(err)
	}
	if a.Cfg.Provider != "or" {
		t.Errorf("CLI -provider came back after /profile: %q", a.Cfg.Provider)
	}
}

func TestSubagentModeIsFixedAtLaunch(t *testing.T) {
	display := devNullPrinter(t)
	a, runner, _ := buildAgent(config.Config{Model: "m"}, options{}, t.TempDir(), nopProvider{}, display, true, false)
	r := &repl{agent: a, display: display, runner: runner}
	r.toggleMode()
	if !runner.ReadOnly() {
		t.Error("subagents launched after switching to read-only would not get -read-only")
	}
}

func TestReloadRereadsInstructionsAndSkills(t *testing.T) {
	dir := t.TempDir()
	off := false
	cfg := config.Config{Model: "m", InstructionFile: []string{"CLAUDE.md"}, SkillDirs: []string{".claude/skills"}, BuiltinSkills: &off}
	a, runner, _ := buildAgent(cfg, options{}, dir, nopProvider{}, devNullPrinter(t), true, false)
	a.Registry = tools.NewRegistry()
	r := &repl{agent: a, display: devNullPrinter(t), runner: runner}
	os.WriteFile(dir+"/CLAUDE.md", []byte("rule one"), 0o600)
	r.reload()
	if !strings.Contains(a.System, "rule one") || len(r.skills) != 0 {
		t.Fatalf("first reload: skills %d, prompt %q", len(r.skills), a.System)
	}
	os.WriteFile(dir+"/CLAUDE.md", []byte("rule two"), 0o600)
	os.MkdirAll(dir+"/.claude/skills/s", 0o755)
	os.WriteFile(dir+"/.claude/skills/s/SKILL.md", []byte("---\nname: s\ndescription: does s\n---\nbody"), 0o600)
	r.reload()
	if !strings.Contains(a.System, "rule two") || strings.Contains(a.System, "rule one") || len(r.skills) != 1 || !strings.Contains(a.System, "- s: does s") {
		t.Errorf("second reload: skills %d, prompt %q", len(r.skills), a.System)
	}
}

func TestPastedTempImagesAreKeptWithTheSession(t *testing.T) {
	t.Setenv("AICODE_SESSION_DIR", t.TempDir())
	a, _, _ := buildAgent(config.Config{Model: "m"}, options{}, t.TempDir(), nopProvider{}, devNullPrinter(t), true, false)
	s, err := session.Create(a.Cwd, "", "m", "interactive", false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a.Session = s
	src := filepath.Join(t.TempDir(), "Screenshot 1.png")
	img := image.NewRGBA(image.Rect(0, 0, 40, 20))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	blocks := userBlocks(a, "look", []string{src})
	if len(blocks) != 3 || blocks[1].Type != llm.BlockImage || blocks[1].Width != 40 {
		t.Fatalf("blocks: %+v", blocks)
	}
	kept := blocks[1].Source
	if !strings.HasPrefix(kept, strings.TrimSuffix(s.Path, ".jsonl")+"-images/") {
		t.Fatalf("image not kept with the session: %s", kept)
	}
	if _, err := os.Stat(kept); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(blocks[2].Text, kept+" 40×20") {
		t.Errorf("note: %q", blocks[2].Text)
	}
}

func TestSubagentKeepsParentMCPFilters(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AICODE_CONFIG_DIR", dir)
	parent := map[string]config.MCPServer{"notes": {Command: "notes", Tools: []string{"*_note"}, ExcludeTools: []string{"delete_*"}}}
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"mcp": {"notes": {"command": "notes"}, "extra": {"command": "extra"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, runner, _ := buildAgent(config.Config{Model: "m", MCP: parent}, options{}, dir, nopProvider{}, devNullPrinter(t), true, false)
	env, err := runner.Env()
	if err != nil {
		t.Fatal(err)
	}
	fromParent := ""
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, config.MCPEnv+"="); ok {
			fromParent = v
		}
	}
	t.Setenv(config.MCPEnv, fromParent)
	t.Setenv("AICODE_SUBAGENT", "1")
	cfg, err := resolveConfig(options{configPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.MCP, parent) || cfg.MCP["notes"].ToolEnabled("delete_note") {
		t.Errorf("subagent mcp: %+v", cfg.MCP)
	}
	if _, set := os.LookupEnv(config.MCPEnv); set {
		t.Error(config.MCPEnv + " left for tools and MCP servers")
	}
	t.Setenv(config.MCPEnv, fromParent)
	t.Setenv("AICODE_SUBAGENT", "")
	if cfg, _ := resolveConfig(options{configPath: path}); len(cfg.MCP) != 2 {
		t.Errorf("top-level agent took %s: %+v", config.MCPEnv, cfg.MCP)
	}
}
