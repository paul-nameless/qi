package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/paul-nameless/qi/internal/llm"
)

const (
	noticeTailLines = 20
	noticeTailBytes = 2000
)

type Jobs struct {
	mu      sync.Mutex
	seq     int
	list    []*job
	notices []Notice
	changed chan struct{}
}

type job struct {
	id      int
	command string
	path    string
	daemon  bool
	nowait  bool
	start   time.Time
	cmd     *exec.Cmd
	cancel  context.CancelFunc
	done    chan struct{}
	status  string
	killed  bool
}

type Notice struct {
	ID     int
	Status string
	Text   string
}

func NewJobs() *Jobs {
	return &Jobs{changed: make(chan struct{}, 1)}
}

func (j *Jobs) Changed() <-chan struct{} { return j.changed }

func (j *Jobs) Waiting() []int {
	j.mu.Lock()
	defer j.mu.Unlock()
	var ids []int
	for _, jb := range j.list {
		if jb.status == "" && !jb.daemon && !jb.nowait {
			ids = append(ids, jb.id)
		}
	}
	return ids
}

func (j *Jobs) StopWaiting() {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, jb := range j.list {
		jb.nowait = true
	}
}

func (j *Jobs) HasNotices() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.notices) > 0
}

func (j *Jobs) TakeNotices() []Notice {
	j.mu.Lock()
	defer j.mu.Unlock()
	n := j.notices
	j.notices = nil
	return n
}

func (j *Jobs) KillAll() {
	j.mu.Lock()
	var running []*job
	for _, jb := range j.list {
		if jb.status == "" {
			jb.killed = true
			running = append(running, jb)
		}
	}
	j.mu.Unlock()
	for _, jb := range running {
		jb.cancel()
	}
	for _, jb := range running {
		<-jb.done
	}
	j.TakeNotices()
}

func (j *Jobs) start(ctx context.Context, cmd *exec.Cmd, cancel context.CancelFunc, timeout time.Duration, command, dir string, daemon bool) (*job, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	j.mu.Lock()
	j.seq++
	id := j.seq
	j.mu.Unlock()
	path := filepath.Join(dir, fmt.Sprintf("job-%d-%s.log", id, time.Now().Format("20060102-150405")))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	cmd.Stdout, cmd.Stderr = f, f
	if err := cmd.Start(); err != nil {
		f.Close()
		return nil, err
	}
	jb := &job{id: id, command: command, path: path, daemon: daemon, start: time.Now(), cmd: cmd, cancel: cancel, done: make(chan struct{})}
	j.mu.Lock()
	j.list = append(j.list, jb)
	j.mu.Unlock()
	go func() {
		err := cmd.Wait()
		f.Close()
		timedOut := errors.Is(ctx.Err(), context.DeadlineExceeded)
		cancel()
		j.finish(jb, err, timedOut, timeout)
	}()
	return jb, nil
}

func (j *Jobs) finish(jb *job, err error, timedOut bool, timeout time.Duration) {
	var exitErr *exec.ExitError
	status := "exit 0"
	switch {
	case timedOut:
		status = fmt.Sprintf("timed out after %s", timeout)
	case errors.As(err, &exitErr) && exitErr.ExitCode() >= 0:
		status = fmt.Sprintf("exit %d", exitErr.ExitCode())
	case errors.As(err, &exitErr):
		status = exitErr.String()
	case err != nil && !errors.Is(err, exec.ErrWaitDelay):
		status = err.Error()
	}
	notice := jb.notice(status)
	j.mu.Lock()
	if jb.killed {
		jb.status = "killed"
	} else {
		jb.status = status
		j.notices = append(j.notices, Notice{ID: jb.id, Status: status, Text: notice})
	}
	j.mu.Unlock()
	close(jb.done)
	select {
	case j.changed <- struct{}{}:
	default:
	}
}

func (jb *job) notice(status string) string {
	lines, tail := fileTail(jb.path)
	var b strings.Builder
	fmt.Fprintf(&b, "<background-job id=\"%d\" status=%q ran=%q>\ncommand: %s\n", jb.id, status, fmtDuration(time.Since(jb.start)), jb.command)
	switch {
	case lines == 0:
		fmt.Fprintf(&b, "output: none (%s)\n", jb.path)
	case lines <= noticeTailLines:
		fmt.Fprintf(&b, "output (%s):\n%s\n", jb.path, tail)
	default:
		fmt.Fprintf(&b, "output: %d lines in %s, last %d:\n%s\n", lines, jb.path, noticeTailLines, tail)
	}
	b.WriteString("</background-job>")
	return b.String()
}

