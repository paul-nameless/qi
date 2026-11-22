package pool

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/paul-nameless/qi/internal/jsvm/bench/harness"
)

type tail struct {
	mu  sync.Mutex
	buf []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > 4096 {
		t.buf = t.buf[len(t.buf)-4096:]
	}
	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(bytes.TrimSpace(t.buf))
}

type proc struct {
	cmd    *exec.Cmd
	in     io.WriteCloser
	lines  chan []byte
	stderr *tail
}

func start(bin string) (*proc, error) {
	cmd := exec.Command(bin, "worker")
	cmd.Env = os.Environ()
	if os.Getenv("TZ") == "" {
		cmd.Env = append(cmd.Env, "TZ=UTC")
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	p := &proc{cmd: cmd, in: in, lines: make(chan []byte, 1), stderr: &tail{}}
	cmd.Stderr = p.stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", bin, err)
	}
	go func() {
		r := bufio.NewReaderSize(out, 1<<20)
		for {
			line, err := r.ReadBytes('\n')
			if len(line) > 0 {
				p.lines <- line
			}
			if err != nil {
				close(p.lines)
				return
			}
		}
	}()
	return p, nil
}

func (p *proc) kill() {
	_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
	_ = p.in.Close()
	_ = p.cmd.Wait()
}

func (p *proc) do(r harness.WorkReq, timeout time.Duration) (harness.WorkResp, bool) {
	b, _ := json.Marshal(r)
	if _, err := p.in.Write(append(b, '\n')); err != nil {
		return harness.WorkResp{ID: r.ID, Err: "crash: " + p.reason(err.Error())}, false
	}
	select {
	case line, ok := <-p.lines:
		if !ok {
			return harness.WorkResp{ID: r.ID, Err: "crash: " + p.reason("worker exited")}, false
		}
		var resp harness.WorkResp
		if err := json.Unmarshal(line, &resp); err != nil {
			return harness.WorkResp{ID: r.ID, Err: "crash: bad response: " + cutLine(line)}, false
		}
		if resp.ID != r.ID {
			return harness.WorkResp{ID: r.ID, Err: fmt.Sprintf("crash: response for %q while waiting for %q: %s", resp.ID, r.ID, cutLine(line))}, false
		}
		return resp, true
	case <-time.After(timeout):
		return harness.WorkResp{ID: r.ID, Err: fmt.Sprintf("hang: no answer in %s", timeout)}, false
	}
}

func cutLine(b []byte) string {
	s := string(bytes.TrimSpace(b))
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

func (p *proc) reason(def string) string {
	if s := p.stderr.String(); s != "" {
		return s
	}
	return def
}

func Run(bin string, workers int, reqs []harness.WorkReq, timeout time.Duration) ([]harness.WorkResp, error) {
	out := make([]harness.WorkResp, len(reqs))
	idx := make(chan int)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- serve(bin, reqs, out, idx, timeout)
		}()
	}
	for i := range reqs {
		idx <- i
	}
	close(idx)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

func serve(bin string, reqs []harness.WorkReq, out []harness.WorkResp, idx chan int, timeout time.Duration) error {
	var p *proc
	defer func() {
		if p != nil {
			p.kill()
		}
	}()
	for i := range idx {
		if p == nil {
			np, err := start(bin)
			if err != nil {
				out[i] = harness.WorkResp{ID: reqs[i].ID, Err: "crash: " + err.Error()}
				continue
			}
			p = np
		}
		resp, healthy := p.do(reqs[i], timeout)
		out[i] = resp
		if !healthy {
			p.kill()
			p = nil
		}
	}
	return nil
}

func Build(engine string) (string, error) {
	path := "bin/" + engine
	cmd := exec.Command("go", "build", "-ldflags=-s -w", "-o", path, "./eng/"+engine)
	if b, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build %s: %w\n%s", engine, err, b)
	}
	return path, nil
}
