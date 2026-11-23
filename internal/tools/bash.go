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
	"time"

	"github.com/paul-nameless/qi/internal/jsvm"
	"github.com/paul-nameless/qi/internal/llm"
	"github.com/paul-nameless/qi/internal/proc"
)

type bashArgs struct {
	Command    string `json:"command"`
	Timeout    int    `json:"timeout"`
	Background bool   `json:"background"`
	Daemon     bool   `json:"daemon"`
}

func Bash() *Tool {
	return &Tool{
		HookNames: []string{"Bash"},
		Def: llm.ToolDef{
			Name:   "bash",
			Schema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"},"timeout":{"type":"integer"},"background":{"type":"boolean"},"daemon":{"type":"boolean"}},"required":["command"]}`),
		},
		Label: func(env *Env, in json.RawMessage) string {
			a, _ := decode[bashArgs](in)
			return "bash " + OneLine(a.Command, 70)
		},
		Run: runBash,
		Code: &CodeSpec{
			Name:   "bash",
			Params: []string{"command"},
			Doc:    "bash(command, {timeout, background, daemon}) → {output, exit_code, timed_out, truncated} — stdin is empty, output is stdout+stderr combined; never throws on a non-zero exit. output keeps the first and last 512 KB; truncated is true when the middle was dropped (redirect to a file for more). timeout in seconds (default 120, max 600). {background: true} starts a job for long builds and test runs and returns at once with a string naming the job id and its output file; you get a message when it exits, so don't poll or sleep for it. {daemon: true} is for servers and watchers that are not meant to exit. Use rg/fd/ls/sed for search and inspection.",
		},
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

func (w *progressWriter) truncated() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.dropped > 0
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

const sandboxProfile = `(version 1)(allow default)(deny file-write*)(allow file-write* (literal "/dev/null") (literal "/dev/zero") (literal "/dev/tty") (literal "/dev/dtracehelper") (regex #"^/dev/fd/") (subpath (param "TMP")))(deny network*)(deny process-exec (literal "/usr/bin/osascript") (literal "/usr/bin/open") (literal "/bin/launchctl") (literal "/usr/bin/defaults") (literal "/usr/bin/security") (literal "/usr/bin/sudo") (literal "/usr/bin/shortcuts") (subpath (param "TMP")))`

func readOnlySandbox(ctx context.Context, command string) *exec.Cmd {
	tmp, _ := filepath.EvalSymlinks(os.TempDir())
	return sandboxCmd(ctx, command, tmp)
}

type sandboxSpec struct {
	path       string
	isolateNet bool
}

var sandbox = sync.OnceValue(findSandbox)

func findSandbox() sandboxSpec {
	if runtime.GOOS == "darwin" {
		path, _ := exec.LookPath("sandbox-exec")
		return sandboxSpec{path, path != ""}
	}
	path, err := exec.LookPath("bwrap")
	if err != nil {
		return sandboxSpec{}
	}
	tmp, _ := filepath.EvalSymlinks(os.TempDir())
	for _, isolate := range []bool{true, false} {
		if exec.Command(path, bwrapArgs("true", tmp, execDenied(), isolate)...).Run() == nil {
			return sandboxSpec{path, isolate}
		}
	}
	return sandboxSpec{}
}

func sandboxCmd(ctx context.Context, command, tmp string) *exec.Cmd {
	sb := sandbox()
	switch {
	case sb.path == "":
		return exec.CommandContext(ctx, "bash", "-c", command)
	case runtime.GOOS == "darwin":
		return exec.CommandContext(ctx, sb.path, "-D", "TMP="+tmp, "-p", sandboxProfile, "bash", "-c", command)
	}
	return exec.CommandContext(ctx, sb.path, bwrapArgs(command, tmp, execDenied(), sb.isolateNet)...)
}

var deniedExecs = []string{
	"sudo", "su", "doas", "pkexec", "run0",
	"xdg-open", "gio", "gnome-open", "kde-open", "kde-open5", "exo-open",
	"systemctl", "loginctl", "systemd-run", "crontab", "at",
	"gsettings", "dconf", "secret-tool", "dbus-send", "gdbus", "busctl", "qdbus",
}

var execDirs = []string{"/usr/local/sbin", "/usr/local/bin", "/usr/sbin", "/usr/bin", "/sbin", "/bin", "/run/wrappers/bin", "/run/current-system/sw/bin"}

var multiCall = map[string]bool{"busybox": true, "toybox": true}

var execDenied = sync.OnceValue(func() []string { return deniedPaths(execDirs, deniedExecs) })

func deniedPaths(dirs, names []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, d := range dirs {
		for _, n := range names {
			p, err := filepath.EvalSymlinks(filepath.Join(d, n))
			if err != nil || seen[p] || multiCall[filepath.Base(p)] {
				continue
			}
			if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	return out
}

func bwrapArgs(command, tmp string, denied []string, isolateNet bool) []string {
	args := []string{"--ro-bind", "/", "/", "--dev", "/dev", "--proc", "/proc", "--bind", tmp, tmp}
	if isolateNet {
		args = append(args, "--unshare-net")
	}
	for _, p := range denied {
		args = append(args, "--ro-bind", "/dev/null", p)
	}
	return append(args, "--die-with-parent", "bash", "-c", command)
}

func HasSandbox() bool {
	return sandbox().path != ""
}

func SandboxIsolatesNetwork() bool {
	return sandbox().isolateNet
}

func readOnlyEnv() []string {
	return []string{
		"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=core.fsmonitor", "GIT_CONFIG_VALUE_0=",
		"GOCACHE=" + filepath.Join(os.TempDir(), "qi-readonly-gocache"), "GOFLAGS=-mod=readonly", "GOPROXY=off",
	}
}

func bashCmd(ctx context.Context, env *Env, command string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	if env.readOnly() {
		cmd = readOnlySandbox(ctx, command)
	}
	cmd.Dir = env.Cwd
	cmd.Env = append(os.Environ(), "PAGER=cat", "GIT_PAGER=cat", "GIT_TERMINAL_PROMPT=0", "TERM=dumb", "NO_COLOR=1", "CLICOLOR=0")
	if env.readOnly() {
		cmd.Env = append(cmd.Env, readOnlyEnv()...)
	}
	proc.Setup(cmd, proc.Session, proc.Grace)
	return cmd
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
			return deniedResult("read-only mode: %v", err)
		}
	}
	if a.Background || a.Daemon {
		return startJob(env, a)
	}
	run, err := execBash(ctx, env, a, progress)
	if err != nil {
		return errResult("%v", err)
	}
	return run.value()
}

type bashRun struct {
	output      string
	code        int
	timeout     int
	timedOut    bool
	interrupted bool
	truncated   bool
}

func execBash(ctx context.Context, env *Env, a bashArgs, progress func(string)) (bashRun, error) {
	timeout := bashTimeout(a.Timeout, env.BashTimeout)
	tctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	cmd := bashCmd(tctx, env, a.Command)
	out := &progressWriter{progress: progress}
	cmd.Stdout = out
	cmd.Stderr = out
	runErr := cmd.Run()
	run := bashRun{output: strings.TrimRight(out.String(), "\n"), timeout: timeout, truncated: out.truncated()}
	var exitErr *exec.ExitError
	switch {
	case runErr == nil:
	case errors.As(runErr, &exitErr):
		run.code = exitErr.ExitCode()
	case errors.Is(runErr, exec.ErrWaitDelay):
	default:
		if ctx.Err() == nil && tctx.Err() == nil {
			return run, runErr
		}
	}
	switch {
	case ctx.Err() != nil:
		run.interrupted = true
	case tctx.Err() != nil:
		run.timedOut, run.code = true, -1
	}
	return run, nil
}

func (r bashRun) lines() int {
	if r.output == "" {
		return 0
	}
	return strings.Count(r.output, "\n") + 1
}

func (r bashRun) summary() string {
	switch {
	case r.interrupted:
		return "interrupted"
	case r.timedOut:
		return fmt.Sprintf("timed out after %ds", r.timeout)
	case r.code != 0:
		return fmt.Sprintf("exit %d · %d lines", r.code, r.lines())
	}
	return fmt.Sprintf("%d lines", r.lines())
}

func (r bashRun) suffix() string {
	switch {
	case r.interrupted:
		return "\n[interrupted by user]"
	case r.timedOut:
		return fmt.Sprintf("\n[timed out after %ds; the process group was killed]", r.timeout)
	case r.code != 0:
		return fmt.Sprintf("\n[exit code %d]", r.code)
	}
	return ""
}

func (r bashRun) value() Result {
	text := r.output + r.suffix()
	if text == "" {
		text = "(no output)"
	}
	if r.interrupted {
		return Result{Text: text, IsError: true, Summary: r.summary()}
	}
	return Result{Text: text, JS: r.jsObject, IsError: r.code != 0, Summary: r.summary()}
}

func (r bashRun) jsObject(vm *jsvm.VM) jsvm.Value {
	obj := vm.NewObject()
	for _, p := range []struct {
		key string
		v   jsvm.Value
	}{
		{"output", jsvm.Str(r.output)},
		{"exit_code", jsvm.Number(float64(r.code))},
		{"timed_out", jsvm.Bool(r.timedOut)},
		{"truncated", jsvm.Bool(r.truncated)},
	} {
		_ = vm.SetProp(obj, p.key, p.v)
	}
	return obj
}
