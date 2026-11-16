package agent

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/paul-nameless/qi/internal/config"
	"github.com/paul-nameless/qi/internal/llm"
	"github.com/paul-nameless/qi/internal/session"
	"github.com/paul-nameless/qi/internal/tools"
)

type Display interface {
	tools.UI
	Text(s string)
	Reasoning(s string)
	EndBlock()
	Info(s string)
	Error(s string)
}

type StopHook func(ctx context.Context, lastText string, active bool) (cont bool, reason string)

type Options struct {
	Config   config.Config
	Cwd      string
	Provider llm.Provider
	UI       Display
	Headless bool
	ReadOnly bool
	Tools    []*tools.Tool
	Subagent tools.SubagentFunc
	SpillDir string
}

type Agent struct {
	cwd      string
	ui       Display
	headless bool
	env      *tools.Env
	readOnly atomic.Bool

	mu   sync.Mutex
	live live

	busy sync.Mutex
	conv conversation
}

type live struct {
	cfg       config.Config
	provider  llm.Provider
	session   *session.Session
	system    SystemPrompt
	steer     []llm.Block
	ctxTokens int
}

type conversation struct {
	messages     []llm.Message
	registry     *tools.Registry
	onStop       StopHook
	announced    bool
	paused       bool
	thrashAt     int
	windowWarned bool
}

func New(o Options) *Agent {
	a := &Agent{cwd: o.Cwd, ui: o.UI, headless: o.Headless}
	a.readOnly.Store(o.ReadOnly)
	a.env = &tools.Env{Cwd: o.Cwd, ReadOnly: a.ReadOnly, Allow: o.Config.ReadOnlyAllow, BashTimeout: o.Config.BashTimeout, SpillDir: o.SpillDir, Subagent: o.Subagent}
	a.live = live{cfg: o.Config, provider: o.Provider}
	a.conv.registry = tools.NewRegistry(o.Tools...)
	return a
}

const (
	readOnlyNote = "<mode>Read-only mode is on: write and edit are refused, and bash only runs allowlisted read-only commands.</mode>"
	yoloNote     = "<mode>Read-only mode is off: all tools are available again.</mode>"
)

func (a *Agent) ReadOnly() bool { return a.readOnly.Load() }

func (a *Agent) SetReadOnly(v bool) {
	a.readOnly.Store(v)
}

func (a *Agent) Cwd() string { return a.cwd }

func (a *Agent) snapshot() live {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.live
}

func (a *Agent) Config() config.Config { return a.snapshot().cfg }

func (a *Agent) Model() string { return a.snapshot().cfg.Model }

func (a *Agent) Session() *session.Session { return a.snapshot().session }

func (a *Agent) System() string { return a.snapshot().system.Text }

func (a *Agent) Messages() []llm.Message {
	a.busy.Lock()
	defer a.busy.Unlock()
	return slices.Clone(a.conv.messages)
}

func (a *Agent) setSession(s *session.Session) {
	a.mu.Lock()
	old := a.live.session
	a.live.session = s
	a.mu.Unlock()
	if old != nil && old != s {
		old.Close()
	}
}

func (a *Agent) resetConversation(msgs []llm.Message) {
	a.conv.messages = msgs
	a.env.ForgetFiles()
	a.conv.announced = false
	a.conv.paused, a.conv.thrashAt = false, 0
}

func (a *Agent) Reset(s *session.Session) {
	a.busy.Lock()
	defer a.busy.Unlock()
	a.setSession(s)
	a.resetConversation(nil)
	a.SetContextTokens(0)
}

func (a *Agent) Load(s *session.Session, ld session.Loaded) {
	a.busy.Lock()
	defer a.busy.Unlock()
	a.setSession(s)
	a.resetConversation(ld.Messages)
	a.restoreMode()
	if ld.CtxTokens == 0 {
		ld.CtxTokens = a.estimateTokens()
	}
	a.SetContextTokens(ld.CtxTokens)
}

