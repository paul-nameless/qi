package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func pasteDir() string {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	return filepath.Join(base, "aicode", "paste")
}

func nextPastePath(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	entries, _ := os.ReadDir(dir)
	for n := len(entries) + 1; ; n++ {
		p := filepath.Join(dir, fmt.Sprintf("%d.png", n))
		if _, err := os.Stat(p); os.IsNotExist(err) {
			return p, nil
		}
	}
}

func pasteImage(command, picker, dir string) (string, error) {
	path, err := nextPastePath(dir)
	if err != nil {
		return "", err
	}
	out, err := runQuiet(command, picker, map[string]string{"file": path}, "")
	if err == nil && !strings.Contains(command, "{file}") {
		err = os.WriteFile(path, []byte(out), 0o600)
	}
	if st, serr := os.Stat(path); err != nil || serr != nil || st.Size() == 0 {
		os.Remove(path)
		return "", fmt.Errorf("no image in clipboard")
	}
	return path, nil
}

func (r *repl) pasteImage() (string, bool) {
	path, err := pasteImage(r.agent.Cfg.PasteImageCmd, r.agent.Cfg.Picker, pasteDir())
	if err != nil {
		r.display.Info(err.Error())
		return "", false
	}
	return path, true
}
