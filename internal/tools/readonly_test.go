package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/paul-nameless/qi/internal/llm"
)

func TestCheckReadOnly(t *testing.T) {
	ok := []string{
		"ls -la",
		"rg -n TODO . | head -20",
		"git status && git diff --stat",
		"grep -r foo src 2>/dev/null",
		"cat a.txt 2>&1 | wc -l",
		`rg "a|b" src`,
		"find . -name '*.go' | sort",
		"go vet ./... &>/dev/null",
		"git branch",
		"git branch -a --contains main",
		"git branch --list 'feat*'",
		"sort -u a | uniq -c",
		"rg 'price: \\$5' .",
		"go env GOPATH",
		"ls *.go",
		"sort a | uniq -c",
		"ls # list files",
		"rg 'a#b' .",
		"echo a#b",
		`rg "a\"b" .`,
		`rg "price \$5" .`,
	}
	for _, c := range ok {
		if err := CheckReadOnly(c, t.TempDir(), nil); err != nil {
			t.Errorf("%q: unexpected error %v", c, err)
		}
	}
	bad := []string{
		"rm -rf x",
		"echo hi > file",
		"cat a >> b",
		"ls >| f",
		"ls >&1file",
		"ls; rm x",
		"ls\nrm x",
		"ls $(rm x)",
		"ls `rm x`",
		`echo "$(rm x)"`,
		"x=-delete; find . $x",
		"find . ${y:--delete}",
		"find . $'-delete'",
		"find . -{delete,print}",
		"find . -del\\\nete",
		"find . -delete",
		"find . -exec rm {} ;",
		"FOO=1 ls",
		"GIT_EXTERNAL_DIFF=x git diff",
		"./test",
		"bin/date",
		"git commit -m x",
		"git push",
		"git branch -D main",
		"git branch newname",
		"git tag v9",
		"git tag -d v1",
		"git remote remove origin",
		"go env -w GOFLAGS=-x",
		"go vet -vettool=/tmp/evil ./...",
		"git grep -Ovim foo",
		"git grep --open-files-in-pager=vim foo",
		"git diff --output=f",
		"rg --pre=x foo",
		"rg --hostname-bin=x foo",
		"find . -execdir touch pwned ;",
		"find . -exec touch x \\;",
		"find . -fprint0 o",
		"find . -fprintf o x",
		"find . -okdir rm \\;",
		"file --compile -m x",
		"go env -u GOFLAGS",
		"git branch --edit-description",
		"ls a#; touch pwned",
		"echo #'\ntouch pwned #'",
		"ls #'\ncp a b #'",
		"cat <<EOF\necho '\nEOF\ntouch pwned #'",
		"find . -ok rm ;",
		"find . -fprint out",
		"find . -fls out",
		"find .\t-delete",
		"fd -X rm",
		"fd --exec-batch rm",
		"go vet -exec=x ./...",
		"go list -overlay=o.json ./...",
		"go list -modfile=x.mod ./...",
		"git diff --ext-diff",
		"git log --textconv",
		"file -C -m x",
		"git branch --set-upstream-to=origin/main",
		"git branch --unset-upstream",
		"git branch --track x",
		"sort -o/tmp/x a",
		"sort -uo out a",
		"sort --compress-program=sh a",
		"tree -o/tmp/x",
		"tree -ao out",
		"tree -R -L 1",
		"rm>/dev/null ls",
		"touch>/dev/null ls",
		"ls >& out",
		"ls >&  out",
		"tree -RH. -L1",
		"fd -xrm",
		"fd -Hx rm",
		"uniq in out",
		"go build ./...",
		"sed -i s/a/b/ f",
		"ls | xargs rm",
		"diff <(ls) b",
		"env rm x",
		"(rm x)",
		"go list -mod=mod ./...",
		"git grep --open-files='touch /tmp/x' x",
		"sort --compress-prog=sh a",
		"sort --outp=FILE in.txt",
		"go vet --toolexec=sh ./...",
		"go env --w GOFLAGS=-x",
		"go list --mod=mod ./...",
		"uniq x*",
		"find . -maxdepth 0 \"-ex\\\nec\" sh -c id \\;",
		"git log \"--out\\\nput=x\"",
		"echo x | uniq - out.txt",
		"uniq -- - -out",
		"uniq -c in.txt",
		"go vet -fix .",
		"go doc -http",
	}
	for _, c := range bad {
		if err := CheckReadOnly(c, t.TempDir(), nil); err == nil {
			t.Errorf("%q: expected rejection", c)
		}
	}
	if err := CheckReadOnly("make lint", t.TempDir(), []string{"make lint"}); err != nil {
		t.Errorf("extra allowlist: %v", err)
	}
}

