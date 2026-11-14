package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/paul-nameless/aicode/internal/config"
	"github.com/paul-nameless/aicode/internal/llm"
	"github.com/paul-nameless/aicode/internal/session"
	"github.com/paul-nameless/aicode/internal/tools"
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

type Agent struct {
	Cfg          config.Config
	Cwd          string
	Provider     llm.Provider
	Registry     *tools.Registry
	Env          *tools.Env
	Session      *session.Session
	System       string
	StaticSystem int
	Messages     []llm.Message
	UI           Display
	OnStop       StopHook
	Headless     bool

	readOnly  atomic.Bool
	announced bool
	mu        sync.Mutex
	steer     []llm.Block
	ctxTokens int
	busy      sync.Mutex

	paused       bool
	thrashAt     int
	windowWarned bool
}

const (
	readOnlyNote = "<mode>Read-only mode is on: write and edit are refused, and bash only runs allowlisted read-only commands.</mode>"
	yoloNote     = "<mode>Read-only mode is off: all tools are available again.</mode>"
)

func (a *Agent) ReadOnly() bool { return a.readOnly.Load() }

func (a *Agent) SetReadOnly(v bool) {
	a.readOnly.Store(v)
}

func (a *Agent) Reset(s *session.Session) {
	a.busy.Lock()
	defer a.busy.Unlock()
	if a.Session != nil {
		a.Session.Close()
	}
	a.Session = s
	a.Messages = nil
	a.announced = false
	a.paused, a.thrashAt = false, 0
	a.SetContextTokens(0)
}

func (a *Agent) Reconfigure(cfg config.Config, p llm.Provider) {
	a.busy.Lock()
	defer a.busy.Unlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.Cfg = cfg
	a.Provider = p
	a.windowWarned = false
	a.paused, a.thrashAt = false, 0
}

func (a *Agent) SetEffort(effort string) {
	a.busy.Lock()
	defer a.busy.Unlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.Cfg.ReasoningEffort = effort
}

func (a *Agent) Model() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.Cfg.Model
}

func (a *Agent) ContextTokens() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.ctxTokens
}

func (a *Agent) SetContextTokens(n int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ctxTokens = n
}

func (a *Agent) Queue(blocks []llm.Block) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.steer = append(a.steer, blocks...)
}

