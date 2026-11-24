package app

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/paul-nameless/qi/internal/agent"
	"github.com/paul-nameless/qi/internal/llm"
	"github.com/paul-nameless/qi/internal/session"
)

type spend struct {
	llm.Usage
	dollars          float64
	requests, priced int
}

func (s *spend) add(u llm.Usage) {
	s.Input += u.Input
	s.Output += u.Output
	s.CacheRead += u.CacheRead
	s.CacheWrite += u.CacheWrite
	if u.Cost != nil {
		s.dollars += *u.Cost
		s.priced++
	}
	s.requests++
}

func (s spend) String() string {
	if s.requests == 0 {
		return "no requests"
	}
	cost := "not reported by the provider"
	switch {
	case s.priced == s.requests:
		cost = dollars(s.dollars)
	case s.priced > 0:
		cost = fmt.Sprintf("%s for %d of %d requests (the rest not reported)", dollars(s.dollars), s.priced, s.requests)
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

func fileSpend(path string) (spend, error) {
	var s spend
	f, err := os.Open(path)
	if err != nil {
		return s, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 256<<20)
	for sc.Scan() {
		var e struct {
			Usage *llm.Usage `json:"usage"`
		}
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.Usage != nil {
			s.add(*e.Usage)
		}
	}
	return s, sc.Err()
}

func subagentSpend(dir, parent string) (spend, int, error) {
	var total spend
	des, err := os.ReadDir(filepath.Join(dir, "subagents"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return total, 0, err
	}
	n := 0
	for _, de := range des {
		f := filepath.Join(dir, "subagents", de.Name())
		if !strings.HasSuffix(de.Name(), ".jsonl") || metaParent(f) != parent {
			continue
		}
		s, err := fileSpend(f)
		if err != nil {
			return total, n, err
		}
		n++
		total = total.mergeAll(s)
	}
	return total, n, nil
}

func (s spend) mergeAll(o spend) spend {
	s.Input += o.Input
	s.Output += o.Output
	s.CacheRead += o.CacheRead
	s.CacheWrite += o.CacheWrite
	s.dollars += o.dollars
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

func (r *repl) costReport() (string, error) {
	s := r.agent.Session()
	if s == nil {
		return "", errors.New("no session")
	}
	own, err := fileSpend(s.Path)
	if err != nil {
		return "", fmt.Errorf("read session: %w", err)
	}
	subs, n, err := subagentSpend(session.Dir(r.agent.Cwd()), s.ID)
	if err != nil {
		return "", fmt.Errorf("read subagent session: %w", err)
	}
	if n == 0 {
		return "cost  " + own.String(), nil
	}
	return fmt.Sprintf("session    %s\nsubagents  %s (%d)\ntotal      %s", own, subs, n, own.mergeAll(subs)), nil
}
