package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"time"
)

var (
	NewVM      func() (VM, error)
	engineName string
	streamJSON bool
)

type Rec struct {
	Name string             `json:"name"`
	Ms   int64              `json:"ms,omitempty"`
	Out  string             `json:"out,omitempty"`
	Err  string             `json:"err,omitempty"`
	Vals map[string]float64 `json:"vals,omitempty"`
}

func record(r Rec) {
	if !streamJSON {
		return
	}
	j, _ := json.Marshal(r)
	fmt.Println("@@" + string(j))
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return fixLines(err.Error(), showOff)
}

type perfItem struct {
	code    string
	timeout time.Duration
	drop    []string
}

const mbSubject = "const t = 'x\u2014' + 'lorem ipsum dolor '.repeat(58254);"

var slowItems = map[string]perfItem{
	"regex-1mb-nonascii": {mbSubject + " return t.match(/\\b\\w+\\b/g).length + ' ' + t.replace(/o(?=r)/g, '0').length + ' ' + t.split(' ').length", 60 * time.Second, nil},
	"str-index-nonascii": {mbSubject + " let x = 1, acc = 0; for (let k = 0; k < 100000; k++) { x = (x * 1103515245 + 12345) % 2147483648; const i = x % (t.length - 8); acc += t.charCodeAt(i) + t[i].length + t.slice(i, i + 5).length + (t.indexOf('dolor', i) - i) } return acc", 60 * time.Second, nil},
	"dict-object":        {"globalThis.counts = {}; for (let i = 0; i < 200000; i++) { const k = 'key' + i; counts[k] = (counts[k] || 0) + 1 } return Object.keys(counts).length", 60 * time.Second, []string{"globalThis.counts = null; return 0", "return 1"}},
	"dict-map":           {"globalThis.m = new Map(); for (let i = 0; i < 200000; i++) { const k = 'key' + i; m.set(k, (m.get(k) || 0) + 1) } return m.size", 60 * time.Second, []string{"globalThis.m = null; return 0", "return 1"}},
	"queue-shift":        {"const q = []; for (let i = 0; i < 200000; i++) q.push(i); let s = 0; while (q.length) s += q.shift(); return s", 120 * time.Second, nil},
	"concat-200k":        {"let s = ''; for (let i = 0; i < 200000; i++) s += 'w' + (i % 100) + ' '; return s.match(/w\\d+/g).length + ' ' + s.split(' ').length", 60 * time.Second, nil},
	"objects-100k":       {"const a = Array.from({length: 100000}, (_, i) => ({g: i % 37, h: (i * 7919) % 1000, id: i})); a.sort((x, y) => x.g - y.g || x.h - y.h); const groups = a.reduce((m, o) => { (m[o.g] ||= []).push(o.id); return m }, {}); const back = JSON.parse(JSON.stringify(a)); return a[0].id + ' ' + a[99999].id + ' ' + Object.keys(groups).length + ' ' + back.length", 60 * time.Second, nil},
	"await-seq-2000":     {"let n = 0; for (let i = 0; i < 2000; i++) n += await sleep(0); return n + ' ok'", 60 * time.Second, nil},
	"await-all-2000":     {"const r = await Promise.all(Array.from({length: 2000}, () => sleep(0))); return r.length", 60 * time.Second, nil},
	"fib27":              {"function fib(n) { return n < 2 ? n : fib(n - 1) + fib(n - 2) } return fib(27)", 60 * time.Second, nil},
	"throw-100k":         {"let c = 0; for (let i = 0; i < 100000; i++) { try { throw new Error('x' + i) } catch (e) { c += e.message.length } } return c", 60 * time.Second, nil},
	"template-join":      {"const rows = []; for (let i = 0; i < 100000; i++) rows.push(`row ${i}: ${i % 7} | ${(i * 1.5).toFixed(1)}`); const s = rows.join('\\n'); return s.length + ' ' + rows.slice(0, 3).join(' / ')", 60 * time.Second, nil},
}

func init() {
	for k, v := range slowItems {
		perfItems[k] = v
	}
}