func (a *Agent) Reconfigure(cfg config.Config, p llm.Provider) {
	a.busy.Lock()
	defer a.busy.Unlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.live.cfg = cfg
	a.live.provider = p
	a.env.Allow, a.env.BashTimeout = cfg.ReadOnlyAllow, cfg.BashTimeout
	a.conv.windowWarned = false
	a.conv.paused, a.conv.thrashAt = false, 0
}

func (a *Agent) ReplaceTool(t *tools.Tool) {
	a.busy.Lock()
	defer a.busy.Unlock()
	a.conv.registry.Replace(t)
}

func (a *Agent) SetHooks(pre tools.Hook, post tools.PostHook, stop StopHook) {
	a.busy.Lock()
	defer a.busy.Unlock()
	a.env.PreHook, a.env.PostHook = pre, post
	a.conv.onStop = stop
}

func (a *Agent) SetSystem(sp SystemPrompt) {
	a.busy.Lock()
	defer a.busy.Unlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.live.system = sp
}

func (a *Agent) SetEffort(effort string) {
	a.busy.Lock()
	defer a.busy.Unlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.live.cfg.ReasoningEffort = effort
}

func (a *Agent) ContextTokens() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.live.ctxTokens
}

func (a *Agent) SetContextTokens(n int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.live.ctxTokens = n
}

func (a *Agent) Queue(blocks []llm.Block) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.live.steer = append(a.live.steer, blocks...)
}

func (a *Agent) DrainQueue() []llm.Block {
	a.mu.Lock()
	defer a.mu.Unlock()
	q := a.live.steer
	a.live.steer = nil
	return q
}

func lastModeNote(msgs []llm.Message) (readOnly, found bool) {
	for i := len(msgs) - 1; i >= 0; i-- {
		for _, b := range msgs[i].Blocks {
			switch b.Text {
			case readOnlyNote:
				return true, true
			case yoloNote:
				return false, true
			}
		}
	}
	return false, false
}

func (a *Agent) restoreMode() {
	if ro, ok := lastModeNote(a.conv.messages); ok {
		a.conv.announced = ro
	}
}

func (a *Agent) restoreModeAfter(hadNote bool) {
	ro, ok := lastModeNote(a.conv.messages)
	switch {
	case ok:
		a.conv.announced = ro
	case hadNote:
		a.conv.announced = !a.ReadOnly()
	default:
		a.conv.announced = false
	}
}

func (a *Agent) changesBlock() (llm.Block, bool) {
	text, paths := a.env.FileChanges()
	if text == "" {
		return llm.Block{}, false
	}
	a.ui.Info("changed on disk, sent to the model: " + strings.Join(paths, ", "))
	return llm.Block{Type: llm.BlockText, Text: text}, true
}

func (a *Agent) modeBlock() (llm.Block, bool) {
	ro := a.ReadOnly()
	if ro == a.conv.announced {
		return llm.Block{}, false
	}
	a.conv.announced = ro
	if ro {
		return llm.Block{Type: llm.BlockText, Text: readOnlyNote}, true
	}
	return llm.Block{Type: llm.BlockText, Text: yoloNote}, true
}

func (a *Agent) appendMsg(m llm.Message, u *llm.Usage) {
	a.conv.messages = append(a.conv.messages, m)
	l := a.snapshot()
	if l.session != nil {
		if err := l.session.Message(m, l.cfg.Model, u); err != nil {
			a.ui.Error("session: " + err.Error())
		}
	}
}

func (a *Agent) request(l live, msgs []llm.Message) llm.Request {
	sid := ""
	if l.session != nil {
		sid = l.session.ID
	}
	return llm.Request{
		Model:           l.cfg.Model,
		System:          l.system.Text,
		StaticSystem:    l.system.Static,
		SessionSystem:   l.system.Session,
		Messages:        msgs,
		Tools:           a.conv.registry.Defs(),
		MaxTokens:       l.cfg.MaxTokens,
		ReasoningEffort: l.cfg.ReasoningEffort,
		SessionID:       sid,
		CacheTTL:        l.cfg.CacheTTL,
	}
}

