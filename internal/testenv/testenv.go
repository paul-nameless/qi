package testenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var cleared = []string{"OPENAI_API_KEY", "OPENROUTER_API_KEY", "ANTHROPIC_API_KEY", "PAGER", "XDG_CONFIG_HOME", "XDG_DATA_HOME"}

func Clean() {
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "QI_") || strings.HasPrefix(name, "GIT_") {
			os.Unsetenv(name)
		}
	}
	for _, name := range cleared {
		os.Unsetenv(name)
	}
}

func Run(m *testing.M) int {
	Clean()
	dir, err := os.MkdirTemp("", "qi-test-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)
	os.Setenv("QI_CONFIG_DIR", filepath.Join(dir, "config"))
	os.Setenv("QI_SESSION_DIR", filepath.Join(dir, "sessions"))
	os.Setenv("TMPDIR", filepath.Join(dir, "tmp"))
	if err := os.Mkdir(filepath.Join(dir, "tmp"), 0o700); err != nil {
		panic(err)
	}
	return m.Run()
}
