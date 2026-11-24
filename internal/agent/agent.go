package agent

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
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
	Mode     Mode
	Tool     *tools.Tool
	Subagent tools.SubagentFunc
	SpillDir string
}

type Agent struct {
	cwd      string
	ui       Display
	headless bool
	env      *tools.Env
	mode     atomic.Int32

	mu   sync.Mutex
	live live

	busy sync.Mutex
	conv conversation
	wake chan struct{}
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
	tool         *tools.Tool
	onStop       StopHook
	announced    Mode
	carry        []llm.Block
	paused       bool
	thrashAt     int
	windowWarned bool
}

func New(o Options) *Agent {
	a := &Agent{cwd: o.Cwd, ui: o.UI, headless: o.Headless, wake: make(chan struct{}, 1)}
	a.mode.Store(int32(o.Mode))
	a.env = &tools.Env{Cwd: o.Cwd, ReadOnly: a.ReadOnly, Project: a.Project, Scratch: a.scratch, Writable: o.Config.ProjectWritable, Allow: o.Config.ReadOnlyAllow, BashTimeout: o.Config.BashTimeout, SpillDir: o.SpillDir, Subagent: o.Subagent, Jobs: tools.NewJobs()}
	a.live = live{cfg: o.Config, provider: o.Provider}
	a.conv.tool = o.Tool
	return a
}

type Mode int32

const (
	ModeYolo Mode = iota
	ModeReadOnly
	ModeProject
	modeUnknown Mode = -1
)

func (m Mode) String() string {
	switch m {
	case ModeReadOnly:
		return "read-only"
	case ModeProject:
		return "project"
	}
	return "yolo"
}

func (m Mode) Next() Mode {
	switch m {
	case ModeYolo:
		return ModeReadOnly
	case ModeReadOnly:
		return ModeProject
	}
	return ModeYolo
}

const (
	readOnlyNote   = "<mode>Read-only mode is on: write and edit are refused, and bash only runs allowlisted read-only commands.</mode>"
	projectNote    = "<mode>Project mode is on: write, edit and bash can only change files inside the working directory (plus temp and build caches), not .git/config or .git/hooks; bash runs sandboxed with the network on; git cannot change its config, so remotes and upstreams cannot be set.</mode>"
	yoloNote       = "<mode>Yolo mode is on: all tools are available with no limits.</mode>"
	legacyYoloNote = "<mode>Read-only mode is off: all tools are available again.</mode>"
)

var modeNotes = map[Mode]string{ModeReadOnly: readOnlyNote, ModeProject: projectNote, ModeYolo: yoloNote}

func (a *Agent) Mode() Mode { return Mode(a.mode.Load()) }

func (a *Agent) SetMode(m Mode) { a.mode.Store(int32(m)) }

func (a *Agent) SetReadOnly(v bool) {
	if v {
		a.SetMode(ModeReadOnly)
		return
	}
	a.SetMode(ModeYolo)
}

func ParseMode(s string) Mode {
	for _, m := range []Mode{ModeYolo, ModeReadOnly, ModeProject} {
		if m.String() == s {
			return m
		}
	}
	return ModeYolo
}

func (a *Agent) ReadOnly() bool { return a.Mode() == ModeReadOnly }

func (a *Agent) scratch() string {
	if s := a.Session(); s != nil {
		return ensureScratch(ScratchDir(s.ID))
	}
	return ""
}

func (a *Agent) Project() bool { return a.Mode() == ModeProject }

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
	a.conv.announced, a.conv.carry = ModeYolo, nil
	a.conv.paused, a.conv.thrashAt = false, 0
}

func (a *Agent) Reset(s *session.Session) {
	a.busy.Lock()
	defer a.busy.Unlock()
	a.env.Jobs.KillAll()
	a.resetVM(false)
	a.setSession(s)
	a.resetConversation(nil)
	a.SetContextTokens(0)
}

func (a *Agent) Load(s *session.Session, ld session.Loaded) {
	a.busy.Lock()
	defer a.busy.Unlock()
	a.env.Jobs.KillAll()
	a.resetVM(hasCells(ld.Messages))
	a.setSession(s)
	a.resetConversation(ld.Messages)
	a.restoreMode()
	if ld.CtxTokens == 0 {
		ld.CtxTokens = a.estimateTokens()
	}
	a.SetContextTokens(ld.CtxTokens)
}

