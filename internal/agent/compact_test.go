package agent

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/paul-nameless/qi/internal/config"
	"github.com/paul-nameless/qi/internal/llm"
	"github.com/paul-nameless/qi/internal/session"
)

type scriptedProvider struct {
	steps    []func(req llm.Request) (llm.Message, error)
	requests []llm.Request
}

func (s *scriptedProvider) Stream(ctx context.Context, req llm.Request, onDelta func(llm.Delta)) (llm.Message, llm.Usage, error) {
	n := len(s.requests)
	s.requests = append(s.requests, req)
	if n < len(s.steps) {
		msg, err := s.steps[n](req)
		return msg, llm.Usage{}, err
	}
	return assistantText("done"), llm.Usage{}, nil
}

func assistantText(s string) llm.Message {
	return llm.Message{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockText, Text: s}}}
}

func reply(m llm.Message) func(llm.Request) (llm.Message, error) {
	return func(llm.Request) (llm.Message, error) { return m, nil }
}

func overflow(llm.Request) (llm.Message, error) {
	return llm.Message{}, &llm.APIError{Status: 400, Body: `{"error":{"message":"prompt is too long: 1200000 tokens > 1000000 maximum"}}`}
}

func isSummaryRequest(req llm.Request) bool {
	last := req.Messages[len(req.Messages)-1]
	return strings.Contains(last.Text(), "Write a summary of the conversation")
}

func toolResult(id, text string) llm.Message {
	return llm.Message{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockToolResult, ToolUseID: id, Text: text}}}
}

func withReasoning(m llm.Message) llm.Message {
	m.Blocks = append([]llm.Block{{Type: llm.BlockReasoning, Raw: json.RawMessage(`{"type":"thinking"}`), Model: "m"}}, m.Blocks...)
	return m
}

func history() []llm.Message {
	return []llm.Message{
		llm.UserText("first request"),
		withReasoning(toolCall("c1", "cat big")),
		toolResult("c1", strings.Repeat("x", 40000)),
		assistantText("read it"),
		llm.UserText("second request"),
		withReasoning(toolCall("c2", "ls")),
		toolResult("c2", "a b c"),
	}
}

func closedHistory() []llm.Message {
	return append(history(), assistantText("all read"))
}

func TestKeepTail(t *testing.T) {
	msgs := history()
	tail := keepTail(msgs, 1000)
	if len(tail) != 4 || tail[0].Text() != "read it" || tail[3].Blocks[0].ToolUseID != "c2" {
		t.Fatalf("tail should stop before the big tool result: %+v", tail)
	}
	for _, m := range tail {
		for _, b := range m.Blocks {
			if b.Type == llm.BlockReasoning {
				t.Errorf("reasoning kept in tail: %+v", m)
			}
		}
	}
	if len(msgs[5].Blocks) != 2 {
		t.Error("keepTail modified the history")
	}
	if tail := keepTail(msgs, 12); len(tail) != 2 || tail[0].ToolUses()[0].ID != "c2" {
		t.Errorf("small budget should cut at the assistant call, keeping its result: %+v", tail)
	}
	if tail := keepTail(msgs, 2); len(tail) != 0 {
		t.Errorf("nothing fits, tail must be empty: %+v", tail)
	}
	if tail := keepTail(msgs, 1<<30); len(tail) != len(msgs)-1 || tail[0].Role != llm.RoleAssistant {
		t.Errorf("the first message is never kept: %d", len(tail))
	}
}

