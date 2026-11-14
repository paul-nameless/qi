package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/paul-nameless/aicode/internal/llm"
)

func lastCodeBlock(msgs []llm.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != "assistant" {
			continue
		}
		if code, ok := lastFence(msgs[i].Text()); ok {
			return code
		}
	}
	return ""
}

func lastFence(text string) (string, bool) {
	var block []string
	var last string
	found, open := false, ""
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		mark := fenceMark(t)
		switch {
		case open == "" && mark != "":
			open, block = mark, nil
		case open != "" && t == mark && strings.HasPrefix(mark, open):
			open, last, found = "", strings.Join(block, "\n"), true
		case open != "":
			block = append(block, line)
		}
	}
	return last, found
}

func fenceMark(line string) string {
	if line == "" || (line[0] != '`' && line[0] != '~') {
		return ""
	}
	n := 0
	for n < len(line) && line[n] == line[0] {
		n++
	}
	if n < 3 {
		return ""
	}
	return line[:n]
}

func (r *repl) copyAnswer(args string) error {
	var text, what string
	switch args {
	case "":
		text, what = lastAnswer(r.agent.Messages), "answer"
	case "code":
		text, what = lastCodeBlock(r.agent.Messages), "code block"
	default:
		return errors.New("usage: /copy or /copy code")
	}
	if text == "" {
		return errors.New("no " + what + " to copy")
	}
	cmd := r.agent.Cfg.CopyCmd
	if !r.toolOn(cmd) {
		return fmt.Errorf("copy_cmd is off (%s not found)", firstWord(cmd))
	}
	if _, err := runQuiet(cmd, r.agent.Cfg.Picker, nil, text); err != nil {
		return fmt.Errorf("copy_cmd: %w", err)
	}
	r.display.Info(fmt.Sprintf("copied %s (%d lines)", what, strings.Count(text, "\n")+1))
	return nil
}