func hasCells(msgs []llm.Message) bool {
	for _, m := range msgs {
		for _, b := range m.Blocks {
			if b.Type == llm.BlockToolUse && b.Name == "run" {
				return true
			}
		}
	}
	return false
}

func (a *Agent) Reconfigure(cfg config.Config, p llm.Provider) {
	a.busy.Lock()
	defer a.busy.Unlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.live.cfg = cfg
	a.live.provider = p
	a.env.Allow, a.env.Writable, a.env.BashTimeout = cfg.ReadOnlyAllow, cfg.ProjectWritable, cfg.BashTimeout
	a.conv.windowWarned = false
	a.conv.paused, a.conv.thrashAt = false, 0
}

func (a *Agent) ReplaceFunction(f *tools.Function) {
	a.busy.Lock()
	defer a.busy.Unlock()
	if a.conv.tool != nil {
		a.conv.tool.Replace(f)
	}
}

func (a *Agent) resetVM(hadCells bool) {
	if a.conv.tool != nil {
		a.conv.tool.Reset(hadCells)
	}
}

func (a *Agent) toolDefs() []llm.ToolDef {
	if a.conv.tool == nil {
		return nil
	}
	return []llm.ToolDef{a.conv.tool.Def}
}

func (a *Agent) execute(ctx context.Context, calls []llm.Block) []llm.Block {
	if a.conv.tool != nil {
		return a.conv.tool.Execute(ctx, a.env, calls, a.ui)
	}
	out := make([]llm.Block, len(calls))
	for i, c := range calls {
		out[i] = llm.Block{Type: llm.BlockToolResult, ToolUseID: c.ID, Text: fmt.Sprintf("unknown tool %q", c.Name), IsError: true}
	}
	return out
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

func (a *Agent) EditConfig(edit func(*config.Config)) {
	a.busy.Lock()
	defer a.busy.Unlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	edit(&a.live.cfg)
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
	a.live.steer = append(a.live.steer, blocks...)
	a.mu.Unlock()
	select {
	case a.wake <- struct{}{}:
	default:
	}
}

func (a *Agent) queued() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.live.steer) > 0
}

func (a *Agent) KillJobs() {
	a.env.Jobs.KillAll()
}

func (a *Agent) jobBlocks() []llm.Block {
	var out []llm.Block
	for _, n := range a.env.Jobs.TakeNotices() {
		a.ui.Info(fmt.Sprintf("background job %d finished: %s", n.ID, n.Status))
		out = append(out, llm.Block{Type: llm.BlockText, Text: n.Text})
	}
	return out
}

func (a *Agent) awaitJobs(ctx context.Context) bool {
	announced := false
	for {
		if a.env.Jobs.HasNotices() {
			return true
		}
		ids := a.env.Jobs.Waiting()
		if len(ids) == 0 {
			return false
		}
		if a.queued() {
			return true
		}
		if !announced {
			announced = true
			a.ui.Info(fmt.Sprintf("waiting for background %s (Ctrl-C stops waiting, the job keeps running)", jobList(ids)))
		}
		select {
		case <-ctx.Done():
			a.env.Jobs.StopWaiting()
			return false
		case <-a.env.Jobs.Changed():
		case <-a.wake:
		}
	}
}

func jobList(ids []int) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.Itoa(id)
	}
	if len(ids) == 1 {
		return "job " + parts[0]
	}
	return "jobs " + strings.Join(parts, ", ")
}

func (a *Agent) DrainQueue() []llm.Block {
	a.mu.Lock()
	defer a.mu.Unlock()
	q := a.live.steer
	a.live.steer = nil
	return q
}

func lastModeNote(msgs []llm.Message) (Mode, bool) {
	for i := len(msgs) - 1; i >= 0; i-- {
		for _, b := range msgs[i].Blocks {
			if b.Text == legacyYoloNote {
				return ModeYolo, true
			}
			for m, note := range modeNotes {
				if b.Text == note {
					return m, true
				}
			}
		}
	}
	return ModeYolo, false
}

