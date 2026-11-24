package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/paul-nameless/qi/internal/agent"
	"github.com/paul-nameless/qi/internal/config"
	"github.com/paul-nameless/qi/internal/llm"
	"github.com/paul-nameless/qi/internal/mcp"
	"github.com/paul-nameless/qi/internal/proc"
	"github.com/paul-nameless/qi/internal/session"
	"github.com/paul-nameless/qi/internal/tools"
	"github.com/paul-nameless/qi/internal/ui"
)

type options struct {
	query            string
	cont             bool
	profile          string
	configPath       string
	model            string
	provider         string
	effort           string
	systemPromptFile string
	readOnly         bool
	sessionID        string
	pickSession      bool
	resumeID         string
	json             bool
	version          string
}

const escalationWait = 6 * time.Second

func run(o options) error {
	defer proc.Settle(escalationWait)
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
	if err := checkJSON(o, headless); err != nil {
		return err
	}

	var display *ui.Printer
	if headless {
		display = ui.NewPrinter(os.Stderr)
	} else {
		display = ui.NewPrinter(os.Stdout)
	}
	display.SetRunCode(cfg.RunCodeOn() && !isSub)

	if headless && cfg.MaxRounds == 0 {
		cfg.MaxRounds = 200
	}
	cfg.DefaultCacheTTL(headless)
	var manager *mcp.Manager
	var extra []*tools.Function
	if len(cfg.MCP) > 0 {
		manager = mcp.NewManager(cfg.MCP)
		defer manager.Close()
		extra = tools.MCPFuncs(manager)
	}
	a, runner := buildAgent(cfg, o, cwd, newProvider(cfg, key), display, headless, isSub, extra...)
	roots := agent.ProjectRoots(cwd)

	resume, cancelled, err := resumePath(o, cfg, cwd, headless)
	if err != nil || cancelled {
		return err
	}
	hooks, err := loadHooks(roots, cfg.HookFiles, headless, display)
	if err != nil {
		return err
	}
	if err := openSession(a, o, cfg, cwd, resume, headless, isSub); err != nil {
		return err
	}
	defer a.Session().Close()
	defer func() { agent.RemoveScratchIfEmpty(a.Session().ID) }()
	defer a.KillJobs()
	skills, sys := agent.LoadPrompt(cwd, cfg, agent.ScratchDir(a.Session().ID))
	a.SetSystem(sys)
	hooks.Session, hooks.SessionID, hooks.ReadOnly = a.Session().Path, a.Session().ID, a.ReadOnly
	if !hooks.Empty() {
		var stop agent.StopHook
		if !isSub {
			stop = hooks.Stop
		}
		a.SetHooks(hooks.PreToolUse, hooks.PostToolUse, stop)
	}
	if isSub {
		hooks.SkipPrompt = true
	}

	if headless {
		if a.ReadOnly() {
			warnNoSandbox(display)
		}
		prompt, err := headlessPrompt(o.query, stdinPiped)
		if err != nil {
			return err
		}
		return runHeadless(a, display, hooks, prompt, o.json)
	}
	r := &repl{agent: a, display: display, skills: skills, commands: agent.LoadCommands(roots), hooks: hooks, runner: runner, opts: o, mcp: manager}
	return r.run()
}

func checkJSON(o options, headless bool) error {
	if o.json && !headless {
		return errors.New("-json works only with -q or piped stdin")
	}
	return nil
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
	case o.pickSession && o.resumeID != "":
		path, err := sessionPath(cwd, o.resumeID)
		return path, false, err
	case o.pickSession:
		if headless {
			return "", false, errors.New("-r needs a terminal")
		}
		path, err := pickSession(runPlain, nil, cfg, cwd, "")
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
		if o.json {
			if err := s.Tee(os.Stdout); err != nil {
				s.Close()
				return err
			}
		}
		a.Load(s, ld)
		return nil
	}
	kind := session.Interactive
	if headless {
		kind = session.Headless
	}
	opts := session.CreateOptions{Cwd: cwd, ID: o.sessionID, Model: cfg.Model, Kind: kind, Subagent: isSub}
	if parent := os.Getenv(agent.ParentSessionEnv); isSub && session.ValidID(parent) {
		opts.ParentSession = parent
	}
	if o.json {
		opts.Tee = os.Stdout
	}
	s, err := session.Create(opts)
	if err != nil {
		return err
	}
	a.Reset(s)
	return nil
}

