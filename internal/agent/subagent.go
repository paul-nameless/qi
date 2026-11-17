package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/paul-nameless/qi/internal/config"
	"github.com/paul-nameless/qi/internal/llm"
	"github.com/paul-nameless/qi/internal/proc"
	"github.com/paul-nameless/qi/internal/session"
	"github.com/paul-nameless/qi/internal/tools"
)

const subagentGrace = 5 * time.Second

type SubagentRunner struct {
	ConfigPath       string
	Profile          string
	Provider         string
	SystemPromptFile string
	ReadOnly         func() bool
	Cwd              string
	MCP              map[string]config.MCPServer
	Label            func(llm.Block) string
	Exe              string
}

func (r *SubagentRunner) Env() ([]string, error) {
	servers, err := config.EncodeMCP(r.MCP)
	if err != nil {
		return nil, err
	}
	return append(os.Environ(), "QI_SUBAGENT=1", config.MCPEnv+"="+servers), nil
}

func (r *SubagentRunner) args(task tools.SubagentTask) []string {
	args := []string{"-q", task.Prompt, "-json", "-model", task.Model}
	provider := task.Provider
	if provider == "" {
		provider = r.Provider
	}
	if provider != "" {
		args = append(args, "-provider", provider)
	}
	if task.Effort != "" {
		args = append(args, "-effort", task.Effort)
	}
	if r.ConfigPath != "" {
		args = append(args, "-config", r.ConfigPath)
	}
	if r.Profile != "" {
		args = append(args, "-p", r.Profile)
	}
	if r.SystemPromptFile != "" {
		args = append(args, "-system-prompt-file", r.SystemPromptFile)
	}
	if r.ReadOnly() {
		args = append(args, "-read-only")
	}
	return args
}

func (r *SubagentRunner) Run(parent context.Context, task tools.SubagentTask, progress func(string)) (string, error) {
	ctx, cancel := context.WithTimeout(parent, 30*time.Minute)
	defer cancel()
	cmd, err := r.command(ctx, task)
	if err != nil {
		return "", err
	}
	stdout, out := io.Pipe()
	stderr := &lineTail{max: 20}
	cmd.Stdout, cmd.Stderr = out, stderr
	if err := cmd.Start(); err != nil {
		return "", err
	}
	read := make(chan entriesResult, 1)
	go func() { read <- r.readEntries(stdout, progress) }()
	waitErr := cmd.Wait()
	out.Close()
	res := <-read
	switch {
	case ctx.Err() != nil:
		return "", ctx.Err()
	case waitErr != nil && !errors.Is(waitErr, exec.ErrWaitDelay):
		return "", fmt.Errorf("%w\n%s", waitErr, stderr.String())
	case res.err != nil:
		return "", fmt.Errorf("read subagent output: %w", res.err)
	case res.answer == "":
		return "(the subagent returned no answer)", nil
	}
	return res.answer, nil
}

type entriesResult struct {
	answer string
	err    error
}

func (r *SubagentRunner) command(ctx context.Context, task tools.SubagentTask) (*exec.Cmd, error) {
	exe := r.Exe
	if exe == "" {
		var err error
		if exe, err = os.Executable(); err != nil {
			return nil, err
		}
	}
	env, err := r.Env()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, exe, r.args(task)...)
	cmd.Dir = r.Cwd
	cmd.Env = env
	proc.Setup(cmd, proc.Session, subagentGrace)
	return cmd, nil
}

func (r *SubagentRunner) readEntries(rd io.Reader, progress func(string)) entriesResult {
	sc := bufio.NewScanner(rd)
	sc.Buffer(make([]byte, 1<<20), 256<<20)
	answer := ""
	for sc.Scan() {
		var e session.Entry
		if json.Unmarshal(sc.Bytes(), &e) != nil || e.Type != "message" || e.Message == nil || e.Message.Role != llm.RoleAssistant {
			continue
		}
		for _, call := range e.Message.ToolUses() {
			progress(r.Label(call))
		}
		answer = strings.TrimSpace(e.Message.Text())
	}
	err := sc.Err()
	if err != nil {
		_, _ = io.Copy(io.Discard, rd)
	}
	return entriesResult{answer, err}
}

type lineTail struct {
	mu      sync.Mutex
	max     int
	lines   []string
	partial []byte
}

func (t *lineTail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.partial = append(t.partial, p...)
	for {
		i := bytes.IndexByte(t.partial, '\n')
		if i < 0 {
			break
		}
		t.add(string(t.partial[:i]))
		t.partial = t.partial[i+1:]
	}
	if len(t.partial) > 64<<10 {
		t.partial = t.partial[len(t.partial)-64<<10:]
	}
	return len(p), nil
}

func (t *lineTail) add(line string) {
	if line = strings.TrimSpace(line); line == "" {
		return
	}
	t.lines = append(t.lines, line)
	if len(t.lines) > t.max {
		t.lines = t.lines[1:]
	}
}

func (t *lineTail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	lines := t.lines
	if rest := strings.TrimSpace(string(t.partial)); rest != "" {
		lines = append(append([]string{}, lines...), rest)
	}
	return strings.Join(lines, "\n")
}