func (a *Agent) restoreMode() {
	if m, ok := lastModeNote(a.conv.messages); ok {
		a.conv.announced = m
	}
}

func (a *Agent) restoreModeAfter(hadNote bool) {
	m, ok := lastModeNote(a.conv.messages)
	switch {
	case ok:
		a.conv.announced = m
	case hadNote:
		a.conv.announced = modeUnknown
	default:
		a.conv.announced = ModeYolo
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
	m := a.Mode()
	if m == a.conv.announced {
		return llm.Block{}, false
	}
	a.conv.announced = m
	return llm.Block{Type: llm.BlockText, Text: modeNotes[m]}, true
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
		Tools:           a.toolDefs(),
		MaxTokens:       l.cfg.MaxTokens,
		ReasoningEffort: l.cfg.ReasoningEffort,
		SessionID:       sid,
		CacheTTL:        l.cfg.CacheTTL,
	}
}

func (a *Agent) onDelta(d llm.Delta, showReasoning bool) bool {
	switch {
	case d.Text == "":
	case d.Kind == llm.DeltaText && !a.headless:
		a.ui.Text(d.Text)
		return true
	case d.Kind == llm.DeltaReasoning && showReasoning:
		a.ui.Reasoning(d.Text)
		return !a.headless
	}
	return false
}

type TurnStats struct {
	Requests int
	Jobs     int
}

func (s TurnStats) String(context string, elapsed time.Duration, done time.Time) string {
	jobs := ""
	switch {
	case s.Jobs == 1:
		jobs = " · 1 bg job"
	case s.Jobs > 1:
		jobs = fmt.Sprintf(" · %d bg jobs", s.Jobs)
	}
	return fmt.Sprintf("%s · cooked for %s · done %s%s", context, fmtElapsed(elapsed), done.Format("15:04"), jobs)
}

func (a *Agent) RunningJobs() int { return a.env.Jobs.Running() }

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
		return FmtTokens(n)
	}
	return fmt.Sprintf("%s/%s (%s)", FmtTokens(n), FmtTokens(window), pct(n, window))
}

func (a *Agent) ContextShort() string {
	window, known := a.Config().Window()
	return usageOf(a.ContextTokens(), window, known)
}

func FmtTokens(n int) string {
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
	blocks, undo := a.addTurnNotes(blocks)
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
			a.recordUsage(usage)
			if ctx.Err() != nil {
				a.appendMsg(llm.Message{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: llm.Interrupted}}}, nil)
				return "", stats, context.Canceled
			}
			if round == 0 && rejected(err) {
				a.dropLast()
				a.dropInterrupted()
				undo()
			}
			return "", stats, err
		}
		stats.Requests++
		a.recordReply(msg, usage)
		calls := msg.ToolUses()
		if a.headless && len(calls) > 0 && strings.TrimSpace(msg.Text()) != "" {
			a.ui.Info(strings.TrimSpace(msg.Text()))
		}
		var next []llm.Block
		if len(calls) > 0 {
			next = a.execute(ctx, calls)
			if ctx.Err() != nil {
				a.appendMsg(llm.Message{Role: llm.RoleUser, Blocks: next}, nil)
				return "", stats, context.Canceled
			}
			if cb, ok := a.changesBlock(); ok {
				next = append(next, cb)
			}
		} else if !a.awaitJobs(ctx) {
			if ctx.Err() != nil {
				return "", stats, context.Canceled
			}
			next = a.stopHookBlocks(ctx, msg.Text(), &stopActive)
			if len(next) == 0 {
				return msg.Text(), stats, nil
			}
		}
		a.appendNext(next)
	}
}

func (a *Agent) addTurnNotes(blocks []llm.Block) ([]llm.Block, func()) {
	n := len(blocks)
	blocks = append(blocks, a.conv.carry...)
	a.conv.carry = nil
	if cb, ok := a.changesBlock(); ok {
		blocks = append(blocks, cb)
	}
	blocks = append(blocks, a.jobBlocks()...)
	carry := slices.Clone(blocks[n:])
	announced := a.conv.announced
	if mb, ok := a.modeBlock(); ok {
		blocks = append(blocks, mb)
	}
	return blocks, func() { a.conv.carry, a.conv.announced = carry, announced }
}

