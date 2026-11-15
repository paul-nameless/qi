package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/paul-nameless/aicode/internal/llm"
	"github.com/paul-nameless/aicode/internal/mcp"
)

type toolSearchArgs struct {
	Query string `json:"query"`
}

type mcpCallArgs struct {
	Server    string          `json:"server"`
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
}

func MCPTools(m *mcp.Manager) []*Tool {
	var servers strings.Builder
	for _, n := range m.Names() {
		fmt.Fprintf(&servers, "\n- %s: %s", n, m.Description(n))
	}
	search := &Tool{
		Def: llm.ToolDef{
			Name:        "tool_search",
			Description: "Find tools on the connected MCP servers by keywords. Returns matching tools with their input schemas; call them with mcp_call. Servers:" + servers.String(),
			Schema:      json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"Keywords, e.g. 'github pull request comments'"}},"required":["query"]}`),
		},
		Label: func(env *Env, in json.RawMessage) string {
			a, _ := decode[toolSearchArgs](in)
			return "tool_search " + OneLine(a.Query, 50)
		},
		Run: func(ctx context.Context, env *Env, in json.RawMessage, _ func(string)) Result {
			a, err := decode[toolSearchArgs](in)
			if err != nil {
				return errResult("%v", err)
			}
			found, errs := m.Search(ctx, a.Query, 10)
			var b strings.Builder
			for _, t := range found {
				fmt.Fprintf(&b, "server: %s\ntool: %s\ndescription: %s\ninput schema: %s\n\n", t.Server, t.Name, OneLine(t.Description, 400), string(t.InputSchema))
			}
			for _, e := range errs {
				fmt.Fprintf(&b, "error: %v\n", e)
			}
			if b.Len() == 0 {
				return Result{Text: "no matching tools; try other keywords", Summary: "no matches"}
			}
			return Result{Text: env.capOutput(b.String()), Summary: fmt.Sprintf("%d tools", len(found))}
		},
	}
	call := &Tool{
		Def: llm.ToolDef{
			Name:        "mcp_call",
			Description: "Call a tool on an MCP server. Find the server, tool name and argument schema with tool_search first.",
			Schema:      json.RawMessage(`{"type":"object","properties":{"server":{"type":"string"},"tool":{"type":"string"},"arguments":{"type":"object","description":"Arguments matching the tool's input schema"}},"required":["server","tool","arguments"]}`),
		},
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
				return errResult("read-only mode: MCP tools may have side effects and are disabled")
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
					if img, err := mcpImage(c.MimeType, c.Data); err == nil {
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
			return Result{Text: env.capOutput(text), IsError: res.IsError, Images: images, Summary: fmt.Sprintf("%d lines", strings.Count(text, "\n")+1)}
		},
	}
	return []*Tool{search, call}
}

func mcpImage(mimeType, data string) (preparedImage, error) {
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
	return prepareImage(raw, mimeType, "", nil)
}
