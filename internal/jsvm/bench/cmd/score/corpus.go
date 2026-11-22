package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/paul-nameless/qi/internal/jsvm/bench/harness"
	"github.com/paul-nameless/qi/internal/jsvm/bench/oracle"
	"github.com/paul-nameless/qi/internal/jsvm/bench/pool"
)

type Agreement struct {
	Pass        int      `json:"pass"`
	Total       int      `json:"total"`
	Wording     int      `json:"wording_items"`
	Fail        []string `json:"fail,omitempty"`
	Crashes     int      `json:"crashes,omitempty"`
	Transcripts int      `json:"transcripts_failing,omitempty"`
	FirstDiv    []string `json:"first_divergence,omitempty"`
	Misses      int      `json:"tape_mismatches,omitempty"`
}

type Count struct {
	Pass  int `json:"pass"`
	Total int `json:"total"`
}

type ExtAgreement struct {
	Agreement
	BySource       map[string]*Count   `json:"by_source"`
	Tier1          Count               `json:"tier1"`
	ByTag          map[string]*Count   `json:"by_tag"`
	FailByBucket   map[string]int      `json:"fail_by_bucket,omitempty"`
	Unexplained    []string            `json:"unexplained,omitempty"`
	Loud           int                 `json:"loud"`
	Silent         int                 `json:"silent"`
	SilentByBucket map[string][]string `json:"silent_by_bucket,omitempty"`
}

func (a *ExtAgreement) explain(allow *oracle.Allowlist, id string, tags []string, exp, alt, act string) {
	b := allow.Bucket(id, tags)
	if b == "" {
		b = "unexplained"
		a.Unexplained = append(a.Unexplained, id)
	}
	if a.FailByBucket == nil {
		a.FailByBucket, a.SilentByBucket = map[string]int{}, map[string][]string{}
	}
	a.FailByBucket[b]++
	if oracle.Loud(exp, alt, act) {
		a.Loud++
		return
	}
	a.Silent++
	a.SilentByBucket[b] = append(a.SilentByBucket[b], id)
}

type expRow struct {
	ID        string `json:"id"`
	Canon     string `json:"canon"`
	Misses    int    `json:"misses"`
	Fallbacks int    `json:"fallbacks"`
	Unused    int    `json:"unused"`
}

var expTapeIssues = map[string]int{}

func readRows(path string) (map[string]string, []string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	m := map[string]string{}
	var order []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var r expRow
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			return nil, nil, err
		}
		m[r.ID] = r.Canon
		order = append(order, r.ID)
		if strings.HasSuffix(path, "goja.jsonl") {
			expTapeIssues[r.ID] = r.Misses + r.Fallbacks + r.Unused
		}
	}
	return m, order, sc.Err()
}

func stateTest(bin string) *StateResult {
	b, err := os.ReadFile("corpus/state.expected.json")
	if err != nil {
		return &StateResult{Fail: []string{err.Error()}}
	}
	var want []oracle.StateStep
	if err := json.Unmarshal(b, &want); err != nil {
		return &StateResult{Fail: []string{err.Error()}}
	}
	got := oracle.StateSteps(runJSON(bin, 2*time.Minute, "state").Recs)
	res := &StateResult{Total: len(want)}
	for i, w := range want {
		if i < len(got) && got[i] == w {
			res.Pass++
			continue
		}
		res.Fail = append(res.Fail, w.Name)
	}
	return res
}

func replayOracle() (map[string]string, map[string]string, []string, error) {
	g, order, err := readRows("corpus/replay/expected.goja.jsonl")
	if err != nil {
		return nil, nil, nil, err
	}
	q, _, err := readRows("corpus/replay/expected.quickjs.jsonl")
	if err != nil {
		return nil, nil, nil, err
	}
	alt := map[string]string{}
	var ids []string
	for _, id := range order {
		if g[id] == q[id] {
			ids = append(ids, id)
			continue
		}
		if ok, _ := oracle.WordingOnly(g[id], q[id], false); ok {
			alt[id] = q[id]
			ids = append(ids, id)
		}
	}
	return g, alt, ids, nil
}

