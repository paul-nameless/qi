package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"time"
)

type longCell struct {
	kind    string
	code    string
	timeout time.Duration
	player  *tapePlayer
	cell    int
}

func extScripts(dir string) []string {
	files, _ := filepath.Glob(filepath.Join(dir, "*.js"))
	sort.Strings(files)
	var out []string
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err == nil && !strings.Contains(string(b), CellSep) {
			out = append(out, string(b))
		}
	}
	return out
}

func specialCell(slot int) (longCell, bool) {
	switch {
	case slot%100 == 50:
		return longCell{kind: "big-result", code: "const big = await mcp('linear', 'list_issues', {n: 6500});\nbig.nodes.filter(i => i.state.name === 'Todo').length", timeout: 20 * time.Second}, true
	case slot%25 == 3:
		return longCell{kind: "busy", code: "let spin = 0;\nwhile (true) { spin++ }", timeout: 100 * time.Millisecond}, true
	case slot%25 == 11:
		return longCell{kind: "never", code: "console.log('waiting');\nawait new Promise(() => {})", timeout: 2 * time.Second}, true
	case slot%25 == 19:
		return longCell{kind: "throw", code: "const boom = {a: 1};\nboom.missing.deeper", timeout: 2 * time.Second}, true
	}
	return longCell{}, false
}

func longPlan(replayDir, extDir string, n int) []longCell {
	ext := extScripts(extDir)
	var plan []longCell
	add := func(c longCell) bool {
		if len(plan) >= n {
			return false
		}
		if sc, ok := specialCell(len(plan)); ok {
			plan = append(plan, sc)
			if len(plan) >= n {
				return false
			}
		}
		plan = append(plan, c)
		return len(plan) < n
	}
	ei := 0
	for _, f := range TranscriptFiles(replayDir, nil) {
		t, err := LoadTranscript(f)
		if err != nil {
			continue
		}
		p := newTapePlayer(t)
		for i, code := range t.Cells {
			if !add(longCell{kind: "replay", code: code, timeout: 20 * time.Second, player: p, cell: i}) {
				return plan
			}
		}
		for k := 0; k < 2 && ei < len(ext); k++ {
			if !add(longCell{kind: "ext", code: ext[ei], timeout: 5 * time.Second}) {
				return plan
			}
			ei++
		}
	}
	return plan
}

func longResolver(c longCell) Resolver {
	if c.player != nil {
		c.player.cell, c.player.misses, c.player.fallbacks = c.cell, 0, 0
		return c.player.answer
	}
	return func(name, args string) Answer {
		if name != "mcp" {
			return virtualTools(name, args)
		}
		var a []json.RawMessage
		_ = json.Unmarshal([]byte(args), &a)
		v, err := fakeTools(name, a)
		if err != nil {
			return Answer{Val: err.Error()}
		}
		j, _ := json.Marshal(v)
		return Answer{OK: true, Val: string(j)}
	}
}

func checkpoint(label string, cells int, counts map[string]int) {
	runtime.GC()
	debug.FreeOSMemory()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	vals := map[string]float64{"cells": float64(cells), "rss_mb": currentRSS(), "heap_inuse_mb": float64(ms.HeapInuse) / (1 << 20), "goroutines": float64(runtime.NumGoroutine()), "peak_rss_mb": rssMB()}
	for k, v := range counts {
		vals["n_"+k] = float64(v)
	}
	record(Rec{Name: label, Vals: vals})
	fmt.Printf("%-12s cells=%3d rss=%6.1fMB heap=%6.1fMB goroutines=%4d\n", label, cells, vals["rss_mb"], vals["heap_inuse_mb"], runtime.NumGoroutine())
}

func longSession(replayDir, extDir string, n int) {
	plan := longPlan(replayDir, extDir, n)
	s := fresh()
	s.Mode = "repl"
	counts := map[string]int{}
	checkpoint("start", 0, counts)
	t := time.Now()
	for i, c := range plan {
		s.Det = longResolver(c)
		_ = runCell(s, c.code, c.timeout)
		counts[c.kind]++
		if (i+1)%100 == 0 {
			checkpoint(fmt.Sprintf("cell-%d", i+1), i+1, counts)
		}
	}
	record(Rec{Name: "wall", Ms: time.Since(t).Milliseconds()})
	checkpoint("end", len(plan), counts)
	s.VM().Close()
	s = nil
	checkpoint("closed", len(plan), counts)
}
