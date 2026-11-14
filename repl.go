package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/paul-nameless/aicode/internal/agent"
	"github.com/paul-nameless/aicode/internal/config"
	"github.com/paul-nameless/aicode/internal/llm"
	"github.com/paul-nameless/aicode/internal/session"
	"github.com/paul-nameless/aicode/internal/tools"
	"github.com/paul-nameless/aicode/internal/ui"
)

type repl struct {
	agent    *agent.Agent
	display  *ui.Printer
	editor   *ui.Editor
	term     *ui.Terminal
	skills   []agent.Skill
	commands []agent.Command
	hooks    *agent.Hooks
	runner   *agent.SubagentRunner
	opts     options

	mu            sync.Mutex
	turnCancel    context.CancelFunc
	turnDone      chan struct{}
	idle          *time.Timer
	idleCancel    context.CancelFunc
	idleGen       int
	lastIntr      time.Time
	lastCancelled bool
}

func (r *repl) prompt() string {
	if r.agent.ReadOnly() {
		return r.display.Color(ui.Yellow, "read-only") + r.display.Color(ui.Cyan, " ❯ ")
	}
	return r.display.Color(ui.Cyan, "❯ ")
}

func (r *repl) toggleMode() {
	ro := !r.agent.ReadOnly()
	r.agent.SetReadOnly(ro)
	if ro && !tools.HasSandbox() {
		r.display.Info("no OS sandbox found (install bubblewrap): read-only relies on the command allowlist only")
	}
	if ro && r.runner != nil {
		r.runner.StopAll()
	}
}

func (r *repl) modeName() string {
	if r.agent.ReadOnly() {
		return "read-only"
	}
	return "yolo"
}

