package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/paul-nameless/aicode/internal/config"
)

func TestMain(m *testing.M) {
	if os.Getenv("FAKE_MCP") == "1" {
		fakeServer()
		return
	}
	os.Exit(m.Run())
}

func fakeServer() {
	sc := bufio.NewScanner(os.Stdin)
	out := json.NewEncoder(os.Stdout)
	for sc.Scan() {
		var req struct {
			ID     *int64          `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(sc.Bytes(), &req) != nil || req.ID == nil {
			continue
		}
		var result any
		switch req.Method {
		case "initialize":
			out.Encode(map[string]any{"jsonrpc": "2.0", "id": 99, "method": "roots/list"})
			result = map[string]any{"protocolVersion": protocolVersion, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "fake"}}
		case "tools/list":
			var p struct{ Cursor string }
			json.Unmarshal(req.Params, &p)
			if p.Cursor == "" {
				result = map[string]any{"tools": []any{map[string]any{"name": "list_issues", "description": "List GitHub issues", "inputSchema": map[string]any{"type": "object"}}}, "nextCursor": "page2"}
			} else {
				result = map[string]any{"tools": []any{map[string]any{"name": "echo", "description": "Echo text back", "inputSchema": map[string]any{"type": "object"}}}}
			}
		case "tools/call":
			var p struct {
				Name      string
				Arguments map[string]any
			}
			json.Unmarshal(req.Params, &p)
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": fmt.Sprintf("%s:%v", p.Name, p.Arguments["text"])}}}
		}
		out.Encode(map[string]any{"jsonrpc": "2.0", "id": *req.ID, "result": result})
	}
}

func TestManagerSearchAndCall(t *testing.T) {
	exe, _ := os.Executable()
	m := NewManager(map[string]config.MCPServer{
		"fake":   {Command: exe, Env: map[string]string{"FAKE_MCP": "1"}, Description: "fake server"},
		"broken": {Command: "/nonexistent/mcp-server"},
	})
	defer m.Close()
	ctx := context.Background()
	found, errs := m.Search(ctx, "github issues", 10)
	if len(found) != 1 || found[0].Name != "list_issues" || found[0].Server != "fake" {
		t.Fatalf("search: %+v", found)
	}
	if len(errs) != 1 {
		t.Errorf("expected one error for the broken server, got %v", errs)
	}
	if all, _ := m.Search(ctx, "echo", 10); len(all) != 1 || all[0].Name != "echo" {
		t.Errorf("paginated tool missing: %+v", all)
	}
	res, err := m.Call(ctx, "fake", "echo", json.RawMessage(`{"text":"hi"}`))
	if err != nil || len(res.Content) != 1 || res.Content[0].Text != "echo:hi" {
		t.Fatalf("call: %+v %v", res, err)
	}
}