func currentRSS() float64 {
	out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(os.Getpid())).Output()
	if err != nil {
		return -1
	}
	kb, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		return -1
	}
	return kb / 1024
}

var perfItems = map[string]perfItem{
	"loop1e7":  {"let s = 0; for (let i = 0; i < 1e7; i++) s += i % 7; return s", 30 * time.Second, nil},
	"map-sort": {"const words = []; for (let i = 0; i < 200000; i++) words.push('w' + (i % 5000)); const m = new Map(); for (const w of words) m.set(w, (m.get(w) || 0) + 1); return [...m.entries()].sort((a, b) => b[1] - a[1]).slice(0, 2).join()", 30 * time.Second, nil},
	"regex":    {"const t = 'lorem ipsum dolor '.repeat(50000); return t.match(/\\b\\w+\\b/g).length + ' ' + t.replace(/o(?=r)/g, '0').length + ' ' + t.split(' ').length", 30 * time.Second, nil},
	"mcp8000":  {"const r = await mcp('linear', 'list_issues', {n: 8000}); return JSON.stringify(r).length + ' ' + r.nodes.filter(i => i.state.name === 'Todo').length", 60 * time.Second, nil},
}

func perf(name string) {
	it, ok := perfItems[name]
	if !ok {
		fmt.Fprintln(os.Stderr, "unknown perf item", name)
		os.Exit(2)
	}
	s := fresh()
	before := rssMB()
	o, e, d := one(s, it.code, it.timeout)
	vals := map[string]float64{"rss_before": before, "rss_after": rssMB(), "us": float64(d.Microseconds())}
	if len(it.drop) > 0 {
		vals["rss_peak_now"] = currentRSS()
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		vals["heap_peak"] = float64(ms.HeapInuse) / (1 << 20)
		for _, c := range it.drop {
			_, _, _ = one(s, c, it.timeout)
		}
		runtime.GC()
		debug.FreeOSMemory()
		vals["rss_after_drop"] = currentRSS()
		runtime.ReadMemStats(&ms)
		vals["heap_after_drop"] = float64(ms.HeapInuse) / (1 << 20)
	}
	record(Rec{Name: name, Ms: d.Milliseconds(), Out: o, Err: errText(e), Vals: vals})
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: bin [-json] gates|state|mem|startup|feat FILE|run FILE DIR [MODE]|serve DIR|perf NAME|record TRANSCRIPT DIR OUT|replay DIR [RID...]|corpus [-strict] FILE...|worker")
	os.Exit(2)
}

func Main(engine string, newVM func() (VM, error)) {
	NewVM, engineName = newVM, engine
	args := os.Args[1:]
	streamJSON = len(args) > 0 && args[0] == "-json"
	if streamJSON {
		args = args[1:]
	}
	if len(args) == 0 {
		usage()
	}
	fmt.Fprintln(os.Stderr, "engine:", engineName)
	dispatch(args)
}

func need(args []string, n int) {
	if len(args) < n {
		usage()
	}
}

func dispatch(args []string) {
	switch args[0] {
	case "gates":
		gates()
	case "state":
		stateTests()
	case "serve":
		need(args, 2)
		serve(args[1])
	case "mem":
		memtest()
	case "startup":
		startup()
	case "feat":
		need(args, 2)
		feat(args[1])
	case "run":
		need(args, 3)
		mode := "iife"
		if len(args) > 3 {
			mode = args[3]
		}
		runFile(args[1], args[2], mode)
	case "perf":
		need(args, 2)
		perf(args[1])
	case "record":
		need(args, 4)
		recordTape(args[1], args[2], args[3])
	case "replay":
		need(args, 2)
		replay(args[1], args[2:])
	case "corpus":
		corpusCmd(args[1:])
	case "worker":
		worker()
	case "longsession":
		need(args, 3)
		n := 500
		if len(args) > 3 {
			n, _ = strconv.Atoi(args[3])
		}
		longSession(args[1], args[2], n)
	default:
		usage()
	}
}
