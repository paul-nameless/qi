package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/paul-nameless/aicode/internal/llm"
	"github.com/paul-nameless/aicode/internal/session"
	"github.com/paul-nameless/aicode/internal/tools"
	"github.com/paul-nameless/aicode/internal/ui"
)

func transcript(entries []session.Entry) []llm.Message {
	var msgs []llm.Message
	for _, e := range entries {
		switch {
		case e.Type == "message" && e.Message != nil:
			msgs = append(msgs, *e.Message)
		case e.Type == "drop" && len(msgs) > 0:
			msgs = msgs[:len(msgs)-1]
		case e.Type == "compaction":
			msgs = append(msgs, llm.Message{Role: "compaction", Blocks: []llm.Block{{Type: llm.BlockText, Text: e.Summary}}})
		}
	}
	return msgs
}

type painter struct {
	width int
}

func (p painter) paint(code, s string) string {
	if p.width == 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = code + l + ui.Reset
		}
	}
	return strings.Join(lines, "\n")
}

func (p painter) markdown(s string) string {
	if p.width == 0 {
		return s + "\n"
	}
	out := ui.Markdown(s, p.width)
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return out
}

func renderTranscript(msgs []llm.Message, resultLines, width int) string {
	var b strings.Builder
	p := painter{width: width}
	for _, m := range msgs {
		for _, blk := range m.Blocks {
			renderBlock(&b, p, m.Role, blk, resultLines)
		}
	}
	return strings.TrimLeft(b.String(), "\n")
}

func renderBlock(b *strings.Builder, p painter, role string, blk llm.Block, resultLines int) {
	text := strings.TrimSpace(ui.Sanitize(blk.Text))
	switch {
	case role == "compaction":
		fmt.Fprintf(b, "\n%s\n%s", p.paint(ui.Dim, "── compacted ──"), p.markdown(text))
	case blk.Type == llm.BlockText && role == "user":
		if session.IsPrompt(blk.Text) {
			fmt.Fprintf(b, "\n%s%s\n", p.paint(ui.Cyan, "❯ "), strings.ReplaceAll(text, "\n", "\n  "))
		}
	case blk.Type == llm.BlockText:
		b.WriteString(p.markdown(text))
	case blk.Type == llm.BlockToolUse:
		b.WriteString(p.paint(ui.Dim, "· "+blk.Name+" "+ui.Sanitize(tools.OneLine(string(blk.Input), 200))) + "\n")
	case blk.Type == llm.BlockToolResult:
		b.WriteString(p.paint(ui.Dim, indentResult(ui.Sanitize(blk.Text), resultLines)))
	case blk.Type == llm.BlockImage:
		b.WriteString(p.paint(ui.Dim, "[image]") + "\n")
	}
}

func indentResult(text string, max int) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	more := 0
	if max > 0 && len(lines) > max {
		more = len(lines) - max
		lines = lines[:max]
	}
	var b strings.Builder
	for i, l := range lines {
		prefix := "    "
		if i == 0 {
			prefix = "  ⎿ "
		}
		b.WriteString(prefix + l + "\n")
	}
	if more > 0 {
		fmt.Fprintf(&b, "    … %d more lines\n", more)
	}
	return b.String()
}

func age(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
