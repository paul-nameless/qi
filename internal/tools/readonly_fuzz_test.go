package tools

import (
	"strings"
	"testing"
)

var fuzzRejected = []string{
	"rm -rf x",
	"echo hi > file",
	"ls; rm x",
	"ls\nrm x",
	"ls $(rm x)",
	"ls `rm x`",
	"find . -delete",
	"find . -exec rm {} ;",
	"FOO=1 ls",
	"git push",
	"ls | sh",
	"cat a > /etc/passwd",
}

func FuzzCheckReadOnly(f *testing.F) {
	for _, s := range append(fuzzRejected, "ls -la", "rg -n TODO . | head -20", `rg "a|b" src`, "git status && git diff --stat", "find . -name '*.go' | sort", "echo a#b") {
		f.Add(s)
	}
	cwd := f.TempDir()
	f.Fuzz(func(t *testing.T, command string) {
		_, _ = splitCommand(command)
		err := CheckReadOnly(command, cwd, nil)
		for _, bad := range fuzzRejected {
			if err == nil && (command == bad || strings.HasPrefix(command, bad+" ")) {
				t.Errorf("%q passed although it starts with %q", command, bad)
			}
		}
	})
}

func TestFuzzSeedsRejected(t *testing.T) {
	for _, c := range fuzzRejected {
		if err := CheckReadOnly(c, t.TempDir(), nil); err == nil {
			t.Errorf("%q accepted", c)
		}
	}
}
