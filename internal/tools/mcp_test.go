package tools

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/paul-nameless/aicode/internal/config"
	"github.com/paul-nameless/aicode/internal/llm"
	"github.com/paul-nameless/aicode/internal/mcp"
	"github.com/paul-nameless/aicode/internal/mcp/mcptest"
)

func TestMain(m *testing.M) {
	if os.Getenv(mcptest.Env) == "1" {
		mcptest.Serve(os.Stdin, os.Stdout)
		return
	}
	os.Exit(m.Run())
}

func fakeMCP(srv config.MCPServer) config.MCPServer {
	exe, _ := os.Executable()
	srv.Command = exe
	srv.Env = map[string]string{mcptest.Env: "1"}
	return srv
}

func runMCP(t *testing.T, r *Registry, name, input string) llm.Block {
	env := &Env{Cwd: t.TempDir()}
	return r.Execute(context.Background(), env, []llm.Block{{ID: "1", Name: name, Input: json.RawMessage(input)}}, nopUI{})[0]
}

func TestMCPCallRefusedInReadOnly(t *testing.T) {
	m := mcp.NewManager(map[string]config.MCPServer{"x": {Command: "/nonexistent"}})
	r := NewRegistry(MCPTools(m)...)
	env := &Env{Cwd: t.TempDir(), ReadOnly: func() bool { return true }}
	res := r.Execute(context.Background(), env, []llm.Block{{ID: "1", Name: "mcp_call", Input: json.RawMessage(`{"server":"x","tool":"t","arguments":{}}`)}}, nopUI{})
	if !res[0].IsError || !strings.Contains(res[0].Text, "read-only") {
		t.Errorf("got %+v", res[0])
	}
}

func TestToolSearchByServer(t *testing.T) {
	m := mcp.NewManager(map[string]config.MCPServer{"fake": fakeMCP(config.MCPServer{ExcludeTools: []string{"delete_*"}})})
	defer m.Close()
	r := NewRegistry(MCPTools(m)...)
	all := runMCP(t, r, "tool_search", `{"server":"fake"}`)
	for _, want := range []string{"tool: list_issues", "tool: get_issue", "tool: echo"} {
		if all.IsError || !strings.Contains(all.Text, want) {
			t.Errorf("listing lacks %q: %s", want, all.Text)
		}
	}
	if strings.Contains(all.Text, "delete_issue") {
		t.Errorf("hidden tool listed: %s", all.Text)
	}
	one := runMCP(t, r, "tool_search", `{"server":"fake","query":"echo"}`)
	if strings.Count(one.Text, "tool: ") != 1 || !strings.Contains(one.Text, "tool: echo") {
		t.Errorf("filtered listing: %s", one.Text)
	}
	if res := runMCP(t, r, "tool_search", `{}`); !res.IsError || !strings.Contains(res.Text, "give query, server or both") {
		t.Errorf("empty search: %+v", res)
	}
	if res := runMCP(t, r, "tool_search", `{"server":"nope"}`); !res.IsError || !strings.Contains(res.Text, `unknown mcp server "nope"`) {
		t.Errorf("unknown server: %+v", res)
	}
	if res := runMCP(t, r, "mcp_call", `{"server":"fake","tool":"delete_issue","arguments":{}}`); !res.IsError || !strings.Contains(res.Text, "is not enabled") {
		t.Errorf("hidden call: %+v", res)
	}
}

func TestToolSearchDescriptionListsExactTools(t *testing.T) {
	m := mcp.NewManager(map[string]config.MCPServer{
		"github": {Command: "gh", Description: "GitHub issues", Tools: []string{"get_issue", "list_issues", "delete_issue"}, ExcludeTools: []string{"delete_*"}},
		"notes":  {Command: "notes", Description: "Notes", Tools: []string{"*_note"}},
		"many":   {Command: "many", Tools: strings.Fields("a b c d e f g h i j k l m")},
		"bare":   {Command: "bare", Tools: []string{"one"}},
		"cut":    {Command: "cut", Tools: strings.Fields("a b c d e f g h i j k l m"), ExcludeTools: []string{"m"}},
	})
	desc := MCPTools(m)[0].Def.Description
	for _, want := range []string{"\n- github: GitHub issues (tools: get_issue, list_issues)\n", "\n- notes: Notes\n", "\n- many: \n", "\n- bare: (tools: one)", "\n- cut: \n"} {
		if !strings.Contains(desc+"\n", want) {
			t.Errorf("description lacks %q:\n%s", want, desc)
		}
	}
}

func TestMCPToolDefsByteStable(t *testing.T) {
	cfg := map[string]config.MCPServer{
		"fake":  fakeMCP(config.MCPServer{Description: "fake", Tools: []string{"echo", "list_issues"}}),
		"other": {Command: "/nonexistent", Description: "other"},
	}
	m := mcp.NewManager(cfg)
	defer m.Close()
	r := NewRegistry(MCPTools(m)...)
	before, _ := json.Marshal(r.Defs())
	if res := runMCP(t, r, "tool_search", `{"server":"fake"}`); res.IsError {
		t.Fatal(res.Text)
	}
	after, _ := json.Marshal(r.Defs())
	fresh, _ := json.Marshal(NewRegistry(MCPTools(mcp.NewManager(cfg))...).Defs())
	if string(before) != string(after) || string(before) != string(fresh) {
		t.Errorf("tool definitions changed:\n%s\n%s\n%s", before, after, fresh)
	}
}
