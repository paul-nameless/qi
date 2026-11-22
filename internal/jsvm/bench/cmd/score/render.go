package main

import (
	"fmt"
	"sort"
	"strings"
)

func head(xs []string, n int) string {
	if len(xs) > n {
		return strings.Join(xs[:n], " ") + fmt.Sprintf(" …(+%d)", len(xs)-n)
	}
	return strings.Join(xs, " ")
}

func render(s Score) string {
	var b strings.Builder
	p := func(f string, a ...any) { fmt.Fprintf(&b, f, a...) }
	p("engine   %s  bin %.1f MB  %s\n", s.Engine, float64(s.BinBytes)/(1<<20), s.When)
	if s.Error != "" {
		p("ERROR    %s\n", s.Error)
		p("wall     %.1fs\n", s.WallSec)
		return b.String()
	}
	if len(s.Gates) > 0 {
		pass := 0
		var fails []string
		for _, g := range s.Gates {
			if g.OK {
				pass++
				continue
			}
			fails = append(fails, g.Name)
		}
		p("gates    %d/%d pass\n", pass, len(s.Gates))
		for _, g := range s.Gates {
			mark := "ok  "
			if !g.OK {
				mark = "FAIL"
			}
			line := fmt.Sprintf("  %s %-22s %6dms  %s", mark, g.Name, g.Ms, g.Got)
			if !g.OK {
				line += "   (want " + g.Want + ")"
			}
			p("%s\n", line)
		}
	}
	if len(s.Perf) > 0 {
		var parts []string
		for _, x := range s.Perf {
			part := fmt.Sprintf("%s %dms", x.Name, x.BestMs)
			if x.Name == "mcp8000" {
				part += fmt.Sprintf(" (rss %.0f→%.0f MB)", x.RSSBefore, x.RSSAfter)
			}
			if x.Failed > 0 {
				part += fmt.Sprintf(" [%d/3 runs failed: %s]", x.Failed, x.Err)
			}
			parts = append(parts, part)
		}
		p("perf     %s   [best of 3, fresh process each]\n", strings.Join(parts, "  "))
	}
	if s.Mem != nil {
		ok := "FAIL"
		if s.Mem.OK {
			ok = "ok"
		}
		p("mem      %s  runaway %q in %dms, rss peak %.0f MB, after %q\n", ok, s.Mem.Runaway, s.Mem.Ms, s.Mem.PeakMB, s.Mem.After)
	}
	if s.Startup != nil {
		p("startup  firstVM %.0fµs  firstRun %.0fµs  nextVM+run %.0fµs  idle RSS %.1f MB  after 20 VMs %.1f MB\n", s.Startup.FirstVMus, s.Startup.FirstRunus, s.Startup.NextVMus, s.Startup.IdleRSS, s.Startup.RSSAfter20)
	}
	if s.State != nil {
		p("state    %d/%d  %s\n", s.State.Pass, s.State.Total, head(s.State.Fail, 10))
	}
	if s.Probes != nil {
		p("probes   %d/%d  fail: %s\n", s.Probes.Pass, s.Probes.Total, strings.Join(s.Probes.Fail, " "))
	}
	if r := s.Replay; r != nil {
		p("replay   %d/%d oracle cells (%d wording-only), tier1 %d/%d, transcripts failing %d, tape mismatches %d, crashes %d\n", r.Pass, r.Total, r.Wording, r.Tier1.Pass, r.Tier1.Total, r.Transcripts, r.Misses, r.Crashes)
		p("  by tag: %s\n", tagLine(r.ByTag))
		if len(r.Fail) > 0 {
			p("  first diffs: %s\n", head(r.Fail, 10))
			p("  first divergence per transcript: %s\n", head(r.FirstDiv, 10))
		}
	}
	if e := s.Ext; e != nil {
		var src []string
		for _, k := range sortedKeys(e.BySource) {
			src = append(src, fmt.Sprintf("%s %d/%d", k, e.BySource[k].Pass, e.BySource[k].Total))
		}
		p("ext      %d/%d (%d wording-only items, crashes %d)  tier1 %d/%d  %s\n", e.Pass, e.Total, e.Wording, e.Crashes, e.Tier1.Pass, e.Tier1.Total, strings.Join(src, "  "))
		p("  by tag: %s\n", tagLine(e.ByTag))
		if len(e.Fail) > 0 {
			p("  first diffs: %s\n", head(e.Fail, 20))
			p("  failing by bucket: %s\n", bucketLine(e.FailByBucket))
			p("  unexplained: %d %s\n", len(e.Unexplained), head(e.Unexplained, 30))
			p("  loud %d / silent %d\n", e.Loud, e.Silent)
			for _, b := range sortedStrKeys(e.SilentByBucket) {
				p("    silent %s (%d): %s\n", b, len(e.SilentByBucket[b]), head(e.SilentByBucket[b], 30))
			}
		}
	}
	p("wall     %.1fs\n", s.WallSec)
	return b.String()
}

func sortedKeys(m map[string]*Count) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func tagLine(m map[string]*Count) string {
	var tags []string
	for _, k := range sortedKeys(m) {
		tags = append(tags, fmt.Sprintf("%s %d/%d", k, m[k].Pass, m[k].Total))
	}
	return strings.Join(tags, ", ")
}

func bucketLine(m map[string]int) string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	var parts []string
	for _, k := range ks {
		parts = append(parts, fmt.Sprintf("%s %d", k, m[k]))
	}
	return strings.Join(parts, ", ")
}

func sortedStrKeys(m map[string][]string) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
