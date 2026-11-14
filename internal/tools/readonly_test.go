package tools

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paul-nameless/aicode/internal/llm"
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
	out, _ := sandboxCmd(context.Background(), "find . -maxdepth 0 -fprint "+target, allowed).CombinedOutput()
	if _, err := os.Stat(target); err == nil {
		t.Errorf("sandbox allowed a write outside TMP: %s", out)
	}
	if err := sandboxCmd(context.Background(), "echo ok > "+filepath.Join(allowed, "ok.txt"), allowed).Run(); err != nil {
		t.Errorf("write to the allowed dir failed: %v", err)
	}
}

func TestReadOnlyBashThroughRegistry(t *testing.T) {
	dir := t.TempDir()
	env := &Env{Cwd: dir, ReadOnly: func() bool { return true }, SpillDir: t.TempDir()}
	res := NewRegistry(Bash()).Execute(context.Background(), env, []llm.Block{{ID: "1", Name: "bash", Input: json.RawMessage(`{"command":"touch x"}`)}}, nopUI{})
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
