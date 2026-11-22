package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

type sample struct {
	Seed     uint64 `json:"seed"`
	Label    string `json:"label,omitempty"`
	Source   string `json:"source"`
	Expected string `json:"expected"`
	Goja     string `json:"goja,omitempty"`
	Actual   string `json:"actual"`
}

type group struct {
	Kind      string     `json:"kind"`
	Signature string     `json:"signature"`
	Count     int        `json:"count"`
	Seeds     []uint64   `json:"seeds"`
	Samples   []sample   `json:"samples"`
	Minimized []minRepro `json:"minimized,omitempty"`
}

type minRepro struct {
	Shape    string `json:"shape"`
	Source   string `json:"source"`
	Expected string `json:"expected"`
	Goja     string `json:"goja,omitempty"`
	Actual   string `json:"actual"`
	Tests    int    `json:"tests"`
}

type stats struct {
	Programs    int            `json:"programs"`
	Verdicts    map[string]int `json:"verdicts"`
	OracleSplit map[string]int `json:"oracle_split"`
	Seconds     float64        `json:"seconds"`
}

var verdictNames = map[verdict]string{vPass: "pass", vOracleBad: "oracle_failed", vOracleSplit: "oracle_split", vMismatch: "mismatch", vCrash: "crash", vDeviation: "deviation"}

type collector struct {
	mu     sync.Mutex
	st     stats
	groups map[string]*group
	splits map[string]sample
}

func (c *collector) add(seed uint64, src string, t triple) {
	v, ds := analyze(t)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.st.Programs++
	c.st.Verdicts[verdictNames[v]]++
	switch v {
	case vOracleSplit:
		sig := diffSignature(t.qjs.canon, t.goja.canon)
		c.st.OracleSplit[sig]++
		if _, ok := c.splits[sig]; !ok {
			c.splits[sig] = sample{Seed: seed, Source: src, Expected: t.qjs.canon, Actual: t.goja.canon}
		}
	case vCrash:
		c.record("crash", crashSignature(t.jsvm), "", seed, src, t)
	case vMismatch:
		seen := map[string]bool{}
		for _, d := range ds {
			if !seen[d.sig] {
				seen[d.sig] = true
				c.record("mismatch", d.sig, d.label, seed, src, t)
			}
		}
	}
}

func (c *collector) record(kind, sig, label string, seed uint64, src string, t triple) {
	key := kind + "\x00" + sig
	g := c.groups[key]
	if g == nil {
		g = &group{Kind: kind, Signature: sig}
		c.groups[key] = g
	}
	g.Count++
	if len(g.Seeds) < 20 {
		g.Seeds = append(g.Seeds, seed)
	}
	if len(g.Samples) < 3 {
		act := t.jsvm.canon
		if t.jsvm.fail != "" {
			act = t.jsvm.fail
		}
		g.Samples = append(g.Samples, sample{Seed: seed, Label: label, Source: src, Expected: t.qjs.canon, Goja: t.goja.canon, Actual: act})
	}
}

func crashSignature(o outcome) string {
	s := o.fail
	if s == "" {
		s = o.canon
	}
	if i := strings.Index(s, "jsvm internal error: "); i >= 0 {
		s = s[i:]
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if strings.HasPrefix(s, "hang:") {
		return "hang"
	}
	return norm(s)
}

func buildBins(out string) (map[string]string, error) {
	bins := map[string]string{}
	for _, e := range []string{"goja", "modernc", "jsvm"} {
		path, err := filepath.Abs(filepath.Join(out, "bin", e))
		if err != nil {
			return nil, err
		}
		cmd := exec.Command("go", "build", "-o", path, "./eng/"+e)
		if b, err := cmd.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("build %s: %w\n%s", e, err, b)
		}
		bins[e] = path
	}
	return bins, nil
}

func programSeed(seed uint64, i int) uint64 {
	return seed*1_000_003 + uint64(i)
}

func generate(seed uint64) program {
	return newGen(seed).program()
}

