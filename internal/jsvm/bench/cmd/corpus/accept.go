package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/paul-nameless/qi/internal/jsvm/bench/harness"
	"github.com/paul-nameless/qi/internal/jsvm/bench/oracle"
	"github.com/paul-nameless/qi/internal/jsvm/bench/pool"
)

type candidate struct {
	src    string
	code   string
	cells  int
	runs   map[string][]harness.WorkResp
	reason string
	detail string
}

type IndexEntry struct {
	ID     string `json:"id"`
	Src    string `json:"src"`
	Kind   string `json:"kind"`
	Sloppy bool   `json:"sloppy,omitempty"`
}

type Dropped struct {
	Src    string `json:"src"`
	Reason string `json:"reason"`
	Detail string `json:"detail,omitempty"`
	Goja   string `json:"goja,omitempty"`
	QJS    string `json:"quickjs,omitempty"`
}

func hash(s string) string {
	h := sha256.Sum256([]byte(strings.TrimSpace(s)))
	return hex.EncodeToString(h[:8])
}

func existing(dest string) (map[string]bool, int) {
	seen := map[string]bool{}
	max := 0
	files, _ := filepath.Glob(filepath.Join(dest, "*.js"))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err == nil {
			seen[hash(string(b))] = true
		}
		var n int
		if _, err := fmt.Sscanf(filepath.Base(f), "%04d.js", &n); err == nil && n > max {
			max = n
		}
	}
	return seen, max
}

func loadCandidates(srcs []string, seen map[string]bool) []*candidate {
	var files []string
	for _, s := range srcs {
		if st, err := os.Stat(s); err == nil && st.IsDir() {
			fs, _ := filepath.Glob(filepath.Join(s, "*.js"))
			sort.Strings(fs)
			files = append(files, fs...)
			continue
		}
		files = append(files, s)
	}
	var cs []*candidate
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil || len(strings.TrimSpace(string(b))) == 0 {
			continue
		}
		h := hash(string(b))
		if seen[h] {
			continue
		}
		seen[h] = true
		code := string(b)
		cs = append(cs, &candidate{src: f, code: code, cells: len(harness.SplitCells(code)), runs: map[string][]harness.WorkResp{}})
	}
	return cs
}

func requests(cs []*candidate) []harness.WorkReq {
	var reqs []harness.WorkReq
	for i, c := range cs {
		for _, k := range []string{"ext", "ext"} {
			reqs = append(reqs, harness.WorkReq{ID: fmt.Sprint(i), Kind: k, Code: c.code})
		}
		if c.cells == 1 {
			reqs = append(reqs, harness.WorkReq{ID: fmt.Sprint(i), Kind: "sloppy", Code: c.code}, harness.WorkReq{ID: fmt.Sprint(i), Kind: "strict", Code: c.code})
		}
	}
	return reqs
}

func runEngines(cs []*candidate, workers int) error {
	reqs := requests(cs)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	for _, eng := range []string{"goja", "modernc"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resps, err := pool.Run("bin/"+eng, workers, reqs, 20*time.Second)
			mu.Lock()
			defer mu.Unlock()
			if err != nil && firstErr == nil {
				firstErr = err
			}
			for i, r := range resps {
				var n int
				fmt.Sscan(reqs[i].ID, &n)
				key := eng + ":" + reqs[i].Kind
				r.Ms = max(r.Ms, 0)
				cs[n].runs[key] = append(cs[n].runs[key], r)
			}
		}()
	}
	wg.Wait()
	return firstErr
}

func judge(c *candidate, keepSyntax bool) (string, string) {
	g, q := c.runs["goja:ext"], c.runs["modernc:ext"]
	for _, rs := range [][]harness.WorkResp{g, q} {
		for _, r := range rs {
			if r.Err != "" {
				return "harness error", r.Err
			}
		}
	}
	switch {
	case g[0].Canon != g[1].Canon:
		return "nondeterministic", "goja " + oracle.FirstDiff(g[0].Canon, g[1].Canon)
	case q[0].Canon != q[1].Canon:
		return "nondeterministic", "quickjs " + oracle.FirstDiff(q[0].Canon, q[1].Canon)
	case min(g[0].Ms, g[1].Ms) > 200 || min(q[0].Ms, q[1].Ms) > 200:
		return "slow", fmt.Sprintf("goja %.0fms quickjs %.0fms", min(g[0].Ms, g[1].Ms), min(q[0].Ms, q[1].Ms))
	}
	gc, qc := g[0].Canon, q[0].Canon
	switch {
	case strings.Contains(gc, "!! Timeout") || strings.Contains(qc, "!! Timeout"):
		return "timeout", ""
	case strings.Contains(gc, "is not available in the corpus") || strings.Contains(qc, "is not available in the corpus"):
		return "calls host tools", ""
	case oracle.PrintsStack(gc) || oracle.PrintsStack(qc):
		return "prints stack", ""
	case !keepSyntax && gc == "" && qc == "":
		return "no output", ""
	case len(gc) > 20000:
		return "huge output", fmt.Sprint(len(gc))
	case !keepSyntax && oracle.OnlyError(gc) && strings.HasPrefix(gc, "!! SyntaxError") && strings.HasPrefix(qc, "!! SyntaxError"):
		return "syntax error in candidate", strings.TrimSpace(gc)
	}
	if gc == qc {
		return "", "exact"
	}
	if ok, _ := oracle.WordingOnly(gc, qc, oracle.PrintsMessage(c.code)); ok {
		return "", "wording"
	}
	return "disagree: " + classify(gc, qc), oracle.FirstDiff(gc, qc)
}

