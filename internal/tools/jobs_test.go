package tools

import (
	"context"
	"encoding/json"
	"strings"
	"syscall"
	"testing"
	"time"
)

func bashIn(t *testing.T, args map[string]any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func jobEnv(t *testing.T) *Env {
	env := &Env{Cwd: t.TempDir(), SpillDir: t.TempDir(), Jobs: NewJobs()}
	t.Cleanup(env.Jobs.KillAll)
	return env
}

func waitNotice(t *testing.T, j *Jobs) Notice {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		if n := j.TakeNotices(); len(n) > 0 {
			return n[0]
		}
		select {
		case <-j.Changed():
		case <-deadline:
			t.Fatal("no job notice")
		}
	}
}

func TestBackgroundJobReturnsAtOnceAndNotifies(t *testing.T) {
	env := jobEnv(t)
	start := time.Now()
	res := runBash(context.Background(), env, bashIn(t, map[string]any{"command": "sleep 0.3; seq 1 30; exit 3", "background": true}), nil)
	if res.IsError || !strings.Contains(res.Text, "Started background job 1") || time.Since(start) > 2*time.Second {
		t.Fatalf("start: %+v after %s", res, time.Since(start))
	}
	if ids := env.Jobs.Waiting(); len(ids) != 1 || ids[0] != 1 {
		t.Fatalf("waiting %v", ids)
	}
	n := waitNotice(t, env.Jobs)
	if n.ID != 1 || n.Status != "exit 3" || !strings.HasPrefix(n.Text, "<background-job") || !strings.Contains(n.Text, "30 lines") || !strings.Contains(n.Text, "\n30\n") || strings.Contains(n.Text, "\n10\n") {
		t.Fatalf("notice %+v", n)
	}
	if len(env.Jobs.Waiting()) != 0 {
		t.Fatal("finished job still waited for")
	}
}

func TestDaemonJobIsNotWaitedFor(t *testing.T) {
	env := jobEnv(t)
	res := runBash(context.Background(), env, bashIn(t, map[string]any{"command": "sleep 30", "daemon": true}), nil)
	if res.IsError || !strings.Contains(res.Text, "daemon") || len(env.Jobs.Waiting()) != 0 {
		t.Fatalf("daemon: %+v waiting %v", res, env.Jobs.Waiting())
	}
}

func TestJobsToolKillsProcessGroup(t *testing.T) {
	env := jobEnv(t)
	runBash(context.Background(), env, bashIn(t, map[string]any{"command": "sleep 30 & sleep 30", "background": true}), nil)
	pgid := env.Jobs.list[0].cmd.Process.Pid
	tool := JobsTool()
	if res := tool.Run(context.Background(), env, json.RawMessage(`{}`), nil); !strings.Contains(res.Text, "running") {
		t.Fatalf("list: %+v", res)
	}
	if res := tool.Run(context.Background(), env, json.RawMessage(`{"kill":1}`), nil); res.IsError {
		t.Fatalf("kill: %+v", res)
	}
	if syscall.Kill(-pgid, 0) == nil {
		t.Fatal("process group still alive after kill")
	}
	if env.Jobs.HasNotices() {
		t.Fatal("a killed job should not send a notice")
	}
	if res := tool.Run(context.Background(), env, json.RawMessage(`{"kill":1}`), nil); !res.IsError {
		t.Fatalf("second kill: %+v", res)
	}
}

func TestKillAllStopsDaemons(t *testing.T) {
	env := jobEnv(t)
	runBash(context.Background(), env, bashIn(t, map[string]any{"command": "sleep 30", "daemon": true}), nil)
	pgid := env.Jobs.list[0].cmd.Process.Pid
	env.Jobs.KillAll()
	if syscall.Kill(-pgid, 0) == nil {
		t.Fatal("daemon survived KillAll")
	}
}

func TestBackgroundJobTimeout(t *testing.T) {
	env := jobEnv(t)
	runBash(context.Background(), env, bashIn(t, map[string]any{"command": "sleep 30", "background": true, "timeout": 1}), nil)
	if n := waitNotice(t, env.Jobs); n.Status != "timed out after 1s" {
		t.Fatalf("notice %+v", n)
	}
}

func TestReadOnlyRefusesBackgroundWrite(t *testing.T) {
	env := jobEnv(t)
	env.ReadOnly = func() bool { return true }
	res := runBash(context.Background(), env, bashIn(t, map[string]any{"command": "rm -rf x", "background": true}), nil)
	if !res.IsError || !strings.Contains(res.Text, "read-only") || len(env.Jobs.list) != 0 {
		t.Fatalf("read-only background: %+v", res)
	}
}

func TestBackgroundTimeoutIsSeconds(t *testing.T) {
	env := jobEnv(t)
	runBash(context.Background(), env, bashIn(t, map[string]any{"command": "sleep 30", "background": true, "timeout": 1800}), nil)
	time.Sleep(2500 * time.Millisecond)
	if len(env.Jobs.Waiting()) != 1 || env.Jobs.HasNotices() {
		t.Fatalf("timeout 1800 must mean seconds: %s", env.Jobs.Describe())
	}
	if jobTimeout(bashArgs{Background: true}) != defaultJobTimeout || jobTimeout(bashArgs{Daemon: true}) != 0 || jobTimeout(bashArgs{Timeout: 999999}) != maxJobTimeout {
		t.Fatal("job timeout defaults")
	}
}