func TestSummaryRequestKeepsHistoryUnchanged(t *testing.T) {
	p := &scriptedProvider{steps: []func(llm.Request) (llm.Message, error){reply(assistantText("SUMMARY"))}}
	a := newAgent(t, &fakeProvider{})
	a.live.provider = p
	a.conv.messages = closedHistory()
	orig := closedHistory()
	if err := a.Compact(context.Background(), "the auth bug"); err != nil {
		t.Fatal(err)
	}
	sent := p.requests[0].Messages
	if !reflect.DeepEqual(sent[:len(orig)], orig) || len(sent) != len(orig)+1 {
		t.Fatal("summary request must be the unchanged history plus one message")
	}
	if ask := sent[len(sent)-1].Text(); !strings.Contains(ask, "## Next steps") || !strings.Contains(ask, compactWithTail) || !strings.HasSuffix(ask, "Focus on: the auth bug") || strings.Contains(ask, "earlier work") {
		t.Errorf("prompt: %q", ask)
	}
	if !strings.HasPrefix(a.conv.messages[0].Text(), compactLead+"SUMMARY") || a.conv.messages[len(a.conv.messages)-1].Text() != "all read" {
		t.Errorf("new history: %+v", a.conv.messages)
	}
	a.conv.messages = append(a.conv.messages, assistantText("more"))
	p.steps = append(p.steps, reply(assistantText("SUMMARY 2")))
	if err := a.Compact(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if ask := p.requests[1].Messages[len(p.requests[1].Messages)-1].Text(); !strings.HasPrefix(ask, compactUpdate) {
		t.Errorf("second compaction should update the summary: %q", ask)
	}
}

func TestSummaryClipsOnlyWhenTooLong(t *testing.T) {
	p := &scriptedProvider{steps: []func(llm.Request) (llm.Message, error){overflow, reply(assistantText("SUMMARY"))}}
	a := newAgent(t, &fakeProvider{})
	a.live.provider = p
	a.conv.messages = history()
	if err := a.Compact(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if len(p.requests) != 2 {
		t.Fatalf("requests: %d", len(p.requests))
	}
	if got := len(p.requests[0].Messages[2].Blocks[0].Text); got != 40000 {
		t.Errorf("first try clipped: %d", got)
	}
	if got := len(p.requests[1].Messages[2].Blocks[0].Text); got > 2*clipToolResult {
		t.Errorf("fallback not clipped: %d", got)
	}
	if len(p.requests[0].Messages[1].Blocks) != 2 || len(p.requests[1].Messages[1].Blocks) != 1 {
		t.Error("only the clipped fallback may drop reasoning, and it must")
	}
}

func TestCompactRejectsCutOffSummary(t *testing.T) {
	cut := llm.Message{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockText, Text: "## Objective\nhalf"}, {Type: llm.BlockText, Text: "\n" + llm.CutOffMarker}}}
	a := newAgent(t, &fakeProvider{replies: []llm.Message{cut}})
	a.conv.messages = history()
	if err := a.Compact(context.Background(), ""); err == nil || len(a.conv.messages) != len(history()) {
		t.Errorf("cut-off summary accepted: %v", err)
	}
}

func TestCompactReannouncesModeLostToSummary(t *testing.T) {
	a := newAgent(t, &fakeProvider{replies: []llm.Message{assistantText("SUMMARY")}})
	a.conv.messages = append([]llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "go"}, {Type: llm.BlockText, Text: readOnlyNote}}}}, history()[1:]...)
	a.conv.announced = true
	if err := a.Compact(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if mb, ok := a.modeBlock(); !ok || mb.Text != yoloNote {
		t.Errorf("yolo should be announced after the read-only note was summarized away: %v %v", mb, ok)
	}
}

func TestOverflowCompactsAndRetriesOnce(t *testing.T) {
	summary := reply(assistantText("SUMMARY"))
	p := &scriptedProvider{steps: []func(llm.Request) (llm.Message, error){overflow, summary}}
	a := newAgent(t, &fakeProvider{})
	a.live.provider = p
	a.conv.messages = history()[:4]
	final, _, err := a.Turn(context.Background(), []llm.Block{{Type: llm.BlockText, Text: "go on"}})
	if err != nil || final != "done" {
		t.Fatalf("final %q err %v", final, err)
	}
	if len(p.requests) != 3 || !isSummaryRequest(p.requests[1]) || isSummaryRequest(p.requests[2]) {
		t.Fatalf("expected overflow, summary, retry; got %d requests", len(p.requests))
	}
	if got := len(p.requests[1].Messages[2].Blocks[0].Text); got > 2*clipToolResult {
		t.Errorf("overflow compaction should clip tool results: %d", got)
	}

	p = &scriptedProvider{steps: []func(llm.Request) (llm.Message, error){overflow, summary, overflow, summary}}
	a.live.provider = p
	a.conv.messages = history()[:4]
	if _, _, err := a.Turn(context.Background(), []llm.Block{{Type: llm.BlockText, Text: "go on"}}); !contextTooLong(err) {
		t.Fatalf("second overflow should be returned, got %v", err)
	}
	if len(p.requests) != 3 {
		t.Errorf("compaction retried more than once: %d requests", len(p.requests))
	}
}

