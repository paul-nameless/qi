package harness

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

const CellSep = "//// cell"

func SplitCells(code string) []string {
	var cells []string
	var cur []string
	for _, l := range strings.Split(code, "\n") {
		if strings.TrimSpace(l) == CellSep {
			cells = append(cells, strings.Join(cur, "\n"))
			cur = nil
			continue
		}
		cur = append(cur, l)
	}
	return append(cells, strings.Join(cur, "\n"))
}

func virtualTools(name, args string) Answer {
	if name == "sleep" {
		d := sleepDelay(name, args)
		return Answer{OK: true, Val: fmt.Sprint(d), Delay: d}
	}
	return Answer{OK: false, Val: "tool " + name + " is not available in the corpus"}
}

func RunExt(code string, strict bool) string {
	s := fresh()
	defer s.VM().Close()
	s.Det = virtualTools
	cells := SplitCells(code)
	if strict {
		s.Mode = "iife"
		return runCell(s, "\"use strict\"; "+code, 3*time.Second)
	}
	s.Mode = "repl"
	if len(cells) == 1 {
		return runCell(s, cells[0], 3*time.Second)
	}
	var b strings.Builder
	for i, c := range cells {
		fmt.Fprintf(&b, "## cell %d\n", i+1)
		b.WriteString(runCell(s, c, 3*time.Second))
	}
	return b.String()
}

func RunSloppyIIFE(code string) string {
	s := fresh()
	defer s.VM().Close()
	s.Det = virtualTools
	s.Mode = "iife"
	return runCell(s, code, 3*time.Second)
}

type WorkReq struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Path   string `json:"path,omitempty"`
	Code   string `json:"code,omitempty"`
	Strict bool   `json:"strict,omitempty"`
}

type WorkResp struct {
	ID    string       `json:"id"`
	Canon string       `json:"canon,omitempty"`
	Cells []ReplayCell `json:"cells,omitempty"`
	Ms    float64      `json:"ms"`
	Err   string       `json:"err,omitempty"`
}

func handle(r WorkReq) WorkResp {
	t := time.Now()
	resp := WorkResp{ID: r.ID}
	code := r.Code
	if r.Path != "" && r.Kind != "replay" {
		b, err := os.ReadFile(r.Path)
		if err != nil {
			resp.Err = err.Error()
			return resp
		}
		code = string(b)
	}
	switch r.Kind {
	case "ext":
		resp.Canon = RunExt(code, false)
	case "strict":
		resp.Canon = RunExt(code, true)
	case "sloppy":
		resp.Canon = RunSloppyIIFE(code)
	case "replay":
		tr, err := LoadTranscript(r.Path)
		if err != nil {
			resp.Err = err.Error()
			return resp
		}
		resp.Cells = ReplayTranscript(tr)
	default:
		resp.Err = "unknown kind " + r.Kind
	}
	resp.Ms = float64(time.Since(t).Microseconds()) / 1000
	return resp
}

func worker() {
	in := bufio.NewReaderSize(os.Stdin, 1<<20)
	enc := json.NewEncoder(os.Stdout)
	for {
		line, err := in.ReadBytes('\n')
		if len(line) == 0 && err != nil {
			return
		}
		var r WorkReq
		if err := json.Unmarshal(line, &r); err != nil {
			_ = enc.Encode(WorkResp{Err: "bad request: " + err.Error()})
			continue
		}
		_ = enc.Encode(handle(r))
	}
}

func corpusCmd(args []string) {
	kind := "ext"
	if len(args) > 0 && strings.HasPrefix(args[0], "-") {
		kind = strings.TrimPrefix(args[0], "-")
		args = args[1:]
	}
	enc := json.NewEncoder(os.Stdout)
	for _, f := range args {
		_ = enc.Encode(handle(WorkReq{ID: f, Kind: kind, Path: f}))
	}
}
