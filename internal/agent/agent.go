package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

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
	Cfg      config.Config
	Cwd      string
	Provider llm.Provider
	Registry *tools.Registry
	Env      *tools.Env
	Session  *session.Session
	System   string
	Messages []llm.Message
	UI       Display
	OnStop   StopHook
	Headless bool

	readOnly  atomic.Bool
	announced bool
	mu        sync.Mutex
	steer     []string
	ctxTokens int
	busy      sync.Mutex
}

const (
	readOnlyNote = "<mode>Read-only mode is on: write and edit are refused, and bash only runs allowlisted read-only commands.</mode>"
	yoloNote     = "<mode>Read-only mode is off: all tools are available again.</mode>"
	compactAsk   = "Summarize this conversation so it can continue in a fresh context. Do not call tools. Include: the user's goal and requests, decisions made, files read or changed (with paths), commands run and their important results, the current state, and the next steps. Be dense and specific; this summary replaces the conversation."
	compactLead  = "This session was compacted to save context. Summary of the conversation so far:\n\n"
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
	a.SetContextTokens(0)
}

func (a *Agent) Reconfigure(cfg config.Config, p llm.Provider) {
	a.busy.Lock()
	defer a.busy.Unlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.Cfg = cfg
	a.Provider = p
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

func (a *Agent) Queue(text string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.steer = append(a.steer, text)
}

func (a *Agent) DrainQueue() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	q := a.steer
	a.steer = nil
	return q
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
	if a.ContextTokens() > a.Cfg.CompactAt {
		if err := a.compact(ctx, false); err != nil {
			a.UI.Error("compaction failed: " + err.Error())
		}
	}
	if mb, ok := a.modeBlock(); ok {
		blocks = append(blocks, mb)
	}
	a.appendMsg(llm.Message{Role: "user", Blocks: blocks}, nil)
	stopActive := false
	for {
		if a.ContextTokens() > a.Cfg.CompactAt {
			if err := a.compact(ctx, true); err != nil {
				a.UI.Error("compaction failed: " + err.Error())
			}
		}
		msg, usage, err := a.Provider.Stream(ctx, a.request(a.Messages), a.onDelta)
		a.UI.EndBlock()
		if err != nil {
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
		} else {
			a.SetContextTokens(a.estimateTokens())
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
					a.UI.Info("stop hook: " + tools.OneLine(reason, 120))
					next = append(next, llm.Block{Type: llm.BlockText, Text: reason})
				}
			}
			if len(next) == 0 {
				return msg.Text(), stats, nil
			}
		}
		if q := a.DrainQueue(); len(q) > 0 {
			next = append(next, llm.Block{Type: llm.BlockText, Text: "<user-message sent while you were working>\n" + strings.Join(q, "\n\n") + "\n</user-message>"})
		}
		if mb, ok := a.modeBlock(); ok {
			next = append(next, mb)
		}
		a.appendMsg(llm.Message{Role: "user", Blocks: next}, nil)
	}
}

func (a *Agent) estimateTokens() int {
	n := len(a.System)
	for _, d := range a.Registry.Defs() {
		n += len(d.Description) + len(d.Schema)
	}
	for _, m := range a.Messages {
		for _, b := range m.Blocks {
			n += len(b.Text) + len(b.Input) + len(b.Raw)
		}
	}
	return n / 4
}

func (a *Agent) Compact(ctx context.Context) error {
	if !a.busy.TryLock() {
		return errors.New("busy")
	}
	defer a.busy.Unlock()
	return a.compact(ctx, false)
}

func (a *Agent) TryCompactIdle(ctx context.Context) error {
	if !a.busy.TryLock() {
		return nil
	}
	defer a.busy.Unlock()
	if a.ContextTokens() < a.Cfg.CompactIdleAt {
		return nil
	}
	return a.compact(ctx, false)
}

func (a *Agent) compact(ctx context.Context, midTurn bool) error {
	if len(a.Messages) == 0 {
		return errors.New("nothing to compact")
	}
	before := a.ContextTokens()
	a.UI.ToolStart("compact", fmt.Sprintf("compacting %s tokens", fmtTokens(before)))
	msgs := append(append([]llm.Message{}, a.Messages...), llm.UserText(compactAsk))
	msg, usage, err := a.Provider.Stream(ctx, a.request(msgs), func(llm.Delta) {})
	summary := strings.TrimSpace(msg.Text())
	if err == nil && summary == "" {
		err = errors.New("model returned no summary")
	}
	if err != nil {
		a.UI.ToolDone("compact", false, "compaction failed")
		return err
	}
	text := compactLead + summary
	if midTurn {
		text += "\n\nContinue the task from where it stopped."
	}
	a.Messages = []llm.Message{llm.UserText(text)}
	a.announced = false
	if a.Session != nil {
		if err := a.Session.Compaction(text, a.Model()); err != nil {
			a.UI.Error("session: " + err.Error())
		}
	}
	a.SetContextTokens(max(usage.Output, len(text)/4))
	a.UI.ToolDone("compact", true, fmt.Sprintf("compacted %s → %s tokens%s", fmtTokens(before), fmtTokens(usage.Output), cachedPct(usage)))
	return nil
}