func TestThrashPausesAutoCompaction(t *testing.T) {
	p := &scriptedProvider{steps: []func(llm.Request) (llm.Message, error){reply(assistantText("SUMMARY"))}}
	a := newAgent(t, &fakeProvider{})
	a.live.provider = p
	a.live.system.Text = strings.Repeat("s", 4000)
	a.live.cfg.CompactAt = config.Threshold{Tokens: 1000}
	a.conv.messages = history()
	a.SetContextTokens(20000)
	if _, _, err := a.Turn(context.Background(), []llm.Block{{Type: llm.BlockText, Text: "next"}}); err != nil {
		t.Fatal(err)
	}
	if !a.conv.paused {
		t.Fatal("compaction that cannot get under the threshold should pause")
	}
	before := len(p.requests)
	a.SetContextTokens(20000)
	if _, _, err := a.Turn(context.Background(), []llm.Block{{Type: llm.BlockText, Text: "again"}}); err != nil {
		t.Fatal(err)
	}
	for _, req := range p.requests[before:] {
		if isSummaryRequest(req) {
			t.Error("compacted again while paused")
		}
	}
	if err := a.Compact(context.Background(), ""); err != nil || a.conv.paused {
		t.Errorf("manual compaction should clear the pause: %v %v", err, a.conv.paused)
	}
}