func TestReadOnlyGlobMatchingFlags(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "--pre=sh"), nil, 0o600)
	os.WriteFile(filepath.Join(dir, "evil"), nil, 0o600)
	for _, c := range []string{"rg TODO *", "sort *", "ls -- e* *", "rg TODO [!a]*", "rg TODO [-]*", "rg TODO [[:punct:]]*"} {
		if err := CheckReadOnly(c, dir, nil); err == nil {
			t.Errorf("%q: expected rejection", c)
		}
	}
	if err := CheckReadOnly("ls e*", dir, nil); err != nil {
		t.Errorf("safe glob rejected: %v", err)
	}
}

func TestReadOnlySandboxBlocksWrites(t *testing.T) {
	if !HasSandbox() {
		t.Skip("no OS sandbox")
	}
	allowed, _ := filepath.EvalSymlinks(t.TempDir())
	target := filepath.Join(t.TempDir(), "probe.txt")
	t.Cleanup(func() { os.Remove(target) })
	out, _ := sandboxCmd(context.Background(), "echo x > "+target, allowed).CombinedOutput()
	if _, err := os.Stat(target); err == nil || !strings.Contains(string(out), "not permitted") && !strings.Contains(string(out), "Read-only file system") {
		t.Errorf("sandbox allowed a write outside TMP: %s", out)
	}
	if err := sandboxCmd(context.Background(), "echo ok > "+filepath.Join(allowed, "ok.txt"), allowed).Run(); err != nil {
		t.Errorf("write to the allowed dir failed: %v", err)
	}
	if runtime.GOOS == "darwin" {
		script := filepath.Join(allowed, "s.sh")
		os.WriteFile(script, []byte("#!/bin/sh\necho ran\n"), 0o700)
		if out, _ := exec.Command(script).CombinedOutput(); !strings.Contains(string(out), "ran") {
			t.Fatalf("probe script does not run outside the sandbox: %s", out)
		}
		if out, _ := sandboxCmd(context.Background(), script, allowed).CombinedOutput(); strings.Contains(string(out), "ran") {
			t.Errorf("script in TMP ran under the sandbox: %s", out)
		}
		for _, bin := range []string{"/usr/bin/osascript", "/usr/bin/open", "/bin/launchctl", "/usr/bin/defaults", "/usr/bin/security", "/usr/bin/sudo", "/usr/bin/shortcuts"} {
			if _, err := os.Stat(bin); err != nil {
				continue
			}
			out, _ := sandboxCmd(context.Background(), bin+" -h", allowed).CombinedOutput()
			if !strings.Contains(string(out), "Operation not permitted") {
				t.Errorf("%s was not denied by the sandbox: %s", bin, out)
			}
		}
	}
}

func TestRunBashUsesSandbox(t *testing.T) {
	if !HasSandbox() {
		t.Skip("no OS sandbox")
	}
	t.Setenv("TMPDIR", t.TempDir())
	outside := t.TempDir()
	target := filepath.Join(outside, "x")
	env := &Env{Cwd: outside, ReadOnly: func() bool { return true }, Allow: []string{"touch"}, SpillDir: t.TempDir()}
	runBash(context.Background(), env, json.RawMessage(`{"command":"touch `+target+`"}`), nil)
	if _, err := os.Stat(target); err == nil {
		t.Error("allowlisted touch wrote outside TMP: runBash is not sandboxed")
	}
}

