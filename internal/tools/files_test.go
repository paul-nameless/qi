package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUnifiedDiff(t *testing.T) {
	var lines []string
	for i := 1; i <= 20; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	old := strings.Join(lines, "\n") + "\n"
	lines[9] = "changed 10"
	lines = append(lines[:15], lines[16:]...)
	got := unifiedDiff(old, strings.Join(lines, "\n")+"\n")
	want := "@@ -7,13 +7,12 @@\n line 7\n line 8\n line 9\n-line 10\n+changed 10\n line 11\n line 12\n line 13\n line 14\n line 15\n-line 16\n line 17\n line 18\n line 19"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestUnifiedDiffSeparateHunks(t *testing.T) {
	var lines []string
	for i := 1; i <= 30; i++ {
		lines = append(lines, fmt.Sprintf("l%d", i))
	}
	old := strings.Join(lines, "\n")
	lines[1], lines[27] = "x2", "x28"
	got := unifiedDiff(old, strings.Join(lines, "\n"))
	if n := strings.Count(got, "@@ -"); n != 2 {
		t.Errorf("want 2 hunks, got %d:\n%s", n, got)
	}
	if !strings.Contains(got, "@@ -25,6 +25,6 @@") {
		t.Errorf("second hunk header wrong:\n%s", got)
	}
}

func runTool(t *testing.T, tool *Tool, env *Env, args map[string]any) Result {
	t.Helper()
	in, _ := json.Marshal(args)
	return tool.Run(context.Background(), env, in, nil)
}

func touchLater(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
}

func TestFileChanges(t *testing.T) {
	dir := t.TempDir()
	env := &Env{Cwd: dir}
	path := filepath.Join(dir, "a.go")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if res := runTool(t, Read(), env, map[string]any{"file_path": "a.go"}); res.IsError {
		t.Fatal(res.Text)
	}
	if text, _ := env.FileChanges(); text != "" {
		t.Fatalf("no change yet, got %q", text)
	}
	if res := runTool(t, Edit(), env, map[string]any{"file_path": "a.go", "old_string": "two", "new_string": "2"}); res.IsError {
		t.Fatal(res.Text)
	}
	if text, _ := env.FileChanges(); text != "" {
		t.Fatalf("own edit must not be reported, got %q", text)
	}
	touchLater(t, path, "one\n2\nthree\nfour\n")
	text, paths := env.FileChanges()
	if len(paths) != 1 || paths[0] != "a.go" || !strings.Contains(text, "+four") || strings.Contains(text, "-2") {
		t.Fatalf("got paths %v text:\n%s", paths, text)
	}
	if text, _ := env.FileChanges(); text != "" {
		t.Fatalf("change reported twice: %q", text)
	}
	touchLater(t, path, "one\n2\nthree\nfour\n")
	if text, _ := env.FileChanges(); text != "" {
		t.Fatalf("touch without content change reported: %q", text)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if text, _ := env.FileChanges(); !strings.Contains(text, "a.go was deleted") {
		t.Fatalf("deletion not reported: %q", text)
	}
	env.ForgetFiles()
}

func TestNoDiffChangesOnOneLine(t *testing.T) {
	dir := t.TempDir()
	env := &Env{Cwd: dir}
	a, b := filepath.Join(dir, "a.png"), filepath.Join(dir, "b.bin")
	for _, p := range []string{a, b} {
		if err := os.WriteFile(p, []byte("x\x00y"), 0o644); err != nil {
			t.Fatal(err)
		}
		env.files.remember(p)
	}
	touchLater(t, a, "x\x00z")
	touchLater(t, b, "x\x00z")
	text, paths := env.FileChanges()
	if len(paths) != 2 || !strings.Contains(text, noDiffNotice+"a.png, b.bin") || strings.Count(text, "a.png") != 1 {
		t.Fatalf("got paths %v text:\n%s", paths, text)
	}
	touchLater(t, a, "x\x00w")
	if text, _ := env.FileChanges(); !strings.Contains(text, noDiffNotice+"a.png") {
		t.Fatalf("second change not reported: %q", text)
	}
}
