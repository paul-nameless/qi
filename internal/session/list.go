package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/paul-nameless/qi/internal/llm"
)

type Info struct {
	ID       string
	Path     string
	Mod      time.Time
	Messages int
	First    string
}

func List(cwd string, interactive bool) []Info {
	files, _ := sessionFiles(Dir(cwd))
	var out []Info
	for _, f := range files {
		if info, ok := summarize(f.path, interactive); ok {
			info.Mod = f.mod
			out = append(out, info)
		}
	}
	return out
}

func summarize(path string, interactive bool) (Info, bool) {
	info := Info{Path: path, ID: strings.TrimSuffix(filepath.Base(path), ".jsonl")}
	f, err := os.Open(path)
	if err != nil {
		return info, false
	}
	defer f.Close()
	sc := NewScanner(f, 64<<10)
	answered := false
	for sc.Scan() {
		line := sc.Text()
		switch {
		case interactive && headlessMeta(line):
			return info, false
		case strings.HasPrefix(line, dropPrefix):
			info.Messages = max(info.Messages-1, 0)
		case strings.HasPrefix(line, compactionPrefix):
			answered = true
		case strings.HasPrefix(line, messagePrefix):
			info.Messages++
			answered = answered || strings.Contains(line, `"role":"assistant"`)
			if info.First == "" && strings.Contains(line, `"role":"user"`) {
				info.First = firstPrompt(sc.Bytes())
			}
		}
	}
	return info, answered
}

func firstPrompt(line []byte) string {
	var e Entry
	if json.Unmarshal(line, &e) != nil || e.Message == nil {
		return ""
	}
	for _, b := range e.Message.Blocks {
		if b.Type == llm.BlockText && IsPrompt(b.Text) {
			return strings.TrimSpace(b.Text)
		}
	}
	return ""
}

func IsPrompt(text string) bool {
	t := strings.TrimSpace(text)
	return t != "" && !llm.IsInBand(t)
}
