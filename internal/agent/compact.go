package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/paul-nameless/aicode/internal/llm"
)

const (
	compactLead     = "This session was compacted to save context. Summary of the conversation so far:\n\n"
	compactContinue = "\n\nContinue the task from where it stopped."
	compactUpdate   = "The conversation above starts with a summary of earlier work. Merge it with everything after it into one updated summary: newer messages win, keep earlier details that still matter, move finished work to done, and drop what is resolved.\n\n"
	compactAsk      = `Write a summary of the conversation above for another agent that will continue this work in a fresh context; only your summary and the most recent messages will remain. Do not call tools and do not continue the task: reply with the summary only, as text, using these sections (write "(none)" for an empty one):

## Objective
What the user is trying to accomplish, in one or two sentences.
## User requests and constraints
Every request, preference and constraint from the user that still applies; quote open requests in the user's own words.
## Decisions
Decisions made and why, including approaches tried and rejected.
## Progress
Done, in progress and blocked; say whether changes are uncommitted, committed or pushed.
## Files
Paths that matter and what was read or changed in each.
## Commands and results
Commands, test runs and their key results, with exact error messages.
## Next steps
An ordered list of what to do next.

Rules: this request comes from aicode, not from the user, so leave it out of the summary. Preserve exact file paths, symbols, commands, error strings and URLs. Keep what the user said close to their words; condense your own explanations to their conclusions. Leave out the system prompt, instruction files and tool descriptions; they are sent again. Be complete on the sections even at the cost of length; keep everything else short.`
	keepTokens     = 20000
	clipToolResult = 2000
	thrashShare    = 4
)

type compactOpts struct {
	auto     bool
	midTurn  bool
	overflow bool
	focus    string
}

func compactPrompt(msgs []llm.Message, focus string) string {
	p := compactAsk
	if len(msgs) > 0 && strings.HasPrefix(msgs[0].Text(), compactLead) {
		p = compactUpdate + p
	}
	if focus = strings.TrimSpace(focus); focus != "" {
		p += "\n\nFocus on: " + focus
	}
	return p
}

func summaryRequest(msgs []llm.Message, prompt string, clip bool) []llm.Message {
	if clip {
		msgs = clipToolResults(msgs)
	}
	out := make([]llm.Message, 0, len(msgs)+1)
	return append(append(out, msgs...), llm.UserText(prompt))
}

func clipToolResults(msgs []llm.Message) []llm.Message {
	out := make([]llm.Message, len(msgs))
	for i, m := range msgs {
		blocks := make([]llm.Block, len(m.Blocks))
		for j, b := range m.Blocks {
			if b.Type == llm.BlockToolResult {
				b.Images = nil
				if len(b.Text) > clipToolResult {
					b.Text = b.Text[:clipToolResult/2] + "\n… [shortened for compaction] …\n" + b.Text[len(b.Text)-clipToolResult/2:]
				}
			}
			blocks[j] = b
		}
		out[i] = withoutReasoning(llm.Message{Role: m.Role, Blocks: blocks})
	}
	return out
}

func cuttable(m llm.Message) bool {
	if m.Role == "assistant" {
		return true
	}
	for _, b := range m.Blocks {
		if b.Type == llm.BlockToolResult {
			return false
		}
	}
	return true
}

func withoutReasoning(m llm.Message) llm.Message {
	var blocks []llm.Block
	for _, b := range m.Blocks {
		if b.Type != llm.BlockReasoning {
			blocks = append(blocks, b)
		}
	}
	return llm.Message{Role: m.Role, Blocks: blocks}
}

func msgTokens(m llm.Message) int {
	n := blockChars(m.Blocks)
	for _, b := range m.Blocks {
		n += len(b.Input) + len(b.Raw)
	}
	return n / 4
}

func keepTail(msgs []llm.Message, budget int) []llm.Message {
	cut, used := len(msgs), 0
	for i := len(msgs) - 1; i > 0; i-- {
		used += msgTokens(withoutReasoning(msgs[i]))
		if used > budget {
			break
		}
		if cuttable(msgs[i]) {
			cut = i
		}
	}
	var tail []llm.Message
	for _, m := range msgs[cut:] {
		if m = withoutReasoning(m); len(m.Blocks) > 0 {
			tail = append(tail, m)
		}
	}
	return tail
}

