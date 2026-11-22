package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func loadScore(path string) (Score, error) {
	var s Score
	b, err := os.ReadFile(path)
	if err != nil {
		return s, err
	}
	return s, json.Unmarshal(b, &s)
}

func perfByName(s Score) map[string]PerfResult {
	m := map[string]PerfResult{}
	for _, p := range s.Perf {
		m[p.Name] = p
	}
	return m
}

func cell(p PerfResult, ok bool) string {
	if !ok || p.BestMs < 0 {
		return "-"
	}
	s := fmt.Sprintf("%dms %.0fMB", p.BestMs, p.RSSAfter)
	if p.RSSDrop > 0 {
		s += fmt.Sprintf(" →%.0f", p.RSSDrop)
	}
	return s
}

func worse(cand PerfResult, refs []PerfResult) []string {
	var why []string
	bestMs, bestRSS, bestDrop := int64(-1), 0.0, 0.0
	for _, r := range refs {
		if r.BestMs < 0 {
			continue
		}
		if bestMs < 0 || r.BestMs < bestMs {
			bestMs = r.BestMs
		}
		if bestRSS == 0 || r.RSSAfter < bestRSS {
			bestRSS = r.RSSAfter
		}
		if r.RSSDrop > 0 && (bestDrop == 0 || r.RSSDrop < bestDrop) {
			bestDrop = r.RSSDrop
		}
	}
	if cand.BestMs < 0 {
		return []string{"FAILED"}
	}
	if bestMs >= 0 && cand.BestMs > bestMs {
		why = append(why, fmt.Sprintf("slower (%dms vs %dms)", cand.BestMs, bestMs))
	}
	if bestRSS > 0 && cand.RSSAfter > bestRSS*1.05+1 {
		why = append(why, fmt.Sprintf("more peak RSS (%.0f vs %.0f MB)", cand.RSSAfter, bestRSS))
	}
	if bestDrop > 0 && cand.RSSDrop > bestDrop*1.05+1 {
		why = append(why, fmt.Sprintf("more RSS after drop (%.0f vs %.0f MB)", cand.RSSDrop, bestDrop))
	}
	for _, r := range refs {
		if r.BestMs >= 0 && r.Out != cand.Out && r.Out != "" {
			why = append(why, fmt.Sprintf("output differs (%q vs %q)", cut(cand.Out, 40), cut(r.Out, 40)))
			break
		}
	}
	return why
}

func cut(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func compareFiles(paths []string) error {
	var ss []Score
	for _, p := range paths {
		s, err := loadScore(p)
		if err != nil {
			return err
		}
		ss = append(ss, s)
	}
	cand := ss[len(ss)-1]
	head := []string{fmt.Sprintf("%-20s", "item")}
	for _, s := range ss {
		head = append(head, fmt.Sprintf("%-22s", s.Engine))
	}
	fmt.Println(strings.Join(head, " ") + " flags")
	maps := make([]map[string]PerfResult, len(ss))
	for i, s := range ss {
		maps[i] = perfByName(s)
	}
	for _, p := range cand.Perf {
		row := []string{fmt.Sprintf("%-20s", p.Name)}
		var refs []PerfResult
		for i := range ss {
			r, ok := maps[i][p.Name]
			row = append(row, fmt.Sprintf("%-22s", cell(r, ok)))
			if i < len(ss)-1 && ok {
				refs = append(refs, r)
			}
		}
		fmt.Println(strings.Join(row, " ") + " " + strings.Join(worse(p, refs), "; "))
	}
	return nil
}
