package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/paul-nameless/qi/internal/config"
	"github.com/paul-nameless/qi/internal/llm"
	"github.com/paul-nameless/qi/internal/proc"
	"github.com/paul-nameless/qi/internal/session"
	"github.com/paul-nameless/qi/internal/tools"
)

const subagentGrace = 5 * time.Second

type SubagentRunner struct {
	ConfigPath string
	Profile    string
	Provider   string
	ReadOnly   func() bool
	Cwd        string
	MCP        map[string]config.MCPServer
	Label      func(llm.Block) string
	Exe        string
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
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	tail := make(chan []string, 1)
	go func() { tail <- lastLines(stderr, 20) }()
	answer, readErr := r.readEntries(stdout, progress)
	lines := <-tail
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("%w\n%s", err, strings.Join(lines, "\n"))
	}
	if readErr != nil {
		return "", fmt.Errorf("read subagent output: %w", readErr)
	}
	if answer == "" {
		answer = "(the subagent returned no answer)"
	}
	return answer, nil
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

func (r *SubagentRunner) readEntries(rd io.Reader, progress func(string)) (string, error) {
	sc := bufio.NewScanner(rd)
	sc.Buffer(make([]byte, 1<<20), 256<<20)
	answer := ""
	for sc.Scan() {
		var e session.Entry
		if json.Unmarshal(sc.Bytes(), &e) != nil || e.Type != "message" || e.Message == nil || e.Message.Role != llm.RoleAssistant {
			continue
		}
		for _, call := range e.Message.ToolUses() {
			progress(r.label(call))
		}
		answer = strings.TrimSpace(e.Message.Text())
	}
	err := sc.Err()
	if err != nil {
		_, _ = io.Copy(io.Discard, rd)
	}
	return answer, err
}

func (r *SubagentRunner) label(call llm.Block) string {
	if r.Label == nil {
		return call.Name
	}
	return r.Label(call)
}

func lastLines(rd io.Reader, n int) []string {
	var tail []string
	sc := bufio.NewScanner(rd)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			tail = append(tail, line)
			if len(tail) > n {
				tail = tail[1:]
			}
		}
	}
	_, _ = io.Copy(io.Discard, rd)
	return tail
}
