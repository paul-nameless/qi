package session

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/paul-nameless/qi/internal/llm"
)

func call(id string) llm.Message {
	return llm.Message{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockToolUse, ID: id, Name: "bash", Input: json.RawMessage(`{}`)}}}
}

func result(id string) llm.Message {
	return llm.Message{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockToolResult, ToolUseID: id, Text: "ok"}}}
}

func resultIDs(m llm.Message) []string {
	var ids []string
	for _, b := range m.Blocks {
		if b.Type == llm.BlockToolResult {
			ids = append(ids, b.ToolUseID)
		}
	}
	return ids
}

func TestRepair(t *testing.T) {
	t.Run("trailing orphan gets a result", func(t *testing.T) {
		out := Repair([]llm.Message{llm.UserText("hi"), call("c1")})
		if len(out) != 3 || len(resultIDs(out[2])) != 1 || resultIDs(out[2])[0] != "c1" {
			t.Fatalf("got %+v", out)
		}
	})
	t.Run("orphan in the middle is answered before the next user text", func(t *testing.T) {
		out := Repair([]llm.Message{llm.UserText("hi"), call("c1"), llm.UserText("again"), {Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockText, Text: "ok"}}}})
		if len(out) != 4 {
			t.Fatalf("len %d: %+v", len(out), out)
		}
		if ids := resultIDs(out[2]); len(ids) != 1 || ids[0] != "c1" || out[2].Text() != "again" {
			t.Fatalf("got %+v", out[2])
		}
	})
	t.Run("answered call is untouched", func(t *testing.T) {
		in := []llm.Message{llm.UserText("hi"), call("c1"), result("c1")}
		if out := Repair(in); len(out) != 3 || len(out[2].Blocks) != 1 {
			t.Fatalf("got %+v", out)
		}
	})
}

func TestRoundTrip(t *testing.T) {
	t.Setenv("QI_SESSION_DIR", t.TempDir())
	s, err := Create(CreateOptions{Cwd: "/tmp/proj", Model: "m", Kind: Interactive})
	if err != nil {
		t.Fatal(err)
	}
	reasoning := llm.Block{Type: llm.BlockReasoning, Raw: json.RawMessage(`{"type":"reasoning.encrypted","data":"x"}`), Model: "m"}
	_ = s.Message(llm.UserText("first"), "m", nil)
	_ = s.Message(llm.Message{Role: llm.RoleAssistant, Blocks: []llm.Block{reasoning, {Type: llm.BlockText, Text: "a"}}}, "m", &llm.Usage{Input: 100, Output: 20})
	_ = s.Compaction("summary text", nil, "m")
	_ = s.Message(llm.UserText("after"), "m", nil)
	_ = s.Message(llm.Message{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockText, Text: "b"}}}, "m", &llm.Usage{Input: 50, Output: 5})
	s.Close()

	f, _ := os.OpenFile(s.Path, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString(`{"type":"message","mess`)
	f.Close()

	s2, ld, err := Open(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ld.Messages) != 3 || ld.Messages[0].Text() != "summary text" || ld.Messages[2].Text() != "b" {
		t.Fatalf("messages: %+v", ld.Messages)
	}
	if ld.CtxTokens != 55 {
		t.Fatalf("ctx tokens %d", ld.CtxTokens)
	}
	if err := s2.Message(llm.UserText("third"), "m", nil); err != nil {
		t.Fatal(err)
	}
	s2.Close()
	_, ld2, err := Open(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	if last := ld2.Messages[len(ld2.Messages)-1]; last.Text() != "third" {
		t.Fatalf("entry after torn line was lost: %+v", ld2.Messages)
	}
}

func TestLatestSkipsEmptySessions(t *testing.T) {
	t.Setenv("QI_SESSION_DIR", t.TempDir())
	s1, _ := Create(CreateOptions{Cwd: "/p", Model: "m", Kind: Interactive})
	s1.Message(llm.UserText("real work"), "m", nil)
	s1.Message(llm.Message{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockText, Text: "done"}}}, "m", nil)
	s1.Close()
	s2, _ := Create(CreateOptions{Cwd: "/p", Model: "m", Kind: Interactive})
	s2.Message(llm.UserText("asked but never answered"), "m", nil)
	s2.Close()
	s3, _ := Create(CreateOptions{Cwd: "/p", Model: "m", Kind: Headless})
	s3.Message(llm.UserText("one-shot"), "m", nil)
	s3.Message(llm.Message{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockText, Text: "ok"}}}, "m", nil)
	s3.Close()
	now := time.Now()
	for i, s := range []*Session{s1, s2, s3} {
		at := now.Add(time.Duration(i-3) * time.Minute)
		if err := os.Chtimes(s.Path, at, at); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := Latest("/p", true); err != nil || got != s1.Path {
		t.Errorf("interactive latest %q, want %q (%v)", got, s1.Path, err)
	}
	if got, _ := Latest("/p", false); got != s3.Path {
		t.Errorf("headless latest %q, want %q", got, s3.Path)
	}
}