func (r *repl) run() error {
	t, err := ui.OpenTerminal()
	if err != nil {
		return fmt.Errorf("terminal: %w", err)
	}
	r.term = t
	defer t.Restore()
	defer func() {
		if p := recover(); p != nil {
			t.Restore()
			panic(p)
		}
	}()
	hist := filepath.Join(config.Home(), ".local", "share", "aicode", "history")
	r.editor = ui.NewEditor(t, r.display, hist)
	r.editor.OnToggle = func() string {
		r.toggleMode()
		return r.prompt()
	}
	r.editor.IsImage = func(text string) []string { return imagePaths(r.agent.Cwd, text) }
	if pickerAvailable(r.agent.Cfg.Picker) {
		r.editor.OnSlash = r.slashPicker
	}
	r.handleSignals()
	r.banner()

	draft := ""
	for {
		line, err := r.editor.ReadLine(r.prompt(), draft)
		draft = ""
		if errors.Is(err, ui.ErrEOF) {
			return nil
		}
		if errors.Is(err, ui.ErrInterrupt) {
			if time.Since(r.lastIntr) < 2*time.Second {
				return nil
			}
			r.lastIntr = time.Now()
			r.display.Info("press Ctrl-C again to exit")
			continue
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		images := r.editor.TakeImages(line)
		text, quit := r.handleInput(line)
		if quit {
			return nil
		}
		if text != "" {
			draft = r.turn(func(ctx context.Context) ([]llm.Block, error) {
				return submitPrompt(ctx, r.agent, r.hooks, text, images...)
			}, draft)
		}
		for q := r.agent.DrainQueue(); len(q) > 0 && !r.lastCancelled; q = r.agent.DrainQueue() {
			draft = r.turn(func(context.Context) ([]llm.Block, error) { return q, nil }, draft)
		}
		if q := r.agent.DrainQueue(); len(q) > 0 {
			draft = strings.TrimSpace(draft + " " + queuedText(q))
		}
		r.armIdle()
	}
}

func (r *repl) banner() {
	where := r.agent.Session.ID
	r.display.Line(r.display.Color(ui.Bold, "aicode") + r.display.Color(ui.Dim, fmt.Sprintf(" %s · %s · %s · session %s", version, r.agent.Model(), r.modeName(), where)))
	if n := len(r.agent.Messages); n > 0 {
		r.printHistory()
	}
	r.display.Info("/help for commands · shift+tab toggles read-only · ctrl+g opens $EDITOR")
}

func (r *repl) handleSignals() {
	ch := make(chan os.Signal, 4)
	signal.Notify(ch, os.Interrupt, syscall.SIGTSTP, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	go func() {
		for s := range ch {
			switch s {
			case os.Interrupt:
				if !r.cancelTurn() {
					select {
					case r.editor.Interrupt <- struct{}{}:
					default:
					}
				}
			case syscall.SIGTSTP:
				r.display.ClearFooter()
				r.term.Suspend()
				signal.Reset(syscall.SIGTSTP)
				_ = syscall.Kill(os.Getpid(), syscall.SIGTSTP)
				time.Sleep(50 * time.Millisecond)
				signal.Notify(ch, syscall.SIGTSTP)
				r.term.Resume()
				r.display.RenderPrompt()
			default:
				r.shutdown()
			}
		}
	}()
}

func (r *repl) cancelTurn() bool {
	r.mu.Lock()
	cancel := r.turnCancel
	r.mu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

func (r *repl) shutdown() {
	r.stopIdle()
	r.mu.Lock()
	done := r.turnDone
	r.mu.Unlock()
	if r.cancelTurn() && done != nil {
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	}
	r.term.Restore()
	os.Exit(1)
}

func (r *repl) cancellable(fn func(ctx context.Context) error) error {
	r.stopIdle()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	r.mu.Lock()
	r.turnCancel, r.turnDone = cancel, done
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.turnCancel, r.turnDone = nil, nil
		r.mu.Unlock()
		cancel()
		close(done)
	}()
	return fn(ctx)
}

func (r *repl) turn(prepare func(context.Context) ([]llm.Block, error), draft string) string {
	var stats agent.TurnStats
	err := r.cancellable(func(ctx context.Context) error {
		blocks, err := prepare(ctx)
		if err != nil {
			return err
		}
		collectCtx, stopCollect := context.WithCancel(context.Background())
		draftCh := make(chan string, 1)
		go func() {
			draftCh <- r.editor.Collect(collectCtx, draft, ui.Steer{
				OnQueue: func(s string) {
					blocks, err := submitPrompt(ctx, r.agent, r.hooks, s)
					if err != nil {
						r.display.Error(err.Error())
						return
					}
					r.agent.Queue(blocks)
					r.display.Info("queued: " + tools.OneLine(s, 80))
				},
				OnCancel: func() { r.cancelTurn() },
				OnToggle: func() {
					r.toggleMode()
					r.display.Info("mode: " + r.modeName())
				},
			})
		}()
		_, stats, err = r.agent.Turn(ctx, blocks)
		stopCollect()
		draft = <-draftCh
		return err
	})
	r.display.EndBlock()
	r.lastCancelled = errors.Is(err, context.Canceled)
	switch {
	case errors.Is(err, context.Canceled):
		r.display.Line(r.display.Color(ui.Yellow, "interrupted"))
	case err != nil:
		r.display.Error(err.Error())
	}
	if stats.Requests > 0 {
		r.display.Info(stats.String(r.agent.ContextTokens()))
	}
	return draft
}

func (r *repl) armIdle() {
	ttl, err := time.ParseDuration(r.agent.Cfg.CacheTTL)
	if err != nil || ttl <= 5*time.Minute {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.idle != nil {
		r.idle.Stop()
	}
	r.idleGen++
	gen := r.idleGen
	r.idle = time.AfterFunc(ttl-5*time.Minute, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		r.mu.Lock()
		if gen != r.idleGen {
			r.mu.Unlock()
			return
		}
		r.idleCancel = cancel
		r.mu.Unlock()
		if err := r.agent.TryCompactIdle(ctx); err != nil && ctx.Err() == nil {
			r.display.Error("idle compaction failed: " + err.Error())
		}
	})
}

func (r *repl) stopIdle() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.idleGen++
	if r.idle != nil {
		r.idle.Stop()
		r.idle = nil
	}
	if r.idleCancel != nil {
		r.idleCancel()
		r.idleCancel = nil
	}
}

func (r *repl) resolve(profile string) (config.Config, error) {
	return config.Resolve(r.opts.configPath, profile, r.opts.provider, "", r.opts.effort)
}

func (r *repl) switchProfile(name string) error {
	r.opts.provider = ""
	cfg, err := r.resolve(name)
	if err != nil {
		return err
	}
	return r.apply(cfg, cfg.Provider)
}

func (r *repl) switchModel(name string) error {
	cfg, err := r.resolve(r.agent.Cfg.Profile)
	if err != nil {
		return err
	}
	base := cfg.Provider
	menus := [][]config.SubagentModel{cfg.SubagentModels}
	for _, p := range cfg.ProfileNames() {
		if pc, err := config.Load(r.opts.configPath, p); err == nil {
			menus = append(menus, withProvider(pc.SubagentModels, pc.Provider))
		}
	}
	for _, menu := range menus {
		for _, m := range menu {
			if m.Name != name {
				continue
			}
			if m.Provider != "" {
				if err := cfg.UseProvider(m.Provider); err != nil {
					return err
				}
			}
			cfg.Model = m.Model
			return r.apply(cfg, base)
		}
	}
	cfg.Model = name
	return r.apply(cfg, base)
}

var efforts = []pickItem{
	{"default", "the model's own default"},
	{"low", "fastest, least thinking"},
	{"medium", ""},
	{"high", ""},
	{"xhigh", "not supported by every model"},
	{"max", "Anthropic only"},
}

func effortName(e string) string {
	if e == "" {
		return "default"
	}
	return e
}

func (r *repl) setEffort(level string) error {
	for _, e := range efforts {
		if e.name == level {
			if level == "default" {
				level = ""
			}
			r.opts.effort = level
			r.agent.SetEffort(level)
			return nil
		}
	}
	return fmt.Errorf("unknown effort %q", level)
}

func withProvider(menu []config.SubagentModel, provider string) []config.SubagentModel {
	out := make([]config.SubagentModel, len(menu))
	for i, m := range menu {
		if m.Provider == "" {
			m.Provider = provider
		}
		out[i] = m
	}
	return out
}

func (r *repl) apply(cfg config.Config, baseProvider string) error {
	cfg.DefaultCacheTTL(false)
	key, err := cfg.ResolveAPIKey()
	if err != nil {
		return err
	}
	r.agent.Reconfigure(cfg, newProvider(cfg, key))
	r.agent.Env.Allow, r.agent.Env.BashTimeout = cfg.ReadOnlyAllow, cfg.BashTimeout
	if r.runner != nil {
		r.runner.Profile, r.runner.Provider = cfg.Profile, baseProvider
		r.agent.Registry.Replace(tools.Subagent(cfg.Model, withProvider(cfg.SubagentModels, baseProvider), cfg.SubagentEfforts))
	}
	return nil
}

func (r *repl) modelList() string {
	cfg := r.agent.Cfg
	var b strings.Builder
	fmt.Fprintf(&b, "model: %s", cfg.Model)
	if cfg.Provider != "" {
		fmt.Fprintf(&b, " (provider %s)", cfg.Provider)
	}
	fmt.Fprintf(&b, " · effort %s", effortName(cfg.ReasoningEffort))
	if cfg.Profile != "" {
		fmt.Fprintf(&b, " · profile %s", cfg.Profile)
	}
	for _, m := range cfg.SubagentModels {
		fmt.Fprintf(&b, "\n  /model %-10s %s", m.Name, m.Model)
	}
	for _, p := range cfg.ProfileNames() {
		fmt.Fprintf(&b, "\n  /profile %s", p)
	}
	return b.String()
}

const historyShown = 40

func (r *repl) printHistory() {
	msgs := r.agent.Messages
	if len(msgs) > historyShown {
		r.display.Info(fmt.Sprintf("… %d earlier messages", len(msgs)-historyShown))
		msgs = msgs[len(msgs)-historyShown:]
	}
	for _, m := range msgs {
		for _, b := range m.Blocks {
			switch {
			case b.Type == llm.BlockText && m.Role == "user":
				if t := strings.TrimSpace(b.Text); t != "" && !strings.HasPrefix(t, "<") && !strings.HasPrefix(t, "The user sent this") {
					r.display.Line(r.display.Color(ui.Cyan, "❯ ") + firstLines(t, 6))
				}
			case b.Type == llm.BlockText:
				r.display.Text(strings.TrimSpace(b.Text))
				r.display.EndBlock()
			case b.Type == llm.BlockToolUse:
				r.display.Info("· " + b.Name + " " + tools.OneLine(string(b.Input), 90))
			case b.Type == llm.BlockImage:
				r.display.Info("[image]")
			}
		}
	}
	r.display.Info(fmt.Sprintf("resumed %d messages", len(r.agent.Messages)))
}

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[:n], "\n") + fmt.Sprintf("\n… %d more lines", len(lines)-n)
}

func queuedText(blocks []llm.Block) string {
	var parts []string
	for _, b := range blocks {
		if b.Type == llm.BlockText && !strings.HasPrefix(b.Text, "<hook-context>") {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, " ")
}

func (r *repl) newSession() error {
	r.stopIdle()
	s, err := session.Create(r.agent.Cwd, "", r.agent.Model(), "interactive", false)
	if err != nil {
		return err
	}
	r.agent.Reset(s)
	r.hooks.Session, r.hooks.SessionID = s.Path, s.ID
	return nil
}
