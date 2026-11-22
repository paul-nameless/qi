package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/paul-nameless/qi/internal/jsvm/bench/pool"
)

type PerfResult struct {
	Name      string  `json:"name"`
	BestMs    int64   `json:"best_ms"`
	RunsMs    []int64 `json:"runs_ms"`
	Out       string  `json:"out,omitempty"`
	Err       string  `json:"err,omitempty"`
	RSSBefore float64 `json:"rss_before_mb"`
	RSSAfter  float64 `json:"rss_after_mb"`
	RSSDrop   float64 `json:"rss_after_drop_mb,omitempty"`
	BestUs    float64 `json:"best_us,omitempty"`
	Failed    int     `json:"failed_runs,omitempty"`
}

type MemResult struct {
	Runaway string  `json:"runaway"`
	Ms      int64   `json:"ms"`
	PeakMB  float64 `json:"rss_peak_mb"`
	After   string  `json:"after"`
	OK      bool    `json:"ok"`
}

type StartupResult struct {
	FirstVMus  float64 `json:"first_vm_us"`
	FirstRunus float64 `json:"first_run_us"`
	NextVMus   float64 `json:"next_vm_run_us"`
	IdleRSS    float64 `json:"idle_rss_mb"`
	RSSAfter20 float64 `json:"rss_after20_mb"`
}

type StateResult struct {
	Pass  int      `json:"pass"`
	Total int      `json:"total"`
	Fail  []string `json:"fail,omitempty"`
}

type ProbeResult struct {
	Pass  int      `json:"pass"`
	Total int      `json:"total"`
	Fail  []string `json:"fail,omitempty"`
}

type Score struct {
	Engine   string         `json:"engine"`
	When     string         `json:"when"`
	BinBytes int64          `json:"bin_bytes"`
	Error    string         `json:"error,omitempty"`
	Gates    []GateResult   `json:"gates"`
	Perf     []PerfResult   `json:"perf"`
	Mem      *MemResult     `json:"mem,omitempty"`
	Startup  *StartupResult `json:"startup,omitempty"`
	State    *StateResult   `json:"state,omitempty"`
	Probes   *ProbeResult   `json:"probes,omitempty"`
	Replay   *ExtAgreement  `json:"replay,omitempty"`
	Ext      *ExtAgreement  `json:"ext,omitempty"`
	WallSec  float64        `json:"wall_sec"`
}

var perfNames = []string{"loop1e7", "map-sort", "regex", "mcp8000"}

var slowNames = []string{"regex-1mb-nonascii", "str-index-nonascii", "dict-object", "dict-map", "queue-shift", "concat-200k", "objects-100k", "await-seq-2000", "await-all-2000", "fib27", "throw-100k", "template-join"}

func perfRun(bin, name string, pr *PerfResult) {
	if pr.Failed > 0 && strings.HasPrefix(pr.Err, "no result: killed") {
		return
	}
	want := perfWant(name)
	out := runJSON(bin, 90*time.Second, "perf", name)
	r, ok := out.get(name)
	switch {
	case !ok:
		pr.Failed++
		pr.Err = firstNonEmpty(pr.Err, "no result: "+out.Err)
		return
	case r.Err != "" || want != "" && r.Out != want:
		pr.Failed++
		pr.Err = firstNonEmpty(pr.Err, shortRec(r)+" (want "+want+")")
		return
	}
	pr.RunsMs = append(pr.RunsMs, r.Ms)
	if pr.BestMs < 0 || r.Vals["us"] < pr.BestUs {
		pr.BestMs, pr.Out, pr.BestUs = r.Ms, r.Out, r.Vals["us"]
		pr.RSSBefore, pr.RSSAfter, pr.RSSDrop = r.Vals["rss_before"], r.Vals["rss_after"], r.Vals["rss_after_drop"]
	}
}

func perf(bin string, names []string) []PerfResult {
	var res []PerfResult
	for _, name := range names {
		pr := PerfResult{Name: name, BestMs: -1}
		for i := 0; i < 3; i++ {
			perfRun(bin, name, &pr)
		}
		res = append(res, pr)
	}
	return res
}

