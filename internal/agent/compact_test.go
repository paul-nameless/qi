package agent

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/paul-nameless/aicode/internal/config"
	"github.com/paul-nameless/aicode/internal/llm"
	"github.com/paul-nameless/aicode/internal/session"
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
	return llm.Message{Role: "assistant", Blocks: []llm.Block{{Type: llm.BlockText, Text: s}}}
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
	return llm.Message{Role: "user", Blocks: []llm.Block{{Type: llm.BlockToolResult, ToolUseID: id, Text: text}}}
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
	if tail := keepTail(msgs, 5); len(tail) != 2 || tail[0].ToolUses()[0].ID != "c2" {
		t.Errorf("small budget should cut at the assistant call, keeping its result: %+v", tail)
	}
	if tail := keepTail(msgs, 2); len(tail) != 0 {
		t.Errorf("nothing fits, tail must be empty: %+v", tail)
	}
	if tail := keepTail(msgs, 1<<30); len(tail) != len(msgs)-1 || tail[0].Role != "assistant" {
		t.Errorf("the first message is never kept: %d", len(tail))
	}
}

func TestSummaryRequestKeepsHistoryUnchanged(t *testing.T) {
	p := &scriptedProvider{steps: []func(llm.Request) (llm.Message, error){reply(assistantText("SUMMARY"))}}
	a := newAgent(t, &fakeProvider{})
	a.Provider = p
	a.Messages = history()
	orig := history()
	if err := a.Compact(context.Background(), "the auth bug"); err != nil {
		t.Fatal(err)
	}
	sent := p.requests[0].Messages
	if !reflect.DeepEqual(sent[:len(orig)], orig) || len(sent) != len(orig)+1 {
		t.Fatal("summary request must be the unchanged history plus one message")
	}
	if ask := sent[len(sent)-1].Text(); !strings.Contains(ask, "## Next steps") || !strings.HasSuffix(ask, "Focus on: the auth bug") || strings.Contains(ask, "earlier work") {
		t.Errorf("prompt: %q", ask)
	}
	if !strings.HasPrefix(a.Messages[0].Text(), compactLead+"SUMMARY") || a.Messages[len(a.Messages)-1].Blocks[0].ToolUseID != "c2" {
		t.Errorf("new history: %+v", a.Messages)
	}
	a.Messages = append(a.Messages, assistantText("more"))
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
	a.Provider = p
	a.Messages = history()
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
	cut := assistantText("## Objective\nhalf\n" + cutOffMarker)
	a := newAgent(t, &fakeProvider{replies: []llm.Message{cut}})
	a.Messages = history()
	if err := a.Compact(context.Background(), ""); err == nil || len(a.Messages) != len(history()) {
		t.Errorf("cut-off summary accepted: %v", err)
	}
}

func TestCompactReannouncesModeLostToSummary(t *testing.T) {
	a := newAgent(t, &fakeProvider{replies: []llm.Message{assistantText("SUMMARY")}})
	a.Messages = append([]llm.Message{{Role: "user", Blocks: []llm.Block{{Type: llm.BlockText, Text: "go"}, {Type: llm.BlockText, Text: readOnlyNote}}}}, history()[1:]...)
	a.announced = true
	if err := a.Compact(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if mb, ok := a.modeBlock(); !ok || mb.Text != yoloNote {
		t.Errorf("yolo should be announced after the read-only note was summarized away: %v %v", mb, ok)
	}
}

func TestRejectedAfterCompactionKeepsHistory(t *testing.T) {
	reject := func(llm.Request) (llm.Message, error) {
		return llm.Message{}, &llm.APIError{Status: 400, Body: "image not supported"}
	}
	p := &scriptedProvider{steps: []func(llm.Request) (llm.Message, error){reply(toolCall("x", "ls")), reply(assistantText("SUMMARY")), reject}}
	a := newAgent(t, &fakeProvider{})
	a.Provider = p
	a.Cfg.CompactAt = config.Threshold{Tokens: 10}
	a.Messages = history()[:4]
	a.SetContextTokens(500)
	if _, _, err := a.Turn(context.Background(), []llm.Block{{Type: llm.BlockText, Text: "go on"}}); err == nil {
		t.Fatal("expected error")
	}
	if !strings.HasPrefix(a.Messages[0].Text(), compactLead) || a.Messages[len(a.Messages)-1].Text() != "go on" {
		t.Errorf("history after a rejected retry: %+v", a.Messages)
	}
}

func TestOverflowCompactsAndRetriesOnce(t *testing.T) {
	summary := reply(assistantText("SUMMARY"))
	p := &scriptedProvider{steps: []func(llm.Request) (llm.Message, error){overflow, summary}}
	a := newAgent(t, &fakeProvider{})
	a.Provider = p
	a.Messages = history()[:4]
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
	a.Provider = p
	a.Messages = history()[:4]
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
	a.Provider = p
	a.System = strings.Repeat("s", 4000)
	a.Cfg.CompactAt = config.Threshold{Tokens: 1000}
	a.Messages = history()
	a.SetContextTokens(20000)
	if _, _, err := a.Turn(context.Background(), []llm.Block{{Type: llm.BlockText, Text: "next"}}); err != nil {
		t.Fatal(err)
	}
	if !a.paused {
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
	if err := a.Compact(context.Background(), ""); err != nil || a.paused {
		t.Errorf("manual compaction should clear the pause: %v %v", err, a.paused)
	}
}

func TestCompactionSurvivesResume(t *testing.T) {
	t.Setenv("AICODE_CONFIG_DIR", t.TempDir())
	p := &scriptedProvider{steps: []func(llm.Request) (llm.Message, error){reply(assistantText("SUMMARY"))}}
	a := newAgent(t, &fakeProvider{})
	a.Provider = p
	s, err := session.Create(a.Cwd, "", "m", "interactive", false)
	if err != nil {
		t.Fatal(err)
	}
	a.Session = s
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
	if !reflect.DeepEqual(ld.Messages, a.Messages) {
		t.Errorf("resumed history differs:\n%+v\n%+v", ld.Messages, a.Messages)
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
