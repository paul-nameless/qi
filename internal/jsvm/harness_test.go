package jsvm

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

const testPrelude = `
globalThis.__p = new Map(); globalThis.__n = 0;
globalThis.__fmt = v => v === undefined ? "" : typeof v === "string" ? v : JSON.stringify(v, null, 1);
globalThis.__err = e => (e && typeof e === "object" && "stack" in e) ? (e.name || "Error") + ": " + e.message + "\n" + e.stack : "Thrown: " + String(e);
globalThis.__settle = (id, ok, v) => { const e = __p.get(id); if (!e) return; __p.delete(id); if (ok === "1") e[0](JSON.parse(v)); else { const err = new Error(v); err.name = "ToolError"; e[1](err); } };
globalThis.__abort = (msg) => { for (const [id, e] of __p) { const err = new Error(msg); err.name = "ToolError"; e[1](err); } __p.clear(); };
for (const name of ["sleep", "read", "bash", "mcp", "fail", "edit", "write"]) globalThis[name] = function __tool(...a) { const at = (new Error("x").stack || "").split("\n").filter(l => /^\s+at /.test(l) && !l.includes("__tool")).join("\n"); return new Promise(function __tool(res, rej) { const id = String(++__n); __p.set(id, [res, e => { e.stack = at; rej(e); }]); __host("call", id, name, JSON.stringify(a)); }); };
globalThis.setTimeout = (f, ms, ...a) => { sleep(ms || 0).then(() => f(...a)); return 0; }; globalThis.clearTimeout = () => {};
globalThis.queueMicrotask = f => { Promise.resolve().then(f); };
globalThis.structuredClone = v => JSON.parse(JSON.stringify(v));
globalThis.console = {}; for (const k of ["log", "error", "warn", "info", "debug"]) console[k] = (...a) => __host("out", a.map(x => typeof x === "string" ? x : __fmt(x)).join(" "), "", "");
globalThis.print = console.log;
`

type hresult struct {
	id, ok, val string
	epoch       int
}

var hangGuard = 60 * time.Second * time.Duration(1+4*raceFactor)

func limitAbove(n int64) int64 {
	forceGC()
	var probe VM
	return int64(probe.heapBytes()) + n
}

type session struct {
	vm      *VM
	out     strings.Builder
	results chan hresult
	pending int
	maxPend int
	calls   int
	vnow    int
	seq     int
	sleeps  map[string][2]int
	held    map[string]hresult
	epoch   int
	tools   func(name string, args []json.RawMessage) (any, error)
}

func newSession(t testing.TB) *session {
	s := &session{vm: New(Options{MemoryLimit: 512 << 20}), results: make(chan hresult, 1024), tools: fakeTool, sleeps: map[string][2]int{}, held: map[string]hresult{}}
	s.vm.SetFunc("__host", func(vm *VM, args []Value) (Value, error) {
		str := func(i int) string {
			if i < len(args) {
				return ToString(args[i])
			}
			return ""
		}
		switch str(0) {
		case "out":
			s.out.WriteString(str(1) + "\n")
		case "call":
			s.pending++
			s.maxPend = max(s.maxPend, s.pending)
			s.calls++
			id, name, raw := str(1), str(2), str(3)
			if name == "sleep" {
				var a []int
				_ = json.Unmarshal([]byte(raw), &a)
				ms := 0
				if len(a) > 0 {
					ms = a[0]
				}
				s.seq++
				s.sleeps[id] = [2]int{s.vnow + ms, s.seq}
			}
			ep := s.epoch
			go func() {
				var a []json.RawMessage
				_ = json.Unmarshal([]byte(raw), &a)
				v, err := s.tools(name, a)
				if err != nil {
					s.results <- hresult{id, "0", err.Error(), ep}
					return
				}
				j, _ := json.Marshal(v)
				s.results <- hresult{id, "1", string(j), ep}
			}()
		}
		return Undefined(), nil
	})
	if _, err := s.vm.RunCell("prelude.js", testPrelude); err != nil {
		t.Fatalf("prelude: %v", err)
	}
	return s
}

func fakeTool(name string, a []json.RawMessage) (any, error) {
	str := func(i int) string {
		if i >= len(a) {
			return ""
		}
		var s string
		if json.Unmarshal(a[i], &s) != nil {
			return string(a[i])
		}
		return s
	}
	switch name {
	case "sleep":
		var ms int
		if len(a) > 0 {
			_ = json.Unmarshal(a[0], &ms)
		}
		time.Sleep(time.Duration(ms) * time.Millisecond)
		return ms, nil
	case "fail":
		return nil, errors.New("boom: " + str(0))
	case "mcp":
		n := 300
		if len(a) > 2 {
			var m map[string]any
			if json.Unmarshal(a[2], &m) == nil {
				if f, ok := m["n"].(float64); ok {
					n = int(f)
				}
			}
		}
		return bigIssues(n), nil
	case "bash":
		return map[string]any{"output": "ok\n", "exit_code": 0}, nil
	case "read":
		return "line1\nline2\n", nil
	}
	return nil, fmt.Errorf("unknown tool %q", name)
}

