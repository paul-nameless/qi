package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/paul-nameless/qi/internal/llm"
	"github.com/paul-nameless/qi/internal/strutil"
)

const (
	compactLead     = "This session was compacted to save context. Summary of the conversation so far:\n\n"
	compactContinue = "\n\nContinue the task from where it stopped."
	compactUpdate   = "The conversation above starts with a summary of earlier work. Merge it with everything after it into one updated summary: newer messages win, keep earlier details that still matter, move finished work to done, and drop what is resolved.\n\n"
	compactWithTail = "only your summary and the most recent messages will remain."
	compactNoTail   = "only your summary will remain, so record the current request in the user's words, the latest tool results that matter and the exact step in progress."
	compactAsk      = `Write a summary of the conversation above for another agent that will continue this work in a fresh context; %s Do not call tools and do not continue the task: reply with the summary only, as text, using these sections (write "(none)" for an empty one):

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

Rules: this request comes from qi, not from the user, so leave it out of the summary. Preserve exact file paths, symbols, commands, error strings and URLs. Keep what the user said close to their words; condense your own explanations to their conclusions. For each image or screenshot, record its path, what it showed and what was concluded from it, since the images themselves may not be kept. Leave out the system prompt, instruction files and tool descriptions; they are sent again. Be complete on the sections even at the cost of length; keep everything else short.`
	keepTokens     = 20000
	clipToolResult = 2000
	clipText       = 8000
	thrashShare    = 4
)

type compactOpts struct {
	auto      bool
	midTurn   bool
	overflow  bool
	focus     string
	threshold int
}

func compactPrompt(msgs []llm.Message, focus string, keepsTail bool) string {
	p := fmt.Sprintf(compactAsk, compactNoTail)
	if keepsTail {
		p = fmt.Sprintf(compactAsk, compactWithTail)
	}
	if isSummary(msgs) {
		p = compactUpdate + p
	}
	if focus = strings.TrimSpace(focus); focus != "" {
		p += "\n\nFocus on: " + focus
	}
	return p
}

func isSummary(msgs []llm.Message) bool {
	return len(msgs) > 0 && strings.HasPrefix(msgs[0].Text(), compactLead)
}

func endsWithToolResults(msgs []llm.Message) bool {
	return len(msgs) > 0 && !cuttable(msgs[len(msgs)-1])
}

func summaryRequest(msgs []llm.Message, prompt string, clip bool) []llm.Message {
	if clip {
		msgs = clipHistory(msgs)
	}
	out := make([]llm.Message, 0, len(msgs)+1)
	return append(append(out, msgs...), llm.UserText(prompt))
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strutil.CutStart(s, n/2) + "\n… [shortened for compaction] …\n" + strutil.CutEnd(s, n/2)
}

func clipBlock(b llm.Block) llm.Block {
	switch b.Type {
	case llm.BlockImage:
		return llm.Block{Type: llm.BlockText, Text: imagePlaceholder(b)}
	case llm.BlockToolResult:
		for _, img := range b.Images {
			b.Text += "\n" + imagePlaceholder(img)
		}
		b.Images = nil
		b.Text = clip(b.Text, clipToolResult)
	case llm.BlockText:
		b.Text = clip(b.Text, clipText)
	case llm.BlockToolUse:
		b.Input = clipInput(b.Input)
	}
	return b
}