func (a *Agent) DrainQueue() []llm.Block {
	a.mu.Lock()
	defer a.mu.Unlock()
	q := a.steer
	a.steer = nil
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

func (a *Agent) RestoreMode() {
	if ro, ok := lastModeNote(a.Messages); ok {
		a.announced = ro
	}
}

func (a *Agent) restoreModeAfter(hadNote bool) {
	ro, ok := lastModeNote(a.Messages)
	switch {
	case ok:
		a.announced = ro
	case hadNote:
		a.announced = !a.ReadOnly()
	default:
		a.announced = false
	}
}

func (a *Agent) modeBlock() (llm.Block, bool) {
	ro := a.ReadOnly()
	if ro == a.announced {
		return llm.Block{}, false
	}
	a.announced = ro
	if ro {
		return llm.Block{Type: llm.BlockText, Text: readOnlyNote}, true
	}
	return llm.Block{Type: llm.BlockText, Text: yoloNote}, true
}

func (a *Agent) appendMsg(m llm.Message, u *llm.Usage) {
	a.Messages = append(a.Messages, m)
	if a.Session != nil {
		if err := a.Session.Message(m, a.Model(), u); err != nil {
			a.UI.Error("session: " + err.Error())
		}
	}
}

func (a *Agent) request(msgs []llm.Message) llm.Request {
	sid := ""
	if a.Session != nil {
		sid = a.Session.ID
	}
	return llm.Request{
		Model:           a.Model(),
		System:          a.System,
		StaticSystem:    a.StaticSystem,
		Messages:        msgs,
		Tools:           a.Registry.Defs(),
		MaxTokens:       a.Cfg.MaxTokens,
		ReasoningEffort: a.Cfg.ReasoningEffort,
		SessionID:       sid,
		CacheTTL:        a.Cfg.CacheTTL,
	}
}

func (a *Agent) onDelta(d llm.Delta) {
	switch d.Kind {
	case llm.DeltaText:
		if !a.Headless {
			a.UI.Text(d.Text)
		}
	case llm.DeltaReasoning:
		if a.Cfg.ShowReasoningOn() {
			a.UI.Reasoning(d.Text)
		}
	}
}

type TurnStats struct {
	Requests int
	Usage    llm.Usage
}

func (s TurnStats) String(ctxTokens int) string {
	return fmt.Sprintf("%d req · in %s%s · out %s · context %s", s.Requests, fmtTokens(s.Usage.Input), cachedPct(s.Usage), fmtTokens(s.Usage.Output), fmtTokens(ctxTokens))
}

func cachedPct(u llm.Usage) string {
	if u.Input == 0 {
		return ""
	}
	return fmt.Sprintf(" (%d%% cached)", u.CacheRead*100/u.Input)
}

func fmtTokens(n int) string {
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
			a.UI.Error("compaction failed: " + err.Error())
		}
	}
	if mb, ok := a.modeBlock(); ok {
		blocks = append(blocks, mb)
	}
	a.appendMsg(llm.Message{Role: "user", Blocks: blocks}, nil)
	stopActive := false
	overflowRetried := false
	for round := 0; ; round++ {
		if a.Cfg.MaxRounds > 0 && round >= a.Cfg.MaxRounds {
			return "", stats, fmt.Errorf("stopped after %d model requests (max_rounds)", a.Cfg.MaxRounds)
		}
		if !compactFailed && a.needsCompaction() {
			if err := a.compact(ctx, compactOpts{auto: true, midTurn: true}); err != nil {
				compactFailed = true
				a.UI.Error("compaction failed: " + err.Error())
			}
		}
		msg, usage, err := a.stream(ctx)
		a.UI.EndBlock()
		if err != nil && !overflowRetried && ctx.Err() == nil && contextTooLong(err) {
			overflowRetried = true
			a.UI.Info("context too long for the model; compacting and retrying")
			cerr := a.compact(ctx, compactOpts{midTurn: true, overflow: true})
			if cerr == nil {
				continue
			}
			a.UI.Error("compaction failed: " + cerr.Error())
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
			a.thrashAt = 0
		} else {
			a.SetContextTokens(a.EstimateTokens())
		}

		calls := msg.ToolUses()
		if a.Headless && len(calls) > 0 && strings.TrimSpace(msg.Text()) != "" {
			a.UI.Info(strings.TrimSpace(msg.Text()))
		}
		var next []llm.Block
		if len(calls) > 0 {
			next = a.Registry.Execute(ctx, a.Env, calls, a.UI)
			if ctx.Err() != nil {
				a.appendMsg(llm.Message{Role: "user", Blocks: next}, nil)
				return "", stats, context.Canceled
			}
		} else {
			if a.OnStop != nil {
				if cont, reason := a.OnStop(ctx, msg.Text(), stopActive); cont {
					stopActive = true
					if strings.TrimSpace(reason) == "" {
						reason = "A Stop hook asked you to continue working."
					}
					a.UI.Info("stop hook: " + tools.OneLine(reason, 120))
					next = append(next, llm.Block{Type: llm.BlockText, Text: reason})
				}
			}
			if len(next) == 0 {
				return msg.Text(), stats, nil
			}
		}
		if q := a.DrainQueue(); len(q) > 0 {
			next = append(next, llm.Block{Type: llm.BlockText, Text: "The user sent this while you were working:"})
			next = append(next, q...)
		}
		if mb, ok := a.modeBlock(); ok {
			next = append(next, mb)
		}
		a.appendMsg(llm.Message{Role: "user", Blocks: next}, nil)
		a.SetContextTokens(a.ContextTokens() + blockChars(next)/4)
	}
}

const imageChars = 8000

func blockChars(blocks []llm.Block) int {
	n := 0
	for _, b := range blocks {
		n += len(b.Text)
		if b.Type == llm.BlockImage {
			n += imageChars
		}
		n += len(b.Images) * imageChars
	}
	return n
}

func rejected(err error) bool {
	var apiErr *llm.APIError
	return errors.As(err, &apiErr) && apiErr.Status >= 400 && apiErr.Status < 500 && apiErr.Status != 429
}

func (a *Agent) dropLast() {
	if len(a.Messages) == 0 {
		return
	}
	a.Messages = a.Messages[:len(a.Messages)-1]
	if a.Session != nil {
		if err := a.Session.Drop(); err != nil {
			a.UI.Error("session: " + err.Error())
		}
	}
}

func (a *Agent) EstimateTokens() int {
	n := len(a.System)
	for _, d := range a.Registry.Defs() {
		n += len(d.Description) + len(d.Schema)
	}
	n /= 4
	for _, m := range a.Messages {
		n += msgTokens(m)
	}
	return n
}

func (a *Agent) stream(ctx context.Context) (llm.Message, llm.Usage, error) {
	for attempt := 0; ; attempt++ {
		emitted := false
		msg, usage, err := a.Provider.Stream(ctx, a.request(a.Messages), func(d llm.Delta) {
			emitted = true
			a.onDelta(d)
		})
		if err == nil || emitted || attempt == 2 || ctx.Err() != nil || !retryableStream(err) {
			return msg, usage, err
		}
		a.UI.Info("retrying: " + tools.OneLine(err.Error(), 100))
		select {
		case <-ctx.Done():
			return msg, usage, ctx.Err()
		case <-time.After(time.Duration(2<<attempt) * time.Second):
		}
	}
}

func retryableStream(err error) bool {
	var apiErr *llm.APIError
	if errors.As(err, &apiErr) {
		return false
	}
	s := err.Error()
	return errors.Is(err, llm.ErrStalled) || strings.Contains(s, "overloaded") || strings.Contains(s, "api_error") || strings.Contains(s, "stream ended") || strings.Contains(s, "unexpected EOF") || strings.Contains(s, "connection reset")
}