func bigIssues(n int) any {
	states := []string{"Todo", "In Progress", "Done", "Backlog", "Canceled"}
	names := []string{"ana", "bo", "cy", "ana"}
	nodes := make([]map[string]any, n)
	for i := range nodes {
		nodes[i] = map[string]any{
			"id": fmt.Sprintf("uuid-%06d", i), "identifier": fmt.Sprintf("SER-%d", 1000+i), "title": fmt.Sprintf("Issue number %d about %s", i, []string{"auth", "billing", "search", "ui"}[i%4]),
			"description": strings.Repeat("Lorem ipsum dolor sit amet, consectetur adipiscing elit. ", 8+i%5),
			"state":       map[string]any{"name": states[i%5], "type": "x"}, "priority": (i / 5) % 5, "estimate": i % 8,
			"assignee": map[string]any{"name": names[i%4]}, "labels": []string{"a", "b"}[:i%3%2+1],
		}
	}
	return map[string]any{"nodes": nodes, "pageInfo": map[string]any{"hasNextPage": false}}
}

func (s *session) nextSleep() (hresult, bool) {
	best := ""
	for id, d := range s.sleeps {
		if b, ok := s.sleeps[best]; !ok || d[0] < b[0] || (d[0] == b[0] && d[1] < b[1]) {
			best = id
		}
	}
	r, ok := s.held[best]
	if !ok {
		return r, false
	}
	s.vnow = s.sleeps[best][0]
	delete(s.sleeps, best)
	delete(s.held, best)
	return r, true
}

func (s *session) settle(r hresult) error {
	s.pending--
	_, err := s.vm.Call("__settle", Str(r.id), Str(r.ok), Str(r.val))
	return err
}

func (s *session) run(code string, timeout time.Duration) (string, error) {
	s.out.Reset()
	s.epoch++
	s.pending = 0
	s.vnow = 0
	s.sleeps = map[string][2]int{}
	s.held = map[string]hresult{}
	deadline := time.Now().Add(timeout)
	stop := s.vm.InterruptAfter(timeout)
	defer func() {
		stop()
		s.vm.ClearInterrupt()
	}()
	res, err := s.vm.RunCell("cell.js", code)
	if err != nil {
		return s.out.String(), err
	}
	for {
		if err := s.vm.RunJobs(); err != nil {
			return s.out.String(), err
		}
		st, v := s.vm.PromiseResult(res)
		switch st {
		case PromiseFulfilled:
			return s.out.String() + s.format(v), nil
		case PromiseRejected:
			return s.out.String(), s.vm.Exception(v)
		}
		if s.pending == 0 {
			return s.out.String(), errors.New("the script is waiting on a promise that can never settle")
		}
		if r, ok := s.nextSleep(); ok {
			if err := s.settle(r); err != nil {
				return s.out.String(), err
			}
			continue
		}
		select {
		case r := <-s.results:
			if r.epoch != s.epoch {
				continue
			}
			if _, ok := s.sleeps[r.id]; ok {
				s.held[r.id] = r
				continue
			}
			if err := s.settle(r); err != nil {
				return s.out.String(), err
			}
		case <-time.After(time.Until(deadline)):
			stop()
			s.vm.ClearInterrupt()
			_, _ = s.vm.Call("__abort", Str("cancelled"))
			_ = s.vm.RunJobs()
			return s.out.String(), errors.New("timed out")
		}
	}
}

func (s *session) format(v Value) string {
	r, err := s.vm.Call("__fmt", v)
	if err != nil {
		return "<fmt error " + err.Error() + ">"
	}
	return ToString(r)
}

func TestScratch(t *testing.T) {
	file := os.Getenv("JSFILE")
	if file == "" {
		t.Skip("JSFILE not set")
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	s := newSession(t)
	for i, cell := range strings.Split(string(b), "\n//----\n") {
		st := time.Now()
		out, err := s.run(cell, 3*time.Second)
		fmt.Printf("[%d] %v out=%q err=%v\n", i, time.Since(st).Round(time.Millisecond), out, err)
	}
}

func TestProbes(t *testing.T) {
	file := os.Getenv("PROBES")
	if file == "" {
		t.Skip("PROBES not set")
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var ps []struct{ Name, Code string }
	if err := json.Unmarshal(b, &ps); err != nil {
		t.Fatal(err)
	}
	pass := 0
	for _, p := range ps {
		s := newSession(t)
		out, err := s.run(p.Code, 3*time.Second)
		s.vm.Close()
		if err == nil && strings.TrimSpace(out) == "true" {
			pass++
			continue
		}
		msg := out
		if err != nil {
			msg = strings.SplitN(err.Error(), "\n", 2)[0]
		}
		fmt.Printf("FAIL %-28s %.110s\n", p.Name, msg)
	}
	fmt.Printf("features: %d/%d\n", pass, len(ps))
}

func TestCorpusParse(t *testing.T) {
	file := os.Getenv("CORPUS")
	if file == "" {
		t.Skip("CORPUS not set")
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	vm := New(Options{})
	n, bad := 0, 0
	for _, line := range strings.Split(string(b), "\n") {
		if line == "" {
			continue
		}
		var c struct{ Code string }
		if json.Unmarshal([]byte(line), &c) != nil {
			continue
		}
		n++
		if _, err := vm.compile("cell.js", c.Code); err != nil {
			bad++
			fmt.Printf("%v | %.80q\n", strings.SplitN(err.Error(), "\n", 2)[0], c.Code)
		}
	}
	fmt.Printf("parsed %d/%d\n", n-bad, n)
}
