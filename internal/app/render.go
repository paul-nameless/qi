package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/paul-nameless/qi/internal/llm"
	"github.com/paul-nameless/qi/internal/session"
	"github.com/paul-nameless/qi/internal/tools"
	"github.com/paul-nameless/qi/internal/ui"
)

const compactionRole llm.Role = "compaction"

func transcript(entries []session.Entry) []llm.Message {
	var msgs []llm.Message
	for _, e := range entries {
		switch {
		case e.Type == "message" && e.Message != nil:
			msgs = append(msgs, *e.Message)
		case e.Type == "drop" && len(msgs) > 0:
			msgs = msgs[:len(msgs)-1]
		case e.Type == "compaction":
			msgs = append(msgs, llm.Message{Role: compactionRole, Blocks: []llm.Block{{Type: llm.BlockText, Text: e.Summary}}})
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

func renderBlock(b *strings.Builder, p painter, role llm.Role, blk llm.Block, resultLines int) {
	text := strings.TrimSpace(ui.Sanitize(blk.Text))
	switch {
	case role == compactionRole:
		fmt.Fprintf(b, "\n%s\n%s", p.paint(ui.Dim, "── compacted ──"), p.markdown(text))
	case blk.Type == llm.BlockText && role == llm.RoleUser:
		if session.IsPrompt(blk.Text) {
			fmt.Fprintf(b, "\n%s%s\n", p.paint(ui.Cyan, "❯ "), strings.ReplaceAll(text, "\n", "\n  "))
		}
	case blk.Type == llm.BlockText:
		b.WriteString(p.markdown(text))
	case blk.Type == llm.BlockToolUse:
		b.WriteString(p.paint(ui.Dim, toolUseText(blk, 200)) + "\n")
	case blk.Type == llm.BlockToolResult:
		b.WriteString(p.paint(ui.Dim, indentResult(ui.Sanitize(blk.Text), resultLines)+callLines(blk.Calls)))
	case blk.Type == llm.BlockImage:
		b.WriteString(p.paint(ui.Dim, "[image]") + "\n")
	}
}

const codeLines = 12

func toolUseText(blk llm.Block, width int) string {
	var a struct {
		Code string `json:"code"`
	}
	if blk.Name != "run" || json.Unmarshal(blk.Input, &a) != nil || strings.TrimSpace(a.Code) == "" {
		return "· " + blk.Name + " " + ui.Sanitize(tools.OneLine(string(blk.Input), width))
	}
	code := firstLines(strings.Trim(ui.Sanitize(a.Code), "\n"), codeLines)
	return "· run\n    " + strings.ReplaceAll(code, "\n", "\n    ")
}

func callLines(calls []llm.InnerCall) string {
	var b strings.Builder
	for _, c := range calls {
		mark, line := "✓", c.Label
		if !c.OK {
			mark = "✗"
		}
		if line == "" {
			line = c.Tool
		}
		if c.Summary != "" {
			line += " · " + tools.OneLine(c.Summary, 80)
		}
		fmt.Fprintf(&b, "    %s %s\n", mark, ui.Sanitize(line))
	}
	return b.String()
}

func indentResult(text string, limit int) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	more := 0
	if limit > 0 && len(lines) > limit {
		more = len(lines) - limit
		lines = lines[:limit]
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
