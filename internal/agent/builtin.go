package agent

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/paul-nameless/qi/internal/config"
)

//go:embed builtin/qi-config/SKILL.md
var configSkill []byte

func BuiltinSkillDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "qi", "skills"), nil
}

func BuiltinSkills() []Skill {
	base, err := BuiltinSkillDir()
	if err != nil {
		return nil
	}
	dir := filepath.Join(base, "qi-config")
	files := map[string][]byte{"SKILL.md": configSkill, "schema.json": config.Schema}
	if err := writeFiles(dir, files); err != nil {
		fmt.Fprintf(os.Stderr, "qi: built-in skill qi-config: %v\n", err)
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
