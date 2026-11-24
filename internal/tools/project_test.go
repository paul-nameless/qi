package tools

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paul-nameless/qi/internal/llm"
)

func projectEnv(t *testing.T) (env *Env, proj, outside string) {
	outside, _ = filepath.EvalSymlinks(t.TempDir())
	proj, _ = filepath.EvalSymlinks(t.TempDir())
	scratch, _ := filepath.EvalSymlinks(t.TempDir())
	if err := os.MkdirAll(filepath.Join(proj, ".git", "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(proj, ".git", "config"), []byte("[core]\n"), 0o600)
	env = &Env{Cwd: proj, Project: func() bool { return true }, Scratch: func() string { return scratch }, SpillDir: t.TempDir()}
	t.Cleanup(func() { os.RemoveAll(projectGoCache(proj)); os.RemoveAll(env.projectTmp()) })
	return env, proj, outside
}

func TestProjectModeWriteAndEditStayInside(t *testing.T) {
	env, proj, outside := projectEnv(t)
	os.WriteFile(filepath.Join(outside, "o.txt"), []byte("a"), 0o600)
	os.Symlink(outside, filepath.Join(proj, "escape"))
	os.Symlink(filepath.Join(outside, "dangling.txt"), filepath.Join(proj, "dangle"))
	r := newFuncSet(Write(), Edit())
	in := func(id, name, args string) llm.Block {
		return llm.Block{ID: id, Name: name, Input: json.RawMessage(args)}
	}
	res := r.Execute(context.Background(), env, []llm.Block{
		in("ok", "write", `{"file_path":"src/new.txt","content":"hi"}`),
		in("abs", "write", `{"file_path":"`+filepath.Join(outside, "x.txt")+`","content":"hi"}`),
		in("dots", "write", `{"file_path":"../`+filepath.Base(outside)+`/y.txt","content":"hi"}`),
		in("link", "write", `{"file_path":"escape/z.txt","content":"hi"}`),
		in("dangle", "write", `{"file_path":"dangle","content":"hi"}`),
		in("gitconfig", "write", `{"file_path":".git/config","content":"[core]\nfsmonitor = touch /tmp/pwned\n"}`),
		in("githook", "write", `{"file_path":".git/hooks/pre-commit","content":"#!/bin/sh\n"}`),
		in("gitcase", "write", `{"file_path":".GIT/CONFIG","content":"[core]\nfsmonitor = x\n"}`),
		in("commondir", "write", `{"file_path":".git/commondir","content":"../evil"}`),
		in("dotgit", "write", `{"file_path":".git","content":"gitdir: ../evil"}`),
		in("edit", "edit", `{"file_path":"`+filepath.Join(outside, "o.txt")+`","old_string":"a","new_string":"b"}`),
		in("tmp", "write", `{"file_path":"`+filepath.Join(env.projectTmp(), "t.txt")+`","content":"hi"}`),
	}, nopUI{})
	for _, b := range res {
		allowed := b.ToolUseID == "ok" || b.ToolUseID == "tmp"
		if b.IsError == allowed || (!allowed && !strings.Contains(b.Text, "project mode")) {
			t.Errorf("%s: error %v: %s", b.ToolUseID, b.IsError, b.Text)
		}
	}
	for _, name := range []string{"x.txt", "y.txt", "z.txt", "dangling.txt"} {
		if _, err := os.Stat(filepath.Join(outside, name)); err == nil {
			t.Errorf("%s was written outside the project", name)
		}
	}
	if data, _ := os.ReadFile(filepath.Join(outside, "o.txt")); string(data) != "a" {
		t.Error("edit changed a file outside the project")
	}
	if data, _ := os.ReadFile(filepath.Join(proj, ".git", "config")); string(data) != "[core]\n" {
		t.Error("write changed .git/config")
	}
}

func TestProjectModeSandboxesBash(t *testing.T) {
	if !HasSandbox() {
		t.Skip("no OS sandbox")
	}
	env, proj, outside := projectEnv(t)
	run := func(cmd string) string {
		in, _ := json.Marshal(bashArgs{Command: cmd})
		return runBash(context.Background(), env, in, nil).Text
	}
	if out := run(`echo in > inside.txt && echo t > "$TMPDIR/t.txt" && echo "$TMPDIR|$GOCACHE"`); !strings.HasPrefix(strings.TrimSpace(out), env.projectTmp()+"|") || !strings.HasSuffix(strings.TrimSpace(out), projectGoCache(proj)) {
		t.Errorf("TMPDIR should be the session's short temp dir and GOCACHE the project's own: %s", out)
	}
	for _, p := range []string{filepath.Join(proj, "inside.txt"), filepath.Join(env.projectTmp(), "t.txt")} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("bash could not write %s: %v", p, err)
		}
	}
	out := run("echo out > " + filepath.Join(outside, "x.txt") + "; rm -rf " + outside + "; echo x >> .git/config; echo x > .git/hooks/pre-commit")
	if _, err := os.Stat(filepath.Join(outside, "x.txt")); err == nil {
		t.Errorf("bash wrote outside the project: %s", out)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Errorf("bash removed a directory outside the project: %s", out)
	}
	if data, _ := os.ReadFile(filepath.Join(proj, ".git", "config")); string(data) != "[core]\n" {
		t.Errorf("bash changed .git/config: %s", out)
	}
	if !strings.Contains(out, "project mode:") {
		t.Errorf("denials should carry the project-mode hint: %s", out)
	}
	if out := run(`sleep 30 & k=$!; kill $k; wait $k; echo "own child $?"; timeout 1 sleep 5; echo "timeout $?"`); !strings.Contains(out, "own child 143") || !strings.Contains(out, "timeout 124") {
		t.Errorf("own processes and timeout must work: %s", out)
	}
	if _, err := exec.LookPath("python3"); err != nil {
		return
	}
	short, err := os.MkdirTemp("/tmp", "qs")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(short)
	sock := filepath.Join(short, "s")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	out = run(`python3 -c "import socket; s=socket.socket(socket.AF_UNIX); s.connect('` + sock + `'); print('CONNECTED')" 2>&1 | tail -1`)
	if strings.Contains(out, "CONNECTED") {
		t.Errorf("bash reached a unix socket outside the sandbox: %s", out)
	}
	out = run(`python3 -c "import os,time; os.setpgrp(); time.sleep(20)" & k=$!; python3 -c "import time; time.sleep(0.3)"; kill $k; wait $k; echo "pgrp child $?"`)
	if !strings.Contains(out, "pgrp child 143") {
		t.Errorf("a child in its own process group must be killable: %s", out)
	}
}