func interleaved(engines, names []string) map[string][]PerfResult {
	res := map[string][]PerfResult{}
	for _, name := range names {
		prs := make([]PerfResult, len(engines))
		for i := range prs {
			prs[i] = PerfResult{Name: name, BestMs: -1}
		}
		for rep := 0; rep < 3; rep++ {
			for k := range engines {
				i := (k + rep) % len(engines)
				perfRun("bin/"+engines[i], name, &prs[i])
			}
		}
		for i, e := range engines {
			res[e] = append(res[e], prs[i])
		}
	}
	return res
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func mem(bin string) *MemResult {
	out := runJSON(bin, 2*time.Minute, "mem")
	m := &MemResult{}
	if r, ok := out.get("memory-runaway"); ok {
		m.Runaway, m.Ms = shortRec(r), r.Ms
		m.OK = strings.HasPrefix(r.Out, "caught RangeError")
	} else {
		m.Runaway = out.Err
	}
	if r, ok := out.get("rss-peak"); ok {
		m.PeakMB = r.Vals["mb"]
	}
	if r, ok := out.get("after-memory"); ok {
		m.After = shortRec(r)
	}
	m.OK = m.OK && m.PeakMB < 1024 && m.After == "2"
	return m
}

func startup(bin string) *StartupResult {
	out := runJSON(bin, time.Minute, "startup")
	r, ok := out.get("startup")
	if !ok {
		return nil
	}
	return &StartupResult{FirstVMus: r.Vals["firstVM_us"], FirstRunus: r.Vals["firstRun_us"], NextVMus: r.Vals["nextVM_us"], IdleRSS: r.Vals["idleRSS"], RSSAfter20: r.Vals["rssAfter"]}
}

func probes(bin string) *ProbeResult {
	out := runJSON(bin, 5*time.Minute, "feat", "corpus/probes.json")
	p := &ProbeResult{}
	if out.Err != "" {
		p.Fail = append(p.Fail, "RUN-FAILED: "+out.Err)
	}
	for _, r := range out.Recs {
		if r.Name == "features" {
			p.Pass, p.Total = int(r.Vals["pass"]), int(r.Vals["total"])
			continue
		}
		p.Fail = append(p.Fail, r.Name)
	}
	return p
}

func check(bin string) error {
	out := runJSON(bin, 30*time.Second, "run", "/dev/null", ".")
	if out.Err != "" {
		return fmt.Errorf("engine unavailable: %s", out.Err)
	}
	return nil
}

func main() {
	engine := flag.String("engine", "goja", "goja | modernc | jsvm")
	jsonOut := flag.String("json", "", "also write the result as JSON to this file")
	diff := flag.String("diff", "", "print expected vs actual for one item (model/0001, edge/0042, replay/<rid>#<n>)")
	skipPerf := flag.Bool("quick", false, "skip gates, perf, mem and startup")
	slow := flag.Bool("slow", false, "also run the slow-path perf gates")
	noBuild := flag.Bool("nobuild", false, "use the existing bin/<engine> instead of rebuilding it")
	compare := flag.String("compare", "", "comma-separated score JSON files to print side by side (last one is the candidate)")
	heldout := flag.Bool("heldout", false, "score only the held-out set in corpus/heldout")
	inter := flag.String("interleave", "", "comma-separated engines: run only perf, interleaved across engines, best of 3, and write <json>-<engine>.json")
	flag.Parse()
	if *inter != "" {
		names := perfNames
		if *slow {
			names = append(append([]string{}, perfNames...), slowNames...)
		}
		engines := strings.Split(*inter, ",")
		for e, prs := range interleaved(engines, names) {
			f := strings.TrimSuffix(*jsonOut, ".json") + "-" + e + ".json"
			b, _ := json.MarshalIndent(Score{Engine: e, When: time.Now().Format(time.RFC3339), Perf: prs}, "", " ")
			if err := os.WriteFile(f, append(b, '\n'), 0o644); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
		}
		var order []string
		for _, e := range engines {
			order = append(order, strings.TrimSuffix(*jsonOut, ".json")+"-"+e+".json")
		}
		if err := compareFiles(order); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if *compare != "" {
		if err := compareFiles(strings.Split(*compare, ",")); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	bin := "bin/" + *engine
	var err error
	if !*noBuild {
		bin, err = pool.Build(*engine)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *diff != "" {
		if err := showDiff(bin, *diff); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if *heldout {
		s := Score{Engine: *engine, When: time.Now().Format(time.RFC3339)}
		t := time.Now()
		s.Ext = scoreItems(bin, itemsUnder("corpus/heldout/*", "heldout/"))
		s.WallSec = time.Since(t).Seconds()
		fmt.Print(render(s))
		writeScore(*jsonOut, s)
		return
	}
	names := perfNames
	if *slow {
		names = append(append([]string{}, perfNames...), slowNames...)
	}
	s := score(*engine, bin, *skipPerf, names)
	fmt.Print(render(s))
	writeScore(*jsonOut, s)
}

func writeScore(path string, s Score) {
	if path == "" {
		return
	}
	b, _ := json.MarshalIndent(s, "", " ")
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func score(engine, bin string, quick bool, names []string) Score {
	t := time.Now()
	s := Score{Engine: engine, When: t.Format(time.RFC3339)}
	if st, err := os.Stat(bin); err == nil {
		s.BinBytes = st.Size()
	}
	if err := check(bin); err != nil {
		s.Error = err.Error()
		s.WallSec = time.Since(t).Seconds()
		return s
	}
	if !quick {
		s.Gates = judgeGates(runJSON(bin, 3*time.Minute, "gates"))
		s.Perf = perf(bin, names)
		s.Mem = mem(bin)
		s.Startup = startup(bin)
	}
	s.State = stateTest(bin)
	s.Probes = probes(bin)
	s.Replay = replayScore(bin)
	s.Ext = extScore(bin)
	s.WallSec = time.Since(t).Seconds()
	return s
}
