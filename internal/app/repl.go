package app

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

	"github.com/paul-nameless/qi/internal/agent"
	"github.com/paul-nameless/qi/internal/config"
	"github.com/paul-nameless/qi/internal/llm"
	"github.com/paul-nameless/qi/internal/mcp"
	"github.com/paul-nameless/qi/internal/session"
	"github.com/paul-nameless/qi/internal/tools"
	"github.com/paul-nameless/qi/internal/ui"
	"golang.org/x/sys/unix"
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
	mcp      *mcp.Manager

	mu            sync.Mutex
	turnCancel    context.CancelFunc
	turnDone      chan struct{}
	idle          *time.Timer
	idleCancel    context.CancelFunc
	idleDone      chan struct{}
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
		r.display.Info("no usable OS sandbox (bubblewrap missing, or user namespaces blocked): read-only relies on the command allowlist only")
	}
}

func (r *repl) modeName() string {
	if r.agent.ReadOnly() {
		return "read-only"
	}
	return "yolo"
}

func (r *repl) run() error {
	t, err := ui.OpenTerminal(os.Stdin, os.Stdout)
	if err != nil {
		return fmt.Errorf("terminal: %w", err)
	}
	r.term = t
	defer t.Restore()
	ui.OnCrash(r.closeResources)
	defer ui.CleanupOnPanic()
	hist := filepath.Join(config.Home(), ".local", "share", "qi", "history")
	r.editor = ui.NewEditor(t, r.display, hist)
	r.editor.OnToggle = func() string {
		r.toggleMode()
		return r.prompt()
	}
	r.editor.IsImage = func(text string) []string { return imagePaths(r.agent.Cwd(), text) }
	if r.toolOn(r.agent.Config().Picker) {
		r.editor.OnSlash = r.slashPicker
	}
	if r.toolOn(r.agent.Config().HistoryPicker) {
		r.editor.OnHistory = r.historyPicker
	}
	if r.toolOn(r.agent.Config().FilePicker) {
		r.editor.OnAt = r.filePicker
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
		r.stopIdle()
		images := r.editor.TakeImages(line)
		text, quit := r.handleInput(line)
		if quit {
			return nil
		}
		if text != "" {
			draft = r.turn(func(ctx context.Context) ([]llm.Block, error) {
				return submitPrompt(ctx, r.agent, r.display, r.hooks, text, images...)
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
	where := r.agent.Session().ID
	r.display.Line(r.display.Color(ui.Bold, "qi") + r.display.Color(ui.Dim, fmt.Sprintf(" %s · %s · %s · %s · session %s", r.opts.version, r.agent.Model(), r.modeName(), r.agent.ContextUsage(), where)))
	if n := len(r.agent.Messages()); n > 0 {
		r.printHistory()
	}
	r.display.Info("/help for commands")
}

func (r *repl) handleSignals() {
	ch := make(chan os.Signal, 4)
	signal.Notify(ch, os.Interrupt, syscall.SIGTSTP, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	go func() {
		defer ui.CleanupOnPanic()
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
				r.stop()
			default:
				r.shutdown()
			}
		}
	}()
}

func jobControl() bool {
	self, err := unix.Getsid(0)
	if err != nil {
		return false
	}
	parent, err := unix.Getsid(os.Getppid())
	return err == nil && parent == self
}

func (r *repl) stop() {
	if !jobControl() {
		return
	}
	child := r.term.ChildRunning()
	if !child {
		r.display.ClearFooter()
		r.term.Suspend()
	}
	stopSelf()
	if !child {
		r.term.Resume()
		r.display.RenderPrompt()
	}
}

func stopSelf() {
	cont := make(chan os.Signal, 1)
	signal.Notify(cont, syscall.SIGCONT)
	defer signal.Stop(cont)
	if syscall.Kill(os.Getpid(), syscall.SIGSTOP) == nil {
		<-cont
	}
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
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.quit()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		if r.term != nil {
			r.term.Restore()
		}
	}
	os.Exit(1)
}

func (r *repl) quit() {
	r.settle(3 * time.Second)
	r.closeResources()
	if r.term != nil {
		r.term.Restore()
	}
}

func (r *repl) settle(limit time.Duration) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.stopIdle()
		r.mu.Lock()
		turnDone := r.turnDone
		r.mu.Unlock()
		if r.cancelTurn() && turnDone != nil {
			<-turnDone
		}
	}()
	select {
	case <-done:
	case <-time.After(limit):
	}
}

func (r *repl) closeResources() {
	if r.mcp != nil {
		r.mcp.Close()
	}
	if s := r.agent.Session(); s != nil {
		s.Close()
	}
}

