package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/paul-nameless/qi/internal/config"
)

const projectDenied = `(deny process-exec (literal "/usr/bin/osascript") (literal "/usr/bin/open") (literal "/bin/launchctl") (literal "/usr/bin/defaults") (literal "/usr/bin/security") (literal "/usr/bin/sudo") (literal "/usr/bin/shortcuts"))(deny signal (target others))`

func projectProfile(roots int) string {
	var b strings.Builder
	b.WriteString(`(version 1)(allow default)(deny file-write*)(allow file-write* (literal "/dev/null") (literal "/dev/zero") (literal "/dev/tty") (literal "/dev/dtracehelper") (regex #"^/dev/fd/")`)
	for i := range roots {
		fmt.Fprintf(&b, ` (subpath (param "W%d"))`, i)
	}
	b.WriteString(")" + projectDenied)
	return b.String()
}

func projectSandbox(ctx context.Context, command string, roots []string) *exec.Cmd {
	sb := sandbox()
	switch {
	case sb.path == "":
		return exec.CommandContext(ctx, "bash", "-c", command)
	case runtime.GOOS == "darwin":
		var args []string
		for i, r := range roots {
			args = append(args, "-D", fmt.Sprintf("W%d=%s", i, r))
		}
		args = append(args, "-p", projectProfile(len(roots)), "bash", "-c", command)
		return exec.CommandContext(ctx, sb.path, args...)
	}
	return exec.CommandContext(ctx, sb.path, bwrapArgs(command, roots, execDenied(), false)...)
}

func cacheDirs() []string {
	home := config.Home()
	dirs := []string{filepath.Join(home, ".npm"), filepath.Join(home, "go", "pkg", "mod"), filepath.Join(home, ".cargo", "registry"), filepath.Join(home, ".cargo", "git")}
	if d, err := os.UserCacheDir(); err == nil {
		dirs = append(dirs, d)
	}
	return dirs
}

func (e *Env) writableRoots() []string {
	roots := append([]string{e.Cwd, os.TempDir()}, cacheDirs()...)
	for _, p := range e.Writable {
		roots = append(roots, config.ExpandHome(p))
	}
	var out []string
	for _, r := range roots {
		if real, err := filepath.EvalSymlinks(r); err == nil {
			out = append(out, real)
		}
	}
	return out
}

func realPath(p string) string {
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
	for _, r := range e.writableRoots() {
		if within(real, r) {
			return nil
		}
	}
	return fmt.Errorf("project mode: %s is outside the working directory; only files inside it (plus temp and build caches) can be changed", path)
}
