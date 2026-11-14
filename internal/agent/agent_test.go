package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/paul-nameless/aicode/internal/config"
	"github.com/paul-nameless/aicode/internal/llm"
	"github.com/paul-nameless/aicode/internal/tools"
)

type fakeProvider struct {
	mu       sync.Mutex
	replies  []llm.Message
	usage    llm.Usage
	requests [][]llm.Message
	onCall   func(n int)
}

func (f *fakeProvider) Stream(ctx context.Context, req llm.Request, onDelta func(llm.Delta)) (llm.Message, llm.Usage, error) {
	f.mu.Lock()
	n := len(f.requests)
	f.requests = append(f.requests, append([]llm.Message{}, req.Messages...))
	f.mu.Unlock()
	if f.onCall != nil {
		f.onCall(n)
	}
	if err := ctx.Err(); err != nil {
		return llm.Message{}, llm.Usage{}, err
	}
	if n >= len(f.replies) {
		return llm.Message{Role: "assistant", Blocks: []llm.Block{{Type: llm.BlockText, Text: "done"}}}, f.usage, nil
	}
	return f.replies[n], f.usage, nil
}

type nopDisplay struct{}

func (nopDisplay) ToolStart(string, string)      {}
func (nopDisplay) ToolProgress(string, string)   {}
func (nopDisplay) ToolDone(string, bool, string) {}
func (nopDisplay) Text(string)                   {}
func (nopDisplay) Reasoning(string)              {}
func (nopDisplay) EndBlock()                     {}
func (nopDisplay) Info(string)                   {}
func (nopDisplay) Error(string)                  {}

func toolCall(id, cmd string) llm.Message {
	in, _ := json.Marshal(map[string]string{"command": cmd})
	return llm.Message{Role: "assistant", Blocks: []llm.Block{{Type: llm.BlockToolUse, ID: id, Name: "bash", Input: in}}}
}

func newAgent(t *testing.T, p *fakeProvider) *Agent {
	a := &Agent{Cfg: config.Config{CompactAt: 1 << 30, Model: "m"}, Cwd: t.TempDir(), Provider: p, UI: nopDisplay{}}
	a.Env = &tools.Env{Cwd: a.Cwd, ReadOnly: a.ReadOnly, SpillDir: t.TempDir()}
	a.Registry = tools.NewRegistry(tools.Bash())
	return a
}

