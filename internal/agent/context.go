package agent

import (
	"fmt"
	"sort"
	"strings"

	"github.com/paul-nameless/aicode/internal/llm"
)

type usageRow struct {
	name   string
	tokens int
	sub    []usageRow
}

func (a *Agent) ContextReport() string {
	rows := []usageRow{a.systemRow(), a.toolsRow(), messagesRow(a.Messages)}
	total := 0
	for _, r := range rows {
		total += r.tokens
	}
	window, known := a.Cfg.Window()
	var b strings.Builder
	head := "context ~" + fmtTokens(total)
	if known && window > 0 {
		head += fmt.Sprintf(" of %s (%s)", fmtTokens(window), pct(total, window))
	}
	head += " · compacts at " + fmtTokens(a.Cfg.CompactTokens())
	if n := a.ContextTokens(); n > 0 {
		head += " · last request " + fmtTokens(n)
	}
	b.WriteString(head + "\n")
	if known && window > total {
		rows = append(rows, usageRow{name: "free", tokens: window - total})
	}
	base := total
	if known && window > 0 {
		base = window
	}
	for _, r := range rows {
		fmt.Fprintf(&b, "  %-30s %7s  %6s\n", fitName(r.name, 30), fmtTokens(r.tokens), pct(r.tokens, base))
		for _, s := range r.sub {
			fmt.Fprintf(&b, "    %-28s %7s\n", fitName(s.name, 28), fmtTokens(s.tokens))
		}
	}
	b.WriteString("estimated at 4 characters per token, " + fmtTokens(imageChars/4) + " per image")
	return b.String()
}

func fitName(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return "…" + string(r[len(r)-n+1:])
}

func pct(n, of int) string {
	if of <= 0 {
		return ""
	}
	return fmt.Sprintf("%.1f%%", float64(n)*100/float64(of))
}

func (a *Agent) systemRow() usageRow {
	r := usageRow{name: "system prompt", tokens: len(a.System) / 4}
	for _, s := range a.Sections {
		r.sub = append(r.sub, usageRow{name: s.Name, tokens: s.Chars / 4})
	}
	return r
}

func (a *Agent) toolsRow() usageRow {
	var r usageRow
	if a.Registry == nil {
		return usageRow{name: "tools (0)"}
	}
	defs := a.Registry.Defs()
	for _, d := range defs {
		n := (len(d.Name) + len(d.Description) + len(d.Schema)) / 4
		r.tokens += n
		r.sub = append(r.sub, usageRow{name: d.Name, tokens: n})
	}
	r.name = fmt.Sprintf("tools (%d)", len(defs))
	return r
}

func messagesRow(msgs []llm.Message) usageRow {
	names := map[string]string{}
	var user, assistant, reasoning, calls, images int
	results := map[string]int{}
	for _, m := range msgs {
		for _, b := range m.Blocks {
			switch b.Type {
			case llm.BlockText:
				if m.Role == "assistant" {
					assistant += len(b.Text)
				} else {
					user += len(b.Text)
				}
			case llm.BlockReasoning:
				reasoning += len(b.Text) + len(b.Raw)
			case llm.BlockToolUse:
				names[b.ID] = b.Name
				calls += len(b.Name) + len(b.Input)
			case llm.BlockToolResult:
				name := names[b.ToolUseID]
				if name == "" {
					name = "other"
				}
				results[name] += len(b.Text)
				images += len(b.Images) * imageChars
			case llm.BlockImage:
				images += imageChars
			}
		}
	}
	r := usageRow{name: fmt.Sprintf("messages (%d)", len(msgs))}
	add := func(name string, chars int) {
		if chars/4 > 0 {
			r.sub = append(r.sub, usageRow{name: name, tokens: chars / 4})
			r.tokens += chars / 4
		}
	}
	add("user text", user)
	add("assistant text", assistant)
	add("reasoning", reasoning)
	add("tool calls", calls)
	tools := make([]string, 0, len(results))
	for name := range results {
		tools = append(tools, name)
	}
	sort.Slice(tools, func(i, j int) bool { return results[tools[i]] > results[tools[j]] })
	for _, name := range tools {
		add(name+" results", results[name])
	}
	add("images", images)
	return r
}
