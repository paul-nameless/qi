package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/paul-nameless/qi/internal/agent"
	"github.com/paul-nameless/qi/internal/config"
	"github.com/paul-nameless/qi/internal/llm"
	"github.com/paul-nameless/qi/internal/mcp"
	"github.com/paul-nameless/qi/internal/session"
	"github.com/paul-nameless/qi/internal/tools"
	"github.com/paul-nameless/qi/internal/ui"
)

type repl struct {
	prices   *priceSource
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
	autoTurns     int
}

const maxAutoTurns = 3

func (r *repl) prompt() string {
	switch r.agent.Mode() {
	case config.ModeReadOnly:
		return r.display.Color(ui.Yellow, "read-only") + r.display.Color(ui.Cyan, " ❯ ")
	case config.ModeProject:
		return r.display.Color(ui.Green, "project") + r.display.Color(ui.Cyan, " ❯ ")
	}
	return r.display.Color(ui.Cyan, "❯ ")
}

func (r *repl) toggleMode() {
	m := r.agent.Mode().Next()
	r.agent.SetMode(m)
	r.warnMode(m)
}

func (r *repl) warnMode(m config.Mode) {
	warnNoSandbox(r.display, m)
	if m == config.ModeProject && broadProject(r.agent.Cwd()) {
		r.display.Info(broadProjectNote)
	}
}

const broadProjectNote = "project mode in your home folder or / protects almost nothing: the model can change everything under it"

func broadProject(cwd string) bool {
	return cwd == "/" || cwd == config.Home()
}

const (
	noSandboxNote        = "no usable OS sandbox (bubblewrap missing, or user namespaces blocked): read-only relies on the command allowlist only"
	noNetworkNote        = "the OS sandbox can't isolate the network here (bwrap --unshare-net failed): read-only commands can still reach it"
	noProjectSandboxNote = "no usable OS sandbox (bubblewrap missing, user namespaces blocked, or qi already sandboxed): project mode refuses bash, write and edit stay limited to the project"
)

func warnNoSandbox(display notifier, m config.Mode) {
	switch {
	case m == config.ModeProject && !tools.HasSandbox():
		display.Info(noProjectSandboxNote)
	case m != config.ModeReadOnly:
	case !tools.HasSandbox():
		display.Info(noSandboxNote)
	case !tools.SandboxIsolatesNetwork():
		display.Info(noNetworkNote)
	}
}

func (r *repl) modeName() string {
	return r.agent.Mode().String()
}

