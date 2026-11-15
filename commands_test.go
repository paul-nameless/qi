package main

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/paul-nameless/aicode/internal/config"
	"github.com/paul-nameless/aicode/internal/mcp"
)

func TestExpandArgs(t *testing.T) {
	cases := []struct {
		body, args string
		positional bool
		want       string
	}{
		{"run awk '{print $1}'", "", false, "run awk '{print $1}'"},
		{"run awk '{print $1}'", "x y", false, "run awk '{print $1}'\n\nArguments: x y"},
		{"Review $ARGUMENTS now", "the diff", false, "Review the diff now"},
		{"Review $ARGUMENTS now", "", false, "Review  now"},
		{"cp $1 $2", "a b", true, "cp a b"},
		{"plain", "a b", true, "plain\n\nArguments: a b"},
	}
	for _, c := range cases {
		if got := expandArgs(c.body, c.args, c.positional); got != c.want {
			t.Errorf("expandArgs(%q, %q) = %q, want %q", c.body, c.args, got, c.want)
		}
	}
}

func TestPathTokens(t *testing.T) {
	got := pathTokens(`look at '/a b.png' and c\ d.jpg`)
	want := []string{"look", "at", "/a b.png", "and", "c d.jpg"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q", got)
	}
}

func TestMCPReport(t *testing.T) {
	got := mcpReport([]mcp.Status{
		{Name: "broken", State: mcp.Failed, Err: errors.New("start mcp server broken: no such file")},
		{Name: "github", State: mcp.Started, Tools: []string{"get_issue", "list_issues"}, Hidden: 40},
		{Name: "idle", State: mcp.NotStarted},
		{Name: "notes", State: mcp.Started},
		{Name: "slow", State: mcp.NotStarted},
		{Name: "stopped", State: mcp.NotStarted},
	}, map[string]error{"slow": context.DeadlineExceeded, "stopped": context.Canceled})
	want := "broken: failed: start mcp server broken: no such file\n" +
		"github: started · 2 tools: get_issue, list_issues · 40 hidden\n" +
		"idle: not started\n" +
		"notes: started · 0 tools\n" +
		"slow: timed out\n" +
		"stopped: cancelled"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestStartMCPReportsTimeoutsAndErrors(t *testing.T) {
	m := mcp.NewManager(map[string]config.MCPServer{
		"broken": {Command: "/nonexistent/mcp-server"},
		"slow":   {Command: "sleep", Args: []string{"30"}},
	})
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	var started []string
	errs := startMCP(ctx, m, func(s string) { started = append(started, s) })
	got := mcpReport(m.Status(), errs)
	if !reflect.DeepEqual(started, []string{"starting broken…", "starting slow…"}) {
		t.Errorf("progress: %q", started)
	}
	if !strings.Contains(got, "broken: failed: start mcp server broken") || !strings.Contains(got, "slow: timed out") {
		t.Errorf("report:\n%s", got)
	}
	if errs := startMCP(ctx, m, func(string) {}); !errors.Is(errs["broken"], context.DeadlineExceeded) {
		t.Errorf("expired context: %v", errs)
	}
}
