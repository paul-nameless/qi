package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/paul-nameless/aicode/internal/config"
	"github.com/paul-nameless/aicode/internal/mcp/mcptest"
)

func TestMain(m *testing.M) {
	if os.Getenv(mcptest.Env) == "1" {
		mcptest.Serve(os.Stdin, os.Stdout)
		return
	}
	os.Exit(m.Run())
}

func fakeServer(t *testing.T, srv config.MCPServer) (config.MCPServer, string) {
	exe, _ := os.Executable()
	log := filepath.Join(t.TempDir(), "calls")
	srv.Command = exe
	srv.Env = map[string]string{mcptest.Env: "1", mcptest.CallLogEnv: log}
	return srv, log
}

func names(tools []Tool) []string {
	var out []string
	for _, t := range tools {
		out = append(out, t.Name)
	}
	return out
}

func TestManagerSearchAndCall(t *testing.T) {
	fake, _ := fakeServer(t, config.MCPServer{Description: "fake server"})
	m := NewManager(map[string]config.MCPServer{"fake": fake, "broken": {Command: "/nonexistent/mcp-server"}})
	defer m.Close()
	ctx := context.Background()
	found, errs := m.Search(ctx, "list issues", 10)
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

func TestManagerFiltersTools(t *testing.T) {
	fake, _ := fakeServer(t, config.MCPServer{Tools: []string{"*_issue*", "echo"}, ExcludeTools: []string{"delete_*"}})
	m := NewManager(map[string]config.MCPServer{"fake": fake})
	defer m.Close()
	ctx := context.Background()
	found, _ := m.Search(ctx, "issue", 10)
	if got := names(found); slices.Contains(got, "delete_issue") || !slices.Contains(got, "get_issue") {
		t.Errorf("search returned %v", got)
	}
	all, err := m.ServerTools(ctx, "fake", "")
	if got := names(all); err != nil || !slices.Equal(got, []string{"list_issues", "get_issue", "echo"}) {
		t.Errorf("server tools: %v %v", got, err)
	}
	if one, _ := m.ServerTools(ctx, "fake", "echo"); !slices.Equal(names(one), []string{"echo"}) {
		t.Errorf("server tools with query: %v", names(one))
	}
	st := m.Status()
	if len(st) != 1 || st[0].State != "started" || st[0].Hidden != 1 || len(st[0].Tools) != 3 {
		t.Errorf("status: %+v", st)
	}
}

func TestManagerCallRejectsWithoutRoundTrip(t *testing.T) {
	fake, log := fakeServer(t, config.MCPServer{Tools: []string{"*_issue*", "echo"}, ExcludeTools: []string{"delete_*"}})
	m := NewManager(map[string]config.MCPServer{"fake": fake})
	defer m.Close()
	ctx := context.Background()
	cases := map[string][]string{
		"delete_issue": {`tool "delete_issue" on server "fake" is not enabled`, "mcp.fake.tools / exclude_tools"},
		"other":        {`tool "other" on server "fake" is not enabled`},
		"nope_issue":   {`unknown tool "nope_issue" on server "fake"`, "use tool_search"},
	}
	for tool, parts := range cases {
		_, err := m.Call(ctx, "fake", tool, nil)
		for _, p := range parts {
			if err == nil || !strings.Contains(err.Error(), p) {
				t.Errorf("call %s: %v, want %q", tool, err, p)
			}
		}
	}
	if _, err := m.Call(ctx, "nope", "echo", nil); err == nil || !strings.Contains(err.Error(), `unknown mcp server "nope" (servers: fake)`) {
		t.Errorf("unknown server: %v", err)
	}
	if data, _ := os.ReadFile(log); len(data) != 0 {
		t.Errorf("rejected calls reached the server: %q", data)
	}
	if _, err := m.Call(ctx, "fake", "echo", nil); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(log); string(data) != "echo\n" {
		t.Errorf("call log: %q", data)
	}
}

func TestManagerStatus(t *testing.T) {
	fake, _ := fakeServer(t, config.MCPServer{ExcludeTools: []string{"delete_*", "get_*"}})
	m := NewManager(map[string]config.MCPServer{"fake": fake, "broken": {Command: "/nonexistent/mcp-server"}, "idle": {Command: "/nonexistent/idle"}})
	defer m.Close()
	ctx := context.Background()
	if err := m.Start(ctx, "fake"); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(ctx, "broken"); err == nil {
		t.Fatal("broken server started")
	}
	st := m.Status()
	if st[0].Name != "broken" || st[0].State != "failed" || st[0].Err == nil {
		t.Errorf("broken: %+v", st[0])
	}
	if st[1].Name != "fake" || st[1].State != "started" || !slices.Equal(st[1].Tools, []string{"list_issues", "echo"}) || st[1].Hidden != 2 {
		t.Errorf("fake: %+v", st[1])
	}
	if st[2].Name != "idle" || st[2].State != "not started" {
		t.Errorf("idle: %+v", st[2])
	}
}