func (r *repl) run() error {
	t, err := ui.OpenTerminal()
	if err != nil {
		return fmt.Errorf("terminal: %w", err)
	}
	r.term = t
	defer r.resumeHint()
	defer t.Restore()
	ui.OnCrash(r.closeResources)
	defer ui.CleanupOnPanic()
	hist := filepath.Join(config.DataDir(), "history")
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
	r.editor.OnCopyID = func() { r.report(r.copyAnswer("id")) }
	r.editor.Wake = r.agent.Jobs().Changed()
	r.editor.Woken = r.agent.Jobs().Exited
	r.handleSignals()
	r.handleResize()
	r.banner()
	r.warnMode(r.agent.Mode())

	draft := ""
	for {
		line, err := r.editor.ReadLine(r.prompt(), draft)
		draft = ""
		if errors.Is(err, ui.ErrEOF) {
			return nil
		}
		if errors.Is(err, ui.ErrWake) {
			draft = r.jobTurn()
			continue
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
		r.autoTurns = 0
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
		draft = r.runQueued(draft)
		r.armIdle()
	}
}

func (r *repl) jobTurn() string {
	if !r.agent.Jobs().Exited() {
		return ""
	}
	if r.autoTurns >= maxAutoTurns {
		r.agent.Jobs().Hold()
		if r.autoTurns == maxAutoTurns {
			r.autoTurns++
			r.display.Info("background jobs keep finishing; their notices wait for your next message")
		}
		return ""
	}
	r.autoTurns++
	r.stopIdle()
	draft := r.runQueued(r.turn(func(context.Context) ([]llm.Block, error) { return nil, nil }, ""))
	r.armIdle()
	return draft
}

func (r *repl) runQueued(draft string) string {
	for q := r.agent.DrainQueue(); len(q) > 0 && !r.lastCancelled; q = r.agent.DrainQueue() {
		draft = r.turn(func(context.Context) ([]llm.Block, error) { return q, nil }, draft)
	}
	if q := r.agent.DrainQueue(); len(q) > 0 {
		draft = strings.TrimSpace(draft + " " + queuedText(q))
	}
	return draft
}

func (r *repl) banner() {
	r.display.Line(r.bannerLine())
	if line := subagentsLine(r.agent.Config().SubagentChoices()); line != "" {
		r.display.Info(line)
	}
	if n := len(r.agent.Messages()); n > 0 {
		r.printHistory()
	}
}

func subagentsLine(choices []config.SubagentChoice) string {
	if len(choices) == 0 || choices[0].Self {
		return ""
	}
	names := make([]string, len(choices))
	for i, c := range choices {
		names[i] = c.String()
	}
	return "subagents: " + strings.Join(names, ", ")
}

func subagentsMenu(choices []config.SubagentChoice) string {
	if line := subagentsLine(choices); line != "" {
		return line
	}
	return "subagents: " + config.SelfChoice
}

func bannerVersion(v string) string {
	if strings.HasPrefix(v, "dev-") {
		return "dev"
	}
	return v
}

func (r *repl) bannerLine() string {
	return r.display.Color(ui.Bold, "qi") + r.display.Color(ui.Dim, fmt.Sprintf(" %s · %s · %s · %s · %s", bannerVersion(r.opts.version), r.agent.Model(), effortName(r.agent.Config().ReasoningEffort), r.modeName(), r.agent.ContextUsage()))
}

func (r *repl) resumeHint() {
	s := r.agent.Session()
	if s == nil || len(r.agent.Messages()) == 0 {
		return
	}
	fmt.Println(r.display.Color(ui.Dim, "qi -r "+s.ID))
}

func (r *repl) status() string {
	cfg := r.agent.Config()
	rows := [][2]string{
		{"version", r.opts.version},
		{"model", cfg.Model + providerNote(cfg.Provider)},
		{"effort", effortName(cfg.ReasoningEffort)},
		{"profile", orDash(cfg.Profile)},
		{"mode", r.modeName()},
		{"context", strings.TrimPrefix(r.agent.ContextUsage(), "context ")},
		{"cwd", config.ShortHome(r.agent.Cwd())},
		{"config", config.ShortHome(r.configPath())},
	}
	if s := r.agent.Session(); s != nil {
		rows = append(rows, [2]string{"session", s.ID}, [2]string{"file", config.ShortHome(s.Path)})
	}
	var b strings.Builder
	for i, row := range rows {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "%-8s %s", row[0], row[1])
	}
	return b.String()
}

func providerNote(p string) string {
	if p == "" {
		return ""
	}
	return " (" + p + ")"
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
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
		r.display.SetRunCode(r.agent.Config().RunCodeOn())
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
	if err == nil && r.agent.Config().TerminalImagesOn() {
		r.display.Images(final)
	}
	r.lastCancelled = errors.Is(err, context.Canceled)
	if r.lastCancelled {
		r.agent.Jobs().Hold()
		r.autoTurns = maxAutoTurns + 1
	}
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
		r.display.Line("")
		stats.Jobs = r.agent.Jobs().Running()
		r.display.Info(stats.String(r.agent.ContextShort(), time.Since(start), time.Now()))
		r.display.Line("")
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
	r.autoTurns = 0
	r.agent.Queue(blocks)
	r.display.Info("queued: " + tools.OneLine(s, 80))
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
				r.display.Info(toolUseText(b, 90))
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
	r.switchSession(func() { r.agent.Reset(s) })
	return nil
}

func (r *repl) switchSession(swap func()) {
	if cur := r.agent.Session(); cur != nil {
		agent.RemoveScratchIfEmpty(cur.ID)
	}
	swap()
	r.report(r.loadPrompt())
}
