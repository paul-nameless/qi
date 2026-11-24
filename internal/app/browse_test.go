package app

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paul-nameless/qi/internal/ui"
)

func keyEditor(t *testing.T, keys ...ui.Key) *ui.Editor {
	f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	term := &ui.Terminal{Keys: make(chan ui.Key, len(keys))}
	for _, k := range keys {
		term.Keys <- k
	}
	return ui.NewEditor(term, ui.NewPrinter(f), "")
}

func runes(s string) []ui.Key {
	var keys []ui.Key
	for _, r := range s {
		keys = append(keys, ui.Key{Kind: ui.KeyRune, Rune: r})
	}
	return keys
}

func browseTree(t *testing.T) string {
	dir := t.TempDir()
	for _, p := range []string{"b dir/inner.go", "a.txt", ".hidden", "sub/x.go"} {
		path := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(dir, "sub"), filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestReadDirListsEverythingFoldersFirst(t *testing.T) {
	var names []string
	for _, it := range readDir(browseTree(t)) {
		names = append(names, it.Name)
	}
	if got := strings.Join(names, ","); got != "b dir/,link/,sub/,.hidden,a.txt" {
		t.Errorf("got %s", got)
	}
}

func TestBrowseFiles(t *testing.T) {
	dir := browseTree(t)
	enter := ui.Key{Kind: ui.KeyEnter}
	cases := []struct {
		start string
		keys  []ui.Key
		want  string
	}{
		{dir, append(runes("bdir"), enter, enter), "b dir/inner.go"},
		{dir, append(runes("sub"), ui.Key{Kind: ui.KeyTab}), "sub/"},
		{filepath.Join(dir, "sub"), append([]ui.Key{{Kind: ui.KeyLeft}}, append(runes("a.txt"), enter)...), filepath.Join(dir, "a.txt")},
	}
	for i, c := range cases {
		got, ok := browseFiles(keyEditor(t, c.keys...), c.start)
		if !ok || got != c.want {
			t.Errorf("%d: got %q %v, want %q", i, got, ok, c.want)
		}
	}
	if _, ok := browseFiles(keyEditor(t, ui.Key{Kind: ui.KeyEsc}), dir); ok {
		t.Error("esc should cancel")
	}
}

func TestPickLine(t *testing.T) {
	lines := readLines(strings.NewReader("alpha\tfirst\n\nbeta\tsecond\r\n"))
	if len(lines) != 2 || lines[1] != "beta\tsecond" {
		t.Fatalf("lines %q", lines)
	}
	got, ok := pickLine(keyEditor(t, append(runes("sec"), ui.Key{Kind: ui.KeyEnter})...), lines, "> ", true, false)
	if !ok || got != "beta\tsecond" {
		t.Errorf("searching descriptions: %q %v", got, ok)
	}
	if _, ok := pickLine(keyEditor(t, append(runes("sec"), ui.Key{Kind: ui.KeyEsc})...), lines, "> ", false, false); ok {
		t.Error("names only: sec matches nothing, esc cancels")
	}
}

func TestPickIgnoresUnknownFlags(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.String("p", "", "")
	fs.Bool("d", false, "")
	got := strings.Join(knownFlags(fs, []string{"--multi", "-p", "@ ", "--preview", "help", "-d", "--bind=x"}), "|")
	if got != "-p|@ |-d" {
		t.Errorf("got %q", got)
	}
}
