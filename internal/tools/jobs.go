package tools

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
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

	"github.com/paul-nameless/qi/internal/proc"
)

const (
	groupPoll       = 100 * time.Millisecond
	groupPollMax    = time.Second
	keepFinished    = 20
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
	start   time.Time
	cmd     *exec.Cmd
	cancel  context.CancelFunc
	gone    chan struct{}
	status  string
	killed  bool
}

type Notice struct {
	ID       int
	Status   string
	Text     string
	TimedOut bool
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
		if jb.status == "" {
			ids = append(ids, jb.id)
		}
	}
	return ids
}

func (j *Jobs) Running() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	n := 0
	for _, jb := range j.list {
		if !jb.isGone() {
			n++
		}
	}
	return n
}

func (j *Jobs) HasNotices() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.notices) > 0
}

func (j *Jobs) Exited() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, n := range j.notices {
		if !n.TimedOut {
			return true
		}
	}
	return false
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
		if !jb.isGone() {
			jb.markKilled()
			running = append(running, jb)
		}
	}
	j.mu.Unlock()
	stopJobs(running...)
	j.TakeNotices()
}

func (jb *job) markKilled() {
	jb.killed = jb.killed || jb.status == ""
}

func stopJobs(jobs ...*job) {
	for _, jb := range jobs {
		jb.cancel()
	}
	for _, jb := range jobs {
		<-jb.gone
	}
}

func (jb *job) isGone() bool {
	select {
	case <-jb.gone:
		return true
	default:
		return false
	}
}

func (j *Jobs) start(ctx context.Context, cmd *exec.Cmd, cancel context.CancelFunc, timeout time.Duration, command, dir string) (*job, error) {
	j.mu.Lock()
	j.seq++
	id := j.seq
	j.mu.Unlock()
	suffix := make([]byte, 3)
	_, _ = rand.Read(suffix)
	path := filepath.Join(dir, fmt.Sprintf("job-%d-%s-%s.log", id, time.Now().Format("20060102-150405"), hex.EncodeToString(suffix)))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	cmd.Stdout, cmd.Stderr = f, f
	if err := cmd.Start(); err != nil {
		f.Close()
		return nil, err
	}
	jb := &job{id: id, command: command, path: path, start: time.Now(), cmd: cmd, cancel: cancel, gone: make(chan struct{})}
	j.mu.Lock()
	j.list = append(pruneFinished(j.list), jb)
	j.mu.Unlock()
	go func() {
		err := cmd.Wait()
		f.Close()
		j.finish(jb, err, errors.Is(ctx.Err(), context.DeadlineExceeded), timeout)
		waitGroup(ctx, cmd.Process.Pid)
		cancel()
		close(jb.gone)
	}()
	return jb, nil
}

func pruneFinished(list []*job) []*job {
	gone := 0
	for _, jb := range list {
		if jb.isGone() {
			gone++
		}
	}
	if gone <= keepFinished {
		return list
	}
	out := list[:0]
	for _, jb := range list {
		if jb.isGone() && gone > keepFinished {
			gone--
			continue
		}
		out = append(out, jb)
	}
	return out
}

func waitGroup(ctx context.Context, pgid int) {
	poll := time.NewTimer(groupPoll)
	defer poll.Stop()
	for wait := groupPoll; proc.Alive(pgid); {
		select {
		case <-ctx.Done():
			_ = proc.Terminate(pgid, proc.Grace)
			for deadline := time.Now().Add(proc.Grace + time.Second); proc.Alive(pgid) && time.Now().Before(deadline); {
				time.Sleep(groupPoll)
			}
			return
		case <-poll.C:
			wait = min(2*wait, groupPollMax)
			poll.Reset(wait)
		}
	}
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
	j.mu.Lock()
	killed := jb.killed
	j.mu.Unlock()
	notice := ""
	if !killed {
		notice = jb.notice(status, !timedOut && proc.Alive(jb.cmd.Process.Pid), timeout)
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if jb.killed {
		jb.status = "killed"
		return
	}
	jb.status = status
	j.notices = append(j.notices, Notice{ID: jb.id, Status: status, Text: notice, TimedOut: timedOut})
	select {
	case j.changed <- struct{}{}:
	default:
	}
}

func (jb *job) notice(status string, leftovers bool, timeout time.Duration) string {
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
	if leftovers {
		fmt.Fprintf(&b, "processes it started are still running until the job's %s timeout; stop them sooner with jobs({kill: %d})\n", timeout, jb.id)
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
		lines += bytes.Count(buf[:n], []byte{'\n'})
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
		switch {
		case state == "":
			state = "running " + fmtDuration(time.Since(jb.start))
		case !jb.isGone():
			state += ", processes it started still running"
		}
		fmt.Fprintf(&b, "%d  %s  pid %d  %s\n    %s\n", jb.id, state, jb.cmd.Process.Pid, jb.path, OneLine(jb.command, 200))
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
	if target.isGone() {
		j.mu.Unlock()
		return fmt.Errorf("job %d already finished: %s", id, target.status)
	}
	target.markKilled()
	j.mu.Unlock()
	stopJobs(target)
	return nil
}

func startJob(env *Env, a bashArgs) Result {
	if env.Jobs == nil {
		return errResult("background jobs are not available here")
	}
	timeout := time.Duration(jobTimeout(a)) * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	cmd := bashCmd(ctx, env, a.Command)
	dir, err := env.outDir()
	if err != nil {
		cancel()
		return errResult("start background job: %v", err)
	}
	jb, err := env.Jobs.start(ctx, cmd, cancel, timeout, a.Command, dir)
	if err != nil {
		cancel()
		return errResult("start background job: %v", err)
	}
	text := fmt.Sprintf("Started background job %d (pid %d, timeout %s), output in %s. When it exits or times out you get a message with its status and output tail: don't poll or sleep for it; continue with other work or answer. Stop it with jobs({kill: %d}) when you no longer need it.", jb.id, cmd.Process.Pid, timeout, jb.path, jb.id)
	return Result{Text: text, Summary: fmt.Sprintf("job %d started", jb.id)}
}

const (
	defaultJobTimeout = 3600
	maxJobTimeout     = 86400
)

func jobTimeout(a bashArgs) int {
	if a.Timeout > 0 {
		return min(a.Timeout, maxJobTimeout)
	}
	return defaultJobTimeout
}

type jobsArgs struct {
	Kill int `json:"kill"`
}

func JobsFunc() *Function {
	return &Function{
		Name: "jobs",
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
		Code: CodeSpec{
			Name:    "jobs",
			Options: []string{"kill"},
			Doc:     "jobs({kill}) → string — list this session's background jobs, or stop one by its number: jobs({kill: 2}).",
		},
	}
}
