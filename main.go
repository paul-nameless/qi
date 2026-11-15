package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/paul-nameless/qi/internal/agent"
	"github.com/paul-nameless/qi/internal/config"
	"github.com/paul-nameless/qi/internal/llm"
	"github.com/paul-nameless/qi/internal/mcp"
	"github.com/paul-nameless/qi/internal/session"
	"github.com/paul-nameless/qi/internal/tools"
	"github.com/paul-nameless/qi/internal/ui"
)

var version = "dev"

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
}

func main() {
	var o options
	flag.StringVar(&o.query, "q", "", "run one `prompt` without the interactive UI and print the answer")
	flag.BoolVar(&o.cont, "c", false, "continue the last session in this directory")
	flag.StringVar(&o.profile, "p", "", "profile `name` from \"profiles\" in the config, or ~/.config/qi/<name>.json")
	flag.StringVar(&o.provider, "provider", "", "provider `name` override (from \"providers\" in the config)")
	flag.StringVar(&o.configPath, "config", "", "JSON config `file` (default ~/.config/qi/config.json)")
	flag.StringVar(&o.model, "model", "", "`model` override")
	flag.StringVar(&o.effort, "effort", "", "reasoning `effort` override")
	flag.BoolVar(&o.readOnly, "read-only", false, "start in read-only mode")
	flag.StringVar(&o.sessionID, "session-id", "", "use this session `id`: continues it if it exists, creates it otherwise")
	flag.BoolVar(&o.pickSession, "r", false, "pick a session of this directory to resume")
	show := flag.String("show", "", "print a `session` of this directory (id or path) as plain text and exit")
	showVersion := flag.Bool("version", false, "print version")
	checkConfig := flag.Bool("check-config", false, "load the config and every profile without calling any API, then exit")
	flag.Usage = usage
	flag.Parse()
	if *showVersion {
		fmt.Println("qi", buildVersion())
		return
	}
	if *show != "" {
		if err := showSession(os.Stdout, *show); err != nil {
			fmt.Fprintln(os.Stderr, "qi:", err)
			os.Exit(1)
		}
		return
	}
	if *checkConfig {
		if err := config.Check(o.configPath, o.profile); err != nil {
			fmt.Fprintln(os.Stderr, "qi:", err)
			os.Exit(1)
		}
		fmt.Println("config ok")
		return
	}
	if err := run(o); err != nil {
		fmt.Fprintln(os.Stderr, "qi:", err)
		os.Exit(1)
	}
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
	a.Env = env
	a.Registry = tools.NewRegistry(toolset...)
	roots := agent.ProjectRoots(cwd)
	skills, sys := agent.LoadPrompt(cwd, cfg)
	a.SetSystem(sys)

	if o.sessionID != "" && !session.ValidID(o.sessionID) {
		return fmt.Errorf("invalid session id %q", o.sessionID)
	}
	resume := ""
	switch {
	case o.sessionID != "":
		if _, err := os.Stat(session.PathFor(cwd, o.sessionID)); err == nil {
			resume = session.PathFor(cwd, o.sessionID)
		}
	case o.cont:
		path, err := session.Latest(cwd, !headless)
		if err != nil {
			return err
		}
		resume = path
	case o.pickSession:
		if headless {
			return errors.New("-r needs a terminal")
		}
		path, err := pickSession(runPlain, cfg, cwd, "")
		if err != nil || path == "" {
			return err
		}
		resume = path
	}
	if resume != "" {
		s, ld, err := session.Open(resume)
		if err != nil {
			return err
		}
		a.Load(s, ld)
	} else {
		kind := "interactive"
		if headless {
			kind = "headless"
		}
		s, err := session.Create(cwd, o.sessionID, cfg.Model, kind, isSub)
		if err != nil {
			return err
		}
		a.Session = s
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
		prompt := o.query
		if stdinPiped {
			data := readStdin(o.query == "")
			if s := strings.TrimSpace(data); s != "" {
				if prompt == "" {
					prompt = s
				} else {
					prompt += "\n\n<stdin>\n" + s + "\n</stdin>"
				}
			}
		}
		if strings.TrimSpace(prompt) == "" {
			return fmt.Errorf("empty prompt")
		}
		return runHeadless(a, display, hooks, prompt)
	}
	r := &repl{agent: a, display: display, skills: skills, commands: agent.LoadCommands(roots), hooks: hooks, runner: runner, opts: o, mcp: manager}
	return r.run()
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

func stdinHasData() bool {
	st, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeNamedPipe != 0 || st.Mode().IsRegular()
}

func readStdin(wait bool) string {
	first := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 64<<10)
		n, _ := os.Stdin.Read(buf)
		first <- buf[:n]
	}()
	var head []byte
	if wait {
		head = <-first
	} else {
		select {
		case head = <-first:
		case <-time.After(5 * time.Second):
			fmt.Fprintln(os.Stderr, "qi: no stdin within 5s, running without it")
			return ""
		}
	}
	if len(head) == 0 {
		return ""
	}
	rest, _ := io.ReadAll(os.Stdin)
	return string(head) + string(rest)
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

func runHeadless(a *agent.Agent, display *ui.Printer, hooks *agent.Hooks, prompt string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		cancel()
	}()
	blocks, err := submitPrompt(ctx, a, hooks, prompt)
	if err != nil {
		return err
	}
	final, stats, err := a.Turn(ctx, blocks)
	display.EndBlock()
	display.Info(stats.String(a.ContextShort()) + " · " + resourceUsage())
	if err != nil {
		return err
	}
	fmt.Println(final)
	return nil
}

func usage() {
	out := flag.CommandLine.Output()
	fmt.Fprintln(out, "Usage: qi [flags]")
	var rows [][2]string
	width := 0
	flag.VisitAll(func(f *flag.Flag) {
		arg, help := flag.UnquoteUsage(f)
		name := "--" + f.Name
		if len(f.Name) == 1 {
			name = "-" + f.Name
		}
		if arg != "" {
			name += " " + arg
		}
		width = max(width, len(name))
		rows = append(rows, [2]string{name, help})
	})
	for _, r := range rows {
		fmt.Fprintf(out, "  %-*s  %s\n", width, r[0], r[1])
	}
}