func lastText(m llm.Message) string {
	var parts []string
	for _, b := range m.Blocks {
		if b.Type == llm.BlockText {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "|")
}

func TestTurnRunsToolsAndQueuesSteering(t *testing.T) {
	p := &fakeProvider{replies: []llm.Message{toolCall("c1", "echo hi")}}
	a := newAgent(t, p)
	p.onCall = func(n int) {
		if n == 0 {
			a.Queue([]llm.Block{{Type: llm.BlockText, Text: "also check x"}})
		}
	}
	final, stats, err := a.Turn(context.Background(), []llm.Block{{Type: llm.BlockText, Text: "go"}})
	if err != nil || final != "done" || stats.Requests != 2 {
		t.Fatalf("final %q stats %+v err %v", final, stats, err)
	}
	second := p.requests[1]
	last := second[len(second)-1]
	if last.Blocks[0].Type != llm.BlockToolResult || !strings.Contains(last.Blocks[0].Text, "hi") {
		t.Errorf("tool result missing: %+v", last)
	}
	if !strings.Contains(lastText(last), "also check x") {
		t.Errorf("steering not delivered: %+v", last)
	}
}

func TestModeNoteSentOnChangeOnly(t *testing.T) {
	p := &fakeProvider{}
	a := newAgent(t, p)
	a.SetReadOnly(true)
	_, _, _ = a.Turn(context.Background(), []llm.Block{{Type: llm.BlockText, Text: "one"}})
	_, _, _ = a.Turn(context.Background(), []llm.Block{{Type: llm.BlockText, Text: "two"}})
	a.SetReadOnly(false)
	_, _, _ = a.Turn(context.Background(), []llm.Block{{Type: llm.BlockText, Text: "three"}})
	users := []string{}
	for _, m := range a.Messages {
		if m.Role == "user" {
			users = append(users, lastText(m))
		}
	}
	if !strings.Contains(users[0], "Read-only mode is on") || strings.Contains(users[1], "<mode>") || !strings.Contains(users[2], "Read-only mode is off") {
		t.Errorf("mode notes: %q", users)
	}
}

func TestCancelDuringToolsKeepsLogValid(t *testing.T) {
	p := &fakeProvider{replies: []llm.Message{toolCall("c1", "sleep 5")}}
	a := newAgent(t, p)
	ctx, cancel := context.WithCancel(context.Background())
	p.onCall = func(n int) {
		if n == 0 {
			go func() {
				time.Sleep(300 * time.Millisecond)
				cancel()
			}()
		}
	}
	_, _, err := a.Turn(ctx, []llm.Block{{Type: llm.BlockText, Text: "go"}})
	if err != context.Canceled {
		t.Fatalf("err %v", err)
	}
	last := a.Messages[len(a.Messages)-1]
	if last.Role != "user" || len(last.Blocks) != 1 || last.Blocks[0].ToolUseID != "c1" || !last.Blocks[0].IsError {
		t.Fatalf("expected interrupted result, got %+v", last)
	}
}

func TestCompactBeforeNewPrompt(t *testing.T) {
	p := &fakeProvider{replies: []llm.Message{{Role: "assistant", Blocks: []llm.Block{{Type: llm.BlockText, Text: "SUMMARY"}}}}}
	a := newAgent(t, p)
	a.Cfg.CompactAt = 100
	a.Messages = []llm.Message{llm.UserText("old"), {Role: "assistant", Blocks: []llm.Block{{Type: llm.BlockText, Text: "old answer"}}}}
	a.SetContextTokens(500)
	if _, _, err := a.Turn(context.Background(), []llm.Block{{Type: llm.BlockText, Text: "new request"}}); err != nil {
		t.Fatal(err)
	}
	if len(p.requests[0]) != 3 || !strings.Contains(lastText(p.requests[0][2]), "Summarize") {
		t.Fatalf("first request should be the compaction: %+v", p.requests[0])
	}
	sent := p.requests[1]
	if len(sent) != 2 || !strings.Contains(lastText(sent[0]), "SUMMARY") || lastText(sent[1]) != "new request" {
		t.Fatalf("after compaction: %+v", sent)
	}
}

func TestCompactRejectsEmptySummary(t *testing.T) {
	p := &fakeProvider{replies: []llm.Message{{Role: "assistant", Blocks: []llm.Block{{Type: llm.BlockToolUse, ID: "x", Name: "bash", Input: json.RawMessage(`{}`)}}}}}
	a := newAgent(t, p)
	a.Messages = []llm.Message{llm.UserText("old")}
	if err := a.Compact(context.Background()); err == nil {
		t.Fatal("expected error")
	}
	if len(a.Messages) != 1 {
		t.Error("history replaced despite failed compaction")
	}
}

func TestStopHookContinuesOnceWithActiveFlag(t *testing.T) {
	p := &fakeProvider{}
	a := newAgent(t, p)
	var actives []bool
	a.OnStop = func(ctx context.Context, last string, active bool) (bool, string) {
		actives = append(actives, active)
		if !active {
			a.Queue([]llm.Block{{Type: llm.BlockText, Text: "queued note"}})
			return true, "run the tests"
		}
		return false, ""
	}
	_, stats, err := a.Turn(context.Background(), []llm.Block{{Type: llm.BlockText, Text: "go"}})
	if err != nil || stats.Requests != 2 || len(actives) != 2 || actives[0] || !actives[1] {
		t.Fatalf("stats %+v actives %v err %v", stats, actives, err)
	}
	last := p.requests[1][len(p.requests[1])-1]
	if txt := lastText(last); !strings.Contains(txt, "run the tests") || !strings.Contains(txt, "queued note") {
		t.Errorf("continuation message %q", txt)
	}
}
