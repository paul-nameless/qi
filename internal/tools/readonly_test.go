package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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
	if _, err := exec.LookPath("sandbox-exec"); err != nil || runtime.GOOS != "darwin" {
		t.Skip("no sandbox-exec")
	}
	dir := t.TempDir()
	home := t.TempDir()
	cmd := readOnlySandbox(context.Background(), "echo hi > "+filepath.Join(home, "x.txt")+"; ls "+dir)
	out, _ := cmd.CombinedOutput()
	if _, err := os.Stat(filepath.Join(home, "x.txt")); err == nil && !strings.HasPrefix(home, os.TempDir()) {
		t.Errorf("sandbox allowed a write: %s", out)
	}
	cmd = readOnlySandbox(context.Background(), "echo hi > /Users/Shared/aicode-sbx-probe-$$")
	out, _ = cmd.CombinedOutput()
	if !strings.Contains(string(out), "not permitted") {
		t.Errorf("expected write denial, got %q", out)
	}
}
