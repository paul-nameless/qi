package config

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestMCPToolEnabled(t *testing.T) {
	cases := []struct {
		srv     MCPServer
		enabled []string
		hidden  []string
	}{
		{MCPServer{}, []string{"anything", "delete_repo"}, nil},
		{MCPServer{Tools: []string{"*_note", "list_notes"}}, []string{"read_note", "list_notes"}, []string{"purge_all", "list_note_ids"}},
		{MCPServer{ExcludeTools: []string{"delete_*"}}, []string{"read_note"}, []string{"delete_note"}},
		{MCPServer{Tools: []string{"*_note"}, ExcludeTools: []string{"delete_*"}}, []string{"write_note"}, []string{"delete_note", "purge_all"}},
		{MCPServer{Tools: []string{"get_?ssue", "[lr]ist_*"}}, []string{"get_issue", "list_x", "rist_y"}, []string{"get_iissue", "mist_x"}},
		{MCPServer{ExcludeTools: []string{"*delete*"}}, []string{"admin/list_users"}, []string{"admin/delete_user", "a/b/delete"}},
		{MCPServer{Tools: []string{"admin/*"}}, []string{"admin/x", "admin/x/y"}, []string{"user/x"}},
		{MCPServer{Tools: []string{"get_?"}}, []string{"get_/"}, []string{"get_ab"}},
		{MCPServer{ExcludeTools: []string{"delete_*"}}, []string{"Delete_note", "DELETE_NOTE"}, []string{"delete_note"}},
		{MCPServer{Tools: []string{"[^d]*"}}, []string{"read_note"}, []string{"delete_note"}},
	}
	for _, c := range cases {
		for _, n := range c.enabled {
			if !c.srv.ToolEnabled(n) {
				t.Errorf("%+v: %s should be enabled", c.srv, n)
			}
		}
		for _, n := range c.hidden {
			if c.srv.ToolEnabled(n) {
				t.Errorf("%+v: %s should be hidden", c.srv, n)
			}
		}
	}
}

func TestMCPFromEnv(t *testing.T) {
	want := map[string]MCPServer{"gh": {Command: "gh"}, "notes": {Command: "notes", Env: map[string]string{"K": "v"}, Tools: []string{"*_note"}, ExcludeTools: []string{"delete_*"}}}
	raw, err := EncodeMCP(want)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(MCPEnv, raw)
	got, ok, err := MCPFromEnv()
	if err != nil || !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v %v %v", got, ok, err)
	}
	if _, set := os.LookupEnv(MCPEnv); set {
		t.Error("env var left for child processes")
	}
	if _, ok, _ := MCPFromEnv(); ok {
		t.Error("unset env var reported as set")
	}
	empty, _ := EncodeMCP(nil)
	t.Setenv(MCPEnv, empty)
	if got, ok, err := MCPFromEnv(); err != nil || !ok || len(got) != 0 {
		t.Errorf("empty: %+v %v %v", got, ok, err)
	}
	t.Setenv(MCPEnv, `{"x": {"command": "x", "tools": ["[a"]}}`)
	if _, _, err := MCPFromEnv(); err == nil {
		t.Error("invalid pattern accepted")
	}
}

const mcpProfiles = `{
  "mcp": {"github": {"command": "gh", "tools": ["get_*"]}, "notes": {"command": "notes"}},
  "profiles": {
    "plain": {"model": "m"},
    "adds": {"mcp": {"db": {"command": "db"}}},
    "narrows": {"mcp": {"github": {"command": "gh2", "tools": ["get_issue"]}}},
    "drops": {"mcp": {"notes": null}},
    "none": {"mcp": null},
    "empty": {"mcp": {}},
    "bad": {"mcp": {"slack": null}},
    "badentry": {"mcp": {"db": {"command": "db", "tools": []}}}
  }
}`

func mcpNames(c Config) []string {
	return sortedKeys(c.MCP)
}

func TestProfileMCPMerge(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("QI_CONFIG_DIR", dir)
	path := writeConfig(t, dir, "config.json", mcpProfiles)
	writeConfig(t, dir, "fileadds.json", `{"mcp": {"db": {"command": "db"}, "notes": null}}`)
	cases := map[string][]string{
		"":         {"github", "notes"},
		"plain":    {"github", "notes"},
		"adds":     {"db", "github", "notes"},
		"narrows":  {"github", "notes"},
		"drops":    {"github"},
		"none":     nil,
		"empty":    {"github", "notes"},
		"fileadds": {"db", "github"},
	}
	for profile, want := range cases {
		c, err := Load(path, profile)
		if err != nil {
			t.Errorf("profile %q: %v", profile, err)
			continue
		}
		if got := mcpNames(c); !reflect.DeepEqual(got, want) && (len(got) > 0 || len(want) > 0) {
			t.Errorf("profile %q: servers %v, want %v", profile, got, want)
		}
	}
	narrows, _ := Load(path, "narrows")
	if g := narrows.MCP["github"]; g.Command != "gh2" || !reflect.DeepEqual(g.Tools, []string{"get_issue"}) || narrows.MCP["notes"].Command != "notes" {
		t.Errorf("narrows: %+v", narrows.MCP)
	}
	if none, _ := Load(path, "none"); none.MCP != nil {
		t.Errorf(`"mcp": null must remove every server: %+v`, none.MCP)
	}
	global, _ := Load(path, "")
	if g := global.MCP["github"]; g.Command != "gh" || !reflect.DeepEqual(g.Tools, []string{"get_*"}) {
		t.Errorf("global github changed: %+v", g)
	}
}

func TestProfileMCPErrors(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("QI_CONFIG_DIR", dir)
	path := writeConfig(t, dir, "config.json", mcpProfiles)
	_, err := Load(path, "bad")
	wantErr(t, err, path, `profile "bad": key "mcp.slack": no global MCP server "slack" to remove`)
	_, err = Load(path, "badentry")
	wantErr(t, err, `profile "badentry"`, `key "mcp.db.tools": empty list`)
	file := writeConfig(t, dir, "filebad.json", `{"mcp": {"slack": null}}`)
	_, err = Load(path, "filebad")
	wantErr(t, err, "profile file "+file, `key "mcp.slack": no global MCP server "slack" to remove`)
	top := writeConfig(t, dir, "top.json", `{"mcp": {"notes": null}}`)
	_, err = Load(top, "")
	wantErr(t, err, top, `key "mcp.notes": null removes a global server and is only allowed in a profile`)
}

func TestGlobalMCPErrorsBlameTheConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("QI_CONFIG_DIR", dir)
	path := writeConfig(t, dir, "config.json", `{"mcp": {"gh": {"command": "gh", "tools": ["[a"]}}, "default_profile": "p", "profiles": {"p": {"mcp": {"gh": {"command": "gh"}}}}}`)
	_, err := Load(path, "")
	wantErr(t, err, "config "+path+`: key "mcp.gh.tools.0"`)
	if err != nil && strings.Contains(err.Error(), "profile") {
		t.Errorf("global error blamed on a profile: %v", err)
	}
	path = writeConfig(t, dir, "config.json", `{"mcp": {"gh": {"command": "gh", "tools": ["get_*"]}}, "profiles": {"p": {"mcp": {"gh": {"tools": ["get_issue"]}}}}}`)
	_, err = Load(path, "p")
	wantErr(t, err, `profile "p": key "mcp.gh": command is required; a profile entry replaces the whole server; repeat command and the other fields`)
}
