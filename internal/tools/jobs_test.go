package tools

import (
	"context"
	"encoding/json"
	"fmt"
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
	t.Parallel()
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

func TestBackgroundJobKilledOutsideNotifies(t *testing.T) {
	t.Parallel()
	env := jobEnv(t)
	res := runBash(context.Background(), env, bashIn(t, map[string]any{"command": "sleep 30", "background": true}), nil)
	if res.IsError || !strings.Contains(res.Text, "timeout 1h0m0s") {
		t.Fatalf("start: %+v", res)
	}
	if err := syscall.Kill(-env.Jobs.list[0].cmd.Process.Pid, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if n := waitNotice(t, env.Jobs); n.Status != "signal: terminated" || !strings.Contains(n.Text, `status="signal: terminated"`) {
		t.Fatalf("notice %+v", n)
	}
}

func TestJobsFuncKillsProcessGroup(t *testing.T) {
	t.Parallel()
	env := jobEnv(t)
	runBash(context.Background(), env, bashIn(t, map[string]any{"command": "sleep 30 & sleep 30", "background": true}), nil)
	pgid := env.Jobs.list[0].cmd.Process.Pid
	tool := JobsFunc()
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

func TestKillAllStopsJobs(t *testing.T) {
	t.Parallel()
	env := jobEnv(t)
	runBash(context.Background(), env, bashIn(t, map[string]any{"command": "sleep 30", "background": true, "timeout": 86400}), nil)
	pgid := env.Jobs.list[0].cmd.Process.Pid
	env.Jobs.KillAll()
	if syscall.Kill(-pgid, 0) == nil {
		t.Fatal("a job survived KillAll")
	}
	if env.Jobs.HasNotices() {
		t.Fatal("KillAll left a notice")
	}
}

func TestBackgroundJobTimeout(t *testing.T) {
	t.Parallel()
	env := jobEnv(t)
	runBash(context.Background(), env, bashIn(t, map[string]any{"command": "sleep 30", "background": true, "timeout": 1}), nil)
	if n := waitNotice(t, env.Jobs); n.Status != "timed out after 1s" {
		t.Fatalf("notice %+v", n)
	}
}

func TestReadOnlyRefusesBackgroundWrite(t *testing.T) {
	t.Parallel()
	env := jobEnv(t)
	env.ReadOnly = func() bool { return true }
	res := runBash(context.Background(), env, bashIn(t, map[string]any{"command": "rm -rf x", "background": true}), nil)
	if !res.IsError || !strings.Contains(res.Text, "read-only") || len(env.Jobs.list) != 0 {
		t.Fatalf("read-only background: %+v", res)
	}
}

func TestBackgroundTimeoutIsSeconds(t *testing.T) {
	t.Parallel()
	env := jobEnv(t)
	runBash(context.Background(), env, bashIn(t, map[string]any{"command": "sleep 30 > /dev/null 2>&1 & echo started", "background": true, "timeout": 1800}), nil)
	if n := waitNotice(t, env.Jobs); n.Status != "exit 0" || !strings.Contains(n.Text, "until the job's 30m0s timeout") {
		t.Fatalf("timeout 1800 must mean seconds: %+v", n)
	}
	if jobTimeout(bashArgs{Background: true}) != defaultJobTimeout || jobTimeout(bashArgs{Timeout: 999999}) != maxJobTimeout {
		t.Fatal("job timeout defaults")
	}
}

func TestLeftoverProcessesStayKillable(t *testing.T) {
	t.Parallel()
	env := jobEnv(t)
	start := time.Now()
	runBash(context.Background(), env, bashIn(t, map[string]any{"command": "sleep 30 > /dev/null 2>&1 & echo started", "background": true}), nil)
	n := waitNotice(t, env.Jobs)
	if n.Status != "exit 0" || !strings.Contains(n.Text, "still running") || time.Since(start) > 5*time.Second {
		t.Fatalf("notice should come at shell exit with its status: %+v after %s", n, time.Since(start))
	}
	if len(env.Jobs.Waiting()) != 0 || !strings.Contains(env.Jobs.Describe(), "still running") {
		t.Fatalf("leftovers must not hold the turn: %v %s", env.Jobs.Waiting(), env.Jobs.Describe())
	}
	env.Jobs.mu.Lock()
	pgid := env.Jobs.list[0].cmd.Process.Pid
	env.Jobs.mu.Unlock()
	if res := JobsFunc().Run(context.Background(), env, json.RawMessage(`{"kill":1}`), nil); res.IsError {
		t.Fatalf("kill: %+v", res)
	}
	if syscall.Kill(-pgid, 0) == nil {
		t.Fatal("leftover child survived kill")
	}
	if res := JobsFunc().Run(context.Background(), env, json.RawMessage(`{"kill":1}`), nil); !res.IsError {
		t.Fatalf("second kill: %+v", res)
	}
}

func TestJobLogNamesAreUnique(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a, b := &Env{Cwd: dir, SpillDir: dir, Jobs: NewJobs()}, &Env{Cwd: dir, SpillDir: dir, Jobs: NewJobs()}
	t.Cleanup(a.Jobs.KillAll)
	t.Cleanup(b.Jobs.KillAll)
	runBash(context.Background(), a, bashIn(t, map[string]any{"command": "true", "background": true}), nil)
	runBash(context.Background(), b, bashIn(t, map[string]any{"command": "true", "background": true}), nil)
	if a.Jobs.list[0].path == b.Jobs.list[0].path {
		t.Fatal("two processes' job 1 share a log file")
	}
}

func TestFinishedJobsArePruned(t *testing.T) {
	t.Parallel()
	env := jobEnv(t)
	for range keepFinished + 5 {
		if res := runBash(context.Background(), env, bashIn(t, map[string]any{"command": "true", "background": true}), nil); res.IsError {
			t.Fatal(res.Text)
		}
		waitNotice(t, env.Jobs)
		env.Jobs.mu.Lock()
		last := env.Jobs.list[len(env.Jobs.list)-1]
		env.Jobs.mu.Unlock()
		<-last.gone
	}
	res := runBash(context.Background(), env, bashIn(t, map[string]any{"command": "sleep 5", "background": true}), nil)
	if !strings.Contains(res.Text, fmt.Sprintf("Started background job %d", keepFinished+6)) {
		t.Fatalf("id reused: %s", res.Text)
	}
	env.Jobs.mu.Lock()
	n, first := len(env.Jobs.list), env.Jobs.list[0].id
	env.Jobs.mu.Unlock()
	if n != keepFinished+1 || first != 6 {
		t.Errorf("list has %d jobs starting at %d, want %d starting at 6", n, first, keepFinished+1)
	}
}