func TestCompactionSurvivesResume(t *testing.T) {
	t.Setenv("QI_CONFIG_DIR", t.TempDir())
	t.Setenv("QI_SESSION_DIR", t.TempDir())
	p := &scriptedProvider{steps: []func(llm.Request) (llm.Message, error){reply(assistantText("SUMMARY"))}}
	a := newAgent(t, &fakeProvider{})
	a.live.provider = p
	s, err := session.Create(session.CreateOptions{Cwd: a.cwd, Model: "m", Kind: session.Interactive})
	if err != nil {
		t.Fatal(err)
	}
	a.live.session = s
	for _, m := range history() {
		a.appendMsg(m, nil)
	}
	if err := a.Compact(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	a.appendMsg(assistantText("after"), nil)
	s.Close()
	_, ld, err := session.Open(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ld.Messages, a.conv.messages) {
		t.Errorf("resumed history differs:\n%+v\n%+v", ld.Messages, a.conv.messages)
	}
	entries, _ := session.Entries(s.Path)
	recorded := false
	for _, e := range entries {
		recorded = recorded || (e.Type == "compaction" && e.Usage != nil)
	}
	if !recorded || ld.CtxTokens != 0 {
		t.Errorf("the compaction entry carries its usage (%v) and does not set the context (%d)", recorded, ld.CtxTokens)
	}
}

func TestContextTooLong(t *testing.T) {
	yes := []*llm.APIError{
		{Status: 400, Body: `{"error":{"type":"invalid_request_error","message":"prompt is too long: 213462 tokens > 200000 maximum"}}`},
		{Status: 400, Body: `{"error":{"message":"This endpoint's maximum context length is 1050000 tokens. However, you requested about 1200000 tokens","code":400}}`},
		{Status: 400, Body: `{"error":{"code":"context_length_exceeded"}}`},
		{Status: 413, Body: `{"error":{"type":"request_too_large"}}`},
	}
	for _, e := range yes {
		if !contextTooLong(e) {
			t.Errorf("not detected: %s", e.Body)
		}
	}
	if !contextTooLong(errors.New("stream error: prompt is too long: 1200000 tokens > 1000000 maximum")) {
		t.Error("overflow reported inside the stream not detected")
	}
	no := []error{
		&llm.APIError{Status: 429, Body: "too many tokens per minute"},
		&llm.APIError{Status: 400, Body: "image not supported"},
		context.Canceled,
	}
	for _, e := range no {
		if contextTooLong(e) {
			t.Errorf("false positive: %v", e)
		}
	}
}

func TestFailedOverflowCompactionDropsPrompt(t *testing.T) {
	p := &scriptedProvider{steps: []func(llm.Request) (llm.Message, error){overflow, overflow, overflow}}
	a := newAgent(t, &fakeProvider{})
	a.live.provider = p
	a.conv.messages = history()[:4]
	if _, _, err := a.Turn(context.Background(), []llm.Block{{Type: llm.BlockText, Text: "huge paste"}}); !contextTooLong(err) {
		t.Fatalf("want the overflow error, got %v", err)
	}
	if len(p.requests) != 2 {
		t.Errorf("overflow compaction must not retry its own clipped request: %d requests", len(p.requests))
	}
	if !reflect.DeepEqual(a.conv.messages, history()[:4]) {
		t.Errorf("rejected prompt should be dropped, history kept: %+v", a.conv.messages)
	}

	p = &scriptedProvider{steps: []func(llm.Request) (llm.Message, error){overflow, overflow, overflow}}
	a.live.provider = p
	a.conv.messages = history()
	if err := a.Compact(context.Background(), ""); err == nil || len(p.requests) != 2 || !reflect.DeepEqual(a.conv.messages, history()) {
		t.Errorf("manual compaction: err %v, %d requests", err, len(p.requests))
	}
}

func TestSummaryRequestMatchesTurnRequest(t *testing.T) {
	p := &scriptedProvider{}
	a := newAgent(t, &fakeProvider{})
	a.live.provider = p
	a.live.system.Text = "system prompt"
	a.live.cfg.MaxTokens, a.live.cfg.ReasoningEffort, a.live.cfg.CacheTTL = 1000, "low", "1h"
	if _, _, err := a.Turn(context.Background(), []llm.Block{{Type: llm.BlockText, Text: "hi"}}); err != nil {
		t.Fatal(err)
	}
	if err := a.Compact(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	turn, sum := p.requests[0], p.requests[1]
	if !reflect.DeepEqual(sum.Messages[:len(turn.Messages)], turn.Messages) {
		t.Error("summary request must extend the last turn's messages")
	}
	turn.Messages, sum.Messages = nil, nil
	if !reflect.DeepEqual(turn, sum) {
		t.Errorf("summary request differs outside messages:\n%+v\n%+v", turn, sum)
	}
}

func TestTryCompactIdle(t *testing.T) {
	cases := []struct {
		name   string
		tokens int
		paused bool
		busy   bool
		want   int
	}{
		{"below threshold", 50, false, false, 0},
		{"above threshold", 500, false, false, 1},
		{"paused", 500, true, false, 0},
		{"busy", 500, false, true, 0},
	}
	for _, tc := range cases {
		p := &fakeProvider{}
		a := newAgent(t, p)
		a.live.cfg.CompactIdleAt = config.Threshold{Tokens: 100}
		a.conv.messages = history()
		a.SetContextTokens(tc.tokens)
		a.conv.paused = tc.paused
		if tc.busy {
			a.busy.Lock()
		}
		if err := a.TryCompactIdle(context.Background()); err != nil {
			t.Errorf("%s: %v", tc.name, err)
		}
		if len(p.requests) != tc.want {
			t.Errorf("%s: %d requests, want %d", tc.name, len(p.requests), tc.want)
		}
		if tc.busy {
			a.busy.Unlock()
		}
	}
	a := newAgent(t, &fakeProvider{})
	a.conv.paused = true
	a.Reset(nil)
	if a.conv.paused {
		t.Error("/clear must lift the compaction pause")
	}
}

func TestMidTurnCompactionKeepsNoTail(t *testing.T) {
	a := newAgent(t, &fakeProvider{})
	a.conv.messages = history()
	if err := a.compact(context.Background(), compactOpts{midTurn: true}); err != nil {
		t.Fatal(err)
	}
	if len(a.conv.messages) != 1 || !strings.HasSuffix(a.conv.messages[0].Text(), compactContinue) {
		t.Errorf("mid-turn compaction leaves an open tool round; it must keep no tail: %+v", a.conv.messages)
	}
	p := &scriptedProvider{}
	a.live.provider = p
	a.conv.messages = history()
	if err := a.Compact(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if len(a.conv.messages) != 1 || strings.HasSuffix(a.conv.messages[0].Text(), compactContinue) || !strings.Contains(p.requests[0].Messages[len(history())].Text(), compactNoTail) {
		t.Errorf("history ending in unanswered tool results (a cancelled turn) keeps no tail and asks for the current state: %+v", a.conv.messages)
	}
	a.conv.messages = closedHistory()
	if err := a.compact(context.Background(), compactOpts{}); err != nil {
		t.Fatal(err)
	}
	if len(a.conv.messages) == 1 || strings.HasSuffix(a.conv.messages[0].Text(), compactContinue) {
		t.Errorf("between turns the tail is kept: %+v", a.conv.messages)
	}
}

func TestClipKeepsPreviousSummary(t *testing.T) {
	prev := llm.UserText(compactLead + strings.Repeat("s", 3*clipText))
	out := clipHistory(append([]llm.Message{prev}, history()...))
	if out[0].Text() != prev.Text() {
		t.Error("the previous summary must not be clipped")
	}
}

func TestClipKeepsCodeHeadAndTail(t *testing.T) {
	code := "const first = 1\n" + strings.Repeat("a && <b>", clipToolResult) + "\nreturn last"
	in, _ := json.Marshal(map[string]any{"code": code, "timeout": 30})
	b := clipBlock(llm.Block{Type: llm.BlockToolUse, Input: in})
	var got struct {
		Code    string `json:"code"`
		Timeout int    `json:"timeout"`
	}
	if err := json.Unmarshal(b.Input, &got); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b.Input), `\u0026`) || !strings.HasPrefix(got.Code, "const first") || !strings.HasSuffix(got.Code, "return last") || !strings.Contains(got.Code, "shortened for compaction") || len(got.Code) > 2*clipToolResult || got.Timeout != 30 {
		t.Errorf("clipped input %d bytes: %s", len(b.Input), b.Input)
	}
	small := json.RawMessage(`{"code":"1+1"}`)
	if b := clipBlock(llm.Block{Type: llm.BlockToolUse, Input: small}); string(b.Input) != string(small) {
		t.Errorf("short input changed: %s", b.Input)
	}
}

func TestThrashPauseFromMeasuredUsage(t *testing.T) {
	p := &fakeProvider{replies: []llm.Message{assistantText("SUMMARY")}, usage: llm.Usage{Input: 5000}}
	a := newAgent(t, p)
	a.live.cfg.CompactAt = config.Threshold{Tokens: 4000}
	a.conv.messages = history()
	a.SetContextTokens(20000)
	if _, _, err := a.Turn(context.Background(), []llm.Block{{Type: llm.BlockText, Text: "next"}}); err != nil {
		t.Fatal(err)
	}
	if !a.conv.paused {
		t.Error("real usage above the threshold right after compaction should pause")
	}
	a.Reconfigure(a.live.cfg, p)
	if a.conv.paused {
		t.Error("/model and /profile should lift the pause")
	}
}

func TestIdleCompactionSizesTailFromIdleThreshold(t *testing.T) {
	a := newAgent(t, &fakeProvider{})
	a.live.cfg.CompactIdleAt = config.Threshold{Tokens: 400}
	a.conv.messages = history()
	a.SetContextTokens(20000)
	if err := a.TryCompactIdle(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, m := range a.conv.messages {
		if msgTokens(m) > 100 {
			t.Errorf("tail budget should come from compact_idle_at: kept %d tokens", msgTokens(m))
		}
	}
}

func TestClipHistory(t *testing.T) {
	big := strings.Repeat("y", 50000)
	in, _ := json.Marshal(map[string]string{"content": big})
	msgs := []llm.Message{
		llm.UserText(big),
		{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockToolUse, ID: "w", Name: "write", Input: in}}},
		toolResult("w", big),
	}
	out := clipHistory(msgs)
	if len(out[0].Blocks[0].Text) > clipText+100 || len(out[1].Blocks[0].Input) > clipToolResult+100 || len(out[2].Blocks[0].Text) > clipToolResult+100 {
		t.Errorf("clipped sizes: %d %d %d", len(out[0].Blocks[0].Text), len(out[1].Blocks[0].Input), len(out[2].Blocks[0].Text))
	}
	if len(msgs[0].Blocks[0].Text) != 50000 || len(msgs[1].Blocks[0].Input) != len(in) {
		t.Error("clipHistory modified the history")
	}
}

