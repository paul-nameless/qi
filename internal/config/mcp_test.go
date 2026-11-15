package config

import (
	"slices"
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

func TestMCPExactTools(t *testing.T) {
	cases := []struct {
		srv  MCPServer
		want []string
	}{
		{MCPServer{}, nil},
		{MCPServer{Tools: []string{"get_issue", "list_issues"}}, []string{"get_issue", "list_issues"}},
		{MCPServer{Tools: []string{"get_issue", "list_*"}}, nil},
		{MCPServer{Tools: []string{"get_issue", "delete_issue"}, ExcludeTools: []string{"delete_*"}}, []string{"get_issue"}},
	}
	for _, c := range cases {
		if got := c.srv.ExactTools(); !slices.Equal(got, c.want) {
			t.Errorf("%+v: got %v, want %v", c.srv, got, c.want)
		}
	}
}
