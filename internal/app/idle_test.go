package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/paul-nameless/qi/internal/config"
	"github.com/paul-nameless/qi/internal/llm"
)

type slowSummary struct {
	called chan struct{}
}

func (p slowSummary) Stream(context.Context, llm.Request, func(llm.Delta)) (llm.Message, llm.Usage, error) {
	close(p.called)
	time.Sleep(100 * time.Millisecond)
	return llm.Message{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockText, Text: "the summary"}}}, llm.Usage{}, nil
}

func TestStopIdleWaitsForIdleCompaction(t *testing.T) {
	p := slowSummary{called: make(chan struct{})}
	a := newTestAgentWith(t, config.Config{Model: "m", CacheTTL: "5m10ms", CompactAt: config.Threshold{Tokens: 1000}, CompactIdleAt: config.Threshold{Tokens: 10}}, t.TempDir(), p, devNullPrinter(t))
	setMessages(a, []llm.Message{llm.UserText("hello"), {Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockText, Text: "hi"}}}})
	a.SetContextTokens(500)
	r := &repl{agent: a, display: devNullPrinter(t)}
	r.armIdle()
	select {
	case <-p.called:
	case <-time.After(5 * time.Second):
		t.Fatal("idle compaction never started")
	}
	r.stopIdle()
	if msgs := a.Messages(); len(msgs) == 0 || !strings.Contains(msgs[0].Text(), "the summary") {
		t.Errorf("stopIdle returned before idle compaction finished: %d messages", len(msgs))
	}
}
