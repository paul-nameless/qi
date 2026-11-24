package app

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/paul-nameless/qi/internal/agent"
	"github.com/paul-nameless/qi/internal/llm"
	"github.com/paul-nameless/qi/internal/session"
)

type spend struct {
	llm.Usage
	requests, priced int
}

func (s *spend) add(u llm.Usage) {
	s.Input += u.Input
	s.Output += u.Output
	s.CacheRead += u.CacheRead
	s.CacheWrite += u.CacheWrite
	s.Cost += u.Cost
	s.requests++
	if u.Cost > 0 {
		s.priced++
	}
}

func (s spend) String() string {
	if s.requests == 0 {
		return "no requests"
	}
	cost := "not reported by the provider"
	switch {
	case s.priced == s.requests:
		cost = dollars(s.Cost)
	case s.priced > 0:
		cost = fmt.Sprintf("%s for %d of %d requests (the rest not reported)", dollars(s.Cost), s.priced, s.requests)
	}
	parts := []string{cost, plural(s.requests, "request"), "input " + agent.FmtTokens(s.Input) + cachedShare(s.Usage), "output " + agent.FmtTokens(s.Output)}
	if s.CacheWrite > 0 {
		parts = append(parts, "cache write "+agent.FmtTokens(s.CacheWrite))
	}
	return strings.Join(parts, " · ")
}

func dollars(v float64) string {
	if v < 0.01 {
		return fmt.Sprintf("$%.4f", v)
	}
	return fmt.Sprintf("$%.2f", v)
}

func cachedShare(u llm.Usage) string {
	if u.Input == 0 {
		return ""
	}
	return fmt.Sprintf(" (%d%% cached)", u.CacheRead*100/u.Input)
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func fileSpend(path string) spend {
	var s spend
	entries, _ := session.Entries(path)
	for _, e := range entries {
		if e.Usage != nil {
			s.add(*e.Usage)
		}
	}
	return s
}

func subagentSpend(dir, parent string) (spend, int) {
	var total spend
	files, _ := filepath.Glob(filepath.Join(dir, "subagents", "*.jsonl"))
	n := 0
	for _, f := range files {
		if metaParent(f) != parent {
			continue
		}
		n++
		total = total.mergeAll(fileSpend(f))
	}
	return total, n
}

func (s spend) mergeAll(o spend) spend {
	s.Input += o.Input
	s.Output += o.Output
	s.CacheRead += o.CacheRead
	s.CacheWrite += o.CacheWrite
	s.Cost += o.Cost
	s.requests += o.requests
	s.priced += o.priced
	return s
}

func metaParent(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	line, _ := bufio.NewReader(f).ReadBytes('\n')
	var e session.Entry
	if json.Unmarshal(line, &e) != nil || e.Type != "meta" {
		return ""
	}
	return e.ParentSession
}

func (r *repl) costReport() string {
	s := r.agent.Session()
	if s == nil {
		return "no session"
	}
	own := fileSpend(s.Path)
	subs, n := subagentSpend(filepath.Dir(s.Path), s.ID)
	if n == 0 {
		return "cost  " + own.String()
	}
	return fmt.Sprintf("session    %s\nsubagents  %s (%d)\ntotal      %s", own, subs, n, own.mergeAll(subs))
}
