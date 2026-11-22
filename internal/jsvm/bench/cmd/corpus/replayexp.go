package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/paul-nameless/qi/internal/jsvm/bench/harness"
	"github.com/paul-nameless/qi/internal/jsvm/bench/oracle"
	"github.com/paul-nameless/qi/internal/jsvm/bench/pool"
)

type ExpRow struct {
	ID        string `json:"id"`
	Canon     string `json:"canon"`
	Misses    int    `json:"misses,omitempty"`
	Fallbacks int    `json:"fallbacks,omitempty"`
	Unused    int    `json:"unused,omitempty"`
}

func replayRun(bin string, files []string) ([]ExpRow, error) {
	reqs := make([]harness.WorkReq, len(files))
	for i, f := range files {
		abs, _ := filepath.Abs(f)
		reqs[i] = harness.WorkReq{ID: filepath.Base(f), Kind: "replay", Path: abs}
	}
	resps, err := pool.Run(bin, 4, reqs, 120*time.Second)
	if err != nil {
		return nil, err
	}
	var rows []ExpRow
	for i, r := range resps {
		t, err := harness.LoadTranscript(files[i])
		if err != nil {
			return nil, err
		}
		if r.Err != "" {
			return nil, fmt.Errorf("%s: %s", files[i], r.Err)
		}
		for n, c := range r.Cells {
			rows = append(rows, ExpRow{ID: fmt.Sprintf("%s#%d", t.RID, n), Canon: c.Canon, Misses: c.Misses, Fallbacks: c.Fallbacks, Unused: c.Unused})
		}
	}
	return rows, nil
}

func writeRows(path string, rows []ExpRow) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, r := range rows {
		if err := enc.Encode(r); err != nil {
			return err
		}
	}
	return nil
}

func stripRecords(files []string) (map[string]string, error) {
	rec := map[string]string{}
	for _, f := range files {
		t, err := harness.LoadTranscript(f)
		if err != nil {
			return nil, err
		}
		if len(t.Record) == 0 {
			continue
		}
		for i, c := range t.Record {
			rec[fmt.Sprintf("%s#%d", t.RID, i)] = c
		}
		t.Record = nil
		b, _ := json.MarshalIndent(t, "", " ")
		if err := os.WriteFile(f, append(b, '\n'), 0o644); err != nil {
			return nil, err
		}
	}
	return rec, nil
}

func replayExpect(args []string) error {
	dir := "corpus/replay"
	files := harness.TranscriptFiles(filepath.Join(dir, "transcripts"), nil)
	rec, err := stripRecords(files)
	if err != nil {
		return err
	}
	out := map[string][]ExpRow{}
	for _, e := range []string{"goja", "modernc"} {
		bin, err := pool.Build(e)
		if err != nil {
			return err
		}
		if out[e], err = replayRun(bin, files); err != nil {
			return err
		}
	}
	g, q := out["goja"], out["modernc"]
	selfBad := 0
	for _, r := range g {
		if c, ok := rec[r.ID]; ok && c != r.Canon {
			selfBad++
			fmt.Println("self-check differs:", r.ID, oracle.FirstDiff(c, r.Canon))
		}
	}
	exact, wording := 0, 0
	for i := range g {
		switch ok, _ := oracle.WordingOnly(g[i].Canon, q[i].Canon, false); {
		case g[i].Canon == q[i].Canon:
			exact++
		case ok:
			wording++
		default:
			fmt.Println("disagree:", g[i].ID, oracle.FirstDiff(g[i].Canon, q[i].Canon))
		}
	}
	fmt.Printf("cells %d, record checked %d (differs %d), exact %d, wording-only %d, oracle %d\n", len(g), len(rec), selfBad, exact, wording, exact+wording)
	if err := writeRows(filepath.Join(dir, "expected.goja.jsonl"), g); err != nil {
		return err
	}
	return writeRows(filepath.Join(dir, "expected.quickjs.jsonl"), q)
}
