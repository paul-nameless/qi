package app

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/paul-nameless/qi/internal/config"
	"github.com/paul-nameless/qi/internal/mcp"
	"github.com/paul-nameless/qi/internal/mcp/mcptest"
	"github.com/paul-nameless/qi/internal/session"
)

func TestQuitStopsMCPServersThatIgnoreEOF(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	pidFile := filepath.Join(t.TempDir(), "pid")
	manager := mcp.NewManager(map[string]config.MCPServer{"fake": {Command: exe, Env: map[string]string{mcptest.Env: "1", mcptest.PidEnv: pidFile, mcptest.LingerEnv: "1"}}})
	if err := manager.Start(context.Background(), "fake"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(pidFile)
	pid, _ := strconv.Atoi(string(data))
	if err != nil || pid == 0 {
		t.Fatalf("no server pid: %v", err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })
	a := newTestAgent(t, config.Config{})
	s, err := session.Create(session.CreateOptions{Cwd: t.TempDir(), Model: "m", Kind: session.Interactive})
	if err != nil {
		t.Fatal(err)
	}
	a.Reset(s)
	r := &repl{agent: a, display: devNullPrinter(t), mcp: manager}
	start := time.Now()
	r.quit()
	if alive(pid) {
		t.Errorf("MCP server %d still running after quit", pid)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("quit took %v", d)
	}
	if err := s.Compaction("x", nil, "m"); err == nil {
		t.Error("session still open after quit")
	}
}

func alive(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	out, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	return err != nil || !containsZombie(string(out))
}

func containsZombie(stat string) bool {
	for i := len(stat) - 1; i > 0; i-- {
		if stat[i] == ')' {
			return len(stat) > i+2 && stat[i+2] == 'Z'
		}
	}
	return false
}
