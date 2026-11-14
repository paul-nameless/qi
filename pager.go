package main

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/paul-nameless/aicode/internal/llm"
	"github.com/paul-nameless/aicode/internal/session"
)

const pagerResultLines = 50

var fullOutput = regexp.MustCompile(`Full output: ([^\n]+)`)

func lastSpill(msgs []llm.Message, dir string) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		blocks := msgs[i].Blocks
		for j := len(blocks) - 1; j >= 0; j-- {
			if blocks[j].Type != llm.BlockToolResult {
				continue
			}
			m := fullOutput.FindAllStringSubmatch(blocks[j].Text, -1)
			if len(m) > 0 {
				if p := filepath.Clean(m[len(m)-1][1]); filepath.Dir(p) == filepath.Clean(dir) {
					return p
				}
			}
		}
	}
	return ""
}

func lastAnswer(msgs []llm.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if t := strings.TrimSpace(msgs[i].Text()); msgs[i].Role == "assistant" && t != "" {
			return t
		}
	}
	return ""
}

func (r *repl) showAnswer() error {
	text := lastAnswer(r.agent.Messages)
	if text == "" {
		return errors.New("no answer yet")
	}
	return r.view("viewer", r.agent.Cfg.Viewer, text+"\n", ".md")
}

func (r *repl) showHistory() error {
	entries, err := session.Entries(r.agent.Session.Path)
	if err != nil {
		return err
	}
	return r.view("pager", r.agent.Cfg.Pager, renderTranscript(transcript(entries), pagerResultLines), ".txt")
}

func (r *repl) showOut() error {
	path := lastSpill(r.agent.Messages, r.agent.Env.SpillPath())
	if path == "" {
		return errors.New("no cut-off tool output in this session")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return r.view("pager", r.agent.Cfg.Pager, string(data), ".txt")
}
