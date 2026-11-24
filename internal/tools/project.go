package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/paul-nameless/qi/internal/config"
)

const projectExecRules = `(deny process-exec (literal "/usr/bin/osascript") (literal "/usr/bin/open") (literal "/bin/launchctl") (literal "/usr/bin/defaults") (literal "/usr/bin/security") (literal "/usr/bin/sudo") (literal "/usr/bin/shortcuts"))`

type projectSpec struct {
	writable []string
	guarded  []string
	pinned   []string
	sockets  []string
}

func projectProfile(spec projectSpec) string {
	var b strings.Builder
	b.WriteString(`(version 1)(allow default)(deny file-write*)(allow file-write* (literal "/dev/null") (literal "/dev/zero") (literal "/dev/tty") (literal "/dev/dtracehelper") (regex #"^/dev/fd/")`)
	for i := range spec.writable {
		fmt.Fprintf(&b, ` (subpath (param "W%d"))`, i)
	}
	b.WriteString(")")
	if len(spec.guarded)+len(spec.pinned) > 0 {
		b.WriteString("(deny file-write*")
		for i := range spec.guarded {
			fmt.Fprintf(&b, ` (subpath (param "G%d"))`, i)
		}
		for i := range spec.pinned {
			fmt.Fprintf(&b, ` (literal (param "P%d"))`, i)
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
		for i, p := range spec.pinned {
			args = append(args, "-D", fmt.Sprintf("P%d=%s", i, p))
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
	for _, g := range append(spec.guarded, spec.pinned...) {
		if fi, err := os.Stat(g); err == nil && (!fi.IsDir() || !slices.Contains(spec.pinned, g)) {
			prefix = append(prefix, "--ro-bind", g, g)
		}
	}
	return append(append(prefix, "--unshare-pid"), args[len(args)-4:]...)
}

var goDirs = sync.OnceValue(func() []string {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "env", "GOMODCACHE", "GOCACHE")
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	out, err := cmd.Output()
	if err != nil {
		return []string{filepath.Join(config.Home(), "go", "pkg", "mod")}
	}
	var dirs []string
	for _, d := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if d = strings.TrimSpace(d); filepath.IsAbs(d) {
			dirs = append(dirs, d)
		}
	}
	return dirs
})

func cacheDirs() []string {
	home := config.Home()
	cargo := os.Getenv("CARGO_HOME")
	if cargo == "" {
		cargo = filepath.Join(home, ".cargo")
	}
	dirs := append(goDirs(), os.TempDir(), filepath.Join(home, "go", "pkg", "sumdb"), filepath.Join(home, ".npm"),
		filepath.Join(home, ".cache", "uv"), filepath.Join(home, ".cache", "golangci-lint"),
		filepath.Join(cargo, "registry"), filepath.Join(cargo, "git"),
		filepath.Join(cargo, ".package-cache"), filepath.Join(cargo, ".global-cache"),
	)
	if d := darwinTempDir(); d != "" {
		dirs = append(dirs, d)
	}
	if len(goDirs()) > 0 {
		dirs = append(dirs, filepath.Join(filepath.Dir(goDirs()[0]), "sumdb"))
	}
	if d, err := os.UserCacheDir(); err == nil {
		dirs = append(dirs, filepath.Join(d, "golangci-lint"))
	}
	return dirs
}

func (e *Env) scratch() string {
	if e.Scratch == nil {
		return ""
	}
	return e.Scratch()
}

const probeTimeout = 5 * time.Second

func (e *Env) gitDirs() []string {
	e.gitOnce.Do(func() {
		if _, err := os.Lstat(filepath.Join(e.Cwd, ".git")); err == nil {
			e.dotGit = realPath(filepath.Join(e.Cwd, ".git"))
		}
		ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
		defer cancel()
		out, err := exec.CommandContext(ctx, "git", "-C", e.Cwd, "-c", "core.fsmonitor=", "rev-parse", "--path-format=absolute", "--git-dir", "--git-common-dir").Output()
		if err != nil {
			return
		}
		for _, d := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if d = strings.TrimSpace(d); !filepath.IsAbs(d) {
				continue
			}
			if d = realPath(d); !slices.Contains(e.git, d) {
				e.git = append(e.git, d)
			}
		}
	})
	return e.git
}

