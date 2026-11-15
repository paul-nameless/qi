package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/paul-nameless/aicode/internal/llm"
	"github.com/paul-nameless/aicode/internal/mcp"
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

const maxListedTools = 12

func serverLines(m *mcp.Manager) string {
	var b strings.Builder
	for _, n := range m.Names() {
		srv := m.Server(n)
		line := srv.Description
		if names := srv.ExactTools(); len(names) > 0 && len(names) <= maxListedTools {
			line = strings.TrimSpace(line + " (tools: " + strings.Join(names, ", ") + ")")
		}
		fmt.Fprintf(&b, "\n- %s: %s", n, line)
	}
	return b.String()
}

func toolSearch(m *mcp.Manager) *Tool {
	return &Tool{
		Def: llm.ToolDef{
			Name:        "tool_search",
			Description: "Find tools on the connected MCP servers by keywords, or list one server's tools with server. Returns matching tools with their input schemas; call them with mcp_call. Servers:" + serverLines(m),
			Schema:      json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"Keywords, e.g. 'github pull request comments'. Optional when server is set"},"server":{"type":"string","description":"Only this server's tools; without query, lists all of them"}}}`),
		},
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
	}
}

func runToolSearch(ctx context.Context, env *Env, m *mcp.Manager, a toolSearchArgs) Result {
	var found []mcp.Tool
	var errs []error
	switch {
	case a.Server != "":
		tools, err := m.ServerTools(ctx, a.Server, a.Query)
		if err != nil {
			return errResult("%v", err)
		}
		found = tools
	case strings.TrimSpace(a.Query) != "":
		found, errs = m.Search(ctx, a.Query, 10)
	default:
		return errResult("give query, server or both")
	}
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
}

func MCPTools(m *mcp.Manager) []*Tool {
	search := toolSearch(m)
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
			return Result{Text: env.capOutput(text), IsError: res.IsError, Images: images, Summary: fmt.Sprintf("%d lines", strings.Count(text, "\n")+1)}
		},
	}
	return []*Tool{search, call}
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
	path := e.spill("")
	if strings.HasPrefix(path, "(") {
		return ""
	}
	dst := strings.TrimSuffix(path, ".txt") + "." + strings.TrimPrefix(mimeType, "image/")
	if os.Rename(path, dst) != nil || os.WriteFile(dst, data, 0o600) != nil {
		return ""
	}
	return dst
}
