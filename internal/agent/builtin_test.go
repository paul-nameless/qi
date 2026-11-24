package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paul-nameless/qi/internal/config"
	"github.com/paul-nameless/qi/internal/llm"
	"github.com/paul-nameless/qi/internal/tools"
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
	skills, a := LoadPrompt(projA, cfg, "")
	_, b := LoadPrompt(projB, cfg, "")
	if len(skills) != 1 || skills[0].Name != "qi-config" || !skills[0].Builtin {
		t.Fatalf("skills %+v", skills)
	}
	static := a.Text[:a.Static]
	if !strings.Contains(static, "- [qi-config]("+skills[0].Path+") - Read and change qi's own settings") || !strings.Contains(static, skills[0].Path) {
		t.Errorf("built-in skill missing from the static part: %q", static)
	}
	if static != b.Text[:b.Static] || strings.Contains(static, projA) {
		t.Error("static part depends on the directory")
	}
	if skills[0].Path != "qi:skills/qi-config/SKILL.md" {
		t.Errorf("built-in skill path %q", skills[0].Path)
	}
	for path, want := range map[string][]byte{configSkillPath: configSkill, configSchemaPath: config.Schema} {
		if got, ok := tools.Embedded(path); !ok || !bytes.Equal(got, want) {
			t.Errorf("%s not served from memory", path)
		}
	}
	if cache, err := os.UserCacheDir(); err == nil {
		if entries, _ := os.ReadDir(cache); len(entries) != 0 {
			t.Errorf("nothing should be written to the cache dir: %v", entries)
		}
	}
	env := &tools.Env{Cwd: t.TempDir()}
	run := tools.Run(tools.Read(), tools.Write())
	res := run.Execute(context.Background(), env, []llm.Block{{ID: "r", Name: "run", Input: json.RawMessage(`{"code":"const s = await read('qi:skills/qi-config/SKILL.md'); const j = JSON.parse(await read('qi:skills/qi-config/schema.json')); let w = ''; try { await write('qi:skills/qi-config/SKILL.md', 'x') } catch (e) { w = e.message } return [s.includes('qi-config'), typeof j.properties, w].join('|')"}`)}}, nopDisplay{})
	if len(res) != 1 || !strings.Contains(res[0].Text, "true|object|") || !strings.Contains(res[0].Text, "read-only") {
		t.Errorf("reading the real built-in paths: %+v", res)
	}
	meta, body := ParseFrontmatter(string(configSkill))
	if meta.Name != "qi-config" || !strings.Contains(body, "schema.json") || !strings.Contains(body, "builtin_skills") {
		t.Errorf("skill file: %+v", meta)
	}
}

func TestBuiltinSkillsCanBeDisabled(t *testing.T) {
	isolateHome(t)
	off := false
	skills, sys := LoadPrompt(t.TempDir(), config.Config{BuiltinSkills: &off}, "")
	if len(skills) != 0 || strings.Contains(sys.Text, "qi-config") {
		t.Errorf("disabled built-in skill still listed: %+v", skills)
	}
}

func TestUserSkillOverridesBuiltin(t *testing.T) {
	home := isolateHome(t)
	own := filepath.Join(home, "skills", "qi-config", "SKILL.md")
	os.MkdirAll(filepath.Dir(own), 0o755)
	os.WriteFile(own, []byte("---\nname: qi-config\ndescription: mine\n---\n"), 0o600)
	skills, sys := LoadPrompt(t.TempDir(), config.Config{SkillDirs: []string{filepath.Join(home, "skills")}}, "")
	if len(skills) != 1 || skills[0].Path != own || !strings.Contains(sys.Text, "- [qi-config]("+own+") - mine") {
		t.Errorf("user skill should win: %+v", skills)
	}
}

func TestBuiltinSkillStaysGlobalInsideHome(t *testing.T) {
	home := isolateHome(t)
	os.MkdirAll(filepath.Join(home, ".git"), 0o755)
	skills, sys := LoadPrompt(home, config.Config{}, "")
	if len(skills) != 1 || !strings.Contains(sys.Text[:sys.Static], "- [qi-config](") {
		t.Errorf("built-in skill moved out of the static part when cwd contains the cache dir")
	}
}

func TestScratchDirNamedAndCreated(t *testing.T) {
	scratch := filepath.Join(t.TempDir(), "qi-1", "S1")
	if err := os.Mkdir(filepath.Dir(scratch), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, sys := LoadPrompt(t.TempDir(), config.Config{}, scratch); strings.Contains(sys.Text, "Scratch directory:") {
		t.Error("scratch named under a parent other users can read")
	}
	if err := os.Chmod(filepath.Dir(scratch), 0o700); err != nil {
		t.Fatal(err)
	}
	_, sys := LoadPrompt(t.TempDir(), config.Config{}, scratch)
	if !strings.Contains(sys.Text, "Scratch directory: "+scratch+"\n") {
		t.Errorf("scratch dir missing from prompt:\n%s", sys.Text[sys.Static:])
	}
	if st, err := os.Stat(scratch); err != nil || !st.IsDir() || st.Mode().Perm() != 0o700 {
		t.Errorf("scratch dir not created 0700: %v %v", st, err)
	}
	if _, sys := LoadPrompt(t.TempDir(), config.Config{}, ""); strings.Contains(sys.Text, "Scratch directory:") {
		t.Error("empty scratch still named")
	}
	if !strings.HasSuffix(sys.Text, "</env>\n") || sys.Session < sys.Static || !strings.HasPrefix(sys.Text[sys.Session:], "\n\n<env>") {
		t.Errorf("env is not the session tail: static %d session %d", sys.Static, sys.Session)
	}
	link := filepath.Join(t.TempDir(), "qi-1")
	if err := os.Symlink(filepath.Dir(scratch), link); err != nil {
		t.Fatal(err)
	}
	if got := ensureScratch(filepath.Join(link, "S2")); got != "" {
		t.Errorf("symlinked parent accepted: %s", got)
	}
	if a, b := ScratchDir("A"), ScratchDir("B"); a == b || filepath.Dir(a) != filepath.Dir(b) {
		t.Errorf("ScratchDir: %s %s", a, b)
	}
}

func TestStaticPromptIsDirectoryFree(t *testing.T) {
	isolateHome(t)
	cfg := config.Config{SkillDirs: []string{".claude/skills"}}
	projA, projB := t.TempDir(), t.TempDir()
	_, a := LoadPrompt(projA, cfg, "")
	_, b := LoadPrompt(projB, cfg, "")
	static := a.Text[:a.Static]
	if static != b.Text[:b.Static] || strings.Contains(static, projA) {
		t.Error("static part depends on the directory")
	}
	if !strings.HasPrefix(static, basePrompt) || !strings.Contains(static, skillsIntro) || !strings.Contains(static, "Your only tool is run") {
		t.Errorf("wrong base prompt or skills intro:\n%s", static)
	}
}
