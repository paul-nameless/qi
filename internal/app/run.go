package app

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/paul-nameless/qi/internal/agent"
	"github.com/paul-nameless/qi/internal/config"
	"github.com/paul-nameless/qi/internal/llm"
	"github.com/paul-nameless/qi/internal/mcp"
	"github.com/paul-nameless/qi/internal/session"
	"github.com/paul-nameless/qi/internal/tools"
	"github.com/paul-nameless/qi/internal/ui"
)

type options struct {
	query       string
	cont        bool
	profile     string
	configPath  string
	model       string
	provider    string
	effort      string
	readOnly    bool
	sessionID   string
	pickSession bool
	version     string
}

func run(o options) error {
	cfg, err := resolveConfig(o)
	if err != nil {
		return err
	}
	key, err := cfg.ResolveAPIKey()
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	stdinPiped := stdinHasData()
	headless := o.query != "" || stdinPiped || !ui.IsTerminal(os.Stdin)
	isSub := os.Getenv("QI_SUBAGENT") != ""

	var display *ui.Printer
	if headless {
		display = ui.NewPrinter(os.Stderr)
	} else {
		display = ui.NewPrinter(os.Stdout)
	}

	if headless && cfg.MaxRounds == 0 {
		cfg.MaxRounds = 200
	}
	cfg.DefaultCacheTTL(headless)
	a, runner, toolset := buildAgent(cfg, o, cwd, newProvider(cfg, key), display, headless, isSub)
	env := a.Env
	var manager *mcp.Manager
	if len(cfg.MCP) > 0 {
		manager = mcp.NewManager(cfg.MCP)
		defer manager.Close()
		toolset = append(toolset, tools.MCPTools(manager)...)
	}
	a.Registry = tools.NewRegistry(toolset...)
	roots := agent.ProjectRoots(cwd)
	skills, sys := agent.LoadPrompt(cwd, cfg)
	a.SetSystem(sys)

	resume, cancelled, err := resumePath(o, cfg, cwd, headless)
	if err != nil || cancelled {
		return err
	}
	if err := openSession(a, o, cfg, cwd, resume, headless, isSub); err != nil {
		return err
	}
	defer a.Session.Close()
	hooks := agent.LoadHooks(roots[0], hookFiles(roots, cfg.HookFiles, headless, display))
	hooks.Session, hooks.SessionID, hooks.ReadOnly = a.Session.Path, a.Session.ID, a.ReadOnly
	if !hooks.Empty() {
		env.PreHook = hooks.PreToolUse
		env.PostHook = hooks.PostToolUse
		if !isSub {
			a.OnStop = hooks.Stop
		}
	}
	if isSub {
		hooks.SkipPrompt = true
	}

	if headless {
		prompt, err := headlessPrompt(o.query, stdinPiped)
		if err != nil {
			return err
		}
		return runHeadless(a, display, hooks, prompt)
	}
	r := &repl{agent: a, display: display, skills: skills, commands: agent.LoadCommands(roots), hooks: hooks, runner: runner, opts: o, mcp: manager}
	return r.run()
}

func resumePath(o options, cfg config.Config, cwd string, headless bool) (string, bool, error) {
	if o.sessionID != "" && !session.ValidID(o.sessionID) {
		return "", false, fmt.Errorf("invalid session id %q", o.sessionID)
	}
	switch {
	case o.sessionID != "":
		if _, err := os.Stat(session.PathFor(cwd, o.sessionID)); err == nil {
			return session.PathFor(cwd, o.sessionID), false, nil
		}
	case o.cont:
		path, err := session.Latest(cwd, !headless)
		return path, false, err
	case o.pickSession:
		if headless {
			return "", false, errors.New("-r needs a terminal")
		}
		path, err := pickSession(runPlain, cfg, cwd, "")
		if err != nil || path == "" {
			return "", true, err
		}
		return path, false, nil
	}
	return "", false, nil
}

func openSession(a *agent.Agent, o options, cfg config.Config, cwd, resume string, headless, isSub bool) error {
	if resume != "" {
		s, ld, err := session.Open(resume)
		if err != nil {
			return err
		}
		a.Load(s, ld)
		return nil
	}
	kind := "interactive"
	if headless {
		kind = "headless"
	}
	s, err := session.Create(cwd, o.sessionID, cfg.Model, kind, isSub)
	if err != nil {
		return err
	}
	a.Session = s
	return nil
}

func resolveConfig(o options) (config.Config, error) {
	parentMCP, fromParent, err := config.MCPFromEnv()
	if err != nil {
		return config.Config{}, err
	}
	cfg, err := config.Resolve(o.configPath, o.profile, o.provider, o.model, o.effort)
	if err != nil {
		return cfg, err
	}
	if fromParent && os.Getenv("QI_SUBAGENT") != "" {
		cfg.MCP = parentMCP
	}
	return cfg, nil
}

func hookFiles(roots, configured []string, headless bool, display *ui.Printer) []string {
	files, skipped := agent.SelectHookFiles(roots, configured, !headless, agent.AskTrust)
	if skipped && headless && os.Getenv("QI_SUBAGENT") == "" {
		display.Info("skipping untrusted project hooks in " + roots[0] + " (start qi interactively there once to trust them)")
	}
	return files
}

func submitPrompt(ctx context.Context, a *agent.Agent, hooks *agent.Hooks, prompt string, images ...string) ([]llm.Block, error) {
	blocked, reason, extra := hooks.UserPromptSubmit(ctx, prompt)
	if blocked {
		return nil, fmt.Errorf("prompt blocked by hook: %s", reason)
	}
	blocks := userBlocks(a, prompt, images)
	if extra != "" {
		blocks = append(blocks, llm.Block{Type: llm.BlockText, Text: "<hook-context>\n" + extra + "\n</hook-context>"})
	}
	return blocks, nil
}

func buildAgent(cfg config.Config, o options, cwd string, p llm.Provider, display agent.Display, headless, isSub bool) (*agent.Agent, *agent.SubagentRunner, []*tools.Tool) {
	a := &agent.Agent{Cfg: cfg, Cwd: cwd, Provider: p, UI: display, Headless: headless}
	a.SetReadOnly(o.readOnly || cfg.ReadOnly)
	a.Env = &tools.Env{Cwd: cwd, ReadOnly: a.ReadOnly, Allow: cfg.ReadOnlyAllow, BashTimeout: cfg.BashTimeout}
	toolset := []*tools.Tool{tools.Read(), tools.Write(), tools.Edit(), tools.Bash()}
	var runner *agent.SubagentRunner
	if !isSub {
		runner = &agent.SubagentRunner{ConfigPath: o.configPath, Profile: cfg.Profile, Provider: cfg.Provider, ReadOnly: a.ReadOnly, Cwd: cwd, MCP: cfg.MCP}
		a.Env.Subagent = runner.Run
		toolset = append(toolset, tools.Subagent(cfg.Model, cfg.SubagentModels, cfg.SubagentEfforts))
	}
	return a, runner, toolset
}

func newProvider(cfg config.Config, key string) llm.Provider {
	if cfg.API == "anthropic" {
		return llm.NewAnthropic(cfg.BaseURL, key)
	}
	return llm.NewOpenAI(cfg.BaseURL, key, cfg.CacheControl)
}