func resolveConfig(o options) (config.Config, error) {
	parentMCP, fromParent, err := config.MCPFromEnv()
	if err != nil {
		return config.Config{}, err
	}
	cfg, err := config.Resolve(config.Options{Path: o.configPath, Profile: o.profile, Provider: o.provider, Model: o.model, Effort: o.effort, SystemPromptFile: o.systemPromptFile})
	if err != nil {
		return cfg, err
	}
	if fromParent && os.Getenv("QI_SUBAGENT") != "" {
		cfg.MCP = parentMCP
	}
	return cfg, nil
}

func loadHooks(roots, configured []string, headless bool, display *ui.Printer) (*agent.Hooks, error) {
	parsed, err := agent.ParseHookFiles(agent.ExpandPaths(roots, configured))
	if err != nil {
		return nil, err
	}
	files, skipped, err := agent.SelectHookFiles(roots[0], parsed, !headless, agent.TrustPrompt(os.Stdin, os.Stdout))
	if err != nil {
		display.Info(err.Error())
	}
	if skipped && headless && os.Getenv("QI_SUBAGENT") == "" {
		display.Info("skipping untrusted or changed project hooks in " + roots[0] + " (start qi interactively there to review them)")
	}
	h := agent.NewHooks(roots[0], files)
	h.Info = display.Info
	return h, nil
}

func submitPrompt(ctx context.Context, a *agent.Agent, display notifier, hooks *agent.Hooks, prompt string, images ...string) ([]llm.Block, error) {
	blocked, reason, extra := hooks.UserPromptSubmit(ctx, prompt)
	if blocked {
		return nil, fmt.Errorf("prompt blocked by hook: %s", reason)
	}
	sessionPath := ""
	if s := a.Session(); s != nil {
		sessionPath = s.Path
	}
	blocks := userBlocks(a.Cwd(), sessionPath, display, prompt, images)
	if extra != "" {
		blocks = append(blocks, llm.Block{Type: llm.BlockText, Text: llm.HookContext(extra)})
	}
	return blocks, nil
}

func buildAgent(cfg config.Config, o options, cwd string, p llm.Provider, display agent.Display, headless, isSub bool, extra ...*tools.Function) (*agent.Agent, *agent.SubagentRunner) {
	opts := agent.Options{Config: cfg, Cwd: cwd, Provider: p, UI: display, Headless: headless, ReadOnly: o.readOnly || cfg.ReadOnly}
	funcs := []*tools.Function{tools.Read(), tools.Write(), tools.Edit(), tools.Bash(), tools.JobsFunc()}
	var runner *agent.SubagentRunner
	if !isSub {
		runner = &agent.SubagentRunner{ConfigPath: o.configPath, Profile: cfg.Profile, Provider: cfg.Provider, SystemPromptFile: o.systemPromptFile, Cwd: cwd, MCP: cfg.MCP}
		opts.Subagent = runner.Run
		funcs = append(funcs, tools.Subagent(cfg.Model, cfg.SubagentModels, cfg.SubagentEfforts))
	}
	funcs = append(funcs, extra...)
	opts.Tool = tools.Run(funcs...)
	a := agent.New(opts)
	if runner != nil {
		runner.ReadOnly = a.ReadOnly
		runner.SessionID = func() string {
			if s := a.Session(); s != nil {
				return s.ID
			}
			return ""
		}
		env := &tools.Env{Cwd: cwd}
		runner.Label = func(call llm.Block) string { return opts.Tool.Label(env, call) }
	}
	return a, runner
}

func newProvider(cfg config.Config, key string) llm.Provider {
	switch cfg.API {
	case "anthropic":
		return llm.NewAnthropic(cfg.BaseURL, key)
	case "responses":
		return llm.NewResponses(cfg.BaseURL, key)
	}
	return llm.NewOpenAI(cfg.BaseURL, key, cfg.CacheControl)
}

func absPath(p string) (string, error) {
	if p == "" {
		return "", nil
	}
	abs, err := filepath.Abs(config.ExpandHome(p))
	if err != nil {
		return "", fmt.Errorf("-system-prompt-file: %w", err)
	}
	return abs, nil
}