func contextTooLong(err error) bool {
	var apiErr *llm.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	if apiErr.Status == 413 {
		return true
	}
	if apiErr.Status != 400 {
		return false
	}
	body := strings.ToLower(apiErr.Body)
	for _, s := range overflowMessages {
		if strings.Contains(body, s) {
			return true
		}
	}
	return false
}

var overflowMessages = []string{
	"prompt is too long", "prompt too long", "request_too_large", "input is too long",
	"exceeds the context window", "context window exceeded", "context_length_exceeded", "context length exceeded",
	"maximum context length", "maximum prompt length", "maximum allowed input length", "input token count",
	"longer than the model's context length", "exceeds the available context size", "greater than the context length",
	"context window exceeds limit", "exceed context limit", "exceeded model token limit", "reduce the length of the messages",
}

func (a *Agent) summarize(ctx context.Context, opts compactOpts) (string, llm.Usage, error) {
	prompt := compactPrompt(a.Messages, opts.focus)
	msg, usage, err := a.Provider.Stream(ctx, a.request(summaryRequest(a.Messages, prompt, opts.overflow)), func(llm.Delta) {})
	if err != nil && !opts.overflow && contextTooLong(err) {
		msg, usage, err = a.Provider.Stream(ctx, a.request(summaryRequest(a.Messages, prompt, true)), func(llm.Delta) {})
	}
	summary := strings.TrimSpace(msg.Text())
	switch {
	case err != nil:
		return "", usage, err
	case len(msg.ToolUses()) > 0:
		return "", usage, errors.New("model called tools instead of summarizing")
	case summary == "":
		return "", usage, errors.New("model returned no summary")
	case strings.HasSuffix(summary, llm.CutOffMarker):
		return "", usage, errors.New("summary was cut off at max_tokens")
	}
	return summary, usage, nil
}

func (a *Agent) compact(ctx context.Context, opts compactOpts) error {
	if len(a.Messages) == 0 {
		return errors.New("nothing to compact")
	}
	before := a.ContextTokens()
	threshold := a.Cfg.CompactTokens()
	a.UI.ToolStart("compact", fmt.Sprintf("compacting %s tokens", fmtTokens(before)))
	summary, usage, err := a.summarize(ctx, opts)
	if err != nil {
		a.UI.ToolDone("compact", false, "compaction failed")
		return err
	}
	tail := keepTail(a.Messages, min(keepTokens, threshold/4))
	text := compactLead + summary
	if opts.midTurn && len(tail) == 0 {
		text += compactContinue
	}
	_, hadNote := lastModeNote(a.Messages)
	a.Messages = append([]llm.Message{llm.UserText(text)}, tail...)
	a.restoreModeAfter(hadNote)
	if a.Session != nil {
		if err := a.Session.Compaction(text, tail, a.Model()); err != nil {
			a.UI.Error("session: " + err.Error())
		}
	}
	after := a.estimateTokens()
	a.SetContextTokens(after)
	a.UI.ToolDone("compact", true, fmt.Sprintf("compacted %s → %s tokens%s", fmtTokens(before), fmtTokens(after), cachedPct(usage)))
	a.paused = opts.auto && after > threshold*(thrashShare-1)/thrashShare
	if a.paused {
		a.UI.Info(fmt.Sprintf("context is still %s tokens after compaction, close to compact_at (%s); automatic compaction is paused until /compact or /clear, raise compact_at", fmtTokens(after), fmtTokens(threshold)))
	}
	return nil
}

func (a *Agent) needsCompaction() bool {
	if a.Cfg.WindowGuessed() && !a.windowWarned {
		a.windowWarned = true
		a.UI.Info("context window of " + a.Model() + " is unknown; compact_at percentages assume 200k, set context_window")
	}
	return !a.paused && a.ContextTokens() > a.Cfg.CompactTokens()
}

func (a *Agent) Compact(ctx context.Context, focus string) error {
	if !a.busy.TryLock() {
		return errors.New("busy")
	}
	defer a.busy.Unlock()
	return a.compact(ctx, compactOpts{focus: focus})
}

func (a *Agent) TryCompactIdle(ctx context.Context) error {
	if !a.busy.TryLock() {
		return nil
	}
	defer a.busy.Unlock()
	if a.paused || a.ContextTokens() < a.Cfg.CompactIdleTokens() {
		return nil
	}
	return a.compact(ctx, compactOpts{auto: true})
}
