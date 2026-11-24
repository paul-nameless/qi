package app

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/paul-nameless/qi/internal/ui"
)

type dirEntry struct {
	name string
	dir  bool
}

func browseFiles(ed *ui.Editor, cwd string) (string, bool) {
	dir := cwd
	var entries []dirEntry
	list := func() *ui.PickList {
		entries = readDir(dir)
		items := make([]ui.PickItem, len(entries))
		for i, e := range entries {
			items[i] = ui.PickItem{Name: e.name}
			if e.dir {
				items[i] = ui.PickItem{Name: e.name + "/", Color: ui.Blue}
			}
		}
		return &ui.PickList{Prompt: "@ " + browsePrompt(cwd, dir), Items: items}
	}
	up := func() *ui.PickList {
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil
		}
		dir = parent
		return list()
	}
	i, ok := ed.Pick(*list(), ui.PickOptions{
		OnPick: func(i int) *ui.PickList {
			if !entries[i].dir {
				return nil
			}
			dir = filepath.Join(dir, entries[i].name)
			return list()
		},
		OnBack: up,
	})
	if !ok {
		return "", false
	}
	path := relPath(cwd, filepath.Join(dir, entries[i].name))
	if entries[i].dir {
		path += "/"
	}
	return path, true
}

func browsePrompt(cwd, dir string) string {
	rel, err := filepath.Rel(cwd, dir)
	switch {
	case err != nil || strings.HasPrefix(rel, ".."):
		return dir + "/"
	case rel == ".":
		return ""
	}
	return rel + "/"
}

func readDir(dir string) []dirEntry {
	des, _ := os.ReadDir(dir)
	out := make([]dirEntry, 0, len(des))
	for _, de := range des {
		isDir := de.IsDir()
		if de.Type()&os.ModeSymlink != 0 {
			if st, err := os.Stat(filepath.Join(dir, de.Name())); err == nil {
				isDir = st.IsDir()
			}
		}
		out = append(out, dirEntry{de.Name(), isDir})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].dir && !out[j].dir })
	return out
}