func (a *Agent) recordUsage(u llm.Usage) {
	l := a.snapshot()
	if l.session == nil || (u.Input == 0 && u.Output == 0 && u.Cost == nil) {
		return
	}
	if err := l.session.Usage(l.cfg.Model, &u); err != nil {
		a.ui.Error("session: " + err.Error())
	}
}

func (a *Agent) recordReply(msg llm.Message, usage llm.Usage) {
	a.appendMsg(msg, &usage)
	if usage.Input > 0 {
		a.SetContextTokens(usage.Input + usage.Output)
		a.pauseIfOver(usage.Input)
		a.conv.thrashAt = 0
		return
	}
	a.SetContextTokens(a.estimateTokens())
}

func (a *Agent) stopHookBlocks(ctx context.Context, answer string, active *bool) []llm.Block {
	if a.conv.onStop == nil {
		return nil
	}
	cont, reason := a.conv.onStop(ctx, answer, *active)
	if !cont {
		return nil
	}
	*active = true
	if strings.TrimSpace(reason) == "" {
		reason = "A Stop hook asked you to continue working."
	}
	a.ui.Info("stop hook: " + tools.OneLine(reason, 120))
	return []llm.Block{{Type: llm.BlockText, Text: reason}}
}

func (a *Agent) appendNext(next []llm.Block) {
	next = append(next, a.jobBlocks()...)
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
	return errors.As(err, &apiErr) && apiErr.Status >= 400 && apiErr.Status < 500 && !apiErr.Throttled() && apiErr.Status != http.StatusRequestTimeout
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

func (a *Agent) dropInterrupted() {
	n := len(a.conv.messages)
	if n < 2 || a.conv.messages[n-1].Text() != llm.Interrupted || hasToolResults(a.conv.messages[n-2]) {
		return
	}
	a.dropLast()
	a.dropLast()
}

func hasToolResults(m llm.Message) bool {
	return slices.ContainsFunc(m.Blocks, func(b llm.Block) bool { return b.Type == llm.BlockToolResult })
}

func (a *Agent) EstimateTokens() int {
	a.busy.Lock()
	defer a.busy.Unlock()
	return a.estimateTokens()
}

func (a *Agent) estimateTokens() int {
	n := len(a.System())
	for _, d := range a.toolDefs() {
		n += len(d.Description) + len(d.Schema)
	}
	n /= 4
	for _, m := range a.conv.messages {
		n += msgTokens(m)
	}
	return n
}

const streamAttempts = 4

func streamBackoff(attempt int) time.Duration {
	return time.Duration(2<<attempt) * time.Second
}

func (a *Agent) stream(ctx context.Context) (llm.Message, llm.Usage, error) {
	return a.withRetries(ctx, func() (llm.Message, llm.Usage, error, bool) {
		shown := false
		l := a.snapshot()
		show := l.cfg.ShowReasoningOn()
		msg, usage, err := l.provider.Stream(ctx, a.request(l, a.conv.messages), func(d llm.Delta) {
			shown = a.onDelta(d, show) || shown
		})
		return msg, usage, err, shown
	})
}

func (a *Agent) withRetries(ctx context.Context, call func() (llm.Message, llm.Usage, error, bool)) (llm.Message, llm.Usage, error) {
	for attempt := 0; ; attempt++ {
		msg, usage, err, shown := call()
		if err == nil || attempt == streamAttempts-1 || ctx.Err() != nil || !llm.Retryable(err) || contextTooLong(err) {
			return msg, usage, err
		}
		a.recordUsage(usage)
		note := ""
		if shown {
			a.ui.EndBlock()
			note = " (the output above is discarded)"
		}
		a.ui.Info("↻ retrying: " + tools.OneLine(err.Error(), 100) + note)
		if err := llm.Sleep(ctx, streamBackoff(attempt)); err != nil {
			return msg, llm.Usage{}, err
		}
	}
}
