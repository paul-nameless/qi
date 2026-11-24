package agent

import (
	"fmt"
	"sort"
	"strings"

	"github.com/paul-nameless/qi/internal/llm"
)

type usageRow struct {
	name   string
	tokens int
	sub    []usageRow
}

func (a *Agent) ContextReport() string {
	a.busy.Lock()
	defer a.busy.Unlock()
	l := a.snapshot()
	rows := []usageRow{systemRow(l.system), a.toolsRow(), messagesRow(a.conv.messages)}
	total := 0
	for _, r := range rows {
		total += r.tokens
	}
	window, known := l.cfg.Window()
	var b strings.Builder
	head := "context " + usageOf(total, window, known) + " · compacts at " + FmtTokens(l.cfg.CompactTokens())
	if n := l.ctxTokens; n > 0 {
		head += " · last request " + FmtTokens(n)
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
		fmt.Fprintf(&b, "  %-30s %7s  %6s\n", fitName(r.name, 30), FmtTokens(r.tokens), pct(r.tokens, base))
		for _, s := range r.sub {
			fmt.Fprintf(&b, "    %-28s %7s\n", fitName(s.name, 28), FmtTokens(s.tokens))
		}
	}
	b.WriteString("estimated at 4 characters per token; images from their size")
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

func systemRow(sp SystemPrompt) usageRow {
	r := usageRow{name: "system prompt", tokens: len(sp.Text) / 4}
	for _, s := range sp.Sections {
		r.sub = append(r.sub, usageRow{name: s.Name, tokens: s.Chars / 4})
	}
	return r
}

func (a *Agent) toolsRow() usageRow {
	var r usageRow
	defs := a.toolDefs()
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
				if m.Role == llm.RoleAssistant {
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
				for _, img := range b.Images {
					images += 4 * llm.ImageTokens(img)
				}
			case llm.BlockImage:
				images += 4 * llm.ImageTokens(b)
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
