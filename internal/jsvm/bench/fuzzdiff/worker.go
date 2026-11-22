package main

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

type outcome struct {
	canon string
	fail  string
}

type tail struct {
	mu  sync.Mutex
	buf []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > 2048 {
		t.buf = t.buf[len(t.buf)-2048:]
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

func startProc(bin string) (*proc, error) {
	cmd := exec.Command(bin, "worker")
	cmd.Env = append(os.Environ(), "TZ=UTC")
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
	go p.read(out)
	return p, nil
}

func (p *proc) read(out io.Reader) {
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
}

func (p *proc) kill() {
	_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
	_ = p.in.Close()
	_ = p.cmd.Wait()
}

func (p *proc) do(id, code string, timeout time.Duration) (outcome, bool) {
	b, _ := json.Marshal(harness.WorkReq{ID: id, Kind: "ext", Code: code})
	if _, err := p.in.Write(append(b, '\n')); err != nil {
		return outcome{fail: "crash: " + p.reason(err.Error())}, false
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case line, ok := <-p.lines:
		if !ok {
			return outcome{fail: "crash: " + p.reason("worker exited")}, false
		}
		var resp harness.WorkResp
		if err := json.Unmarshal(line, &resp); err != nil || resp.ID != id {
			return outcome{fail: "crash: bad response"}, false
		}
		if resp.Err != "" {
			return outcome{fail: "error: " + resp.Err}, true
		}
		return outcome{canon: resp.Canon}, true
	case <-timer.C:
		return outcome{fail: fmt.Sprintf("hang: no answer in %s", timeout)}, false
	}
}

func (p *proc) reason(def string) string {
	if s := p.stderr.String(); s != "" {
		return s
	}
	return def
}

type engine struct {
	name    string
	bin     string
	timeout time.Duration
	free    chan *proc
	seq     int64
	mu      sync.Mutex
}

func newEngine(name, bin string, workers int, timeout time.Duration) *engine {
	e := &engine{name: name, bin: bin, timeout: timeout, free: make(chan *proc, workers)}
	for i := 0; i < workers; i++ {
		e.free <- nil
	}
	return e
}

func (e *engine) nextID() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.seq++
	return fmt.Sprint(e.seq)
}

func (e *engine) run(code string) outcome {
	p := <-e.free
	if p == nil {
		np, err := startProc(e.bin)
		if err != nil {
			e.free <- nil
			return outcome{fail: "crash: " + err.Error()}
		}
		p = np
	}
	out, healthy := p.do(e.nextID(), code, e.timeout)
	if !healthy {
		p.kill()
		p = nil
	}
	e.free <- p
	return out
}

func (e *engine) close() {
	for i := 0; i < cap(e.free); i++ {
		if p := <-e.free; p != nil {
			p.kill()
		}
	}
}