func fileTail(path string) (int, string) {
	f, err := os.Open(path)
	if err != nil {
		return 0, ""
	}
	defer f.Close()
	lines, size := 0, int64(0)
	buf := make([]byte, 64<<10)
	var last byte
	for {
		n, err := f.Read(buf)
		for _, c := range buf[:n] {
			if c == '\n' {
				lines++
			}
		}
		if n > 0 {
			last = buf[n-1]
			size += int64(n)
		}
		if err != nil {
			break
		}
	}
	if size == 0 {
		return 0, ""
	}
	if last != '\n' {
		lines++
	}
	from := max(0, size-8*noticeTailBytes)
	chunk := make([]byte, size-from)
	if _, err := f.ReadAt(chunk, from); err != nil && err != io.EOF {
		return lines, ""
	}
	parts := strings.Split(strings.TrimRight(string(chunk), "\n"), "\n")
	if from > 0 && len(parts) > 1 {
		parts = parts[1:]
	}
	parts = parts[max(0, len(parts)-noticeTailLines):]
	tail := strings.Join(parts, "\n")
	if len(tail) > noticeTailBytes {
		tail = "…" + tail[len(tail)-noticeTailBytes:]
	}
	return lines, tail
}

func fmtDuration(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return strings.TrimSuffix(d.String(), "0s")
}

func (j *Jobs) Describe() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	if len(j.list) == 0 {
		return "no background jobs"
	}
	var b strings.Builder
	for _, jb := range j.list {
		state := jb.status
		if state == "" {
			state = "running " + fmtDuration(time.Since(jb.start))
		}
		kind := ""
		if jb.daemon {
			kind = " (daemon)"
		}
		fmt.Fprintf(&b, "%d  %s%s  pid %d  %s\n    %s\n", jb.id, state, kind, jb.cmd.Process.Pid, jb.path, OneLine(jb.command, 200))
	}
	return strings.TrimRight(b.String(), "\n")
}

func (j *Jobs) kill(id int) error {
	j.mu.Lock()
	var target *job
	for _, jb := range j.list {
		if jb.id == id {
			target = jb
		}
	}
	if target == nil {
		j.mu.Unlock()
		return fmt.Errorf("no background job %d", id)
	}
	if target.status != "" {
		j.mu.Unlock()
		return fmt.Errorf("job %d already finished: %s", id, target.status)
	}
	target.killed = true
	j.mu.Unlock()
	target.cancel()
	<-target.done
	return nil
}

func startJob(env *Env, a bashArgs) Result {
	if env.Jobs == nil {
		return errResult("background jobs are not available here")
	}
	var ctx context.Context
	var cancel context.CancelFunc
	timeout := time.Duration(jobTimeout(a)) * time.Second
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), timeout)
	} else {
		ctx, cancel = context.WithCancel(context.Background())
	}
	cmd := bashCmd(ctx, env, a.Command)
	jb, err := env.Jobs.start(ctx, cmd, cancel, timeout, a.Command, env.outDir(), a.Daemon)
	if err != nil {
		cancel()
		return errResult("start background job: %v", err)
	}
	wait := "When it exits you get a message with its status and output tail: don't poll or sleep for it. Continue with other work, or end your turn to wait."
	if a.Daemon {
		wait = "It runs as a daemon: your turn won't wait for it, and you get a message only if it exits."
	}
	text := fmt.Sprintf("Started background job %d (pid %d), output in %s. %s Stop it with the jobs tool.", jb.id, cmd.Process.Pid, jb.path, wait)
	return Result{Text: text, Summary: fmt.Sprintf("job %d started", jb.id)}
}

const (
	defaultJobTimeout = 3600
	maxJobTimeout     = 86400
)

func jobTimeout(a bashArgs) int {
	switch {
	case a.Timeout > 0:
		return min(a.Timeout, maxJobTimeout)
	case a.Daemon:
		return 0
	}
	return defaultJobTimeout
}

type jobsArgs struct {
	Kill int `json:"kill"`
}

func JobsTool() *Tool {
	return &Tool{
		Def: llm.ToolDef{
			Name:        "jobs",
			Description: "List the background bash jobs of this session with their state, pid and output file, or stop one with kill (its whole process group is terminated). Read a job's output with bash on its output file.",
			Schema:      json.RawMessage(`{"type":"object","properties":{"kill":{"type":"integer","description":"Id of a running job to stop"}}}`),
		},
		Label: func(env *Env, in json.RawMessage) string {
			a, _ := decode[jobsArgs](in)
			if a.Kill > 0 {
				return "jobs kill " + strconv.Itoa(a.Kill)
			}
			return "jobs"
		},
		Run: func(ctx context.Context, env *Env, in json.RawMessage, progress func(string)) Result {
			a, err := decode[jobsArgs](in)
			if err != nil {
				return errResult("%v", err)
			}
			if env.Jobs == nil {
				return errResult("background jobs are not available here")
			}
			if a.Kill > 0 {
				if err := env.Jobs.kill(a.Kill); err != nil {
					return errResult("%v", err)
				}
				return Result{Text: fmt.Sprintf("job %d killed", a.Kill), Summary: fmt.Sprintf("killed %d", a.Kill)}
			}
			text := env.Jobs.Describe()
			return Result{Text: text, Summary: strings.SplitN(text, "\n", 2)[0]}
		},
		ParallelSafe: true,
	}
}
