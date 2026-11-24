package app

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/paul-nameless/qi/internal/agent"
	"github.com/paul-nameless/qi/internal/config"
	"github.com/paul-nameless/qi/internal/llm"
	"github.com/paul-nameless/qi/internal/mcp"
	"github.com/paul-nameless/qi/internal/session"
	"github.com/paul-nameless/qi/internal/tools"
	"github.com/paul-nameless/qi/internal/ui"
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
	a, runner := buildAgent(config.Config{Model: "m"}, options{}, t.TempDir(), nopProvider{}, display, true, false)
	if a.ReadOnly() || runner.Mode() != agent.ModeYolo {
		t.Fatal("read-only on by default")
	}
	a.SetReadOnly(true)
	if runner.Mode() != agent.ModeReadOnly {
		t.Error("subagent runner does not follow the agent's read-only mode")
	}
	a2, _ := buildAgent(config.Config{Model: "m"}, options{readOnly: true}, t.TempDir(), nopProvider{}, display, true, false)
	if !a2.ReadOnly() {
		t.Error("-read-only flag not applied")
	}
	a3, _ := buildAgent(config.Config{Model: "m", ReadOnly: true, Mode: "project"}, options{}, t.TempDir(), nopProvider{}, display, true, false)
	if !a3.ReadOnly() {
		t.Error("read_only: true in config not applied, or lost to mode")
	}
	a4, _ := buildAgent(config.Config{Model: "m", Mode: "project"}, options{}, t.TempDir(), nopProvider{}, display, true, false)
	a5, _ := buildAgent(config.Config{Model: "m"}, options{project: true}, t.TempDir(), nopProvider{}, display, true, false)
	if a4.Mode() != agent.ModeProject || a5.Mode() != agent.ModeProject {
		t.Errorf("project from config %v, from -project %v", a4.Mode(), a5.Mode())
	}
}

func TestProfileSwitchDropsCLIProvider(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("QI_CONFIG_DIR", dir)
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
	cfg, err := config.Resolve(config.Options{Path: path, Provider: o.provider})
	if err != nil {
		t.Fatal(err)
	}
	a, runner := buildAgent(cfg, o, dir, nopProvider{}, devNullPrinter(t), true, false)
	r := &repl{agent: a, display: devNullPrinter(t), runner: runner, opts: o}
	if err := r.switchProfile("p"); err != nil {
		t.Fatal(err)
	}
	if saved, err := config.Resolve(config.Options{Path: path}); err != nil || saved.Profile != "p" {
		t.Errorf("/profile p not saved as default: %q, %v", saved.Profile, err)
	}
	if a.Config().Provider != "or" || runner.Provider != "or" {
		t.Errorf("CLI -provider came back after /profile: agent %q, subagent default %q", a.Config().Provider, runner.Provider)
	}
}

func TestSubagentModeIsFixedAtLaunch(t *testing.T) {
	display := devNullPrinter(t)
	a, runner := buildAgent(config.Config{Model: "m"}, options{}, t.TempDir(), nopProvider{}, display, true, false)
	r := &repl{agent: a, display: display, runner: runner}
	var seen []string
	for range 3 {
		r.toggleMode()
		seen = append(seen, runner.Mode().String())
	}
	if strings.Join(seen, ",") != "read-only,project,yolo" {
		t.Errorf("shift+tab cycle as seen by subagents: %v", seen)
	}
}