func (a *Agent) onDelta(d llm.Delta, showReasoning bool) {
	switch d.Kind {
	case llm.DeltaText:
		if !a.headless {
			a.ui.Text(d.Text)
		}
	case llm.DeltaReasoning:
		if showReasoning {
			a.ui.Reasoning(d.Text)
		}
	}
}

type TurnStats struct {
	Requests int
	Usage    llm.Usage
}

func (s TurnStats) String(context string, elapsed time.Duration, done time.Time) string {
	return fmt.Sprintf("%d req · %s · cooked for %s · done %s", s.Requests, context, fmtElapsed(elapsed), done.Format("15:04"))
}

func cachedPct(u llm.Usage) string {
	if u.Input == 0 {
		return ""
	}
	return fmt.Sprintf(" (%d%% cached)", u.CacheRead*100/u.Input)
}

func fmtElapsed(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
}

func (a *Agent) ContextUsage() string {
	n := a.ContextTokens()
	if n == 0 {
		n = a.EstimateTokens()
	}
	window, known := a.Config().Window()
	return usageOf(n, window, known)
}

func usageOf(n, window int, known bool) string {
	if !known || window <= 0 {
		return fmtTokens(n)
	}
	return fmt.Sprintf("%s/%s (%s)", fmtTokens(n), fmtTokens(window), pct(n, window))
}

func (a *Agent) ContextShort() string {
	window, known := a.Config().Window()
	return usageOf(a.ContextTokens(), window, known)
}

