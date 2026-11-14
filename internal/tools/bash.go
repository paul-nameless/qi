package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/paul-nameless/aicode/internal/llm"
)

type bashArgs struct {
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
}

func Bash() *Tool {
	return &Tool{
		Def: llm.ToolDef{
			Name:        "bash",
			Description: "Run a shell command with bash in the working directory. stdin is empty, output is stdout+stderr combined, long output is truncated to head and tail with the full text saved to a file. Use rg/fd/ls/cat/sed for search and inspection.",
			Schema:      json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"},"timeout":{"type":"integer","description":"Timeout in seconds (default 120, max 600)"}},"required":["command"]}`),
		},
		Label: func(env *Env, in json.RawMessage) string {
			a, _ := decode[bashArgs](in)
			return "bash " + OneLine(a.Command, 70)
		},
		Run: runBash,
	}
}

const keepBytes = 512 << 10

type progressWriter struct {
	mu       sync.Mutex
	head     []byte
	tail     []byte
	dropped  int
	progress func(string)
	last     time.Time
}

func (w *progressWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(p)
	if room := keepBytes - len(w.head); room > 0 {
		k := min(room, len(p))
		w.head = append(w.head, p[:k]...)
		p = p[k:]
	}
	if len(p) > 0 {
		w.tail = append(w.tail, p...)
		if over := len(w.tail) - keepBytes; over > 0 {
			w.dropped += over
			w.tail = append(w.tail[:0], w.tail[over:]...)
		}
	}
	if w.progress != nil && time.Since(w.last) > 150*time.Millisecond {
		w.last = time.Now()
		recent := w.tail
		if len(recent) == 0 {
			recent = w.head
		}
		w.progress(string(recent[max(0, len(recent)-512):]))
	}
	return n, nil
}

func (w *progressWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.dropped == 0 {
		return string(w.head) + string(w.tail)
	}
	return string(w.head) + fmt.Sprintf("\n… [%d bytes dropped] …\n", w.dropped) + string(w.tail)
}

func bashTimeout(requested, configured int) int {
	switch {
	case requested >= 1000:
		requested = (requested + 999) / 1000
	case requested <= 0:
		requested = configured
	}
	if requested <= 0 {
		requested = 120
	}
	return min(requested, 600)
}

const sandboxProfile = `(version 1)(allow default)(deny file-write*)(allow file-write* (literal "/dev/null") (literal "/dev/zero") (literal "/dev/tty") (literal "/dev/dtracehelper") (regex #"^/dev/fd/") (subpath (param "TMP")))(deny process-exec (literal "/usr/bin/osascript") (literal "/usr/bin/open") (literal "/bin/launchctl") (literal "/usr/bin/defaults") (literal "/usr/bin/security") (literal "/usr/bin/sudo") (literal "/usr/bin/shortcuts") (subpath (param "TMP")))`

func readOnlySandbox(ctx context.Context, command string) *exec.Cmd {
	tmp, _ := filepath.EvalSymlinks(os.TempDir())
	return sandboxCmd(ctx, command, tmp)
}

func sandboxCmd(ctx context.Context, command, tmp string) *exec.Cmd {
	if runtime.GOOS == "darwin" {
		if sbx, err := exec.LookPath("sandbox-exec"); err == nil {
			return exec.CommandContext(ctx, sbx, "-D", "TMP="+tmp, "-p", sandboxProfile, "bash", "-c", command)
		}
	}
	if bwrap, err := exec.LookPath("bwrap"); err == nil {
		return exec.CommandContext(ctx, bwrap, "--ro-bind", "/", "/", "--dev", "/dev", "--proc", "/proc", "--bind", tmp, tmp, "--die-with-parent", "bash", "-c", command)
	}
	return exec.CommandContext(ctx, "bash", "-c", command)
}

func HasSandbox() bool {
	if runtime.GOOS == "darwin" {
		_, err := exec.LookPath("sandbox-exec")
		return err == nil
	}
	_, err := exec.LookPath("bwrap")
	return err == nil
}

func readOnlyEnv() []string {
	return []string{
		"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=core.fsmonitor", "GIT_CONFIG_VALUE_0=",
		"GOCACHE=" + filepath.Join(os.TempDir(), "aicode-readonly-gocache"), "GOFLAGS=-mod=readonly", "GOPROXY=off",
	}
}

func runBash(ctx context.Context, env *Env, in json.RawMessage, progress func(string)) Result {
	a, err := decode[bashArgs](in)
	if err != nil {
		return errResult("%v", err)
	}
	if strings.TrimSpace(a.Command) == "" {
		return errResult("command is required")
	}
	if env.readOnly() {
		if err := CheckReadOnly(a.Command, env.Cwd, env.Allow); err != nil {
			return errResult("read-only mode: %v", err)
		}
	}
	timeout := bashTimeout(a.Timeout, env.BashTimeout)
	tctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(tctx, "bash", "-c", a.Command)
	if env.readOnly() {
		cmd = readOnlySandbox(tctx, a.Command)
	}
	cmd.Dir = env.Cwd
	cmd.Env = append(os.Environ(), "PAGER=cat", "GIT_PAGER=cat", "GIT_TERMINAL_PROMPT=0", "TERM=dumb", "NO_COLOR=1", "CLICOLOR=0")
	if env.readOnly() {
		cmd.Env = append(cmd.Env, readOnlyEnv()...)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second
	out := &progressWriter{progress: progress}
	cmd.Stdout = out
	cmd.Stderr = out
	runErr := cmd.Run()
	text := strings.TrimRight(out.String(), "\n")
	code := 0
	var exitErr *exec.ExitError
	switch {
	case runErr == nil:
	case errors.As(runErr, &exitErr):
		code = exitErr.ExitCode()
	case errors.Is(runErr, exec.ErrWaitDelay):
	default:
		if ctx.Err() == nil && tctx.Err() == nil {
			return errResult("%v", runErr)
		}
	}
	lines := 0
	if text != "" {
		lines = strings.Count(text, "\n") + 1
	}
	text = env.capOutput(text)
	summary := fmt.Sprintf("%d lines", lines)
	switch {
	case ctx.Err() != nil:
		text += "\n[interrupted by user]"
		summary = "interrupted"
	case tctx.Err() != nil:
		text += fmt.Sprintf("\n[timed out after %ds; the process group was killed]", timeout)
		summary = fmt.Sprintf("timed out after %ds", timeout)
		code = -1
	case code != 0:
		text += fmt.Sprintf("\n[exit code %d]", code)
		summary = fmt.Sprintf("exit %d · %d lines", code, lines)
	}
	if text == "" {
		text = "(no output)"
	}
	return Result{Text: text, IsError: code != 0 || ctx.Err() != nil, Summary: summary}
}
