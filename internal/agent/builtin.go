package agent

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/paul-nameless/aicode/internal/config"
)

//go:embed builtin/aicode-config/SKILL.md
var configSkill []byte

func BuiltinSkillDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "aicode", "skills"), nil
}

func BuiltinSkills() []Skill {
	base, err := BuiltinSkillDir()
	if err != nil {
		return nil
	}
	dir := filepath.Join(base, "aicode-config")
	files := map[string][]byte{"SKILL.md": configSkill, "schema.json": config.Schema}
	if err := writeFiles(dir, files); err != nil {
		fmt.Fprintf(os.Stderr, "aicode: built-in skill aicode-config: %v\n", err)
		return nil
	}
	meta, _ := ParseFrontmatter(string(configSkill))
	return []Skill{{Name: meta.Name, Description: strings.TrimSpace(meta.Description), Path: filepath.Join(dir, "SKILL.md"), Builtin: true}}
}

func writeFiles(dir string, files map[string][]byte) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for name, data := range files {
		path := filepath.Join(dir, name)
		if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, data) {
			continue
		}
		if err := writeAtomic(path, data); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	}
	return nil
}

func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