func (r *repl) cancellable(fn func(ctx context.Context) error) error {
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
	var final string
	start := time.Now()
	err := r.cancellable(func(ctx context.Context) error {
		blocks, err := prepare(ctx)
		if err != nil {
			return err
		}
		collectCtx, stopCollect := context.WithCancel(context.Background())
		queue := r.startQueue(ctx)
		draftCh := make(chan string, 1)
		go func() {
			defer ui.CleanupOnPanic()
			draftCh <- r.editor.Collect(collectCtx, draft, ui.Steer{
				OnQueue:  queue.add,
				OnCancel: func() { r.cancelTurn() },
				OnToggle: func() {
					r.toggleMode()
					r.display.Info("mode: " + r.modeName())
				},
			})
		}()
		final, stats, err = r.agent.Turn(ctx, blocks)
		stopCollect()
		draft = <-draftCh
		queue.wait()
		return err
	})
	r.display.EndBlock()
	r.lastCancelled = errors.Is(err, context.Canceled)
	if !r.lastCancelled {
		r.notifyTurn(final, err, time.Since(start))
	}
	switch {
	case errors.Is(err, context.Canceled):
		r.display.Line(r.display.Color(ui.Yellow, "interrupted"))
	case err != nil:
		r.display.Error(err.Error())
	}
	if stats.Requests > 0 {
		r.display.Info(stats.String(r.agent.ContextShort()))
	}
	return draft
}

type submitQueue struct {
	lines chan string
	done  chan struct{}
}

func (r *repl) startQueue(ctx context.Context) *submitQueue {
	q := &submitQueue{lines: make(chan string, 64), done: make(chan struct{})}
	go func() {
		defer close(q.done)
		defer ui.CleanupOnPanic()
		for s := range q.lines {
			r.queueMessage(ctx, s)
		}
	}()
	return q
}

func (q *submitQueue) add(s string) { q.lines <- s }

func (q *submitQueue) wait() {
	close(q.lines)
	<-q.done
}

func (r *repl) queueMessage(ctx context.Context, s string) {
	blocks, err := submitPrompt(ctx, r.agent, r.display, r.hooks, s)
	if err != nil {
		r.display.Error(err.Error())
		return
	}
	r.agent.Queue(blocks)
	r.display.Info("queued: " + tools.OneLine(s, 80))
}

func (r *repl) armIdle() {
	ttl, err := time.ParseDuration(r.agent.Config().CacheTTL)
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
	r.idle = time.AfterFunc(ttl-5*time.Minute, func() { r.idleCompact(gen) })
}

func (r *repl) idleCompact(gen int) {
	defer ui.CleanupOnPanic()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	done := make(chan struct{})
	defer close(done)
	r.mu.Lock()
	if gen != r.idleGen {
		r.mu.Unlock()
		return
	}
	r.idleCancel, r.idleDone = cancel, done
	r.mu.Unlock()
	if err := r.agent.TryCompactIdle(ctx); err != nil && ctx.Err() == nil {
		r.display.Error("idle compaction failed: " + err.Error())
	}
}

func (r *repl) stopIdle() {
	r.mu.Lock()
	r.idleGen++
	if r.idle != nil {
		r.idle.Stop()
		r.idle = nil
	}
	if r.idleCancel != nil {
		r.idleCancel()
	}
	done := r.idleDone
	r.idleCancel, r.idleDone = nil, nil
	r.mu.Unlock()
	if done != nil {
		<-done
	}
}

func (r *repl) resolve(profile string) (config.Config, error) {
	return config.Resolve(config.Options{Path: r.opts.configPath, Profile: profile, Provider: r.opts.provider, Effort: r.opts.effort})
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
	cfg, err := r.resolve(r.agent.Config().Profile)
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
			cfg.SetModel(m.Model)
			return r.apply(cfg, base)
		}
	}
	cfg.SetModel(name)
	return r.apply(cfg, base)
}

var efforts = []pickItem{
	{"default", "the model's own default"},
	{"none", "thinking off (Haiku and some others; Opus can't)"},
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
	if r.runner != nil {
		r.runner.Profile, r.runner.Provider = cfg.Profile, baseProvider
		r.agent.ReplaceTool(tools.Subagent(cfg.Model, withProvider(cfg.SubagentModels, baseProvider), cfg.SubagentEfforts))
	}
	return nil
}

func (r *repl) modelList() string {
	cfg := r.agent.Config()
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
	all := r.agent.Messages()
	msgs := all
	if len(msgs) > historyShown {
		r.display.Info(fmt.Sprintf("… %d earlier messages", len(msgs)-historyShown))
		msgs = msgs[len(msgs)-historyShown:]
	}
	for _, m := range msgs {
		for _, b := range m.Blocks {
			switch {
			case b.Type == llm.BlockText && m.Role == llm.RoleUser:
				if t := strings.TrimSpace(b.Text); t != "" && !llm.IsInBand(t) {
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
	r.display.Info(fmt.Sprintf("resumed %d messages", len(all)))
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
		if b.Type == llm.BlockText && !llm.IsAttachment(b.Text) {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, " ")
}

func (r *repl) newSession() error {
	s, err := session.Create(session.CreateOptions{Cwd: r.agent.Cwd(), Model: r.agent.Model(), Kind: session.Interactive})
	if err != nil {
		return err
	}
	r.agent.Reset(s)
	r.hooks.Session, r.hooks.SessionID = s.Path, s.ID
	return nil
}
