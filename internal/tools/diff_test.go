package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paul-nameless/aicode/internal/llm"
)

type doneUI struct {
	nopUI
	events *[]string
}

func (u doneUI) ToolDone(id string, _ bool, _ string) { *u.events = append(*u.events, "done "+id) }

func TestEditWriteReportDiffs(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("a\nold\nc\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "h.txt"), []byte("one\ntwo\n"), 0o600)
	var events []string
	env := &Env{Cwd: dir, OnDiff: func(d string) { events = append(events, d) }}
	reg := NewRegistry(Read(), Edit(), Write())
	for _, f := range []string{"f.txt", "h.txt"} {
		reg.Execute(context.Background(), env, []llm.Block{{ID: "r", Name: "read", Input: json.RawMessage(`{"file_path":"` + f + `"}`)}}, nopUI{})
	}
	calls := []llm.Block{
		{ID: "1", Name: "edit", Input: json.RawMessage(`{"file_path":"f.txt","old_string":"old","new_string":"new"}`)},
		{ID: "2", Name: "write", Input: json.RawMessage(`{"file_path":"g.txt","content":"x\n"}`)},
		{ID: "3", Name: "write", Input: json.RawMessage(`{"file_path":"h.txt","content":"one\n\u001b]52;c;x\u0007three\n"}`)},
	}
	res := reg.Execute(context.Background(), env, calls, doneUI{events: &events})
	if len(events) != 6 {
		t.Fatalf("events %q", events)
	}
	checks := []struct {
		i    int
		want []string
	}{
		{0, []string{"done 1"}},
		{1, []string{"--- a/f.txt\n+++ b/f.txt\n", "-old\n+new\n"}},
		{3, []string{"--- a/g.txt", "+x\n"}},
		{5, []string{"-two\n+]52;c;xthree\n"}},
	}
	for _, c := range checks {
		for _, w := range c.want {
			if !strings.Contains(events[c.i], w) {
				t.Errorf("event %d %q lacks %q", c.i, events[c.i], w)
			}
		}
	}
	for _, r := range res {
		if strings.Contains(r.Text, "+++") {
			t.Errorf("diff leaked into the model result: %q", r.Text)
		}
	}
}
