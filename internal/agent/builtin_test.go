package agent

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paul-nameless/aicode/internal/config"
)

func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", "")
	return home
}

func TestBuiltinSkillInStaticPrompt(t *testing.T) {
	isolateHome(t)
	cfg := config.Config{SkillDirs: []string{".claude/skills"}}
	projA, projB := t.TempDir(), t.TempDir()
	skills, a := LoadPrompt(projA, cfg)
	_, b := LoadPrompt(projB, cfg)
	if len(skills) != 1 || skills[0].Name != "aicode-config" || !skills[0].Builtin {
		t.Fatalf("skills %+v", skills)
	}
	static := a.Text[:a.Static]
	if !strings.Contains(static, "- aicode-config: Read and change aicode's own settings") || !strings.Contains(static, skills[0].Path) {
		t.Errorf("built-in skill missing from the static part: %q", static)
	}
	if static != b.Text[:b.Static] || strings.Contains(static, projA) {
		t.Error("static part depends on the directory")
	}
	dir := filepath.Dir(skills[0].Path)
	for name, want := range map[string][]byte{"SKILL.md": configSkill, "schema.json": config.Schema} {
		if got, err := os.ReadFile(filepath.Join(dir, name)); err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s not written: %v", name, err)
		}
	}
	meta, body := ParseFrontmatter(string(configSkill))
	if meta.Name != "aicode-config" || !strings.Contains(body, "schema.json") || !strings.Contains(body, "builtin_skills") {
		t.Errorf("skill file: %+v", meta)
	}
}

func TestBuiltinSkillsCanBeDisabled(t *testing.T) {
	isolateHome(t)
	off := false
	skills, sys := LoadPrompt(t.TempDir(), config.Config{BuiltinSkills: &off})
	if len(skills) != 0 || strings.Contains(sys.Text, "aicode-config") {
		t.Errorf("disabled built-in skill still listed: %+v", skills)
	}
}

func TestUserSkillOverridesBuiltin(t *testing.T) {
	home := isolateHome(t)
	own := filepath.Join(home, "skills", "aicode-config", "SKILL.md")
	os.MkdirAll(filepath.Dir(own), 0o755)
	os.WriteFile(own, []byte("---\nname: aicode-config\ndescription: mine\n---\n"), 0o600)
	skills, sys := LoadPrompt(t.TempDir(), config.Config{SkillDirs: []string{filepath.Join(home, "skills")}})
	if len(skills) != 1 || skills[0].Path != own || !strings.Contains(sys.Text, "- aicode-config: mine") {
		t.Errorf("user skill should win: %+v", skills)
	}
}

func TestBuiltinSkillStaysGlobalInsideHome(t *testing.T) {
	home := isolateHome(t)
	os.MkdirAll(filepath.Join(home, ".git"), 0o755)
	skills, sys := LoadPrompt(home, config.Config{})
	if len(skills) != 1 || !strings.Contains(sys.Text[:sys.Static], "- aicode-config:") {
		t.Errorf("built-in skill moved out of the static part when cwd contains the cache dir")
	}
}
