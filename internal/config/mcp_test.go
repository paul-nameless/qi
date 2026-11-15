package config

import (
	"os"
	"reflect"
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