func TestDropEntry(t *testing.T) {
	t.Setenv("QI_SESSION_DIR", t.TempDir())
	s, _ := Create(CreateOptions{Cwd: "/p", Model: "m", Kind: Interactive})
	s.Message(llm.UserText("keep"), "m", nil)
	s.Message(llm.UserText("bad image prompt"), "m", nil)
	s.Drop()
	s.Close()
	_, ld, err := Open(s.Path)
	if err != nil || len(ld.Messages) != 1 || ld.Messages[0].Text() != "keep" {
		t.Errorf("drop not honored: %+v %v", ld.Messages, err)
	}
}

func TestIDs(t *testing.T) {
	id := NewID()
	if len(id) != 36 || strings.ToUpper(id) != id || !ValidID(id) {
		t.Errorf("bad id %q", id)
	}
	for _, bad := range []string{"", "../x", "a/b", "x y"} {
		if ValidID(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
	t.Setenv("QI_SESSION_DIR", t.TempDir())
	s, err := Create(CreateOptions{Cwd: "/p", ID: "MY-ID", Model: "m", Kind: Interactive})
	if err != nil || s.ID != "MY-ID" || s.Path != PathFor("/p", "MY-ID") {
		t.Errorf("create with id: %+v %v", s, err)
	}
	if !strings.HasSuffix(Dir("/Users/x/my.proj"), "-Users-x-my-proj") {
		t.Errorf("dir %q", Dir("/Users/x/my.proj"))
	}
}

func TestDirMigratesLegacyName(t *testing.T) {
	base := t.TempDir()
	t.Setenv("QI_SESSION_DIR", base)
	legacy := filepath.Join(base, "Users-x-a.b")
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	dir := Dir("/Users/x/a.b")
	if _, err := os.Stat(dir); err != nil || dir == legacy {
		t.Fatalf("legacy dir not renamed to %s: %v", dir, err)
	}
}

func TestSessionDirFollowsXDGDataHome(t *testing.T) {
	data := t.TempDir()
	t.Setenv("QI_SESSION_DIR", "")
	t.Setenv("XDG_DATA_HOME", data)
	if got := Dir("/p"); got != filepath.Join(data, "qi", "sessions", "-p") {
		t.Errorf("dir %q", got)
	}
	t.Setenv("QI_SESSION_DIR", "/s")
	if got := Dir("/p"); got != "/s/-p" {
		t.Errorf("QI_SESSION_DIR does not win: %q", got)
	}
}

func TestTeeMatchesFile(t *testing.T) {
	t.Setenv("QI_SESSION_DIR", t.TempDir())
	var out bytes.Buffer
	s, err := Create(CreateOptions{Cwd: "/p", Model: "m", Kind: Headless, Tee: &out})
	if err != nil {
		t.Fatal(err)
	}
	s.Message(llm.UserText("hi"), "m", nil)
	s.Drop()
	s.Compaction("sum", nil, "m")
	s.Close()
	data, err := os.ReadFile(s.Path)
	if err != nil || !bytes.Equal(data, out.Bytes()) || bytes.Count(data, []byte("\n")) != 4 {
		t.Errorf("tee differs from the file:\nfile %s\ntee  %s", data, out.Bytes())
	}
	r, _, err := Open(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	var later bytes.Buffer
	r.SetTee(&later)
	r.Message(llm.UserText("again"), "m", nil)
	r.Close()
	data, _ = os.ReadFile(s.Path)
	if !bytes.HasSuffix(data, later.Bytes()) || bytes.Count(later.Bytes(), []byte("\n")) != 1 {
		t.Errorf("tee after open: %s", later.Bytes())
	}
}

func TestSessionLock(t *testing.T) {
	t.Setenv("QI_SESSION_DIR", t.TempDir())
	s, err := Create(CreateOptions{Cwd: "/p", Model: "m", Kind: Interactive})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = Open(s.Path)
	want := fmt.Sprintf("session %s is open in another qi (pid %d)", s.ID, os.Getpid())
	if err == nil || err.Error() != want {
		t.Fatalf("second open: %v, want %q", err, want)
	}
	if _, err := Create(CreateOptions{Cwd: "/p", ID: s.ID, Model: "m"}); err == nil {
		t.Error("create with the id of an open session succeeded")
	}
	if _, err := Entries(s.Path); err != nil {
		t.Errorf("reading an open session: %v", err)
	}
	s.Close()
	r, _, err := Open(s.Path)
	if err != nil {
		t.Fatalf("open after close: %v", err)
	}
	r.Close()
	if _, err := os.Stat(pidFile(s.Path)); err == nil {
		t.Error("pid file left after close")
	}
}