func TestReloadRereadsInstructionsAndSkills(t *testing.T) {
	dir := t.TempDir()
	off := false
	cfg := config.Config{Model: "m", InstructionFile: []string{"CLAUDE.md"}, SkillDirs: []string{".claude/skills"}, BuiltinSkills: &off}
	a, runner := buildAgent(cfg, options{}, dir, nopProvider{}, devNullPrinter(t), true, false)
	r := &repl{agent: a, display: devNullPrinter(t), runner: runner}
	os.WriteFile(dir+"/CLAUDE.md", []byte("rule one"), 0o600)
	r.reload()
	if !strings.Contains(a.System(), "rule one") || len(r.skills) != 0 {
		t.Fatalf("first reload: skills %d, prompt %q", len(r.skills), a.System())
	}
	os.WriteFile(dir+"/CLAUDE.md", []byte("rule two"), 0o600)
	os.MkdirAll(dir+"/.claude/skills/s", 0o755)
	os.WriteFile(dir+"/.claude/skills/s/SKILL.md", []byte("---\nname: s\ndescription: does s\n---\nbody"), 0o600)
	r.reload()
	if sys := a.System(); !strings.Contains(sys, "rule two") || strings.Contains(sys, "rule one") || len(r.skills) != 1 || !strings.Contains(sys, "/SKILL.md) - does s") {
		t.Errorf("second reload: skills %d, prompt %q", len(r.skills), sys)
	}
}

func TestSystemPromptFileFollowsProfileReloadAndFlag(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("QI_CONFIG_DIR", dir)
	write := func(name, text string) { os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600) }
	write("a.md", "prompt a")
	write("b.md", "prompt b")
	path := filepath.Join(dir, "config.json")
	write("config.json", `{"api": "anthropic", "api_key": "k", "model": "m", "builtin_skills": false, "instruction_files": ["none.md"], "skill_dirs": ["none"], "system_prompt_file": "a.md", "profiles": {"b": {"system_prompt_file": "b.md"}, "plain": {"system_prompt_file": null}}}`)
	o := options{configPath: path}
	cfg, err := config.Resolve(config.Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	a, runner := buildAgent(cfg, o, dir, nopProvider{}, devNullPrinter(t), true, false)
	r := &repl{agent: a, display: devNullPrinter(t), runner: runner, opts: o}
	expect := func(step, want string) {
		t.Helper()
		sys := a.System()
		for _, p := range []string{"prompt a", "prompt b", "prompt b2", "prompt cli"} {
			if strings.Contains(sys, p+"\n") != (p == want) {
				t.Fatalf("%s: want %q, prompt starts %q", step, want, sys[:min(len(sys), 40)])
			}
		}
	}
	if err := r.loadPrompt(); err != nil {
		t.Fatal(err)
	}
	expect("start", "prompt a")
	if err := r.switchProfile("b"); err != nil {
		t.Fatal(err)
	}
	expect("/profile b", "prompt b")
	write("b.md", "prompt b2")
	r.reload()
	expect("/reload after editing the file", "prompt b2")
	os.Remove(filepath.Join(dir, "b.md"))
	if err := r.loadPrompt(); err == nil {
		t.Error("a missing prompt file must be reported")
	}
	expect("/reload with the file gone", "prompt b2")
	if err := r.switchProfile("plain"); err != nil {
		t.Fatal(err)
	}
	expect("/profile plain", "")
	cli := filepath.Join(t.TempDir(), "cli.md")
	os.WriteFile(cli, []byte("prompt cli"), 0o600)
	r.opts.systemPromptFile = cli
	if err := r.switchProfile("plain"); err != nil {
		t.Fatal(err)
	}
	expect("-system-prompt-file after /profile", "prompt cli")
}

