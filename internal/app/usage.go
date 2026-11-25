package app

import (
	"bufio"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/paul-nameless/qi/internal/agent"
	"github.com/paul-nameless/qi/internal/config"
	"github.com/paul-nameless/qi/internal/llm"
	"github.com/paul-nameless/qi/internal/session"
)

const usageDays = 30

type spend struct {
	llm.Usage
	reported, estimated            float64
	requests, priced, estimatedReq int
}

func (s *spend) add(u llm.Usage, model string, book *priceBook) {
	s.Input += u.Input
	s.Output += u.Output
	s.CacheRead += u.CacheRead
	s.CacheWrite += u.CacheWrite
	s.requests++
	if u.Cost != nil {
		s.reported += *u.Cost
		s.priced++
		return
	}
	if c, ok := book.lookup(model); ok {
		s.estimated += c.estimate(u)
		s.estimatedReq++
	}
}

func (s spend) merge(o spend) spend {
	s.Input += o.Input
	s.Output += o.Output
	s.CacheRead += o.CacheRead
	s.CacheWrite += o.CacheWrite
	s.reported += o.reported
	s.estimated += o.estimated
	s.requests += o.requests
	s.priced += o.priced
	s.estimatedReq += o.estimatedReq
	return s
}

func (s spend) total() float64 { return s.reported + s.estimated }

func (s spend) cost() string {
	var text string
	switch {
	case s.estimatedReq > 0 && s.priced > 0:
		text = fmt.Sprintf("~%s (%s reported)", dollars(s.total()), dollars(s.reported))
	case s.estimatedReq > 0:
		text = "~" + dollars(s.estimated)
	case s.priced > 0:
		text = dollars(s.reported)
	}
	unpriced := s.requests - s.priced - s.estimatedReq
	switch {
	case unpriced == 0:
		return text
	case text == "":
		return "no price"
	}
	return fmt.Sprintf("%s, %d unpriced", text, unpriced)
}

func (s spend) brief() string {
	if s.requests == 0 {
		return "no requests"
	}
	return s.cost() + " · " + plural(s.requests, "request")
}

func (s spend) String() string {
	if s.requests == 0 {
		return "no requests"
	}
	parts := []string{s.brief(), "input " + agent.FmtTokens(s.Input) + cachedShare(s.Usage), "output " + agent.FmtTokens(s.Output)}
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

func cachedPart(u llm.Usage) string {
	if u.Input == 0 {
		return ""
	}
	return fmt.Sprintf(" · %d%% cached", u.CacheRead*100/u.Input)
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

type usageLine struct {
	Time  string     `json:"time"`
	Cwd   string     `json:"cwd"`
	Model string     `json:"model"`
	Usage *llm.Usage `json:"usage"`
}

func eachUsage(path string, fn func(e usageLine, model string)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 256<<20)
	model := ""
	for sc.Scan() {
		var e usageLine
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		if e.Model != "" {
			model = e.Model
		}
		if e.Usage != nil {
			fn(e, model)
		}
	}
	return sc.Err()
}

func fileSpend(path string, book *priceBook) (spend, error) {
	var s spend
	err := eachUsage(path, func(e usageLine, model string) { s.add(*e.Usage, model, book) })
	return s, err
}

func subagentSpend(dir, parent string, book *priceBook) (spend, int, error) {
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
		s, err := fileSpend(f, book)
		if err != nil {
			return total, n, err
		}
		n++
		total = total.merge(s)
	}
	return total, n, nil
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

type usageTotals struct {
	days     map[string]*spend
	all      spend
	models   map[string]*spend
	projects map[string]*spend
}

func addTo(m map[string]*spend, key string, u llm.Usage, model string, book *priceBook) {
	s := m[key]
	if s == nil {
		s = &spend{}
		m[key] = s
	}
	s.add(u, model, book)
}

func scanUsage(root string, since time.Time, loc *time.Location, book *priceBook) (usageTotals, error) {
	t := usageTotals{days: map[string]*spend{}, models: map[string]*spend{}, projects: map[string]*spend{}}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == root && errors.Is(err, fs.ErrNotExist) {
				return fs.SkipAll
			}
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		if info, err := d.Info(); err != nil || info.ModTime().Before(since) {
			return nil
		}
		project := cmp.Or(projectOf(path), filepath.Base(filepath.Dir(path)))
		return eachUsage(path, func(e usageLine, model string) {
			at, err := time.Parse(time.RFC3339Nano, e.Time)
			if err != nil || at.Before(since) {
				return
			}
			u := *e.Usage
			addTo(t.days, at.In(loc).Format(time.DateOnly), u, model, book)
			addTo(t.models, model, u, model, book)
			addTo(t.projects, project, u, model, book)
			t.all.add(u, model, book)
		})
	})
	return t, err
}

