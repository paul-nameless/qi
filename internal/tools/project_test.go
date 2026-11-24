package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paul-nameless/qi/internal/llm"
)

func projectDirs(t *testing.T) (proj, outside string) {
	outside, _ = filepath.EvalSymlinks(t.TempDir())
	proj, _ = filepath.EvalSymlinks(t.TempDir())
	t.Setenv("TMPDIR", t.TempDir())
	return proj, outside
}

func TestProjectModeWriteAndEditStayInside(t *testing.T) {
	proj, outside := projectDirs(t)
	os.WriteFile(filepath.Join(outside, "o.txt"), []byte("a"), 0o600)
	os.Symlink(outside, filepath.Join(proj, "escape"))
	env := &Env{Cwd: proj, Project: func() bool { return true }}
	r := newFuncSet(Write(), Edit())
	in := func(id, name, args string) llm.Block {
		return llm.Block{ID: id, Name: name, Input: json.RawMessage(args)}
	}
	res := r.Execute(context.Background(), env, []llm.Block{
		in("ok", "write", `{"file_path":"src/new.txt","content":"hi"}`),
		in("abs", "write", `{"file_path":"`+filepath.Join(outside, "x.txt")+`","content":"hi"}`),
		in("dots", "write", `{"file_path":"../`+filepath.Base(outside)+`/y.txt","content":"hi"}`),
		in("link", "write", `{"file_path":"escape/z.txt","content":"hi"}`),
		in("edit", "edit", `{"file_path":"`+filepath.Join(outside, "o.txt")+`","old_string":"a","new_string":"b"}`),
		in("tmp", "write", `{"file_path":"`+filepath.Join(os.TempDir(), "t.txt")+`","content":"hi"}`),
	}, nopUI{})
	for _, b := range res {
		allowed := b.ToolUseID == "ok" || b.ToolUseID == "tmp"
		if b.IsError == allowed || (!allowed && !strings.Contains(b.Text, "project mode")) {
			t.Errorf("%s: error %v: %s", b.ToolUseID, b.IsError, b.Text)
		}
	}
	for _, name := range []string{"x.txt", "y.txt", "z.txt"} {
		if _, err := os.Stat(filepath.Join(outside, name)); err == nil {
			t.Errorf("%s was written outside the project", name)
		}
	}
	if data, _ := os.ReadFile(filepath.Join(outside, "o.txt")); string(data) != "a" {
		t.Error("edit changed a file outside the project")
	}
}

func TestProjectModeSandboxesBash(t *testing.T) {
	if !HasSandbox() {
		t.Skip("no OS sandbox")
	}
	proj, outside := projectDirs(t)
	env := &Env{Cwd: proj, Project: func() bool { return true }, SpillDir: t.TempDir()}
	run := func(cmd string) Result {
		in, _ := json.Marshal(bashArgs{Command: cmd})
		return runBash(context.Background(), env, in, nil)
	}
	run("echo in > inside.txt && mkdir -p sub && echo t > \"$TMPDIR/t.txt\"")
	if _, err := os.Stat(filepath.Join(proj, "inside.txt")); err != nil {
		t.Errorf("bash could not write inside the project: %v", err)
	}
	if _, err := os.Stat(filepath.Join(os.TempDir(), "t.txt")); err != nil {
		t.Errorf("bash could not write to temp: %v", err)
	}
	res := run("echo out > " + filepath.Join(outside, "x.txt") + "; rm -rf " + outside)
	if _, err := os.Stat(filepath.Join(outside, "x.txt")); err == nil {
		t.Errorf("bash wrote outside the project: %s", res.Text)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Errorf("bash removed a directory outside the project: %s", res.Text)
	}
	if res := run("sleep 30 & kill $!; timeout 1 sleep 5; echo done"); !strings.Contains(res.Text, "done") {
		t.Errorf("own processes and timeout must work: %s", res.Text)
	}
}