type billedErrorProvider struct{}

func (billedErrorProvider) Stream(context.Context, llm.Request, func(llm.Delta)) (llm.Message, llm.Usage, error) {
	cost := 0.002
	return llm.Message{}, llm.Usage{Input: 900, Output: 7, Cost: &cost}, errors.New("the model returned an empty response")
}

func TestBilledFailuresRecordTheirUsage(t *testing.T) {
	t.Setenv("QI_SESSION_DIR", t.TempDir())
	a := newAgent(t, &fakeProvider{})
	a.live.provider = billedErrorProvider{}
	s, err := session.Create(session.CreateOptions{Cwd: a.cwd, Model: "m", Kind: session.Interactive})
	if err != nil {
		t.Fatal(err)
	}
	a.live.session = s
	if _, _, err := a.Turn(context.Background(), []llm.Block{{Type: llm.BlockText, Text: "hi"}}); err == nil {
		t.Fatal("the empty reply should fail the turn")
	}
	a.conv.messages = history()
	if err := a.Compact(context.Background(), ""); err == nil {
		t.Fatal("the failed summary should fail compaction")
	}
	s.Close()
	entries, _ := session.Entries(s.Path)
	n := 0
	for _, e := range entries {
		if e.Type == "usage" && e.Usage != nil && e.Usage.Input == 900 && *e.Usage.Cost == 0.002 {
			n++
		}
	}
	if n != 2 {
		t.Errorf("want 2 usage entries (rejected reply, failed summary), got %d", n)
	}
	if _, ld, err := session.Open(s.Path); err != nil || ld.CtxTokens != 0 {
		t.Errorf("usage entries must not change the restored context: %d %v", ld.CtxTokens, err)
	}
}

type flakyProvider struct{ calls int }

func (f *flakyProvider) Stream(context.Context, llm.Request, func(llm.Delta)) (llm.Message, llm.Usage, error) {
	f.calls++
	if f.calls == 1 {
		return llm.Message{}, llm.Usage{}, errors.New("stream error: stream ID 63; INTERNAL_ERROR; received from peer")
	}
	return assistantText("SUMMARY"), llm.Usage{}, nil
}

func TestCompactionRetriesATransientFailure(t *testing.T) {
	old := llm.Sleep
	llm.Sleep = func(context.Context, time.Duration) error { return nil }
	t.Cleanup(func() { llm.Sleep = old })
	a := newAgent(t, &fakeProvider{})
	p := &flakyProvider{}
	a.live.provider = p
	a.conv.messages = history()
	if err := a.Compact(context.Background(), ""); err != nil || p.calls != 2 {
		t.Errorf("compaction after one reset: %v, %d calls", err, p.calls)
	}
}
