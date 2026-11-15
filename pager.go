package main

import "github.com/paul-nameless/aicode/internal/session"

func (r *repl) showHistory() error {
	entries, err := session.Entries(r.agent.Session.Path)
	if err != nil {
		return err
	}
	width := 0
	if r.display.TTY {
		width = r.display.Width()
	}
	return r.view("pager", r.agent.Cfg.Pager, renderTranscript(transcript(entries), 0, width), ".txt")
}