func main() {
	n := flag.Int("n", 1000, "programs to run")
	seed := flag.Uint64("seed", 1, "base seed")
	out := flag.String("out", "fuzzout", "output directory")
	workers := flag.Int("workers", max(runtime.NumCPU()/3, 1), "worker processes per engine")
	minPer := flag.Int("min-per-group", 2, "programs to minimise per mismatch group")
	maxGroups := flag.Int("max-groups", 150, "groups to minimise")
	budget := flag.Int("budget", 500, "engine runs per minimisation")
	show := flag.Uint64("show", 0, "print the program for this seed and exit")
	file := flag.String("file", "", "run this program file on all engines and print the outputs")
	avoidList := flag.String("avoid", "", "comma-separated known bugs to steer the generator around: tz,ownnames,hypot,splitsticky,values")
	flag.Parse()
	for _, a := range strings.Split(*avoidList, ",") {
		if a != "" {
			avoid[a] = true
		}
	}
	if *show != 0 {
		fmt.Println(generate(*show).source())
		return
	}
	if *file != "" {
		if err := runOne(*file, *out); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if err := run(*n, *seed, *out, *workers, *minPer, *maxGroups, *budget); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(n int, seed uint64, out string, workers, minPer, maxGroups, budget int) error {
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	bins, err := buildBins(out)
	if err != nil {
		return err
	}
	r := &runner{
		goja: newEngine("goja", bins["goja"], workers, 10*time.Second),
		qjs:  newEngine("quickjs", bins["modernc"], workers, 10*time.Second),
		jsvm: newEngine("jsvm", bins["jsvm"], workers, 10*time.Second),
	}
	defer r.goja.close()
	defer r.qjs.close()
	defer r.jsvm.close()
	c := &collector{st: stats{Verdicts: map[string]int{}, OracleSplit: map[string]int{}}, groups: map[string]*group{}, splits: map[string]sample{}}
	start := time.Now()
	fuzz(r, c, n, seed, workers*3)
	c.st.Seconds = time.Since(start).Seconds()
	fmt.Fprintf(os.Stderr, "ran %d programs in %.0fs: %v\n", c.st.Programs, c.st.Seconds, c.st.Verdicts)
	minimizeGroups(r, c, minPer, maxGroups, budget)
	return writeReport(out, c)
}

func fuzz(r *runner, c *collector, n int, seed uint64, par int) {
	idx := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < par; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range idx {
				s := programSeed(seed, i)
				src := generate(s).source()
				c.add(s, src, r.run(src))
			}
		}()
	}
	last := time.Now()
	for i := 0; i < n; i++ {
		idx <- i
		if time.Since(last) > 30*time.Second {
			last = time.Now()
			c.mu.Lock()
			fmt.Fprintf(os.Stderr, "%d/%d %v\n", c.st.Programs, n, c.st.Verdicts)
			c.mu.Unlock()
		}
	}
	close(idx)
	wg.Wait()
}

func sortedGroups(c *collector) []*group {
	var gs []*group
	for _, g := range c.groups {
		gs = append(gs, g)
	}
	sort.Slice(gs, func(i, j int) bool {
		if gs[i].Kind != gs[j].Kind {
			return gs[i].Kind < gs[j].Kind
		}
		return gs[i].Count > gs[j].Count
	})
	return gs
}

func minimizeGroups(r *runner, c *collector, minPer, maxGroups, budget int) {
	gs := sortedGroups(c)
	for gi, g := range gs {
		if gi >= maxGroups {
			break
		}
		want := vMismatch
		b := budget
		if g.Kind == "crash" {
			want = vCrash
			if g.Signature == "hang" {
				b = 40
			}
		}
		for k := 0; k < len(g.Samples) && k < minPer; k++ {
			m := &minimizer{r: r, want: want, label: g.Samples[k].Label, budget: b}
			p := m.minimize(generate(g.Samples[k].Seed))
			src := p.source()
			t := r.run(src)
			act := t.jsvm.canon
			if t.jsvm.fail != "" {
				act = t.jsvm.fail
			}
			g.Minimized = append(g.Minimized, minRepro{Shape: shape(p), Source: src, Expected: t.qjs.canon, Goja: t.goja.canon, Actual: act, Tests: m.tests})
		}
		fmt.Fprintf(os.Stderr, "minimised group %d/%d (%s, %d hits)\n", gi+1, min(len(gs), maxGroups), g.Kind, g.Count)
	}
}

type report struct {
	Stats  stats    `json:"stats"`
	Groups []*group `json:"groups"`
	Splits []split  `json:"oracle_splits"`
}

type split struct {
	Signature string `json:"signature"`
	Count     int    `json:"count"`
	Sample    sample `json:"sample"`
}

func writeReport(out string, c *collector) error {
	rep := report{Stats: c.st, Groups: sortedGroups(c)}
	for sig, n := range c.st.OracleSplit {
		rep.Splits = append(rep.Splits, split{Signature: sig, Count: n, Sample: c.splits[sig]})
	}
	sort.Slice(rep.Splits, func(i, j int) bool { return rep.Splits[i].Count > rep.Splits[j].Count })
	rep.Stats.OracleSplit = nil
	b, err := json.MarshalIndent(rep, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(out, "report.json"), b, 0o644)
}

func runOne(file, out string) error {
	src, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	bins, err := buildBins(out)
	if err != nil {
		return err
	}
	for _, e := range []string{"modernc", "goja", "jsvm"} {
		eng := newEngine(e, bins[e], 1, 10*time.Second)
		o := eng.run(string(src))
		eng.close()
		fmt.Printf("== %s %s\n%s\n", e, o.fail, o.canon)
	}
	return nil
}