func errOf(c string) (string, string, bool) {
	for _, l := range strings.Split(c, "\n") {
		if strings.HasPrefix(l, "!! ") {
			name, _, _ := strings.Cut(strings.TrimPrefix(l, "!! "), ":")
			at := l[strings.LastIndex(l, "@")+1:]
			return name, at, true
		}
	}
	return "", "", false
}

func classify(g, q string) string {
	gn, gl, ge := errOf(g)
	qn, ql, qe := errOf(q)
	switch {
	case ge && qe && gn == qn && gl != ql:
		return "error line differs"
	case ge && qe && gn != qn:
		return "error type differs"
	case ge != qe:
		return "only one engine throws"
	case strings.Contains(g, "\n=> ") != strings.Contains(q, "\n=> ") || strings.HasPrefix(g, "=> ") != strings.HasPrefix(q, "=> "):
		return "completion value differs"
	}
	return "output differs"
}

func sloppy(c *candidate) bool {
	for _, e := range []string{"goja", "modernc"} {
		s, t := c.runs[e+":sloppy"], c.runs[e+":strict"]
		if len(s) == 1 && len(t) == 1 && s[0].Canon != t[0].Canon {
			return true
		}
	}
	return false
}

func appendJSONL(path string, v any) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	b, _ := json.Marshal(v)
	_, err = f.Write(append(b, '\n'))
	return err
}

func accept(args []string) error {
	fs := flag.NewFlagSet("accept", flag.ExitOnError)
	dest := fs.String("dest", "", "destination directory (corpus/ext/<source>)")
	keepSyntax := fs.Bool("edge", false, "hand-written edge cases: keep scripts with no output or only an agreed SyntaxError")
	workers := fs.Int("j", 4, "workers per engine")
	limit := fs.Int("limit", 0, "accept at most this many")
	_ = fs.Parse(args)
	if *dest == "" || fs.NArg() == 0 {
		return fmt.Errorf("usage: corpus accept -dest DIR CANDIDATE_DIR_OR_FILES...")
	}
	if err := os.MkdirAll(*dest, 0o755); err != nil {
		return err
	}
	for _, e := range []string{"goja", "modernc"} {
		if _, err := pool.Build(e); err != nil {
			return err
		}
	}
	seen, next := existing(*dest)
	cs := loadCandidates(fs.Args(), seen)
	if err := runEngines(cs, *workers); err != nil {
		return err
	}
	stats := map[string]int{}
	accepted := 0
	for _, c := range cs {
		reason, detail := judge(c, *keepSyntax)
		if reason == "" && *limit > 0 && accepted >= *limit {
			reason = "over limit"
		}
		if reason != "" {
			stats[reason]++
			d := Dropped{Src: filepath.Base(c.src), Reason: reason, Detail: detail}
			if strings.HasPrefix(reason, "disagree") {
				d.Goja, d.QJS = c.runs["goja:ext"][0].Canon, c.runs["modernc:ext"][0].Canon
			}
			if err := appendJSONL(filepath.Join(*dest, "dropped.jsonl"), d); err != nil {
				return err
			}
			if err := saveDropped(*dest, c); err != nil {
				return err
			}
			continue
		}
		next++
		accepted++
		stats["accepted "+detail]++
		id := fmt.Sprintf("%04d", next)
		base := filepath.Join(*dest, id)
		gc, qc := c.runs["goja:ext"][0].Canon, c.runs["modernc:ext"][0].Canon
		if err := os.WriteFile(base+".js", []byte(c.code), 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(base+".out", []byte(gc), 0o644); err != nil {
			return err
		}
		if gc != qc {
			if err := os.WriteFile(base+".alt", []byte(qc), 0o644); err != nil {
				return err
			}
		}
		if err := appendJSONL(filepath.Join(*dest, "index.jsonl"), IndexEntry{ID: id, Src: filepath.Base(c.src), Kind: detail, Sloppy: sloppy(c)}); err != nil {
			return err
		}
	}
	keys := make([]string, 0, len(stats))
	for k := range stats {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("%-28s %d\n", k, stats[k])
	}
	fmt.Printf("candidates %d\n", len(cs))
	return nil
}

func saveDropped(dest string, c *candidate) error {
	dir := filepath.Join(dest, "dropped")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, filepath.Base(c.src)), []byte(c.code), 0o644)
}
