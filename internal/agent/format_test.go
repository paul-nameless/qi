package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/paul-nameless/qi/internal/config"
)

func TestFmtTokens(t *testing.T) {
	t.Parallel()
	cases := map[int]string{42: "42", 5234: "5.2k", 200000: "200k", 1000000: "1M", 1050000: "1.05M"}
	for n, want := range cases {
		if got := FmtTokens(n); got != want {
			t.Errorf("FmtTokens(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestContextUsageBeforeFirstRequest(t *testing.T) {
	t.Parallel()
	a := New(Options{Config: config.Config{Model: "claude-opus-5-5", ContextWindow: 200000}})
	a.SetSystem(SystemPrompt{Text: strings.Repeat("x", 40000)})
	if got, want := a.ContextUsage(), "10.0k/200k (5.0%)"; got != want {
		t.Errorf("ContextUsage() = %q, want %q", got, want)
	}
}

func TestTurnStatsLine(t *testing.T) {
	s := TurnStats{Requests: 1}
	done := time.Date(2026, 11, 15, 21, 11, 5, 0, time.Local)
	if got := s.String(usageOf(8400, 1000000, true), 11400*time.Millisecond, done); got != "8.4k/1M (0.8%) · cooked for 11s · done 21:11" {
		t.Errorf("stats %q", got)
	}
	for jobs, want := range map[int]string{1: "cooked for 11s · done 21:11 · 1 bg job", 3: "cooked for 11s · done 21:11 · 3 bg jobs"} {
		s.Jobs = jobs
		if got := s.String("x", 11*time.Second, done); !strings.HasSuffix(got, want) {
			t.Errorf("%d jobs: %q", jobs, got)
		}
	}
	for d, want := range map[time.Duration]string{400 * time.Millisecond: "0s", 65 * time.Second: "1m 5s", 3*time.Hour + 4*time.Minute: "3h 4m"} {
		if got := fmtElapsed(d); got != want {
			t.Errorf("fmtElapsed(%v) = %q, want %q", d, got, want)
		}
	}
}
