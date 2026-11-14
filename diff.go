package main

import (
	"fmt"
	"strings"

	"github.com/paul-nameless/aicode/internal/ui"
)

const diffLines = 40

func (r *repl) showDiff(diff string) {
	out, err := runQuiet(r.agent.Cfg.DiffCmd, r.agent.Cfg.Picker, nil, diff)
	if err != nil {
		r.display.Info("diff_cmd: " + err.Error())
		return
	}
	if s := capLines(r.display, strings.TrimLeft(out, "\n"), diffLines); s != "" {
		r.display.Line(s)
	}
}

func capLines(p *ui.Printer, s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) <= n {
		return strings.TrimRight(s, "\n")
	}
	head := strings.Join(lines[:n], "\n")
	if p.TTY {
		head += ui.Reset
	}
	return head + "\n" + p.Color(ui.Dim, fmt.Sprintf("… %d more lines", len(lines)-n))
}
