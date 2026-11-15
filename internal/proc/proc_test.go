package proc

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func startGroup(t *testing.T, ctx context.Context, script string, grace time.Duration) (*exec.Cmd, string) {
	pidFile := filepath.Join(t.TempDir(), "child")
	cmd := exec.CommandContext(ctx, "sh", "-c", script, "sh", pidFile)
	Setup(cmd, Session, grace)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	return cmd, pidFile
}

func childPid(t *testing.T, pidFile string) int {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(pidFile); err == nil && strings.TrimSpace(string(data)) != "" {
			pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
			return pid
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("child never started")
	return 0
}

func gone(pid int) bool {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) != nil {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func TestCancelTerminatesTheGroup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cmd, pidFile := startGroup(t, ctx, `sleep 30 & echo $! > "$1"; wait`, Grace)
	child := childPid(t, pidFile)
	start := time.Now()
	cancel()
	_ = cmd.Wait()
	if !gone(child) {
		t.Errorf("grandchild %d survived", child)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("SIGTERM took %v", d)
	}
}

func TestCancelKillsWhatIgnoresSIGTERM(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cmd, pidFile := startGroup(t, ctx, `trap '' TERM; (trap '' TERM; sleep 30) & echo $! > "$1"; wait`, 300*time.Millisecond)
	child := childPid(t, pidFile)
	start := time.Now()
	cancel()
	_ = cmd.Wait()
	if !gone(child) {
		t.Errorf("grandchild %d ignoring SIGTERM survived", child)
	}
	if d := time.Since(start); d < 250*time.Millisecond || d > 3*time.Second {
		t.Errorf("escalation after %v, want the grace period", d)
	}
}

func TestStop(t *testing.T) {
	cmd, pidFile := startGroup(t, context.Background(), `trap '' TERM; echo $$ > "$1"; while :; do sleep 0.05; done`, 200*time.Millisecond)
	childPid(t, pidFile)
	start := time.Now()
	_ = Stop(cmd, 200*time.Millisecond)
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("stop took %v", d)
	}
	if err := Stop(&exec.Cmd{}, Grace); err != nil {
		t.Errorf("stop before start: %v", err)
	}
}
