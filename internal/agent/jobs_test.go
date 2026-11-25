package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/paul-nameless/qi/internal/llm"
)

func bgCall(id, cmd string) llm.Message {
	in, _ := json.Marshal(map[string]any{"command": cmd, "background": true})
	return runCall(id, "return await bash("+string(in)+")")
}

func gated(gate, then string) string {
	return fmt.Sprintf("i=0; while [ ! -e %[1]q ] && [ $i -lt 500 ]; do sleep 0.01; i=$((i+1)); done; [ -e %[1]q ] || exit 9; %[2]s", gate, then)
}

func openGate(t *testing.T, gate string) {
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Error(err)
	}
}

func textMsg(s string) llm.Message {
	return llm.Message{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockText, Text: s}}}
}

func TestHeadlessTurnWaitsForBackgroundJob(t *testing.T) {
	t.Parallel()
	gate := filepath.Join(t.TempDir(), "gate")
	p := &fakeProvider{replies: []llm.Message{bgCall("c1", gated(gate, "echo finished")), textMsg("waiting for it")}}
	a := newHeadlessAgent(t, p, onJobWait(func() { openGate(t, gate) }))
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

func TestCancelWhileHeadlessWaitsForJob(t *testing.T) {
	t.Parallel()
	p := &fakeProvider{replies: []llm.Message{bgCall("c1", "sleep 30"), textMsg("waiting")}}
	ctx, cancel := context.WithCancel(context.Background())
	a := newHeadlessAgent(t, p, onJobWait(cancel))
	t.Cleanup(a.KillJobs)
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

func TestInteractiveTurnDoesNotWaitForJobs(t *testing.T) {
	t.Parallel()
	p := &fakeProvider{replies: []llm.Message{bgCall("c1", "sleep 30"), textMsg("server up")}}
	a := newAgent(t, p)
	t.Cleanup(a.KillJobs)
	start := time.Now()
	final, stats, err := a.Turn(context.Background(), []llm.Block{{Type: llm.BlockText, Text: "go"}})
	if err != nil || final != "server up" || stats.Requests != 2 || time.Since(start) > 5*time.Second {
		t.Fatalf("final %q stats %+v err %v after %s", final, stats, err, time.Since(start))
	}
	if a.HasJobNotices() {
		t.Fatal("a running job left a notice")
	}
}

func TestSteeringWakesHeadlessJobWait(t *testing.T) {
	t.Parallel()
	gate := filepath.Join(t.TempDir(), "gate")
	p := &fakeProvider{replies: []llm.Message{bgCall("c1", gated(gate, "true")), textMsg("waiting")}}
	var a *Agent
	asked := false
	a = newHeadlessAgent(t, p, onJobWait(func() {
		if !asked {
			asked = true
			a.Queue([]llm.Block{{Type: llm.BlockText, Text: "status?"}})
		}
	}))
	t.Cleanup(a.KillJobs)
	p.onCall = func(n int) {
		if n == 2 {
			openGate(t, gate)
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
	gate := filepath.Join(t.TempDir(), "gate")
	p := &fakeProvider{replies: []llm.Message{bgCall("c1", gated(gate, "exit 4")), textMsg("waiting")}}
	a := newAgent(t, p)
	t.Cleanup(a.KillJobs)
	if _, _, err := a.Turn(context.Background(), []llm.Block{{Type: llm.BlockText, Text: "go"}}); err != nil {
		t.Fatal(err)
	}
	openGate(t, gate)
	timeout := time.After(10 * time.Second)
	for !a.HasJobNotices() {
		select {
		case <-a.JobsChanged():
		case <-timeout:
			t.Fatal("the job never finished")
		}
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