func runReplay(bin string, rids []string) (map[string]harness.ReplayCell, int) {
	files := harness.TranscriptFiles("corpus/replay/transcripts", rids)
	reqs := make([]harness.WorkReq, len(files))
	for i, f := range files {
		abs, _ := filepath.Abs(f)
		reqs[i] = harness.WorkReq{ID: strings.TrimSuffix(filepath.Base(f), ".json"), Kind: "replay", Path: abs}
	}
	resps, _ := pool.Run(bin, 4, reqs, 2*time.Minute)
	got := map[string]harness.ReplayCell{}
	crashes := 0
	for i, r := range resps {
		if r.Err != "" {
			crashes++
			got[reqs[i].ID+"#crash"] = harness.ReplayCell{Canon: "!! " + r.Err}
			continue
		}
		for n, c := range r.Cells {
			got[fmt.Sprintf("%s#%d", reqs[i].ID, n)] = c
		}
	}
	return got, crashes
}

func cellOf(got map[string]harness.ReplayCell, id string) string {
	if c, ok := got[id]; ok {
		return c.Canon
	}
	rid, _, _ := strings.Cut(id, "#")
	if c, ok := got[rid+"#crash"]; ok {
		return c.Canon
	}
	return "<missing>"
}

func replayScore(bin string) *ExtAgreement {
	exp, alt, ids, err := replayOracle()
	if err != nil {
		return &ExtAgreement{Agreement: Agreement{Fail: []string{err.Error()}}}
	}
	got, crashes := runReplay(bin, nil)
	feats := loadFeatures()
	a := &ExtAgreement{Agreement: Agreement{Total: len(ids), Wording: len(alt), Crashes: crashes}, ByTag: map[string]*Count{}}
	failingT := map[string]bool{}
	for _, id := range ids {
		ok, _ := oracle.Compare(exp[id], alt[id], cellOf(got, id))
		if issues := got[id].TapeIssues(); issues > expTapeIssues[id] {
			ok = false
			a.Misses += issues - expTapeIssues[id]
		}
		tags := feats["replay/"+id]
		if oracle.Tier1(tags) {
			a.Tier1.Total++
			if ok {
				a.Tier1.Pass++
			}
		}
		for _, t := range tags {
			bump(a.ByTag, t, ok)
		}
		if ok {
			a.Pass++
			continue
		}
		a.Fail = append(a.Fail, id)
		rid, _, _ := strings.Cut(id, "#")
		if !failingT[rid] {
			failingT[rid] = true
			a.FirstDiv = append(a.FirstDiv, id)
		}
	}
	a.Transcripts = len(failingT)
	return a
}

type extItem struct {
	id, path, exp, alt string
}

func extItems() []extItem { return itemsUnder("corpus/ext/*", "") }

func itemsUnder(pattern, prefix string) []extItem {
	var items []extItem
	dirs, _ := filepath.Glob(pattern)
	sort.Strings(dirs)
	for _, d := range dirs {
		files, _ := filepath.Glob(filepath.Join(d, "*.js"))
		sort.Strings(files)
		for _, f := range files {
			base := strings.TrimSuffix(f, ".js")
			exp, err := os.ReadFile(base + ".out")
			if err != nil {
				continue
			}
			alt, _ := os.ReadFile(base + ".alt")
			abs, _ := filepath.Abs(f)
			items = append(items, extItem{id: prefix + filepath.Base(d) + "/" + filepath.Base(base), path: abs, exp: string(exp), alt: string(alt)})
		}
	}
	return items
}