func TestReadOnlyBashThroughHooksPath(t *testing.T) {
	dir := t.TempDir()
	env := &Env{Cwd: dir, ReadOnly: func() bool { return true }, SpillDir: t.TempDir()}
	res := newFuncSet(Bash()).Execute(context.Background(), env, []llm.Block{{ID: "1", Name: "bash", Input: json.RawMessage(`{"command":"touch x"}`)}}, nopUI{})
	if !res[0].IsError || !strings.Contains(res[0].Text, "read-only") {
		t.Errorf("touch allowed: %+v", res[0])
	}
	if _, err := os.Stat(filepath.Join(dir, "x")); err == nil {
		t.Error("file created in read-only mode")
	}
}

func TestReadOnlyGitIgnoresFsmonitor(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	mark := filepath.Join(t.TempDir(), "fsmonitor-ran")
	for _, args := range [][]string{{"init", "-q"}, {"config", "core.fsmonitor", "touch " + mark}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	env := &Env{Cwd: dir, ReadOnly: func() bool { return true }, SpillDir: t.TempDir()}
	runBash(context.Background(), env, json.RawMessage(`{"command":"git status"}`), nil)
	if _, err := os.Stat(mark); err == nil {
		t.Error("core.fsmonitor ran in read-only mode")
	}
}

func TestBwrapExecDenyList(t *testing.T) {
	root := t.TempDir()
	usr := filepath.Join(root, "usr", "bin")
	os.MkdirAll(usr, 0o755)
	alt := filepath.Join(root, "etc", "alternatives")
	os.MkdirAll(alt, 0o755)
	os.WriteFile(filepath.Join(usr, "sudo"), []byte("x"), 0o755)
	os.WriteFile(filepath.Join(usr, "busybox"), []byte("x"), 0o755)
	os.WriteFile(filepath.Join(usr, "real-open"), []byte("x"), 0o755)
	os.Symlink(filepath.Join(usr, "real-open"), filepath.Join(alt, "xdg-open"))
	os.Symlink(filepath.Join(alt, "xdg-open"), filepath.Join(usr, "xdg-open"))
	os.Symlink(filepath.Join(usr, "busybox"), filepath.Join(usr, "su"))
	os.Symlink(usr, filepath.Join(root, "bin"))
	got := deniedPaths([]string{usr, filepath.Join(root, "bin"), filepath.Join(root, "missing")}, []string{"sudo", "su", "pkexec", "xdg-open"})
	real, _ := filepath.EvalSymlinks(usr)
	if !slices.Equal(got, []string{filepath.Join(real, "sudo"), filepath.Join(real, "real-open")}) {
		t.Errorf("denied %q: want sudo and the alternatives target, not busybox, no duplicates", got)
	}
	args := strings.Join(bwrapArgs("ls", []string{"/tmp/x"}, []string{"/usr/bin/sudo"}, true), " ")
	if args != "--ro-bind / / --dev /dev --proc /proc --bind /tmp/x /tmp/x --unshare-net --ro-bind /dev/null /usr/bin/sudo --die-with-parent bash -c ls" {
		t.Errorf("bwrap args: %s", args)
	}
}

func requireLinuxSandbox(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux only")
	}
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap not installed")
	}
	if HasSandbox() {
		return
	}
	if err := exec.Command("unshare", "-U", "true").Run(); err != nil {
		t.Skipf("user namespaces unavailable (unshare -U true: %v)", err)
	}
	t.Fatal("bwrap is installed and user namespaces work, but HasSandbox() is false")
}

