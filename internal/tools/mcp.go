package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/paul-nameless/qi/internal/llm"
	"github.com/paul-nameless/qi/internal/mcp"
)

type toolSearchArgs struct {
	Query  string `json:"query"`
	Server string `json:"server"`
}

type mcpCallArgs struct {
	Server    string          `json:"server"`
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
}

const (
	maxSchemaTools  = 10
	maxListingBytes = 24000
	maxListingLines = 350
)

func serverLines(m *mcp.Manager) string {
	var b strings.Builder
	for _, n := range m.Names() {
		fmt.Fprintf(&b, "\n- %s: %s", n, m.Description(n))
	}
	return b.String()
}

func toolSearch(m *mcp.Manager) *Function {
	return &Function{
		Name: "tool_search",
		Label: func(env *Env, in json.RawMessage) string {
			a, _ := decode[toolSearchArgs](in)
			label := strings.TrimSpace("tool_search " + a.Server)
			if a.Server != "" && a.Query != "" {
				label += ":"
			}
			return strings.TrimSpace(label + " " + OneLine(a.Query, 50))
		},
		Run: func(ctx context.Context, env *Env, in json.RawMessage, _ func(string)) Result {
			a, err := decode[toolSearchArgs](in)
			if err != nil {
				return errResult("%v", err)
			}
			return runToolSearch(ctx, env, m, a)
		},
		Code: CodeSpec{
			Name:   "tool_search",
			Params: []string{"query", "server"},
			Doc:    "tool_search(query, server) → string — find tools on the connected MCP servers by keywords, or list one server's tools; returns their input schemas. Servers:" + strings.ReplaceAll(serverLines(m), "\n- ", "\n  - "),
		},
	}
}

func runToolSearch(ctx context.Context, env *Env, m *mcp.Manager, a toolSearchArgs) Result {
	a.Query = strings.TrimSpace(a.Query)
	var l mcp.Listing
	switch {
	case a.Server != "":
		var err error
		if l, err = m.ServerTools(ctx, a.Server, a.Query); err != nil {
			return errResult("%v", err)
		}
	case a.Query != "":
		l = m.Search(ctx, a.Query)
	default:
		return errResult("give query, server or both")
	}
	text, shown := formatListing(a, l)
	summary := fmt.Sprintf("%d tools", shown)
	if shown < len(l.Tools) {
		summary = fmt.Sprintf("%d of %d tools", shown, len(l.Tools))
	}
	return Result{Text: env.capOutput(text), Summary: summary}
}

func formatListing(a toolSearchArgs, l mcp.Listing) (string, int) {
	compact := a.Server != "" && a.Query == "" && len(l.Tools) > maxSchemaTools
	entries := schemaEntries(l.Tools)
	if compact {
		entries = compactEntries(l.Tools)
	}
	var b strings.Builder
	shown := fitEntries(&b, entries)
	switch {
	case compact:
		fmt.Fprintf(&b, "shown %d of %d; call tool_search with server and query=<tool name> for its input schema\n", shown, len(l.Tools))
	case shown < len(l.Tools):
		fmt.Fprintf(&b, "shown %d of %d matching tools; refine the query to see the others\n", shown, len(l.Tools))
	case len(l.Tools) == 0:
		b.WriteString(emptyListing(a, l) + "\n")
	}
	writeNotices(&b, l)
	return strings.TrimRight(b.String(), "\n"), shown
}

func schemaEntries(tools []mcp.Tool) []string {
	entries := make([]string, 0, maxSchemaTools)
	for _, t := range tools[:min(len(tools), maxSchemaTools)] {
		entries = append(entries, fmt.Sprintf("server: %s\ntool: %s\ndescription: %s\ninput schema: %s\n\n", t.Server, t.Name, OneLine(t.Description, 400), string(t.InputSchema)))
	}
	return entries
}

func compactEntries(tools []mcp.Tool) []string {
	entries := make([]string, len(tools))
	for i, t := range tools {
		entries[i] = fmt.Sprintf("- %s: %s\n", t.Name, OneLine(t.Description, 150))
	}
	if len(tools) > 0 {
		entries[0] = "server: " + tools[0].Server + "\n" + entries[0]
	}
	return entries
}

func fitEntries(b *strings.Builder, entries []string) int {
	lines := 0
	for i, e := range entries {
		lines += strings.Count(e, "\n")
		if i > 0 && (b.Len()+len(e) > maxListingBytes || lines > maxListingLines) {
			return i
		}
		b.WriteString(e)
	}
	return len(entries)
}

func emptyListing(a toolSearchArgs, l mcp.Listing) string {
	if a.Server != "" && a.Query == "" {
		return fmt.Sprintf("server %q has no enabled tools", a.Server)
	}
	if len(l.Errs) > 0 {
		return "no tools found"
	}
	return "no matching tools; try other keywords"
}