func TestPastedTempImagesAreKeptWithTheSession(t *testing.T) {
	t.Setenv("QI_SESSION_DIR", t.TempDir())
	a, _ := buildAgent(config.Config{Model: "m"}, options{}, t.TempDir(), nopProvider{}, devNullPrinter(t), true, false)
	s, err := session.Create(session.CreateOptions{Cwd: a.Cwd(), Model: "m", Kind: session.Interactive})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a.Reset(s)
	src := filepath.Join(t.TempDir(), "Screenshot 1.png")
	img := image.NewRGBA(image.Rect(0, 0, 40, 20))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	blocks := userBlocks(a.Cwd(), s.Path, devNullPrinter(t), "look", []string{src})
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
	t.Setenv("QI_CONFIG_DIR", dir)
	path := filepath.Join(dir, "config.json")
	writeFile := func(data string) {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(`{"mcp": {"notes": {"command": "notes", "tools": ["*_note"], "exclude_tools": ["delete_*"]}, "gh": {"command": "gh"}},
  "profiles": {"p": {"mcp": {"gh": null, "db": {"command": "db"}}}}}`)
	parentCfg, err := resolveConfig(options{configPath: path, profile: "p"})
	if err != nil {
		t.Fatal(err)
	}
	parent := parentCfg.MCP
	if !reflect.DeepEqual(sortedNames(parent), []string{"db", "notes"}) {
		t.Fatalf("parent profile servers: %+v", parent)
	}
	writeFile(`{"mcp": {"notes": {"command": "notes"}, "extra": {"command": "extra"}}, "profiles": {"p": {"mcp": {"gh2": {"command": "gh"}}}}}`)
	_, runner := buildAgent(parentCfg, options{configPath: path, profile: "p"}, dir, nopProvider{}, devNullPrinter(t), true, false)
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
	t.Setenv("QI_SUBAGENT", "1")
	cfg, err := resolveConfig(options{configPath: path, profile: "p"})
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
	t.Setenv("QI_SUBAGENT", "")
	if cfg, _ := resolveConfig(options{configPath: path, profile: "p"}); !reflect.DeepEqual(sortedNames(cfg.MCP), []string{"extra", "gh2", "notes"}) {
		t.Errorf("top-level agent took %s: %+v", config.MCPEnv, cfg.MCP)
	}
}

func sortedNames(m map[string]config.MCPServer) []string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func TestProfileSwitchKeepsMCPSetup(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("QI_CONFIG_DIR", dir)
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"api_key": "k", "mcp": {"notes": {"command": "notes", "exclude_tools": ["delete_*"]}}, "profiles": {"none": {"mcp": null}, "more": {"mcp": {"db": {"command": "db"}}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	o := options{configPath: path}
	cfg, err := resolveConfig(o)
	if err != nil {
		t.Fatal(err)
	}
	a, runner := buildAgent(cfg, o, dir, nopProvider{}, devNullPrinter(t), true, false)
	manager := mcp.NewManager(cfg.MCP)
	display, out := capturePrinter(t)
	r := &repl{agent: a, display: display, runner: runner, opts: o, mcp: manager}
	for _, p := range []string{"more", "none"} {
		if err := r.switchProfile(p); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(runner.MCP, cfg.MCP) || !reflect.DeepEqual(manager.Names(), []string{"notes"}) || r.mcp != manager {
			t.Errorf("/profile %s changed the MCP setup: runner %+v, manager %v", p, runner.MCP, manager.Names())
		}
	}
	if n := strings.Count(out.String(), "MCP servers unchanged; restart qi to apply the new profile's MCP setup"); n != 2 {
		t.Errorf("restart note printed %d times:\n%s", n, out.String())
	}
	if !strings.Contains(r.noMCP(), "no MCP servers configured") {
		t.Errorf("profile none: %q", r.noMCP())
	}
	if err := r.switchProfile("more"); err != nil || !strings.Contains(r.noMCP(), "restart to use the ones in this profile") {
		t.Errorf("no restart hint: %q %v", r.noMCP(), err)
	}
}

func TestMalformedHookFileFailsStartup(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("QI_CONFIG_DIR", filepath.Join(home, ".config", "qi"))
	bad := filepath.Join(home, ".claude", "settings.json")
	os.MkdirAll(filepath.Dir(bad), 0o700)
	os.WriteFile(bad, []byte("{\"hooks\": {\"PreToolUse\": [}}"), 0o600)
	cfgPath := filepath.Join(home, "config.json")
	os.WriteFile(cfgPath, []byte(`{"api_key": "k", "base_url": "http://127.0.0.1:1"}`), 0o600)
	err := run(options{query: "hi", configPath: cfgPath})
	if err == nil || err.Error() != "hooks: ~/.claude/settings.json: invalid JSON at line 1; fix it or remove it from hook_files" {
		t.Fatalf("startup with a malformed hook file: %v", err)
	}
	if entries, _ := os.ReadDir(filepath.Join(home, ".local", "share", "qi")); len(entries) > 0 {
		t.Errorf("a failed start left files behind: %v", entries)
	}
	missing := filepath.Join(home, "missing.json")
	h, err := loadHooks([]string{home}, []string{missing, "~/.claude/settings.local.json"}, true, devNullPrinter(t))
	if err != nil || !h.Empty() {
		t.Errorf("missing hook files: %v", err)
	}
}

func TestNoSandboxWarning(t *testing.T) {
	display, out := capturePrinter(t)
	warnNoSandbox(display, agent.ModeReadOnly)
	warnNoSandbox(display, agent.ModeProject)
	warnNoSandbox(display, agent.ModeYolo)
	got := out.String()
	if tools.HasSandbox() == strings.Contains(got, noSandboxNote) || tools.HasSandbox() == strings.Contains(got, noProjectSandboxNote) {
		t.Errorf("sandbox %v, output %q", tools.HasSandbox(), got)
	}
}

func TestBuildAgentSetsSubagentLabels(t *testing.T) {
	_, runner := buildAgent(config.Config{Model: "m"}, options{}, t.TempDir(), nopProvider{}, devNullPrinter(t), true, false)
	if got := runner.Label(llm.Block{Type: llm.BlockToolUse, Name: "bash", Input: json.RawMessage(`{"command":"ls"}`)}); got != "bash ls" {
		t.Errorf("label %q", got)
	}
	if got := runner.Label(llm.Block{Type: llm.BlockToolUse, Name: "run", Input: json.RawMessage(`{"code":"await read(\"a\")"}`)}); got != `run await read("a")` {
		t.Errorf("run label %q", got)
	}
	if got := runner.Label(llm.Block{Type: llm.BlockToolUse, Name: "nope", Input: json.RawMessage(`{}`)}); got != "nope" {
		t.Errorf("unknown tool label %q", got)
	}
}

func TestProfileModeAppliesOnSwitch(t *testing.T) {
	t.Setenv("QI_SESSION_DIR", t.TempDir())
	path := filepath.Join(t.TempDir(), "config.json")
	data := `{"api_key": "k", "model": "m", "profiles": {"wild": {"mode": "project"}, "plain": {}, "careful": {"read_only": true}}}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Resolve(config.Options{Path: path, Profile: "wild"})
	if err != nil {
		t.Fatal(err)
	}
	display, _ := capturePrinter(t)
	o := options{configPath: path}
	a, runner := buildAgent(cfg, o, t.TempDir(), scripted(), display, true, false)
	r := &repl{agent: a, display: display, hooks: &agent.Hooks{}, runner: runner, opts: o}
	if err := r.newSession(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.closeResources() })
	steps := []struct{ profile, want string }{{"", "project"}, {"plain", "project"}, {"careful", "read-only"}, {"wild", "project"}}
	for _, s := range steps {
		if s.profile != "" {
			if err := r.switchProfile(s.profile); err != nil {
				t.Fatal(err)
			}
		}
		if got := r.agent.Mode().String(); got != s.want {
			t.Errorf("after %q: mode %s, want %s", s.profile, got, s.want)
		}
	}
	if _, err := config.Resolve(config.Options{Path: path, Profile: "wild"}); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(t.TempDir(), "bad.json")
	os.WriteFile(bad, []byte(`{"model": "m", "profiles": {"x": {"mode": "dir"}}}`), 0o600)
	if _, err := config.Resolve(config.Options{Path: bad, Profile: "x"}); err == nil || !strings.Contains(err.Error(), "mode") {
		t.Errorf("an unknown mode should be rejected: %v", err)
	}
}