func clipInput(in json.RawMessage) json.RawMessage {
	if len(in) <= clipToolResult {
		return in
	}
	var fields map[string]any
	if json.Unmarshal(in, &fields) != nil {
		return json.RawMessage("{}")
	}
	for k, v := range fields {
		if s, ok := v.(string); ok {
			fields[k] = clip(s, clipToolResult)
		}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if enc.Encode(fields) != nil || buf.Len() > 4*clipToolResult {
		return json.RawMessage("{}")
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
}

func clipHistory(msgs []llm.Message) []llm.Message {
	out := make([]llm.Message, len(msgs))
	for i, m := range msgs {
		if i == 0 && isSummary(msgs) {
			out[i] = m
			continue
		}
		blocks := make([]llm.Block, len(m.Blocks))
		for j, b := range m.Blocks {
			blocks[j] = clipBlock(b)
		}
		out[i] = withoutReasoning(llm.Message{Role: m.Role, Blocks: blocks})
	}
	return out
}

func cuttable(m llm.Message) bool {
	if m.Role == llm.RoleAssistant {
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
	if err == nil {
		return false
	}
	body := err.Error()
	var apiErr *llm.APIError
	if errors.As(err, &apiErr) {
		if apiErr.Status == 413 {
			return true
		}
		if apiErr.Status != 400 {
			return false
		}
		body = apiErr.Body
	}
	body = strings.ToLower(body)
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
	"context window exceeds limit", "exceed context limit", "exceeded model token limit", "reduce the length of the messages", "many-image requests",
}

func (a *Agent) summarize(ctx context.Context, opts compactOpts, keepsTail bool) (string, llm.Usage, error) {
	prompt := compactPrompt(a.conv.messages, opts.focus, keepsTail)
	l := a.snapshot()
	msg, usage, err := l.provider.Stream(ctx, a.request(l, summaryRequest(a.conv.messages, prompt, opts.overflow)), func(llm.Delta) {})
	if err != nil && !opts.overflow && contextTooLong(err) {
		msg, usage, err = l.provider.Stream(ctx, a.request(l, summaryRequest(a.conv.messages, prompt, true)), func(llm.Delta) {})
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
	if len(a.conv.messages) == 0 {
		return errors.New("nothing to compact")
	}
	a.evictImages()
	before := a.ContextTokens()
	threshold := opts.threshold
	if threshold == 0 {
		threshold = a.Config().CompactTokens()
	}
	a.ui.ToolStart("compact", fmt.Sprintf("compacting %s tokens", FmtTokens(before)))
	var tail []llm.Message
	if !opts.midTurn && !endsWithToolResults(a.conv.messages) {
		tail = keepTail(a.conv.messages, min(keepTokens, threshold/4))
	}
	summary, usage, err := a.summarize(ctx, opts, len(tail) > 0)
	if err != nil {
		a.recordUsage(usage)
		a.ui.ToolDone("compact", false, "compaction failed")
		return err
	}
	text := compactLead + summary
	if opts.midTurn {
		text += compactContinue
	}
	_, hadNote := lastModeNote(a.conv.messages)
	a.conv.messages = append([]llm.Message{llm.UserText(text)}, tail...)
	a.restoreModeAfter(hadNote)
	if l := a.snapshot(); l.session != nil {
		if err := l.session.Compaction(text, tail, l.cfg.Model, &usage); err != nil {
			a.ui.Error("session: " + err.Error())
		}
	}
	after := a.estimateTokens()
	a.SetContextTokens(after)
	a.ui.ToolDone("compact", true, fmt.Sprintf("compacted %s → %s tokens%s", FmtTokens(before), FmtTokens(after), cachedPct(usage)))
	a.conv.paused, a.conv.thrashAt = false, 0
	if opts.auto || opts.overflow {
		a.conv.thrashAt = threshold * (thrashShare - 1) / thrashShare
		a.pauseIfOver(after)
	}
	return nil
}

func (a *Agent) pauseIfOver(tokens int) {
	if a.conv.thrashAt > 0 && tokens > a.conv.thrashAt {
		a.conv.paused, a.conv.thrashAt = true, 0
		a.ui.Info(fmt.Sprintf("context is still %s tokens right after compaction, too close to the threshold; automatic compaction is paused until /compact or /clear, raise compact_at", FmtTokens(tokens)))
	}
}

func (a *Agent) needsCompaction() bool {
	cfg := a.Config()
	if cfg.WindowGuessed() && !a.conv.windowWarned {
		a.conv.windowWarned = true
		a.ui.Info("context window of " + cfg.Model + " is unknown; compact_at percentages assume 200k, set context_window")
	}
	return !a.conv.paused && a.ContextTokens() > cfg.CompactTokens()
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
	idle := a.Config().CompactIdleTokens()
	if a.conv.paused || a.ContextTokens() < idle {
		return nil
	}
	return a.compact(ctx, compactOpts{auto: true, threshold: idle})
}