func loadFeatures() map[string][]string {
	m := map[string][]string{}
	b, err := os.ReadFile("corpus/features.json")
	if err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

func runExt(bin string, items []extItem) []harness.WorkResp {
	reqs := make([]harness.WorkReq, len(items))
	for i, it := range items {
		reqs[i] = harness.WorkReq{ID: it.id, Kind: "ext", Path: it.path}
	}
	resps, _ := pool.Run(bin, 6, reqs, 20*time.Second)
	return resps
}

func bump(m map[string]*Count, k string, ok bool) {
	if m[k] == nil {
		m[k] = &Count{}
	}
	m[k].Total++
	if ok {
		m[k].Pass++
	}
}

func extScore(bin string) *ExtAgreement { return scoreItems(bin, extItems()) }

func scoreItems(bin string, items []extItem) *ExtAgreement {
	feats := loadFeatures()
	resps := runExt(bin, items)
	allow, _ := oracle.LoadAllowlist("corpus/allowlist.json")
	a := &ExtAgreement{BySource: map[string]*Count{}, ByTag: map[string]*Count{}}
	a.Total = len(items)
	for i, it := range items {
		act := resps[i].Canon
		if resps[i].Err != "" {
			act = "!! " + resps[i].Err
			a.Crashes++
		}
		if it.alt != "" {
			a.Wording++
		}
		ok, _ := oracle.Compare(it.exp, it.alt, act)
		if ok {
			a.Pass++
		} else {
			a.Fail = append(a.Fail, it.id)
			a.explain(allow, it.id, feats[it.id], it.exp, it.alt, act)
		}
		src, _, _ := strings.Cut(strings.TrimPrefix(it.id, "heldout/"), "/")
		bump(a.BySource, src, ok)
		tags := feats[it.id]
		if oracle.Tier1(tags) {
			a.Tier1.Total++
			if ok {
				a.Tier1.Pass++
			}
		}
		for _, t := range tags {
			bump(a.ByTag, t, ok)
		}
	}
	return a
}

func showDiff(bin, id string) error {
	if strings.HasPrefix(id, "replay/") {
		id = strings.TrimPrefix(id, "replay/")
		exp, alt, _, err := replayOracle()
		if err != nil {
			return err
		}
		q, _, _ := readRows("corpus/replay/expected.quickjs.jsonl")
		rid, _, _ := strings.Cut(id, "#")
		got, _ := runReplay(bin, []string{rid})
		t, err := harness.LoadTranscript(filepath.Join("corpus/replay/transcripts", rid+".json"))
		if err == nil {
			var n int
			fmt.Sscanf(strings.TrimPrefix(id, rid+"#"), "%d", &n)
			if n < len(t.Cells) {
				fmt.Printf("=== code\n%s\n", t.Cells[n])
			}
		}
		printDiff(exp[id], q[id], alt[id], cellOf(got, id))
		c := got[id]
		fmt.Printf("=== tape: misses %d, fallbacks %d, unused %d (expected issues %d)\n", c.Misses, c.Fallbacks, c.Unused, expTapeIssues[id])
		if c.TapeIssues() > expTapeIssues[id] {
			fmt.Println("=== cell fails on tape issues")
		}
		return nil
	}
	for _, it := range extItems() {
		if it.id != id {
			continue
		}
		code, _ := os.ReadFile(it.path)
		fmt.Printf("=== code (%s)\n%s\n", it.path, code)
		resps := runExt(bin, []extItem{it})
		act := resps[0].Canon
		if resps[0].Err != "" {
			act = "!! " + resps[0].Err
		}
		printDiff(it.exp, it.alt, it.alt, act)
		fmt.Printf("=== tags %v\n", loadFeatures()[id])
		return nil
	}
	return fmt.Errorf("no corpus item %q", id)
}

func printDiff(goja, quickjs, alt, act string) {
	fmt.Printf("=== expected (goja)\n%s", goja)
	if quickjs != "" && quickjs != goja {
		fmt.Printf("=== expected (quickjs)\n%s", quickjs)
	}
	fmt.Printf("=== actual\n%s", act)
	ok, d := oracle.Compare(goja, alt, act)
	if ok {
		fmt.Println("=== match")
		return
	}
	fmt.Println("=== differs:", d)
}