func fmtTokens(n int) string {
	switch {
	case n >= 1000000:
		return strconv.FormatFloat(math.Round(float64(n)/1e4)/100, 'f', -1, 64) + "M"
	case n >= 100000:
		return fmt.Sprintf("%dk", n/1000)
	}
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

func (a *Agent) Turn(ctx context.Context, blocks []llm.Block) (string, TurnStats, error) {
	a.busy.Lock()
	defer a.busy.Unlock()
	var stats TurnStats
	compactFailed := false
	if a.needsCompaction() {
		if err := a.compact(ctx, compactOpts{auto: true}); err != nil {
			compactFailed = true
			a.ui.Error("compaction failed: " + err.Error())
		}
	}
	if cb, ok := a.changesBlock(); ok {
		blocks = append(blocks, cb)
	}
	if mb, ok := a.modeBlock(); ok {
		blocks = append(blocks, mb)
	}
	a.appendMsg(llm.Message{Role: llm.RoleUser, Blocks: blocks}, nil)
	maxRounds := a.Config().MaxRounds
	stopActive := false
	overflowRetried := false
	for round := 0; ; round++ {
		if maxRounds > 0 && round >= maxRounds {
			return "", stats, fmt.Errorf("stopped after %d model requests (max_rounds)", maxRounds)
		}
		if !compactFailed && a.needsCompaction() {
			if err := a.compact(ctx, compactOpts{auto: true, midTurn: true}); err != nil {
				compactFailed = true
				a.ui.Error("compaction failed: " + err.Error())
			}
		}
		a.evictImages()
		msg, usage, err := a.stream(ctx)
		a.ui.EndBlock()
		if err != nil && !overflowRetried && ctx.Err() == nil && contextTooLong(err) {
			overflowRetried = true
			a.ui.Info("context too long for the model; compacting and retrying")
			cerr := a.compact(ctx, compactOpts{midTurn: true, overflow: true})
			if cerr == nil {
				continue
			}
			a.ui.Error("compaction failed: " + cerr.Error())
		}
		if err != nil {
			if round == 0 && (ctx.Err() != nil || rejected(err)) {
				a.dropLast()
			}
			if ctx.Err() != nil {
				return "", stats, context.Canceled
			}
			return "", stats, err
		}
		stats.Requests++
		stats.Usage.Add(usage)
		a.appendMsg(msg, &usage)
		if usage.Input > 0 {
			a.SetContextTokens(usage.Input + usage.Output)
			a.pauseIfOver(usage.Input)
			a.conv.thrashAt = 0
		} else {
			a.SetContextTokens(a.estimateTokens())
		}

		calls := msg.ToolUses()
		if a.headless && len(calls) > 0 && strings.TrimSpace(msg.Text()) != "" {
			a.ui.Info(strings.TrimSpace(msg.Text()))
		}
		var next []llm.Block
		if len(calls) > 0 {
			next = a.conv.registry.Execute(ctx, a.env, calls, a.ui)
			if ctx.Err() != nil {
				a.appendMsg(llm.Message{Role: llm.RoleUser, Blocks: next}, nil)
				return "", stats, context.Canceled
			}
			if cb, ok := a.changesBlock(); ok {
				next = append(next, cb)
			}
		} else {
			if a.conv.onStop != nil {
				if cont, reason := a.conv.onStop(ctx, msg.Text(), stopActive); cont {
					stopActive = true
					if strings.TrimSpace(reason) == "" {
						reason = "A Stop hook asked you to continue working."
					}
					a.ui.Info("stop hook: " + tools.OneLine(reason, 120))
					next = append(next, llm.Block{Type: llm.BlockText, Text: reason})
				}
			}
			if len(next) == 0 {
				return msg.Text(), stats, nil
			}
		}
		if q := a.DrainQueue(); len(q) > 0 {
			next = append(next, llm.Block{Type: llm.BlockText, Text: llm.QueuedLead})
			next = append(next, q...)
		}
		if mb, ok := a.modeBlock(); ok {
			next = append(next, mb)
		}
		a.appendMsg(llm.Message{Role: llm.RoleUser, Blocks: next}, nil)
		a.SetContextTokens(a.ContextTokens() + blockChars(next)/4)
	}
}

func blockChars(blocks []llm.Block) int {
	n := 0
	for _, b := range blocks {
		n += len(b.Text)
		if b.Type == llm.BlockImage {
			n += 4 * llm.ImageTokens(b)
		}
		for _, img := range b.Images {
			n += 4 * llm.ImageTokens(img)
		}
	}
	return n
}

func rejected(err error) bool {
	var apiErr *llm.APIError
	return errors.As(err, &apiErr) && apiErr.Status >= 400 && apiErr.Status < 500 && apiErr.Status != 429
}

func (a *Agent) dropLast() {
	if len(a.conv.messages) == 0 {
		return
	}
	a.conv.messages = a.conv.messages[:len(a.conv.messages)-1]
	if s := a.Session(); s != nil {
		if err := s.Drop(); err != nil {
			a.ui.Error("session: " + err.Error())
		}
	}
}

func (a *Agent) EstimateTokens() int {
	a.busy.Lock()
	defer a.busy.Unlock()
	return a.estimateTokens()
}

func (a *Agent) estimateTokens() int {
	n := len(a.System())
	for _, d := range a.conv.registry.Defs() {
		n += len(d.Description) + len(d.Schema)
	}
	n /= 4
	for _, m := range a.conv.messages {
		n += msgTokens(m)
	}
	return n
}

const streamAttempts = 3

func streamBackoff(attempt int) time.Duration {
	return time.Duration(2<<attempt) * time.Second
}

func (a *Agent) stream(ctx context.Context) (llm.Message, llm.Usage, error) {
	for attempt := 0; ; attempt++ {
		emitted := false
		l := a.snapshot()
		show := l.cfg.ShowReasoningOn()
		msg, usage, err := l.provider.Stream(ctx, a.request(l, a.conv.messages), func(d llm.Delta) {
			emitted = true
			a.onDelta(d, show)
		})
		if err == nil || emitted || attempt == streamAttempts-1 || ctx.Err() != nil || !llm.Retryable(err) {
			return msg, usage, err
		}
		a.ui.Info("retrying: " + tools.OneLine(err.Error(), 100))
		if err := llm.Sleep(ctx, streamBackoff(attempt)); err != nil {
			return msg, usage, err
		}
	}
}