func projectOf(path string) string {
	line, err := firstJSONLine(path)
	if err != nil {
		return ""
	}
	var e usageLine
	if json.Unmarshal(line, &e) != nil {
		return ""
	}
	return e.Cwd
}

func firstJSONLine(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return bufio.NewReader(f).ReadBytes('\n')
}

type usageOptions struct {
	root    string
	session *session.Session
	now     time.Time
	book    *priceBook
}

func usageReport(o usageOptions) (string, error) {
	var b strings.Builder
	row := func(label, text string) { fmt.Fprintf(&b, "%-14s%s\n", label, text) }
	if s := o.session; s != nil {
		own, err := fileSpend(s.Path, o.book)
		if err != nil {
			return "", fmt.Errorf("read session: %w", err)
		}
		subs, n, err := subagentSpend(filepath.Dir(s.Path), s.ID, o.book)
		if err != nil {
			return "", fmt.Errorf("read subagent session: %w", err)
		}
		row("this session", own.merge(subs).String())
		if n > 0 {
			row("  subagents", fmt.Sprintf("%s (%d)", subs.brief(), n))
		}
	}
	loc := o.now.Location()
	today := time.Date(o.now.Year(), o.now.Month(), o.now.Day(), 0, 0, 0, 0, loc)
	since := today.AddDate(0, 0, 1-usageDays)
	t, err := scanUsage(o.root, since, loc, o.book)
	if err != nil {
		return "", fmt.Errorf("read sessions: %w", err)
	}
	for i := range 7 {
		day := today.AddDate(0, 0, -i)
		label := day.Format("Mon 01-02")
		if i == 0 {
			label = "today"
		}
		s := spend{}
		if d := t.days[day.Format(time.DateOnly)]; d != nil {
			s = *d
		}
		row(label, s.brief())
	}
	row(fmt.Sprintf("last %d days", usageDays), t.all.brief())
	breakdown(&b, "by model", t.models, 0, func(k string) string { return cmp.Or(k, "(unknown model)") })
	breakdown(&b, "by project", t.projects, 5, shortHome)
	return strings.TrimRight(b.String(), "\n"), nil
}

func breakdown(b *strings.Builder, title string, m map[string]*spend, limit int, name func(string) string) {
	if len(m) == 0 {
		return
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(x, y string) int {
		return cmp.Or(cmp.Compare(m[y].total(), m[x].total()), cmp.Compare(m[y].requests, m[x].requests), strings.Compare(x, y))
	})
	more := 0
	if limit > 0 && len(keys) > limit {
		more = len(keys) - limit
		keys = keys[:limit]
	}
	width := 0
	for _, k := range keys {
		width = max(width, len(name(k)))
	}
	fmt.Fprintf(b, "%s, last %d days\n", title, usageDays)
	for _, k := range keys {
		s := m[k]
		fmt.Fprintf(b, "  %-*s  %s%s\n", width, name(k), s.brief(), cachedPart(s.Usage))
	}
	if more > 0 {
		fmt.Fprintf(b, "  … %d more\n", more)
	}
}

func shortHome(path string) string {
	home := config.Home()
	if rest, ok := strings.CutPrefix(path, home); ok && (rest == "" || strings.HasPrefix(rest, string(filepath.Separator))) {
		return "~" + rest
	}
	return path
}
