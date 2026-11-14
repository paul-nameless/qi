package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/paul-nameless/aicode/internal/config"
	"github.com/paul-nameless/aicode/internal/llm"
	"github.com/paul-nameless/aicode/internal/mcp"
)

func TestMCPCallRefusedInReadOnly(t *testing.T) {
	m := mcp.NewManager(map[string]config.MCPServer{"x": {Command: "/nonexistent"}})
	r := NewRegistry(MCPTools(m)...)
	env := &Env{Cwd: t.TempDir(), ReadOnly: func() bool { return true }}
	res := r.Execute(context.Background(), env, []llm.Block{{ID: "1", Name: "mcp_call", Input: json.RawMessage(`{"server":"x","tool":"t","arguments":{}}`)}}, nopUI{})
	if !res[0].IsError || !strings.Contains(res[0].Text, "read-only") {
		t.Errorf("got %+v", res[0])
	}
}
