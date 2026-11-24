package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	fileListLimit = 20000
	fileListTime  = 2 * time.Second
)

func listFiles(dir string, limit int) []string {
	seen := map[string]bool{}
	var files []string
	add := func(p string) bool {
		if !seen[p] {
			seen[p] = true
			files = append(files, p)
		}
		return len(files) < limit
	}
	for _, p := range gitFiles(dir) {
		if !add(p) {
			return files
		}
	}
	walkFiles(dir, time.Now().Add(fileListTime), add)
	return files
}

func gitFiles(dir string) []string {
	ctx, cancel := context.WithTimeout(context.Background(), fileListTime)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-c", "core.fsmonitor=", "ls-files", "-co", "--exclude-standard", "-z")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		return nil
	}
	files := strings.Split(strings.TrimRight(string(out), "\x00"), "\x00")
	sort.SliceStable(files, func(i, j int) bool { return depth(files[i]) < depth(files[j]) })
	return files
}

func depth(path string) int {
	return strings.Count(path, "/")
}

func walkFiles(dir string, deadline time.Time, add func(string) bool) {
	queue := []string{"."}
	for len(queue) > 0 && time.Now().Before(deadline) {
		rel := queue[0]
		queue = queue[1:]
		entries, err := os.ReadDir(filepath.Join(dir, rel))
		if err != nil {
			continue
		}
		for _, e := range entries {
			p := filepath.Join(rel, e.Name())
			switch {
			case e.IsDir():
				if e.Name() != ".git" {
					queue = append(queue, p)
				}
			case !add(p):
				return
			}
		}
	}
}
