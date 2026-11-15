package mcptest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const Env = "FAKE_MCP"

const CallLogEnv = "FAKE_MCP_CALLS"

const BulkEnv = "FAKE_MCP_BULK"

const PidEnv = "FAKE_MCP_PID"

const LingerEnv = "FAKE_MCP_LINGER"

var page1 = []string{"list_issues", "get_issue", "delete_issue"}

var page2 = []string{"echo"}

var descriptions = map[string]string{
	"list_issues":  "List GitHub issues",
	"get_issue":    "Get one GitHub issue",
	"delete_issue": "Delete a GitHub issue",
	"echo":         "Echo text back",
}

type request struct {
	ID     *int64          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func Serve(in io.Reader, w io.Writer) {
	if p := os.Getenv(PidEnv); p != "" {
		_ = os.WriteFile(p, []byte(strconv.Itoa(os.Getpid())), 0o600)
	}
	defer linger()
	sc := bufio.NewScanner(in)
	out := json.NewEncoder(w)
	for sc.Scan() {
		var req request
		if json.Unmarshal(sc.Bytes(), &req) != nil || req.ID == nil {
			continue
		}
		if req.Method == "initialize" {
			_ = out.Encode(map[string]any{"jsonrpc": "2.0", "id": 99, "method": "roots/list"})
		}
		_ = out.Encode(respond(req))
	}
}

func linger() {
	if os.Getenv(LingerEnv) != "" {
		signal.Ignore(syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT)
		time.Sleep(time.Hour)
	}
}

func respond(req request) map[string]any {
	msg := map[string]any{"jsonrpc": "2.0", "id": *req.ID}
	switch req.Method {
	case "initialize":
		msg["result"] = map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "fake"}}
	case "tools/list":
		msg["result"] = listTools(req.Params)
	case "tools/call":
		result, err := callTool(req.Params)
		if err != nil {
			msg["error"] = map[string]any{"code": -32602, "message": err.Error()}
		} else {
			msg["result"] = result
		}
	}
	return msg
}

func listTools(params json.RawMessage) map[string]any {
	var p struct{ Cursor string }
	_ = json.Unmarshal(params, &p)
	if p.Cursor == "" {
		return map[string]any{"tools": toolDefs(page1), "nextCursor": "page2"}
	}
	return map[string]any{"tools": append(toolDefs(page2), bulkTools()...)}
}

func bulkTools() []any {
	n, _ := strconv.Atoi(os.Getenv(BulkEnv))
	var defs []any
	for i := range n {
		props := map[string]any{}
		for j := range 8 {
			props[fmt.Sprintf("field_%d", j)] = map[string]any{"type": "string", "description": strings.Repeat("padding ", 4)}
		}
		defs = append(defs, map[string]any{"name": fmt.Sprintf("bulk_%03d", i), "description": fmt.Sprintf("Bulk tool number %d", i), "inputSchema": map[string]any{"type": "object", "properties": props}})
	}
	return defs
}

func toolDefs(names []string) []any {
	var defs []any
	for _, n := range names {
		defs = append(defs, map[string]any{"name": n, "description": descriptions[n], "inputSchema": map[string]any{"type": "object"}})
	}
	return defs
}

func callTool(params json.RawMessage) (map[string]any, error) {
	var p struct {
		Name      string
		Arguments map[string]any
	}
	_ = json.Unmarshal(params, &p)
	logCall(p.Name)
	if !slices.Contains(page1, p.Name) && !slices.Contains(page2, p.Name) && !strings.HasPrefix(p.Name, "bulk_") {
		return nil, fmt.Errorf("fake server: no tool %s", p.Name)
	}
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": fmt.Sprintf("%s:%v", p.Name, p.Arguments["text"])}}}, nil
}

func logCall(name string) {
	path := os.Getenv(CallLogEnv)
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = fmt.Fprintln(f, name)
}
