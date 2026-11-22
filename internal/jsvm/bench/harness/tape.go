package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type TapeEntry struct {
	Cell int    `json:"c"`
	Name string `json:"n"`
	Args string `json:"a"`
	OK   bool   `json:"ok"`
	Val  string `json:"v,omitempty"`
	Big  int    `json:"big,omitempty"`
}

type Transcript struct {
	RID    string      `json:"rid"`
	Task   string      `json:"task"`
	Cells  []string    `json:"cells"`
	Tape   []TapeEntry `json:"tape"`
	Record []string    `json:"record,omitempty"`
}

type tapePlayer struct {
	entries   []TapeEntry
	used      []bool
	cell      int
	misses    int
	fallbacks int
}

func (p *tapePlayer) unused(cell int) int {
	n := 0
	for i, e := range p.entries {
		if e.Cell == cell && !p.used[i] {
			n++
		}
	}
	return n
}

func newTapePlayer(t *Transcript) *tapePlayer {
	return &tapePlayer{entries: t.Tape, used: make([]bool, len(t.Tape))}
}

func sleepDelay(name, args string) int {
	if name != "sleep" {
		return 0
	}
	var a []json.RawMessage
	_ = json.Unmarshal([]byte(args), &a)
	return num(a, 0)
}

func (p *tapePlayer) take(i int) Answer {
	p.used[i] = true
	e := p.entries[i]
	val := e.Val
	if e.Big > 0 {
		j, _ := json.Marshal(bigIssues(e.Big))
		val = string(j)
	}
	return Answer{OK: e.OK, Val: val, Delay: sleepDelay(e.Name, e.Args)}
}

func (p *tapePlayer) answer(name, args string) Answer {
	for i, e := range p.entries {
		if !p.used[i] && e.Cell == p.cell && e.Name == name && e.Args == args {
			return p.take(i)
		}
	}
	for i, e := range p.entries {
		if !p.used[i] && e.Cell == p.cell && e.Name == name {
			p.fallbacks++
			return p.take(i)
		}
	}
	p.misses++
	return Answer{OK: false, Val: "tape miss: " + name + " " + cut(args, 200), Delay: sleepDelay(name, args)}
}

type spikeTranscript struct {
	Metrics struct {
		ID   string `json:"id"`
		Task string `json:"task"`
	} `json:"metrics"`
	Cells []struct {
		Code *string `json:"code"`
	} `json:"cells"`
}

func loadSpike(path string) (*Transcript, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var st spikeTranscript
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	t := &Transcript{RID: st.Metrics.ID, Task: st.Metrics.Task}
	for _, c := range st.Cells {
		if c.Code != nil {
			t.Cells = append(t.Cells, *c.Code)
		}
	}
	return t, nil
}

func realAnswer(name, args string) (Answer, int) {
	var a []json.RawMessage
	_ = json.Unmarshal([]byte(args), &a)
	if name == "sleep" {
		return Answer{OK: true, Val: fmt.Sprint(num(a, 0)), Delay: num(a, 0)}, 0
	}
	v, err := fakeTools(name, a)
	if err != nil {
		return Answer{OK: false, Val: err.Error()}, 0
	}
	j, _ := json.Marshal(v)
	big := 0
	if name == "mcp" {
		if m, ok := v.(map[string]any); ok {
			if nodes, ok := m["nodes"].([]map[string]any); ok {
				big = len(nodes)
			}
		}
	}
	return Answer{OK: true, Val: string(j)}, big
}

func recordTape(src, dir, out string) {
	t, err := loadSpike(src)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	toolDir = dir
	s := fresh()
	s.Mode = "repl"
	cell := 0
	s.Det = func(name, args string) Answer {
		ans, big := realAnswer(name, args)
		e := TapeEntry{Cell: cell, Name: name, Args: args, OK: ans.OK, Val: ans.Val}
		if big > 0 {
			e.Val, e.Big = "", big
		}
		t.Tape = append(t.Tape, e)
		return ans
	}
	for i, code := range t.Cells {
		cell = i
		t.Record = append(t.Record, runCell(s, code, 120*time.Second))
	}
	writeJSON(out, t)
}

func runCell(s *Session, code string, d time.Duration) string {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	_, err := s.Run(ctx, code)
	o, v := s.LastParts()
	return Canon(o, v, err, s.Off)
}

func writeJSON(path string, v any) {
	j, err := json.MarshalIndent(v, "", " ")
	if err == nil {
		err = os.WriteFile(path, append(j, '\n'), 0o644)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "write", path, err)
		os.Exit(1)
	}
}

func LoadTranscript(path string) (*Transcript, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var t Transcript
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &t, nil
}

type ReplayCell struct {
	Canon     string `json:"canon"`
	Misses    int    `json:"misses,omitempty"`
	Fallbacks int    `json:"fallbacks,omitempty"`
	Unused    int    `json:"unused,omitempty"`
}

func (c ReplayCell) TapeIssues() int { return c.Misses + c.Fallbacks + c.Unused }

func ReplayTranscript(t *Transcript) []ReplayCell {
	s := fresh()
	defer s.VM().Close()
	s.Mode = "repl"
	p := newTapePlayer(t)
	s.Det = p.answer
	var out []ReplayCell
	for i, code := range t.Cells {
		p.cell, p.misses, p.fallbacks = i, 0, 0
		c := runCell(s, code, 20*time.Second)
		out = append(out, ReplayCell{Canon: c, Misses: p.misses, Fallbacks: p.fallbacks, Unused: p.unused(i)})
	}
	return out
}

func TranscriptFiles(dir string, rids []string) []string {
	if len(rids) > 0 {
		var fs []string
		for _, r := range rids {
			fs = append(fs, filepath.Join(dir, strings.TrimSuffix(r, ".json")+".json"))
		}
		return fs
	}
	fs, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	sort.Strings(fs)
	return fs
}

func replay(dir string, rids []string) {
	enc := json.NewEncoder(os.Stdout)
	for _, f := range TranscriptFiles(dir, rids) {
		t, err := LoadTranscript(f)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		for i, c := range ReplayTranscript(t) {
			_ = enc.Encode(map[string]any{"id": fmt.Sprintf("%s#%d", t.RID, i), "canon": c.Canon, "misses": c.Misses, "fallbacks": c.Fallbacks, "unused": c.Unused})
		}
	}
}
