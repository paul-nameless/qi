package agent

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/paul-nameless/qi/internal/config"
	"github.com/paul-nameless/qi/internal/proc"
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
}

func (r *SubagentRunner) Env() ([]string, error) {
	servers, err := config.EncodeMCP(r.MCP)
	if err != nil {
		return nil, err
	}
	return append(os.Environ(), "QI_SUBAGENT=1", config.MCPEnv+"="+servers), nil
}

func (r *SubagentRunner) args(task tools.SubagentTask) []string {
	args := []string{"-q", task.Prompt, "-model", task.Model}
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
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	env, err := r.Env()
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, exe, r.args(task)...)
	cmd.Dir = r.Cwd
	cmd.Env = env
	proc.Setup(cmd, proc.Session, subagentGrace)
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
	scanErr := sc.Err()
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("%w\n%s", err, strings.Join(tail, "\n"))
	}
	if scanErr != nil {
		return "", fmt.Errorf("read subagent output: %w", scanErr)
	}
	answer := strings.TrimSpace(stdout.String())
	if answer == "" {
		answer = "(the subagent returned no answer)"
	}
	return answer, nil
}
