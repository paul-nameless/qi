package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func bigIssues(n int) any {
	states := []string{"Todo", "In Progress", "Done", "Backlog", "Canceled"}
	names := []string{"ana", "bo", "cy", "ana"}
	nodes := make([]map[string]any, n)
	for i := range nodes {
		nodes[i] = map[string]any{
			"id": fmt.Sprintf("uuid-%06d", i), "identifier": fmt.Sprintf("SER-%d", 1000+i), "title": fmt.Sprintf("Issue number %d about %s", i, []string{"auth", "billing", "search", "ui"}[i%4]),
			"description": strings.Repeat("Lorem ipsum dolor sit amet, consectetur adipiscing elit. ", 8+i%5),
			"state":       map[string]any{"name": states[i%5], "type": "x"}, "priority": (i / 5) % 5, "estimate": i % 8,
			"assignee": map[string]any{"name": names[i%4]}, "labels": []string{"a", "b"}[:i%3%2+1],
		}
	}
	return map[string]any{"nodes": nodes, "pageInfo": map[string]any{"hasNextPage": false}}
}

func resolve(p string) string {
	if strings.HasPrefix(p, "/") {
		return p
	}
	return toolDir + "/" + p
}

func num(a []json.RawMessage, i int) int {
	n := 0
	if i < len(a) {
		_ = json.Unmarshal(a[i], &n)
	}
	return n
}

func str(a []json.RawMessage, i int) string {
	if i >= len(a) {
		return ""
	}
	var s string
	if json.Unmarshal(a[i], &s) != nil {
		return string(a[i])
	}
	return s
}

var toolDir = "."

func fakeTools(name string, a []json.RawMessage) (any, error) {
	switch name {
	case "sleep":
		var ms int
		if len(a) > 0 {
			_ = json.Unmarshal(a[0], &ms)
		}
		time.Sleep(time.Duration(ms) * time.Millisecond)
		return ms, nil
	case "read":
		b, err := os.ReadFile(resolve(str(a, 0)))
		if err != nil {
			return nil, err
		}
		off, lim := num(a, 1), num(a, 2)
		if off <= 1 && lim <= 0 {
			return string(b), nil
		}
		lines := strings.SplitAfter(string(b), "\n")
		if off > 1 {
			if off-1 >= len(lines) {
				return "", nil
			}
			lines = lines[off-1:]
		}
		if lim > 0 && lim < len(lines) {
			lines = lines[:lim]
		}
		return strings.Join(lines, ""), nil
	case "write":
		p := resolve(str(a, 0))
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(str(a, 1)), 0o644); err != nil {
			return nil, err
		}
		return fmt.Sprintf("wrote %d bytes to %s", len(str(a, 1)), str(a, 0)), nil
	case "edit":
		p := resolve(str(a, 0))
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		if str(a, 1) == "" {
			return nil, errors.New("edit: old is empty")
		}
		if n := strings.Count(string(b), str(a, 1)); n != 1 {
			return nil, fmt.Errorf("edit: old occurs %d times in %s, must occur exactly once", n, str(a, 0))
		}
		return "edited " + str(a, 0), os.WriteFile(p, []byte(strings.Replace(string(b), str(a, 1), str(a, 2), 1)), 0o644)
	case "bash":
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "bash", "-c", str(a, 0))
		cmd.Dir = toolDir
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
		cmd.WaitDelay = time.Second
		out, err := cmd.CombinedOutput()
		if ctx.Err() != nil {
			return map[string]any{"output": string(out) + "\n[timed out after 60s]", "exit_code": -1}, nil
		}
		code := 0
		if err != nil {
			code = 1
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				code = ee.ExitCode()
			}
		}
		return map[string]any{"output": string(out), "exit_code": code}, nil
	case "mcp":
		if str(a, 0) != "linear" || str(a, 1) != "list_issues" {
			return nil, fmt.Errorf("unknown MCP tool %s/%s; available: linear/list_issues {team: string}", str(a, 0), str(a, 1))
		}
		n := 300
		if len(a) > 2 {
			var m map[string]any
			if json.Unmarshal(a[2], &m) == nil {
				if f, ok := m["n"].(float64); ok {
					n = int(f)
				}
			}
		}
		return bigIssues(n), nil
	case "fail":
		return nil, errors.New("boom: " + str(a, 0))
	}
	return nil, fmt.Errorf("unknown tool %q", name)
}
