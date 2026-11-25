package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
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
	if a.Mode() == config.ModeReadOnly || runner.Mode() != config.ModeYolo {
		t.Fatal("read-only on by default")
	}
	a.SetMode(config.ModeReadOnly)
	if runner.Mode() != config.ModeReadOnly {
		t.Error("subagent runner does not follow the agent's read-only mode")
	}
	a2, _ := buildAgent(config.Config{Model: "m"}, options{readOnly: true}, t.TempDir(), nopProvider{}, display, true, false)
	if a2.Mode() != config.ModeReadOnly {
		t.Error("-read-only flag not applied")
	}
	a3, _ := buildAgent(config.Config{Model: "m", ReadOnly: true, Mode: "project"}, options{}, t.TempDir(), nopProvider{}, display, true, false)
	if a3.Mode() != config.ModeReadOnly {
		t.Error("read_only: true in config not applied, or lost to mode")
	}
	a4, _ := buildAgent(config.Config{Model: "m", Mode: "project"}, options{}, t.TempDir(), nopProvider{}, display, true, false)
	a5, _ := buildAgent(config.Config{Model: "m"}, options{project: true}, t.TempDir(), nopProvider{}, display, true, false)
	if a4.Mode() != config.ModeProject || a5.Mode() != config.ModeProject {
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
      "subagent_profiles": ["p", "q"]
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

func TestSubagentModeFlagWinsOverProfile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("QI_CONFIG_DIR", dir)
	path := filepath.Join(dir, "config.json")
	os.WriteFile(path, []byte(`{"model": "m", "subagent_profiles": ["wild", "locked", "proj"], "profiles": {"wild": {"mode": "yolo"}, "locked": {"read_only": true}, "proj": {"mode": "project"}}}`), 0o600)
	cfg, err := config.Resolve(config.Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		profile string
		parent  config.Mode
	}{{"wild", config.ModeReadOnly}, {"wild", config.ModeProject}, {"locked", config.ModeYolo}, {"proj", config.ModeReadOnly}, {"proj", config.ModeYolo}} {
		mode := c.parent
		runner := &agent.SubagentRunner{ConfigPath: path, Mode: func() config.Mode { return mode }}
		task := tools.SubagentTask{Prompt: "x", Profile: c.profile}
		args, err := parseArgs(runner.Args(task), io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		child, _, err := resolveConfig(args.opts)
		if err != nil {
			t.Fatal(err)
		}
		if child.Profile != c.profile || child.StartMode() == c.parent {
			t.Fatalf("profile %s should set its own mode: %+v", c.profile, child)
		}
		if got := startMode(args.opts, child); got != c.parent {
			t.Errorf("profile %s under a %s parent starts in %s", c.profile, c.parent, got)
		}
	}
	if choices := cfg.SubagentChoices(); len(choices) != 3 {
		t.Errorf("choices %+v", choices)
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
	parentCfg, _, err := resolveConfig(options{configPath: path, profile: "p"})
	if err != nil {
		t.Fatal(err)
	}
	parent := parentCfg.MCP
	if !reflect.DeepEqual(sortedNames(parent), []string{"db", "notes"}) {
		t.Fatalf("parent profile servers: %+v", parent)
	}
	writeFile(`{"mcp": {"notes": {"command": "notes"}, "extra": {"command": "extra"}}, "profiles": {"p": {"mcp": {"gh2": {"command": "gh"}}}}}`)
	_, runner := buildAgent(parentCfg, options{configPath: path, profile: "p"}, dir, nopProvider{}, devNullPrinter(t), true, false)
	childEnv(t, runner)
	fromParent := os.Getenv(config.MCPEnv)
	cfg, _, err := resolveConfig(options{configPath: path, profile: "p"})
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
	if cfg, _, _ := resolveConfig(options{configPath: path, profile: "p"}); !reflect.DeepEqual(sortedNames(cfg.MCP), []string{"extra", "gh2", "notes"}) {
		t.Errorf("top-level agent took %s: %+v", config.MCPEnv, cfg.MCP)
	}
}

func childEnv(t *testing.T, runner *agent.SubagentRunner) {
	t.Helper()
	env, err := runner.Env()
	if err != nil {
		t.Fatal(err)
	}
	for _, kv := range env {
		for _, key := range []string{agent.SubagentEnv, agent.ParentSessionEnv, config.MCPEnv, config.AccessEnv} {
			if v, ok := strings.CutPrefix(kv, key+"="); ok {
				t.Setenv(key, v)
			}
		}
	}
}

func TestSubagentTakesParentAccess(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("QI_CONFIG_DIR", dir)
	path := filepath.Join(dir, "config.json")
	os.WriteFile(path, []byte(`{"api_key": "k", "model": "m",
  "project_writable": ["/parent"], "read_only_allow": ["make"], "hook_files": ["~/parent.json", ".qi/hooks.json"],
  "subagent_profiles": ["wide"],
  "profiles": {
    "wide": {"model": "w", "project_writable": ["/", "~/"], "read_only_allow": ["rm", "curl"], "hook_files": ["/other.json"], "subagent_profiles": ["missing"]},
    "next": {"project_writable": ["/next"], "read_only_allow": null, "hook_files": ["/next.json"], "subagent_profiles": ["wide"]}
  }}`), 0o600)
	o := options{configPath: path}
	parent, _, err := resolveConfig(o)
	if err != nil {
		t.Fatal(err)
	}
	a, runner := buildAgent(parent, o, dir, nopProvider{}, devNullPrinter(t), true, false)
	hookFiles := agent.ExpandPaths(agent.ProjectRoots(dir), parent.HookFiles)
	child := func() config.Config {
		t.Helper()
		childEnv(t, runner)
		args, err := parseArgs(runner.Args(tools.SubagentTask{Prompt: "x", Profile: "wide"}), io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		cfg, _, err := resolveConfig(args.opts)
		if err != nil {
			t.Fatal(err)
		}
		if _, set := os.LookupEnv(config.AccessEnv); set {
			t.Error(config.AccessEnv + " left for tools and hooks")
		}
		t.Setenv(agent.SubagentEnv, "")
		return cfg
	}
	got := child()
	if got.Profile != "wide" || got.Model != "w" {
		t.Errorf("child did not run the listed profile: %q %q", got.Profile, got.Model)
	}
	if !reflect.DeepEqual(got.ProjectWritable, []string{"/parent"}) || !reflect.DeepEqual(got.ReadOnlyAllow, []string{"make"}) || !reflect.DeepEqual(got.HookFiles, hookFiles) {
		t.Errorf("child access: writable %v, allow %v, hooks %v; want the parent's %v", got.ProjectWritable, got.ReadOnlyAllow, got.HookFiles, hookFiles)
	}
	if !reflect.DeepEqual(agent.ExpandPaths(agent.ProjectRoots(dir), got.HookFiles), hookFiles) {
		t.Errorf("child expands the hook files differently: %v", got.HookFiles)
	}
	r := &repl{agent: a, display: devNullPrinter(t), runner: runner, opts: o}
	if err := r.switchProfile("next"); err != nil {
		t.Fatal(err)
	}
	got = child()
	if !reflect.DeepEqual(got.ProjectWritable, []string{"/next"}) || got.ReadOnlyAllow != nil || !reflect.DeepEqual(got.HookFiles, hookFiles) {
		t.Errorf("after /profile next: writable %v, allow %v, hooks %v (hooks stay those loaded at start)", got.ProjectWritable, got.ReadOnlyAllow, got.HookFiles)
	}
	childEnv(t, runner)
	t.Setenv(agent.SubagentEnv, "")
	if _, _, err := resolveConfig(options{configPath: path, profile: "wide"}); err == nil || !strings.Contains(err.Error(), `unknown profile "missing"`) {
		t.Errorf("a top-level agent checks its subagent_profiles: %v", err)
	}
	childEnv(t, runner)
	t.Setenv(agent.SubagentEnv, "")
	top, _, err := resolveConfig(options{configPath: path, profile: "next"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(top.ProjectWritable, []string{"/next"}) {
		t.Errorf("top-level agent took %s: %v", config.AccessEnv, top.ProjectWritable)
	}
	t.Setenv(agent.SubagentEnv, "1")
	t.Setenv(config.AccessEnv, `{"project_writable": ["rel"], "read_only_allow": null, "hook_files": null}`)
	if _, _, err := resolveConfig(options{configPath: path}); err == nil || !strings.Contains(err.Error(), config.AccessEnv+" from the parent agent") {
		t.Errorf("bad %s: %v", config.AccessEnv, err)
	}
}

func TestProfileSwitchPrintsSubagents(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("QI_CONFIG_DIR", dir)
	path := filepath.Join(dir, "config.json")
	os.WriteFile(path, []byte(`{"api_key": "k", "model": "m", "default_profile": "main", "profiles": {
  "main": {"subagent_profiles": ["a", "b"]},
  "twin": {"subagent_profiles": ["a", "b"]},
  "solo": {},
  "a": {"model": "ma"},
  "b": {"model": "mb"}
}}`), 0o600)
	o := options{configPath: path}
	cfg, _, err := resolveConfig(o)
	if err != nil {
		t.Fatal(err)
	}
	a, runner := buildAgent(cfg, o, dir, nopProvider{}, devNullPrinter(t), true, false)
	display, out := capturePrinter(t)
	r := &repl{agent: a, display: display, runner: runner, opts: o}
	for _, p := range []string{"twin", "solo", "a", "main"} {
		if err := r.switchProfile(p); err != nil {
			t.Fatal(err)
		}
	}
	if n := strings.Count(out.String(), "subagents: a (ma), b (mb)"); n != 1 {
		t.Errorf("subagents line printed %d times, want once (back from solo):\n%s", n, out.String())
	}
	if n := strings.Count(out.String(), "subagents: current"); n != 1 {
		t.Errorf("default menu printed %d times, want once (switching to solo):\n%s", n, out.String())
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
	cfg, _, err := resolveConfig(o)
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
	h, _, err := loadHooks([]string{home}, []string{missing, "~/.claude/settings.local.json"}, true, false, devNullPrinter(t))
	if err != nil || !h.Empty() {
		t.Errorf("missing hook files: %v", err)
	}
}

func TestNoSandboxWarning(t *testing.T) {
	display, out := capturePrinter(t)
	warnNoSandbox(display, config.ModeReadOnly)
	warnNoSandbox(display, config.ModeProject)
	warnNoSandbox(display, config.ModeYolo)
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

func TestSubagentEnvNeedsAccessAndIsCleared(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("QI_CONFIG_DIR", dir)
	path := filepath.Join(dir, "config.json")
	os.WriteFile(path, []byte(`{"api_key": "k", "model": "m"}`), 0o600)
	vars := []string{agent.SubagentEnv, agent.ParentSessionEnv, config.MCPEnv, config.AccessEnv}
	cleared := func(when string) {
		t.Helper()
		for _, key := range vars {
			if v, set := os.LookupEnv(key); set {
				t.Errorf("%s: %s=%q left for tools, hooks and MCP servers", when, key, v)
			}
		}
	}
	for _, access := range []string{"", "null", "{}", "[]", `{"project_writable": [], "read_only_allow": []}`} {
		t.Setenv(agent.SubagentEnv, "1")
		t.Setenv(config.AccessEnv, access)
		if _, _, err := resolveConfig(options{configPath: path}); err == nil || !strings.Contains(err.Error(), config.AccessEnv+" from the parent agent") {
			t.Errorf("QI_ACCESS=%q: %v", access, err)
		}
		cleared("invalid access " + access)
	}
	t.Setenv(agent.SubagentEnv, "1")
	t.Setenv(config.AccessEnv, "")
	os.Unsetenv(config.AccessEnv)
	if _, _, err := resolveConfig(options{configPath: path}); err == nil || !strings.Contains(err.Error(), config.AccessEnv+" is missing") {
		t.Errorf("no access: %v", err)
	}
	cleared("no access")
	for _, key := range vars[1:] {
		t.Setenv(key, "stray{")
	}
	cfg, parent, err := resolveConfig(options{configPath: path})
	if err != nil || parent.sub || cfg.Model != "m" {
		t.Errorf("top-level qi with stray values: %v %+v", err, parent)
	}
	cleared("top level")
}

func TestSubagentFailsOnChangedProjectHooks(t *testing.T) {
	srv := headlessServer(t)
	path := headlessConfig(t, srv.URL)
	t.Setenv("QI_CONFIG_DIR", t.TempDir())
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(cwd, ".claude", "settings.json")
	os.MkdirAll(filepath.Dir(settings), 0o700)
	writeHooks := func(cmd string) {
		os.WriteFile(settings, []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"`+cmd+`"}]}]}}`), 0o600)
	}
	writeHooks("true")
	roots := agent.ProjectRoots(cwd)
	all := agent.ExpandPaths(roots, []string{"~/.claude/settings.json", ".claude/settings.json"})
	child := func(hookFiles []string) error {
		t.Helper()
		access, err := config.EncodeAccess(config.Access{HookFiles: hookFiles})
		if err != nil {
			t.Fatal(err)
		}
		t.Setenv(agent.SubagentEnv, "1")
		t.Setenv(agent.ParentSessionEnv, "P-1")
		t.Setenv(config.AccessEnv, access)
		withStdio(t, func() { err = run(options{query: "hi", configPath: path}) })
		return err
	}
	sessions := func() []string {
		files, _ := filepath.Glob(filepath.Join(os.Getenv("QI_SESSION_DIR"), "*", "subagents", "*.jsonl"))
		return files
	}
	_, skipped, err := loadHooks(roots, all, true, false, devNullPrinter(t))
	if err != nil || !skipped {
		t.Fatalf("untrusted headless parent: skipped %v, %v", skipped, err)
	}
	if got := userHookFiles(all); len(got) != 1 || got[0] != all[0] {
		t.Fatalf("a parent that skipped project hooks passes %v", got)
	}
	if err := child(userHookFiles(all)); err != nil {
		t.Errorf("child of a parent that skipped project hooks: %v", err)
	}
	parsed, err := agent.ParseHookFiles(all)
	if err != nil {
		t.Fatal(err)
	}
	yes := func(string, string, []string) bool { return true }
	if _, skipped, err := agent.SelectHookFiles(roots[0], parsed, true, yes); err != nil || skipped {
		t.Fatalf("trust: %v %v", skipped, err)
	}
	if err := child(all); err != nil {
		t.Fatalf("child with trusted project hooks: %v", err)
	}
	for _, key := range []string{agent.SubagentEnv, agent.ParentSessionEnv, config.AccessEnv} {
		if _, set := os.LookupEnv(key); set {
			t.Errorf("%s left for tools, hooks and MCP servers", key)
		}
	}
	files := sessions()
	if len(files) != 2 {
		t.Fatalf("subagent sessions %v", files)
	}
	for _, f := range files {
		if data, _ := os.ReadFile(f); !strings.Contains(string(data), `"parent_session":"P-1"`) {
			t.Errorf("%s has no parent_session:\n%s", f, data)
		}
	}
	writeHooks("echo changed")
	want := fmt.Sprintf(changedHooksError, roots[0])
	if err := child(all); err == nil || err.Error() != want {
		t.Errorf("child after the project hooks changed: %v, want %q", err, want)
	}
	if got := sessions(); len(got) != 2 {
		t.Errorf("a failed child left a session: %v", got)
	}
}
