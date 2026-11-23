package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/paul-nameless/qi/internal/config"
	"github.com/paul-nameless/qi/internal/llm"
	"github.com/paul-nameless/qi/internal/mcp"
	"github.com/paul-nameless/qi/internal/mcp/mcptest"
	"github.com/paul-nameless/qi/internal/testenv"
)

func TestMain(m *testing.M) {
	if os.Getenv(mcptest.Env) == "1" {
		mcptest.Serve(os.Stdin, os.Stdout)
		return
	}
	os.Exit(testenv.Run(m))
}

func fakeMCP(srv config.MCPServer) config.MCPServer {
	exe, _ := os.Executable()
	srv.Command = exe
	if srv.Env == nil {
		srv.Env = map[string]string{}
	}
	srv.Env[mcptest.Env] = "1"
	return srv
}

const disabledNotice = `server "fake": 1 more tools on this server are disabled by the user; don't try to work around this.`

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
	if strings.Contains(all.Text, "delete_issue") || !strings.HasSuffix(all.Text, disabledNotice) || strings.Contains(all.Text, "shown") {
		t.Errorf("listing: %s", all.Text)
	}
	one := runMCP(t, r, "tool_search", `{"server":"fake","query":"echo"}`)
	if strings.Count(one.Text, "tool: ") != 1 || !strings.Contains(one.Text, "tool: echo") || !strings.HasSuffix(one.Text, disabledNotice) {
		t.Errorf("filtered listing: %s", one.Text)
	}
	if res := runMCP(t, r, "tool_search", `{"query":"delete issue"}`); !strings.HasSuffix(res.Text, disabledNotice) || strings.Contains(res.Text, "delete_issue") {
		t.Errorf("search: %s", res.Text)
	}
	if res := runMCP(t, r, "tool_search", `{}`); !res.IsError || !strings.Contains(res.Text, "give query, server or both") {
		t.Errorf("empty search: %+v", res)
	}
	if res := runMCP(t, r, "tool_search", `{"server":"nope"}`); !res.IsError || !strings.Contains(res.Text, `unknown mcp server "nope"`) {
		t.Errorf("unknown server: %+v", res)
	}
	if res := runMCP(t, r, "mcp_call", `{"server":"fake","tool":"delete_issue","arguments":{}}`); !res.IsError || !strings.Contains(res.Text, "is disabled by the user; don't try to work around this") {
		t.Errorf("hidden call: %+v", res)
	}
}

func TestToolSearchDocListsServers(t *testing.T) {
	m := mcp.NewManager(map[string]config.MCPServer{
		"github": {Command: "gh", Description: "GitHub issues", Tools: []string{"get_issue", "list_issues"}},
		"bare":   {Command: "bare"},
	})
	desc := MCPTools(m)[0].Code.Doc
	if !strings.HasSuffix(desc, "Servers:\n  - bare: \n  - github: GitHub issues") {
		t.Errorf("description:\n%s", desc)
	}
}

func TestToolSearchNoEnabledTools(t *testing.T) {
	m := mcp.NewManager(map[string]config.MCPServer{"fake": fakeMCP(config.MCPServer{Tools: []string{"nothing_*"}})})
	defer m.Close()
	res := runMCP(t, NewRegistry(MCPTools(m)...), "tool_search", `{"server":"fake"}`)
	want := "server \"fake\" has no enabled tools\nserver \"fake\": 4 more tools on this server are disabled by the user; don't try to work around this."
	if res.Text != want {
		t.Errorf("got:\n%s", res.Text)
	}
}

func TestToolSearchLargeServer(t *testing.T) {
	m := mcp.NewManager(map[string]config.MCPServer{"fake": fakeMCP(config.MCPServer{Env: map[string]string{mcptest.BulkEnv: "200"}})})
	defer m.Close()
	r := NewRegistry(MCPTools(m)...)
	list := runMCP(t, r, "tool_search", `{"server":"fake"}`)
	if strings.Contains(list.Text, "input schema:") || strings.Contains(list.Text, "omitted") || !strings.Contains(list.Text, "- bulk_000: Bulk tool number 0\n") {
		t.Errorf("compact listing: %.300s", list.Text)
	}
	if !strings.HasSuffix(list.Text, "shown 204 of 204; call tool_search with server and query=<tool name> for its input schema") {
		t.Errorf("listing footer: %s", list.Text[len(list.Text)-200:])
	}
	search := runMCP(t, r, "tool_search", `{"server":"fake","query":"bulk"}`)
	if strings.Count(search.Text, "input schema: ") != 10 || !strings.HasSuffix(search.Text, "shown 10 of 200 matching tools; refine the query to see the others") {
		t.Errorf("query listing: %s", search.Text[len(search.Text)-200:])
	}
	one := runMCP(t, r, "tool_search", `{"server":"fake","query":"bulk_150"}`)
	if !strings.Contains(one.Text, "tool: bulk_150\n") || strings.Contains(one.Text, "shown") {
		t.Errorf("exact query: %.300s", one.Text)
	}
}

func TestFitEntriesNeverCutsTheMiddle(t *testing.T) {
	entries := make([]string, 50)
	for i := range entries {
		entries[i] = strings.Repeat("x", 1000) + "\n"
	}
	var b strings.Builder
	if n := fitEntries(&b, entries); n != maxListingBytes/1001 || b.Len() != n*1001 {
		t.Errorf("fit %d entries, %d bytes", n, b.Len())
	}
	b.Reset()
	if n := fitEntries(&b, []string{strings.Repeat("y", maxListingBytes*2)}); n != 1 {
		t.Errorf("a single oversized entry must still be shown, got %d", n)
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

func TestToolSearchHugeServerReportsShown(t *testing.T) {
	m := mcp.NewManager(map[string]config.MCPServer{"fake": fakeMCP(config.MCPServer{Env: map[string]string{mcptest.BulkEnv: "500"}})})
	defer m.Close()
	res := runToolSearch(context.Background(), &Env{Cwd: t.TempDir()}, m, toolSearchArgs{Server: "fake"})
	shown := maxListingLines - 1
	footer := fmt.Sprintf("shown %d of 504; call tool_search with server and query=<tool name> for its input schema", shown)
	if strings.Contains(res.Text, "omitted") || !strings.HasSuffix(res.Text, footer) || res.Summary != fmt.Sprintf("%d of 504 tools", shown) {
		t.Errorf("summary %q, tail: %s", res.Summary, res.Text[len(res.Text)-200:])
	}
}