func gitPins(dirs []string) []string {
	var pins []string
	for _, d := range dirs {
		for a := d; ; a = filepath.Dir(a) {
			if !slices.Contains(pins, a) {
				pins = append(pins, a)
			}
			parent := filepath.Dir(a)
			inside := false
			for _, o := range dirs {
				inside = inside || within(parent, o)
			}
			if !inside || parent == a {
				break
			}
		}
	}
	return pins
}

var gitControl = []string{"config", "config.worktree", "hooks", "commondir", "gitdir"}

func (e *Env) projectSpec() projectSpec {
	roots := append([]string{e.Cwd, filepath.Join(config.Home(), ".Trash")}, cacheDirs()...)
	if s := e.scratch(); s != "" {
		roots = append(roots, s)
	}
	for _, p := range e.Writable {
		roots = append(roots, config.ExpandHome(p))
	}
	dirs := e.gitDirs()
	roots = append(roots, dirs...)
	gitDirs := slices.Clone(dirs)
	spec := projectSpec{pinned: gitPins(dirs)}
	if e.dotGit != "" {
		gitDirs = append(gitDirs, e.dotGit)
		if !slices.Contains(spec.pinned, e.dotGit) {
			spec.pinned = append(spec.pinned, e.dotGit)
		}
	}
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		spec.sockets = append(spec.sockets, realPath(sock))
	}
	for _, r := range roots {
		spec.writable = append(spec.writable, realPath(r))
	}
	for _, g := range gitDirs {
		for _, name := range gitControl {
			if p := filepath.Join(g, name); !slices.Contains(spec.guarded, p) {
				spec.guarded = append(spec.guarded, p)
			}
		}
	}
	return spec
}

func (e *Env) projectEnv() []string {
	return []string{"TMUX="}
}

const maxLinks = 40

func realPath(p string) string {
	p = filepath.Clean(p)
	for range maxLinks {
		dir := existingReal(filepath.Dir(p))
		p = filepath.Join(dir, filepath.Base(p))
		fi, err := os.Lstat(p)
		if err != nil || fi.Mode()&os.ModeSymlink == 0 {
			return p
		}
		target, err := os.Readlink(p)
		if err != nil {
			return p
		}
		if !filepath.IsAbs(target) {
			target = existingReal(dir + string(filepath.Separator) + target)
		}
		p = filepath.Clean(target)
	}
	return p
}

func existingReal(p string) string {
	parts := strings.Split(p, string(filepath.Separator))
	for n := len(parts); n > 0; n-- {
		prefix := strings.Join(parts[:n], string(filepath.Separator))
		if prefix == "" {
			prefix = string(filepath.Separator)
		}
		if real, err := filepath.EvalSymlinks(prefix); err == nil {
			return filepath.Join(append([]string{real}, parts[n:]...)...)
		}
	}
	return filepath.Clean(p)
}

func sameAsAny(path string, targets []string) bool {
	var stats []os.FileInfo
	for _, t := range targets {
		if fi, err := os.Stat(t); err == nil {
			stats = append(stats, fi)
		}
	}
	for a := path; ; a = filepath.Dir(a) {
		if fi, err := os.Stat(a); err == nil {
			for _, t := range stats {
				if os.SameFile(fi, t) {
					return true
				}
			}
		}
		if filepath.Dir(a) == a {
			return false
		}
	}
}

func within(path, root string) bool {
	if runtime.GOOS == "darwin" {
		path, root = strings.ToLower(path), strings.ToLower(root)
	}
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
}

func (e *Env) checkWritable(path string) error {
	if !e.project() || path == "" {
		return nil
	}
	real := realPath(e.resolve(path))
	spec := e.projectSpec()
	if sameAsAny(real, spec.guarded) {
		return fmt.Errorf("project mode: %s is git configuration or hooks, which would run outside the sandbox", path)
	}
	for _, g := range append(spec.guarded, spec.pinned...) {
		if within(real, g) && (!slices.Contains(spec.pinned, g) || samePath(real, g)) {
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

func samePath(a, b string) bool {
	if runtime.GOOS == "darwin" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

const projectDenialHint = "\n(project mode: writes outside the working directory, temp and build caches are blocked, and so are .git/config and .git/hooks)"

func projectHint(out string) string {
	if low := strings.ToLower(out); strings.Contains(low, "operation not permitted") || strings.Contains(low, "read-only file system") {
		return out + projectDenialHint
	}
	return out
}

var darwinTempDir = sync.OnceValue(func() string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	out, err := exec.Command("getconf", "DARWIN_USER_TEMP_DIR").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
})
