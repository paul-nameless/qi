package agent

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

type SubagentRunner struct {
	ConfigPath string
	Profile    string
	Provider   string
	ReadOnly   func() bool
	Cwd        string
	mu         sync.Mutex
	running    map[int]context.CancelCauseFunc
	next       int
}

var ErrModeChanged = errors.New("stopped because read-only mode was switched on")

func (r *SubagentRunner) StopAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, cancel := range r.running {
		cancel(ErrModeChanged)
	}
}

func (r *SubagentRunner) Track(ctx context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(ctx)
	r.mu.Lock()
	if r.running == nil {
		r.running = map[int]context.CancelCauseFunc{}
	}
	id := r.next
	r.next++
	r.running[id] = cancel
	r.mu.Unlock()
	return ctx, func() {
		r.mu.Lock()
		delete(r.running, id)
		r.mu.Unlock()
		cancel(nil)
	}
}

func (r *SubagentRunner) args(prompt, model, provider, effort string) []string {
	args := []string{"-q", prompt, "-model", model}
	if provider == "" {
		provider = r.Provider
	}
	if provider != "" {
		args = append(args, "-provider", provider)
	}
	if effort != "" {
		args = append(args, "-effort", effort)
	}
	if r.ConfigPath != "" {
		args = append(args, "-config", r.ConfigPath)
	}
	if r.Profile != "" {
		args = append(args, "-p", r.Profile)
	}
	if r.ReadOnly() {
		args = append(args, "-read-only")
	}
	return args
}

func (r *SubagentRunner) Run(parent context.Context, prompt, model, provider, effort string, progress func(string)) (string, error) {
	ctx, done := r.Track(parent)
	defer done()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, exe, r.args(prompt, model, provider, effort)...)
	cmd.Dir = r.Cwd
	cmd.Env = append(os.Environ(), "AICODE_SUBAGENT=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error { return syscall.Kill(cmd.Process.Pid, syscall.SIGTERM) }
	cmd.WaitDelay = 5 * time.Second
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	var tail []string
	sc := bufio.NewScanner(stderr)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		progress(line)
		tail = append(tail, line)
		if len(tail) > 20 {
			tail = tail[1:]
		}
	}
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return "", context.Cause(ctx)
		}
		return "", fmt.Errorf("%v\n%s", err, strings.Join(tail, "\n"))
	}
	answer := strings.TrimSpace(stdout.String())
	if answer == "" {
		answer = "(the subagent returned no answer)"
	}
	return answer, nil
}
