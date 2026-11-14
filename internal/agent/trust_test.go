package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTrustAndSplit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AICODE_CONFIG_DIR", filepath.Join(home, ".config", "aicode"))
	user := filepath.Join(home, ".claude", "settings.json")
	plugin := filepath.Join(home, ".claude", "plugins", "x", ".claude", "settings.json")
	proj := "/work/repo/.claude/settings.json"
	u, p := SplitHookFiles([]string{user, plugin, proj})
	if len(u) != 1 || u[0] != user || len(p) != 2 {
		t.Errorf("split user=%v project=%v", u, p)
	}
	if Trusted("/a") {
		t.Error("trusted before trusting")
	}
	if err := Trust("/a/b"); err != nil {
		t.Fatal(err)
	}
	if !Trusted("/a/b") || Trusted("/a") {
		t.Error("trust lookup wrong")
	}
	dir := t.TempDir()
	f := filepath.Join(dir, "settings.json")
	os.WriteFile(f, []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo hi"}]}]}}`), 0o600)
	if cmds := HookCommands([]string{f}); len(cmds) != 1 || cmds[0] != "Stop: echo hi" {
		t.Errorf("commands %v", cmds)
	}
}

func TestSelectHookFilesAndRoots(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AICODE_CONFIG_DIR", filepath.Join(home, ".config", "aicode"))
	repo := t.TempDir()
	os.MkdirAll(filepath.Join(repo, ".git"), 0o700)
	os.MkdirAll(filepath.Join(repo, "sub", ".claude"), 0o700)
	os.MkdirAll(filepath.Join(repo, ".claude"), 0o700)
	os.WriteFile(filepath.Join(repo, ".claude", "settings.json"), []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"evil"}]}]}}`), 0o600)
	roots := ProjectRoots(filepath.Join(repo, "sub"))
	if len(roots) != 2 || roots[0] != repo {
		t.Fatalf("roots %v", roots)
	}
	never := func(string, []string) bool { return false }
	files, skipped := SelectHookFiles(roots, []string{".claude/settings.json"}, false, never)
	if !skipped || len(files) != 0 {
		t.Errorf("untrusted headless: %v %v", files, skipped)
	}
	files, skipped = SelectHookFiles(roots, []string{".claude/settings.json"}, true, func(string, []string) bool { return true })
	if skipped || len(files) != 2 {
		t.Errorf("approved interactive: %v %v", files, skipped)
	}
	Trust(repo)
	if _, skipped := SelectHookFiles(roots, []string{".claude/settings.json"}, false, never); skipped {
		t.Error("trusted repo skipped")
	}
}

func TestSubagentArgsAndStopAll(t *testing.T) {
	ro := true
	r := &SubagentRunner{Profile: "p", Provider: "openrouter", ReadOnly: func() bool { return ro }}
	args := strings.Join(r.args("do it", "m", "", "low"), " ")
	if !strings.Contains(args, "-read-only") || !strings.Contains(args, "-provider openrouter") || !strings.Contains(args, "-p p") {
		t.Errorf("args %q", args)
	}
	ro = false
	if strings.Contains(strings.Join(r.args("x", "m", "anth", ""), " "), "-read-only") {
		t.Error("read-only flag without read-only mode")
	}
	ctx, done := r.track(context.Background())
	defer done()
	r.StopAll()
	if context.Cause(ctx) != errModeChanged {
		t.Errorf("cause %v", context.Cause(ctx))
	}
}
