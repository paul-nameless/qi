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

var neverAccepted = map[string][]string{
	"find": {"-exec", "-execdir", "-delete", "-ok", "-okdir"},
	"go":   {"-toolexec", "-vettool", "-gcflags", "-asmflags", "-ldflags", "-debug-", "-overlay", "-modfile", "-exec", "-o"},
	"git":  {"-output", "-ext-diff", "-textconv", "-open-files-in-pager"},
	"rg":   {"-pre"},
}

func FuzzCheckReadOnly(f *testing.F) {
	seeds := append(append([]string{}, fuzzRejected...), escapePoCs...)
	for _, s := range append(seeds, "ls -la", "rg -n TODO . | head -20", `rg "a|b" src`, "git status && git diff --stat", "find . -name '*.go' | sort", "echo a#b", "printf '%s' x", "test -f x") {
		f.Add(s)
	}
	cwd := f.TempDir()
	f.Fuzz(func(t *testing.T, command string) {
		if CheckReadOnly(command, cwd, nil) != nil {
			return
		}
		segments, err := splitCommand(command)
		if err != nil {
			t.Fatalf("%q accepted but does not split: %v", command, err)
		}
		for _, words := range segments {
			checkAcceptedSegment(t, command, words)
		}
	})
}

func checkAcceptedSegment(t *testing.T, command string, words []string) {
	if !allowed(words, DefaultAllow) || isAssignment(words[0]) || assigningBuiltins[words[0]] {
		t.Fatalf("%q accepted with segment %q", command, words)
	}
	for _, w := range words[1:] {
		if shellBuiltins[words[0]] && strings.ContainsAny(w, "$`") {
			t.Fatalf("%q accepted with an expansion in a builtin argument", command)
		}
		if (words[0] == "printf" || words[0] == "test") && (w == "-v" || w == "-R") {
			t.Fatalf("%q accepted with %s", command, w)
		}
		flag := "-" + strings.TrimLeft(w, "-")
		for _, bad := range neverAccepted[words[0]] {
			if strings.HasPrefix(w, "-") && (flag == bad || strings.HasPrefix(flag, bad+"=") || strings.HasSuffix(bad, "-") && strings.HasPrefix(flag, bad)) {
				t.Fatalf("%q accepted with %s", command, w)
			}
		}
	}
}

func TestFuzzSeedsRejected(t *testing.T) {
	t.Parallel()
	for _, c := range append(append([]string{}, fuzzRejected...), escapePoCs...) {
		if err := CheckReadOnly(c, t.TempDir(), nil); err == nil {
			t.Errorf("%q accepted", c)
		}
	}
}
