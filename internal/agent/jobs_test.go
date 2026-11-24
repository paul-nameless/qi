package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/paul-nameless/qi/internal/llm"
)

func bgCall(id, cmd string, daemon bool) llm.Message {
	args := map[string]any{"command": cmd, "background": true}
	if daemon {
		args = map[string]any{"command": cmd, "daemon": true}
	}
	in, _ := json.Marshal(args)
	return runCall(id, "return await bash("+string(in)+")")
}

func textMsg(s string) llm.Message {
	return llm.Message{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockText, Text: s}}}
}

func TestTurnWaitsForBackgroundJob(t *testing.T) {
	t.Parallel()
	p := &fakeProvider{replies: []llm.Message{bgCall("c1", "sleep 0.3; echo finished", false), textMsg("waiting for it")}}
	a := newAgent(t, p)
	t.Cleanup(a.KillJobs)
	final, stats, err := a.Turn(context.Background(), []llm.Block{{Type: llm.BlockText, Text: "go"}})
	if err != nil || final != "done" || stats.Requests != 3 {
		t.Fatalf("final %q stats %+v err %v", final, stats, err)
	}
	if res := p.requests[1][len(p.requests[1])-1].Blocks[0]; !strings.Contains(res.Text, "Started background job 1") {
		t.Fatalf("start result %q", res.Text)
	}
	last := p.requests[2][len(p.requests[2])-1]
	if last.Role != llm.RoleUser || !strings.Contains(lastText(last), `<background-job id="1" status="exit 0"`) || !strings.Contains(lastText(last), "finished") {
		t.Fatalf("notice not delivered: %+v", last)
	}
}

func TestCancelWhileWaitingForJob(t *testing.T) {
	t.Parallel()
	p := &fakeProvider{replies: []llm.Message{bgCall("c1", "sleep 30", false), textMsg("waiting")}}
	a := newAgent(t, p)
	t.Cleanup(a.KillJobs)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	start := time.Now()
	_, _, err := a.Turn(ctx, []llm.Block{{Type: llm.BlockText, Text: "go"}})
	if !errors.Is(err, context.Canceled) || time.Since(start) > 5*time.Second {
		t.Fatalf("err %v after %s", err, time.Since(start))
	}
	if ids := a.env.Jobs.Waiting(); len(ids) != 0 {
		t.Fatalf("after Ctrl-C later turns should not wait for the job again: %v", ids)
	}
	if !strings.Contains(a.env.Jobs.Describe(), "running") {
		t.Fatal("Ctrl-C should leave the job running")
	}
	msgs := a.Messages()
	if msgs[len(msgs)-1].Text() != "waiting" {
		t.Fatalf("history should end with the assistant reply: %+v", msgs[len(msgs)-1])
	}
}

func TestDaemonDoesNotHoldTurn(t *testing.T) {
	t.Parallel()
	p := &fakeProvider{replies: []llm.Message{bgCall("c1", "sleep 30", true), textMsg("server up")}}
	a := newAgent(t, p)
	t.Cleanup(a.KillJobs)
	start := time.Now()
	final, stats, err := a.Turn(context.Background(), []llm.Block{{Type: llm.BlockText, Text: "go"}})
	if err != nil || final != "server up" || stats.Requests != 2 || time.Since(start) > 5*time.Second {
		t.Fatalf("final %q stats %+v err %v after %s", final, stats, err, time.Since(start))
	}
}

func TestSteeringWakesJobWait(t *testing.T) {
	t.Parallel()
	p := &fakeProvider{replies: []llm.Message{bgCall("c1", "sleep 1", false), textMsg("waiting")}}
	a := newAgent(t, p)
	t.Cleanup(a.KillJobs)
	p.onCall = func(n int) {
		if n == 1 {
			time.AfterFunc(200*time.Millisecond, func() { a.Queue([]llm.Block{{Type: llm.BlockText, Text: "status?"}}) })
		}
	}
	final, stats, err := a.Turn(context.Background(), []llm.Block{{Type: llm.BlockText, Text: "go"}})
	if err != nil || final != "done" || stats.Requests != 4 {
		t.Fatalf("final %q stats %+v err %v", final, stats, err)
	}
	if got := lastText(p.requests[2][len(p.requests[2])-1]); !strings.Contains(got, "status?") || strings.Contains(got, "<background-job") {
		t.Fatalf("queued message should wake the wait before the job ends: %q", got)
	}
	if !strings.Contains(lastText(p.requests[3][len(p.requests[3])-1]), "<background-job") {
		t.Fatal("turn should keep waiting for the job after answering")
	}
}

func TestNoticeOfIdleJobJoinsNextTurn(t *testing.T) {
	t.Parallel()
	p := &fakeProvider{replies: []llm.Message{bgCall("c1", "sleep 0.5; exit 4", false), textMsg("waiting")}}
	a := newAgent(t, p)
	t.Cleanup(a.KillJobs)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	_, _, _ = a.Turn(ctx, []llm.Block{{Type: llm.BlockText, Text: "go"}})
	deadline := time.Now().Add(10 * time.Second)
	for !a.env.Jobs.HasNotices() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if _, _, err := a.Turn(context.Background(), []llm.Block{{Type: llm.BlockText, Text: "next"}}); err != nil {
		t.Fatal(err)
	}
	first := p.requests[len(p.requests)-1]
	if got := lastText(first[len(first)-1]); !strings.Contains(got, "next") || !strings.Contains(got, `status="exit 4"`) {
		t.Fatalf("notice of a job that finished between turns should join the next user message: %q", got)
	}
}

func TestQueueDuringLastReplyWithoutJobsEndsTurn(t *testing.T) {
	t.Parallel()
	p := &fakeProvider{}
	a := newAgent(t, p)
	p.onCall = func(int) { a.Queue([]llm.Block{{Type: llm.BlockText, Text: "later"}}) }
	if _, stats, err := a.Turn(context.Background(), []llm.Block{{Type: llm.BlockText, Text: "go"}}); err != nil || stats.Requests != 1 {
		t.Fatalf("stats %+v err %v", stats, err)
	}
	if q := a.DrainQueue(); len(q) != 1 {
		t.Fatalf("queued item should be left for the next turn, got %v", q)
	}
}
