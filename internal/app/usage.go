package app

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
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

const (
	usageDays     = 30
	usageProjects = 5
)

type spend struct {
	llm.Usage
	reported, estimated            float64
	requests, priced, estimatedReq int
}

type pricedUsage struct {
	u         llm.Usage
	model     string
	longCache bool
}

func (s *spend) add(p pricedUsage, book *priceBook) {
	u := p.u
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
	if c, ok := book.lookup(p.model); ok {
		s.estimated += c.estimate(u, p.longCache)
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

func (s spend) unpriced() int { return s.requests - s.priced - s.estimatedReq }

func (s spend) amount() string {
	switch {
	case s.requests == 0:
		return "–"
	case s.estimatedReq > 0:
		return "~" + dollars(s.total())
	case s.priced > 0:
		return dollars(s.reported)
	}
	return "no price"
}

func (s spend) count() string {
	if n := s.unpriced(); n > 0 && n < s.requests {
		return fmt.Sprintf("%d (%d unpriced)", s.requests, n)
	}
	return fmt.Sprint(s.requests)
}

func (s spend) detail() string {
	if s.requests == 0 {
		return "no requests"
	}
	cost := "**" + s.amount() + "**"
	if s.estimatedReq > 0 && s.priced > 0 {
		cost += " (" + dollars(s.reported) + " reported)"
	}
	if n := s.unpriced(); n > 0 && n < s.requests {
		cost += fmt.Sprintf(", %d unpriced", n)
	}
	parts := []string{cost, plural(s.requests, "request"), "input " + agent.FmtTokens(s.Input) + cachedShare(s.Usage), "output " + agent.FmtTokens(s.Output)}
	if s.CacheWrite > 0 {
		parts = append(parts, "cache write "+agent.FmtTokens(s.CacheWrite))
	}
	return strings.Join(parts, " · ")
}

func (s spend) cached() string {
	if s.Input == 0 {
		return "–"
	}
	return fmt.Sprintf("%d%%", s.CacheRead*100/s.Input)
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

type usageLine struct {
	Type  string       `json:"type"`
	Time  string       `json:"time"`
	Cwd   string       `json:"cwd"`
	Kind  session.Kind `json:"kind"`
	Model string       `json:"model"`
	Usage *llm.Usage   `json:"usage"`
}

type usageFile struct {
	project string
	long    bool
}

func eachUsage(path string, fn func(f usageFile, e usageLine, p pricedUsage)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 256<<20)
	var file usageFile
	model := ""
	for sc.Scan() {
		var e usageLine
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		if e.Type == "meta" {
			file = usageFile{project: e.Cwd, long: e.Kind == session.Interactive}
		}
		if e.Model != "" {
			model = e.Model
		}
		if u := e.Usage; u != nil && (u.Input > 0 || u.Output > 0 || u.Cost != nil) {
			fn(file, e, pricedUsage{u: *u, model: model, longCache: file.long})
		}
	}
	return sc.Err()
}

func fileSpend(path string, book *priceBook) (spend, error) {
	var s spend
	err := eachUsage(path, func(_ usageFile, _ usageLine, p pricedUsage) { s.add(p, book) })
	return s, err
}

func subagentSpend(dir, parent string, book *priceBook) (spend, int) {
	var total spend
	des, _ := os.ReadDir(filepath.Join(dir, "subagents"))
	n := 0
	for _, de := range des {
		f := filepath.Join(dir, "subagents", de.Name())
		if !strings.HasSuffix(de.Name(), ".jsonl") || metaParent(f) != parent {
			continue
		}
		if s, err := fileSpend(f, book); err == nil {
			n++
			total = total.merge(s)
		}
	}
	return total, n
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

func addTo(m map[string]*spend, key string, p pricedUsage, book *priceBook) {
	s := m[key]
	if s == nil {
		s = &spend{}
		m[key] = s
	}
	s.add(p, book)
}

func scanUsage(ctx context.Context, root string, since time.Time, loc *time.Location, book *priceBook) (usageTotals, error) {
	t := usageTotals{days: map[string]*spend{}, models: map[string]*spend{}, projects: map[string]*spend{}}
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		if info, err := d.Info(); err != nil || info.ModTime().Before(since) {
			return nil
		}
		_ = eachUsage(path, func(f usageFile, e usageLine, p pricedUsage) {
			at, err := time.Parse(time.RFC3339Nano, e.Time)
			if err != nil || at.Before(since) {
				return
			}
			addTo(t.days, at.In(loc).Format(time.DateOnly), p, book)
			addTo(t.models, p.model, p, book)
			addTo(t.projects, cmp.Or(f.project, filepath.Base(filepath.Dir(path))), p, book)
			t.all.add(p, book)
		})
		return nil
	})
	return t, err
}

type usageOptions struct {
	root     string
	session  *session.Session
	subagent string
	now      time.Time
	book     *priceBook
}

func usageReport(ctx context.Context, o usageOptions) (string, error) {
	var b strings.Builder
	if s := o.session; s != nil {
		own, err := fileSpend(s.Path, o.book)
		if err != nil {
			return "", fmt.Errorf("read session: %w", err)
		}
		subs, n := subagentSpend(cmp.Or(o.subagent, filepath.Dir(s.Path)), s.ID, o.book)
		fmt.Fprintf(&b, "**this session** · %s\n", own.merge(subs).detail())
		if n > 0 {
			fmt.Fprintf(&b, "of which subagents (%d) · %s\n", n, subs.detail())
		}
		b.WriteString("\n")
	}
	loc := o.now.Location()
	today := time.Date(o.now.Year(), o.now.Month(), o.now.Day(), 0, 0, 0, 0, loc)
	since := today.AddDate(0, 0, 1-usageDays)
	t, err := scanUsage(ctx, o.root, since, loc, o.book)
	if err != nil {
		return "", fmt.Errorf("read sessions: %w", err)
	}
	b.WriteString("| day | spend | requests |\n|:--|--:|--:|\n")
	for i := range 7 {
		day := today.AddDate(0, 0, -i)
		label := day.Format("Mon 01-02")
		if i == 0 {
			label = "**today**"
		}
		s := spend{}
		if d := t.days[day.Format(time.DateOnly)]; d != nil {
			s = *d
		}
		fmt.Fprintf(&b, "| %s | %s | %s |\n", label, s.amount(), s.count())
	}
	fmt.Fprintf(&b, "| **last %d days** | **%s** | **%s** |\n", usageDays, t.all.amount(), t.all.count())
	breakdown(&b, "model", t.models, 0, func(k string) string { return cmp.Or(k, "unknown") })
	breakdown(&b, "project", t.projects, usageProjects, shortHome)
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
	fmt.Fprintf(b, "\n| %s, last %d days | spend | requests | cached |\n|:--|--:|--:|--:|\n", title, usageDays)
	for _, k := range keys {
		s := m[k]
		fmt.Fprintf(b, "| `%s` | %s | %s | %s |\n", name(k), s.amount(), s.count(), s.cached())
	}
	if more > 0 {
		fmt.Fprintf(b, "| … %d more | | | |\n", more)
	}
}

func shortHome(path string) string {
	home := config.Home()
	if rest, ok := strings.CutPrefix(path, home); ok && (rest == "" || strings.HasPrefix(rest, string(filepath.Separator))) {
		return "~" + rest
	}
	return path
}