func writeNotices(b *strings.Builder, l mcp.Listing) {
	servers := make([]string, 0, len(l.Hidden))
	for s := range l.Hidden {
		servers = append(servers, s)
	}
	sort.Strings(servers)
	for _, s := range servers {
		fmt.Fprintf(b, "server %q: %d more tools on this server are disabled by the user; don't try to work around this.\n", s, l.Hidden[s])
	}
	for _, e := range l.Errs {
		fmt.Fprintf(b, "error: %v\n", e)
	}
}

func MCPFuncs(m *mcp.Manager) []*Function {
	search := toolSearch(m)
	call := &Function{
		Name: "mcp_call",
		Label: func(env *Env, in json.RawMessage) string {
			a, _ := decode[mcpCallArgs](in)
			return fmt.Sprintf("mcp %s.%s", a.Server, a.Tool)
		},
		Run: func(ctx context.Context, env *Env, in json.RawMessage, _ func(string)) Result {
			a, err := decode[mcpCallArgs](in)
			if err != nil {
				return errResult("%v", err)
			}
			if env.readOnly() {
				return deniedResult("read-only mode: MCP tools may have side effects and are disabled")
			}
			res, err := m.Call(ctx, a.Server, a.Tool, a.Arguments)
			if err != nil {
				return errResult("%v", err)
			}
			var b strings.Builder
			var images []llm.Block
			for _, c := range res.Content {
				switch c.Type {
				case "text":
					b.WriteString(c.Text + "\n")
				case "image":
					if img, err := mcpImage(env, c.MimeType, c.Data); err == nil {
						images = append(images, img.block)
						b.WriteString(img.note + "\n")
					} else {
						fmt.Fprintf(&b, "[image omitted: %s, %v]\n", c.MimeType, err)
					}
				default:
					fmt.Fprintf(&b, "[%s content]\n", c.Type)
				}
			}
			text := strings.TrimRight(b.String(), "\n")
			if text == "" {
				text = "(no output)"
			}
			out := Result{Text: text, IsError: res.IsError, Images: images, Summary: fmt.Sprintf("%d lines", strings.Count(text, "\n")+1)}
			if !out.IsError {
				out.Value = jsonValue(text)
			}
			return out
		},
		Code: CodeSpec{
			Name:     "mcp",
			Params:   []string{"server", "tool", "arguments"},
			Required: 3,
			Defaults: map[string]string{"arguments": "{}"},
			Doc:      "mcp(server, tool, args) → value — call an MCP tool found with tool_search; a JSON result comes back parsed, anything else as a string.",
		},
	}
	call.Unwrap, call.Rewrap = unwrapMCPCall, rewrapMCPCall
	return []*Function{search, call}
}

func jsonValue(text string) json.RawMessage {
	t := strings.TrimSpace(text)
	if (strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[")) && json.Valid([]byte(t)) {
		return json.RawMessage(t)
	}
	return nil
}

func unwrapMCPCall(input json.RawMessage) (string, json.RawMessage) {
	var a struct {
		Server    string          `json:"server"`
		Tool      string          `json:"tool"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if json.Unmarshal(input, &a) != nil {
		return "", nil
	}
	name := ""
	if a.Server != "" {
		name = "mcp__" + a.Server + "__" + a.Tool
	}
	if string(a.Arguments) == "null" {
		a.Arguments = nil
	}
	return name, a.Arguments
}

func rewrapMCPCall(input, args json.RawMessage) json.RawMessage {
	var orig map[string]json.RawMessage
	if json.Unmarshal(input, &orig) != nil {
		return args
	}
	orig["arguments"] = args
	out, err := json.Marshal(orig)
	if err != nil {
		return args
	}
	return out
}

func mcpImage(env *Env, mimeType, data string) (preparedImage, error) {
	if imageTypes["."+strings.TrimPrefix(mimeType, "image/")] == "" {
		return preparedImage{}, errors.New("unsupported image type")
	}
	if len(data)*3/4 > maxImageFile {
		return preparedImage{}, fmt.Errorf("%d bytes is too large", len(data)*3/4)
	}
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return preparedImage{}, fmt.Errorf("invalid base64: %w", err)
	}
	return prepareImage(raw, mimeType, env.saveImage(raw, mimeType), nil)
}

func (e *Env) saveImage(data []byte, mimeType string) string {
	path, err := e.Spill("")
	if err != nil {
		return ""
	}
	dst := strings.TrimSuffix(path, ".txt") + "." + strings.TrimPrefix(mimeType, "image/")
	if os.Rename(path, dst) != nil || os.WriteFile(dst, data, 0o600) != nil {
		return ""
	}
	return dst
}