func TestLinuxSandboxDeniesExec(t *testing.T) {
	requireLinuxSandbox(t)
	denied := execDenied()
	if len(denied) == 0 {
		t.Skip("none of the denied binaries is installed")
	}
	allowed, _ := filepath.EvalSymlinks(t.TempDir())
	for _, bin := range denied {
		out, err := sandboxCmd(context.Background(), bin+" --help", allowed).CombinedOutput()
		if err == nil || !strings.Contains(string(out), "Permission denied") {
			t.Errorf("%s ran under the sandbox: %v %s", bin, err, out)
		}
	}
	if out, err := sandboxCmd(context.Background(), "ls /", allowed).CombinedOutput(); err != nil {
		t.Errorf("ls failed under the sandbox: %v %s", err, out)
	}
}

var escapePoCs = []string{
	`printf -v 'a[$(touch PWNED1)]' x`,
	`test -v 'a[$(touch PWNED2)]'`,
	`printf -v PATH %s ./bin; ls`,
	`printf -v GIT_CONFIG_VALUE_0 %s 'echo hi'; git status`,
	`printf -v GOFLAGS %s -toolexec=sh; go vet .`,
	`printf '%s' -v`,
	`test 'a[$(touch PWNED9)]' -eq 1`,
	"test 'a[`touch PWNED9b`]' -eq 1",
	`test -R 'a[$(touch PWNED10)]'`,
	`echo '$(touch PWNED13)'`,
	`git branch -l --no-list zzbranch`,
	`git branch --list --no-lis zzbranch`,
	`go list -export -gcflags=-asmhdr=victim.txt .`,
	`go list -debug-actiongraph=PWNED11 .`,
	`go vet -debug-trace=/tmp/x .`,
	`go vet -ldflags=-X=a .`,
	`go list -asmflags=-x .`,
	`go list -pkgdir=/tmp/p .`,
	`go list -o x .`,
	`go doc -cmd -u -http=:6060`,
	`go env -w GOFLAGS=-x`,
	`export PATH=./bin; ls`,
	`read -r PATH; ls`,
	`declare -x GOFLAGS=-x`,
}

func TestReadOnlyEscapePoCs(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "bin"), 0o755)
	os.WriteFile(filepath.Join(dir, "bin", "ls"), []byte("#!/bin/sh\ntouch PWNED-ls\n"), 0o755)
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n\ngo 1.21\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "x.go"), []byte("package x\n"), 0o644)
	env := &Env{Cwd: dir, ReadOnly: func() bool { return true }, SpillDir: t.TempDir()}
	for _, c := range escapePoCs {
		if err := CheckReadOnly(c, dir, nil); err == nil {
			t.Errorf("%q accepted in read-only mode", c)
		}
		in, _ := json.Marshal(map[string]string{"command": c})
		if res := runBash(context.Background(), env, in, nil); !res.IsError {
			t.Errorf("%q ran: %s", c, res.Text)
		}
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "PWNED") {
			t.Errorf("escape created %s", e.Name())
		}
	}
	for _, ok := range []string{"printf '%s\\n' a b", "test -f go.mod", "test -n abc", "echo hi", "go list -f '{{.Name}}' .", "go list -m -json all", "go vet -json ./...", "go doc -all fmt", "go env -json GOPATH", "git branch --list 'f*'"} {
		if err := CheckReadOnly(ok, dir, nil); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
}

func TestSandboxDeniesNetwork(t *testing.T) {
	if !SandboxIsolatesNetwork() {
		t.Skip("no OS sandbox with network isolation")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan bool, 1)
	go func() {
		c, err := ln.Accept()
		if err == nil {
			c.Close()
		}
		got <- err == nil
	}()
	allowed, _ := filepath.EvalSymlinks(t.TempDir())
	port := ln.Addr().(*net.TCPAddr).Port
	out, _ := sandboxCmd(context.Background(), fmt.Sprintf("echo x > /dev/tcp/127.0.0.1/%d", port), allowed).CombinedOutput()
	select {
	case <-got:
		t.Errorf("sandboxed command reached the network: %s", out)
	case <-time.After(300 * time.Millisecond):
	}
}
