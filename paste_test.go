package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPasteImage(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "paste")
	p1, err := pasteImage("printf PNG > {file}", "", dir)
	if err != nil || filepath.Base(p1) != "1.png" {
		t.Fatalf("got %q, %v", p1, err)
	}
	p2, err := pasteImage("printf PNG2", "", dir)
	data, _ := os.ReadFile(p2)
	if err != nil || filepath.Base(p2) != "2.png" || string(data) != "PNG2" {
		t.Fatalf("stdout mode: %q %q %v", p2, data, err)
	}
	for _, c := range []string{"false", "true", ": > {file}"} {
		if _, err := pasteImage(c, "", dir); err == nil || err.Error() != "no image in clipboard" {
			t.Errorf("%s: %v", c, err)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 2 {
		t.Errorf("failed pastes left files: %d", len(entries))
	}
}
