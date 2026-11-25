package agent

import (
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/paul-nameless/qi/internal/llm"
)

type TurnStats struct {
	Requests int
	Jobs     int
}

func (s TurnStats) String(context string, elapsed time.Duration, done time.Time) string {
	jobs := ""
	switch {
	case s.Jobs == 1:
		jobs = " · 1 bg job"
	case s.Jobs > 1:
		jobs = fmt.Sprintf(" · %d bg jobs", s.Jobs)
	}
	return fmt.Sprintf("%s · cooked for %s · done %s%s", context, fmtElapsed(elapsed), done.Format("15:04"), jobs)
}

func cachedPct(u llm.Usage) string {
	if u.Input == 0 {
		return ""
	}
	return fmt.Sprintf(" (%d%% cached)", u.CacheRead*100/u.Input)
}

func fmtElapsed(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
}

func (a *Agent) ContextUsage() string {
	n := a.ContextTokens()
	if n == 0 {
		n = a.EstimateTokens()
	}
	window, known := a.Config().Window()
	return usageOf(n, window, known)
}

func usageOf(n, window int, known bool) string {
	if !known || window <= 0 {
		return FmtTokens(n)
	}
	return fmt.Sprintf("%s/%s (%s)", FmtTokens(n), FmtTokens(window), pct(n, window))
}

func (a *Agent) ContextShort() string {
	window, known := a.Config().Window()
	return usageOf(a.ContextTokens(), window, known)
}

func FmtTokens(n int) string {
	switch {
	case n >= 1000000:
		return strconv.FormatFloat(math.Round(float64(n)/1e4)/100, 'f', -1, 64) + "M"
	case n >= 100000:
		return fmt.Sprintf("%dk", n/1000)
	}
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}
