package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/paul-nameless/qi/internal/config"
)

const projectExecRules = `(deny process-exec (literal "/usr/bin/osascript") (literal "/usr/bin/open") (literal "/bin/launchctl") (literal "/usr/bin/defaults") (literal "/usr/bin/security") (literal "/usr/bin/sudo") (literal "/usr/bin/shortcuts"))`

type projectSpec struct {
	writable []string
	guarded  []string
	sockets  []string
}

func projectProfile(spec projectSpec) string {
	var b strings.Builder
	b.WriteString(`(version 1)(allow default)(deny file-write*)(allow file-write* (literal "/dev/null") (literal "/dev/zero") (literal "/dev/tty") (literal "/dev/ptmx") (literal "/dev/dtracehelper") (regex #"^/dev/fd/") (regex #"^/dev/ttys")`)
	for i := range spec.writable {
		fmt.Fprintf(&b, ` (subpath (param "W%d"))`, i)
	}
	b.WriteString(")")
	if len(spec.guarded) > 0 {
		b.WriteString("(deny file-write*")
		for i := range spec.guarded {
			fmt.Fprintf(&b, ` (subpath (param "G%d"))`, i)
		}
		b.WriteString(")")
	}
	b.WriteString(projectExecRules)
	b.WriteString(`(deny network-outbound (remote unix-socket))(allow network-outbound (remote unix-socket (path-literal "/private/var/run/mDNSResponder"))`)
	for i := range spec.writable {
		fmt.Fprintf(&b, ` (remote unix-socket (subpath (param "W%d")))`, i)
	}
	for i := range spec.sockets {
		fmt.Fprintf(&b, ` (remote unix-socket (path-literal (param "S%d")))`, i)
	}
	b.WriteString(")")
	return b.String()
}

func projectSandbox(ctx context.Context, command string, spec projectSpec) *exec.Cmd {
	sb := sandbox()
	switch {
	case sb.path == "":
		return exec.CommandContext(ctx, "bash", "-c", command)
	case runtime.GOOS == "darwin":
		var args []string
		for i, r := range spec.writable {
			args = append(args, "-D", fmt.Sprintf("W%d=%s", i, r))
		}
		for i, g := range spec.guarded {
			args = append(args, "-D", fmt.Sprintf("G%d=%s", i, g))
		}
		for i, s := range spec.sockets {
			args = append(args, "-D", fmt.Sprintf("S%d=%s", i, s))
		}
		args = append(args, "-p", projectProfile(spec), "bash", "-c", command)
		return exec.CommandContext(ctx, sb.path, args...)
	}
	return exec.CommandContext(ctx, sb.path, projectBwrapArgs(command, spec)...)
}

func projectBwrapArgs(command string, spec projectSpec) []string {
	var writable []string
	for _, w := range spec.writable {
		if _, err := os.Stat(w); err == nil {
			writable = append(writable, w)
		}
	}
	args := bwrapArgs(command, writable, execDenied(), false)
	prefix := append([]string{}, args[:len(args)-4]...)
	for _, g := range spec.guarded {
		if _, err := os.Stat(g); err == nil {
			prefix = append(prefix, "--ro-bind", g, g)
		}
	}
	return append(append(prefix, "--unshare-pid"), args[len(args)-4:]...)
}

var goModCache = sync.OnceValue(func() string {
	if d := os.Getenv("GOMODCACHE"); d != "" {
		return d
	}
	if out, err := exec.Command("go", "env", "GOMODCACHE").Output(); err == nil && strings.TrimSpace(string(out)) != "" {
		return strings.TrimSpace(string(out))
	}
	return filepath.Join(config.Home(), "go", "pkg", "mod")
})

func cacheDirs() []string {
	home := config.Home()
	cargo := os.Getenv("CARGO_HOME")
	if cargo == "" {
		cargo = filepath.Join(home, ".cargo")
	}
	return []string{
		goModCache(), filepath.Join(home, ".npm"),
		filepath.Join(cargo, "registry"), filepath.Join(cargo, "git"),
		filepath.Join(cargo, ".package-cache"), filepath.Join(cargo, ".global-cache"),
	}
}

func (e *Env) scratch() string {
	if e.Scratch == nil {
		return ""
	}
	return e.Scratch()
}

func (e *Env) gitDir() string {
	out, err := exec.Command("git", "-C", e.Cwd, "-c", "core.fsmonitor=", "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func projectGoCache(cwd string) string {
	base, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	sum := sha256.Sum256([]byte(cwd))
	return filepath.Join(base, "qi", "gocache", hex.EncodeToString(sum[:8]))
}

func (e *Env) projectSpec() projectSpec {
	roots := append([]string{e.Cwd, filepath.Join(config.Home(), ".Trash")}, cacheDirs()...)
	if tmp := e.scratch(); tmp != "" {
		roots = append(roots, tmp)
	}
	if gc := projectGoCache(e.Cwd); gc != "" {
		roots = append(roots, gc)
	}
	for _, p := range e.Writable {
		roots = append(roots, config.ExpandHome(p))
	}
	gitDirs := []string{filepath.Join(e.Cwd, ".git")}
	if g := e.gitDir(); g != "" {
		roots = append(roots, g)
		gitDirs = append(gitDirs, g)
	}
	var spec projectSpec
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		spec.sockets = append(spec.sockets, realPath(sock))
	}
	for _, r := range roots {
		spec.writable = append(spec.writable, realPath(r))
	}
	for _, g := range gitDirs {
		g = realPath(g)
		spec.guarded = append(spec.guarded, filepath.Join(g, "config"), filepath.Join(g, "hooks"))
	}
	return spec
}

func (e *Env) projectEnv() []string {
	env := []string{"TMUX="}
	if tmp := e.scratch(); tmp != "" {
		env = append(env, "TMPDIR="+tmp)
	}
	if gc := projectGoCache(e.Cwd); gc != "" {
		env = append(env, "GOCACHE="+gc)
	}
	return env
}

const maxLinks = 40

func realPath(p string) string {
	for range maxLinks {
		fi, err := os.Lstat(p)
		if err != nil || fi.Mode()&os.ModeSymlink == 0 {
			break
		}
		target, err := os.Readlink(p)
		if err != nil {
			break
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(p), target)
		}
		p = filepath.Clean(target)
	}
	rest := ""
	for {
		if real, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Join(real, rest)
		}
		parent := filepath.Dir(p)
		if parent == p {
			return filepath.Join(p, rest)
		}
		rest = filepath.Join(filepath.Base(p), rest)
		p = parent
	}
}

func within(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
}

func (e *Env) checkWritable(path string) error {
	if !e.project() || path == "" {
		return nil
	}
	real := realPath(e.resolve(path))
	spec := e.projectSpec()
	for _, g := range spec.guarded {
		if within(real, g) {
			return fmt.Errorf("project mode: %s is git configuration or hooks, which would run outside the sandbox", path)
		}
	}
	for _, r := range spec.writable {
		if within(real, r) {
			return nil
		}
	}
	return fmt.Errorf("project mode: %s is outside the working directory; only files inside it (plus temp and build caches) can be changed", path)
}

const projectDenialHint = "\n(project mode: writes outside the working directory, temp and build caches are blocked, and so are .git/config and .git/hooks)"

func projectHint(out string) string {
	if strings.Contains(out, "Operation not permitted") || strings.Contains(out, "Read-only file system") {
		return out + projectDenialHint
	}
	return out
}
