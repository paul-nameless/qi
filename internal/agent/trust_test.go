package agent

import (
	"os"
	"path/filepath"
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