func TestProjectModeGitWorktree(t *testing.T) {
	if !HasSandbox() {
		t.Skip("no OS sandbox")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	repo, _ := filepath.EvalSymlinks(t.TempDir())
	wt := filepath.Join(repo, "wt")
	git := func(dir string, args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	git(repo, "init", "-q")
	os.WriteFile(filepath.Join(repo, "a"), []byte("a"), 0o600)
	git(repo, "add", "a")
	git(repo, "commit", "-qm", "a")
	git(repo, "worktree", "add", "-q", "-b", "w", wt)
	scratch, _ := filepath.EvalSymlinks(t.TempDir())
	env := &Env{Cwd: wt, Project: func() bool { return true }, Scratch: func() string { return scratch }, SpillDir: t.TempDir()}
	t.Cleanup(func() { os.RemoveAll(projectGoCache(wt)); os.RemoveAll(env.projectTmp()) })
	in, _ := json.Marshal(bashArgs{Command: "echo b > b && git add b && git -c user.email=t@t -c user.name=t commit -qm b && git log --oneline | wc -l; echo x >> " + filepath.Join(repo, ".git", "config")})
	out := runBash(context.Background(), env, in, nil).Text
	if !strings.HasPrefix(strings.TrimSpace(out), "2") {
		t.Errorf("commit in a worktree should work in project mode: %s", out)
	}
	if data, _ := os.ReadFile(filepath.Join(repo, ".git", "config")); strings.Contains(string(data), "x\n") {
		t.Errorf("the main repo's .git/config was changed from a worktree: %s", out)
	}
}

func TestProjectModeGitCannotBeRedirected(t *testing.T) {
	if !HasSandbox() {
		t.Skip("no OS sandbox")
	}
	env, proj, outside := projectEnv(t)
	run := func(cmd string) string {
		in, _ := json.Marshal(bashArgs{Command: cmd})
		return runBash(context.Background(), env, in, nil).Text
	}
	evil := filepath.Join(outside, "evil")
	out := run("mkdir -p " + evil + " 2>&1; echo ../evil > .git/commondir; echo x > .git/config.worktree; mv .git .git2; echo 'gitdir: .git2' > .git.tmp && mv .git.tmp .git; ls -a")
	for _, name := range []string{"commondir", "config.worktree"} {
		if _, err := os.Stat(filepath.Join(proj, ".git", name)); err == nil {
			t.Errorf(".git/%s was written: %s", name, out)
		}
	}
	if fi, err := os.Stat(filepath.Join(proj, ".git")); err != nil || !fi.IsDir() {
		t.Errorf(".git was renamed or replaced: %v %s", err, out)
	}
	if out := run(`go env GOCACHE 2>/dev/null || echo "$GOCACHE"`); !strings.Contains(out, projectGoCache(proj)) {
		t.Errorf("GOCACHE: %s", out)
	}
	if _, err := os.Stat(projectGoCache(proj)); err != nil {
		t.Errorf("the per-project Go cache should be created by qi: %v", err)
	}
}

func TestProjectModeTrickyPaths(t *testing.T) {
	env, proj, outside := projectEnv(t)
	os.MkdirAll(filepath.Join(proj, "d"), 0o755)
	os.MkdirAll(filepath.Join(proj, "s1", "s2", "s3"), 0o755)
	os.Symlink(filepath.Join(proj, "d"), filepath.Join(proj, "s1", "s2", "s3", "a"))
	rel, _ := filepath.Rel(filepath.Join(proj, "d"), filepath.Join(outside, "f.txt"))
	os.Symlink(rel, filepath.Join(proj, "d", "link"))
	r := newFuncSet(Write(), Edit())
	in := func(id, name, args string) llm.Block {
		return llm.Block{ID: id, Name: name, Input: json.RawMessage(args)}
	}
	res := r.Execute(context.Background(), env, []llm.Block{
		in("chain", "write", `{"file_path":"s1/s2/s3/a/link","content":"x"}`),
		in("ligature", "edit", `{"file_path":".git/con\ufb01g","old_string":"[core]","new_string":"[core]\nfsmonitor = x"}`),
		in("longs", "write", `{"file_path":".git/hook\u017f/pre-commit","content":"#!/bin/sh"}`),
	}, nopUI{})
	for _, b := range res {
		if !b.IsError || !strings.Contains(b.Text, "project mode") {
			t.Errorf("%s: %+v", b.ToolUseID, b)
		}
	}
	if _, err := os.Stat(filepath.Join(outside, "f.txt")); err == nil {
		t.Error("a relative symlink chain wrote outside the project")
	}
	if data, _ := os.ReadFile(filepath.Join(proj, ".git", "config")); string(data) != "[core]\n" {
		t.Errorf("a lookalike name changed .git/config: %q", data)
	}
}

func TestProjectModeShortTmpAndGitInit(t *testing.T) {
	if !HasSandbox() {
		t.Skip("no OS sandbox")
	}
	proj, _ := filepath.EvalSymlinks(t.TempDir())
	scratch, _ := filepath.EvalSymlinks(t.TempDir())
	env := &Env{Cwd: proj, Project: func() bool { return true }, Scratch: func() string { return scratch }, SpillDir: t.TempDir()}
	t.Cleanup(func() { os.RemoveAll(projectGoCache(proj)); os.RemoveAll(env.projectTmp()) })
	if tmp := env.projectTmp(); len(tmp) > 40 {
		t.Errorf("TMPDIR %q leaves too little room for unix socket names", tmp)
	}
	in, _ := json.Marshal(bashArgs{Command: "git init -q && git -c user.email=t@t -c user.name=t commit -q --allow-empty -m x && echo committed"})
	if out := runBash(context.Background(), env, in, nil).Text; !strings.Contains(out, "committed") {
		t.Errorf("git init in a folder without a repo should work: %s", out)
	}
}

func TestProjectModeRepoSubdirCannotMoveGit(t *testing.T) {
	if !HasSandbox() {
		t.Skip("no OS sandbox")
	}
	repo, _ := filepath.EvalSymlinks(t.TempDir())
	sub := filepath.Join(repo, "sub")
	os.MkdirAll(sub, 0o755)
	if out, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	scratch, _ := filepath.EvalSymlinks(t.TempDir())
	env := &Env{Cwd: sub, Project: func() bool { return true }, Scratch: func() string { return scratch }, SpillDir: t.TempDir()}
	t.Cleanup(func() { os.RemoveAll(projectGoCache(sub)); os.RemoveAll(env.projectTmp()) })
	in, _ := json.Marshal(bashArgs{Command: "mv ../.git ./x; ls -a"})
	out := runBash(context.Background(), env, in, nil).Text
	if fi, err := os.Stat(filepath.Join(repo, ".git")); err != nil || !fi.IsDir() {
		t.Errorf("the repo's .git was moved from a subdirectory: %s", out)
	}
}

func TestProjectModeScratchAndDotDotLinks(t *testing.T) {
	env, proj, outside := projectEnv(t)
	os.MkdirAll(filepath.Join(outside, "sub"), 0o755)
	os.MkdirAll(filepath.Join(proj, "d"), 0o755)
	os.Symlink(filepath.Join(outside, "sub"), filepath.Join(proj, "d", "s"))
	os.Symlink("s/../f.txt", filepath.Join(proj, "d", "link"))
	r := newFuncSet(Write())
	res := r.Execute(context.Background(), env, []llm.Block{
		{ID: "dotdot", Name: "write", Input: json.RawMessage(`{"file_path":"d/link","content":"x"}`)},
		{ID: "scratch", Name: "write", Input: json.RawMessage(`{"file_path":"` + filepath.Join(env.scratch(), "notes.txt") + `","content":"x"}`)},
	}, nopUI{})
	if !res[0].IsError {
		t.Errorf("a link through a symlink and .. wrote outside: %s", res[0].Text)
	}
	if res[1].IsError {
		t.Errorf("the session scratch dir must stay writable: %s", res[1].Text)
	}
	if _, err := os.Stat(filepath.Join(outside, "f.txt")); err == nil {
		t.Error("f.txt was written outside the project")
	}
}
