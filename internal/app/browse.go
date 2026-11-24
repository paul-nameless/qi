package app

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/paul-nameless/qi/internal/ui"
)

func browseFiles(ed *ui.Editor, cwd string) (string, bool) {
	dir := cwd
	var items []ui.PickItem
	list := func() *ui.PickList {
		items = readDir(dir)
		prompt := "@ "
		if rel := relPath(cwd, dir); rel != "." {
			prompt += strings.TrimSuffix(rel, "/") + "/"
		}
		return &ui.PickList{Prompt: prompt, Items: items}
	}
	i, ok := ed.Pick(*list(), ui.PickOptions{
		OnPick: func(i int) *ui.PickList {
			if !strings.HasSuffix(items[i].Name, "/") {
				return nil
			}
			dir = filepath.Join(dir, items[i].Name)
			return list()
		},
		OnBack: func() *ui.PickList {
			if filepath.Dir(dir) == dir {
				return nil
			}
			dir = filepath.Dir(dir)
			return list()
		},
	})
	if !ok {
		return "", false
	}
	path := relPath(cwd, filepath.Join(dir, items[i].Name))
	if strings.HasSuffix(items[i].Name, "/") {
		path += "/"
	}
	return path, true
}

func readDir(dir string) []ui.PickItem {
	des, _ := os.ReadDir(dir)
	items := make([]ui.PickItem, 0, len(des))
	for _, de := range des {
		item := ui.PickItem{Name: de.Name()}
		if st, err := os.Stat(filepath.Join(dir, de.Name())); err == nil && st.IsDir() {
			item = ui.PickItem{Name: de.Name() + "/", Color: ui.Blue}
		}
		items = append(items, item)
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Color != "" && items[j].Color == "" })
	return items
}
