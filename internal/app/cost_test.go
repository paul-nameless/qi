package app

import (
	"strings"
	"testing"

	"github.com/paul-nameless/qi/internal/config"
	"github.com/paul-nameless/qi/internal/llm"
	"github.com/paul-nameless/qi/internal/session"
)

func TestSpendString(t *testing.T) {
	cases := []struct {
		usage []llm.Usage
		want  string
	}{
		{nil, "no requests"},
		{[]llm.Usage{{Input: 1000, CacheRead: 750, Output: 20, Cost: 0.5}, {Input: 1000, CacheRead: 750, Output: 30, Cost: 0.25}}, "$0.75 · 2 requests · input 2.0k (75% cached) · output 50"},
		{[]llm.Usage{{Input: 7000, CacheWrite: 6900, Output: 120, Cost: 0.00093}}, "$0.0009 · 1 request · input 7.0k (0% cached) · output 120 · cache write 6.9k"},
		{[]llm.Usage{{Input: 5000, Output: 10}}, "not reported by the provider · 1 request · input 5.0k (0% cached) · output 10"},
		{[]llm.Usage{{Input: 100, Cost: 0.0009}, {Input: 100, CacheWrite: 90}}, "$0.0009 for 1 of 2 requests (the rest not reported) · 2 requests · input 200 (0% cached) · output 0 · cache write 90"},
	}
	for _, c := range cases {
		var s spend
		for _, u := range c.usage {
			s.add(u)
		}
		if got := s.String(); got != c.want {
			t.Errorf("got  %s\nwant %s", got, c.want)
		}
	}
}

func TestCostReportCountsSubagentsAndCompaction(t *testing.T) {
	r, _ := testRepl(t, config.Config{Model: "m"}, scripted())
	s := r.agent.Session()
	_ = s.Message(llm.UserText("hi"), "m", nil)
	_ = s.Message(llm.Message{Role: llm.RoleAssistant}, "m", &llm.Usage{Input: 1000, CacheRead: 500, Output: 10, Cost: 0.02})
	_ = s.Compaction("sum", nil, "m", &llm.Usage{Input: 1000, Output: 100, Cost: 0.03})
	if got := r.costReport(); got != "cost  $0.05 · 2 requests · input 2.0k (25% cached) · output 110" {
		t.Errorf("own session: %s", got)
	}
	child, err := session.Create(session.CreateOptions{Cwd: r.agent.Cwd(), Model: "m", Subagent: true, ParentSession: s.ID})
	if err != nil {
		t.Fatal(err)
	}
	_ = child.Message(llm.Message{Role: llm.RoleAssistant}, "m", &llm.Usage{Input: 400, Output: 4, Cost: 0.01})
	child.Close()
	other, _ := session.Create(session.CreateOptions{Cwd: r.agent.Cwd(), Model: "m", Subagent: true, ParentSession: "someone-else"})
	_ = other.Message(llm.Message{Role: llm.RoleAssistant}, "m", &llm.Usage{Input: 9999, Cost: 9})
	other.Close()
	got := r.costReport()
	for _, want := range []string{"session    $0.05", "subagents  $0.01 · 1 request · input 400 (0% cached) · output 4 (1)", "total      $0.06 · 3 requests · input 2.4k"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
}
