package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/paul-nameless/qi/internal/llm"
	"github.com/paul-nameless/qi/internal/session"
	"github.com/paul-nameless/qi/internal/tools"
)

func entryLines(t *testing.T, entries ...session.Entry) string {
	var b strings.Builder
	for _, e := range entries {
		data, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(append(data, '\n'))
	}
	return b.String()
}

func fakeChild(t *testing.T, stdout, stderr string, code int) string {
	dir := t.TempDir()
	out, errf := filepath.Join(dir, "out"), filepath.Join(dir, "err")
	os.WriteFile(out, []byte(stdout), 0o600)
	os.WriteFile(errf, []byte(stderr), 0o600)
	script := filepath.Join(dir, "qi")
	body := "#!/bin/sh\ncase \" $* \" in *\" -json \"*) ;; *) echo 'no -json' >&2; exit 3;; esac\ncat " + out + "\ncat " + errf + " >&2\nexit " + string(rune('0'+code)) + "\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return script
}

func TestSubagentReadsJSONEntries(t *testing.T) {
	call := llm.Block{Type: llm.BlockToolUse, ID: "c1", Name: "bash", Input: json.RawMessage(`{"command":"ls"}`)}
	stream := entryLines(t,
		session.Entry{Type: "meta", Kind: session.Headless},
		session.Entry{Type: "message", Message: &llm.Message{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "count files"}}}},
		session.Entry{Type: "message", Message: &llm.Message{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockText, Text: "Looking."}, call}}},
		session.Entry{Type: "message", Message: &llm.Message{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockToolResult, ToolUseID: "c1", Text: "a\nb"}}}},
		session.Entry{Type: "message", Message: &llm.Message{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockText, Text: " There are 2 files. "}}}},
	) + "not json\n"
	r := &SubagentRunner{ReadOnly: func() bool { return false }, Cwd: t.TempDir(), Exe: fakeChild(t, stream, "progress noise\n", 0)}
	r.Label = func(b llm.Block) string { return tools.Bash().Label(nil, b.Input) }
	var progress []string
	answer, err := r.Run(context.Background(), tools.SubagentTask{Prompt: "count files", Model: "m"}, func(s string) { progress = append(progress, s) })
	if err != nil || answer != "There are 2 files." {
		t.Fatalf("answer %q err %v", answer, err)
	}
	if !reflect.DeepEqual(progress, []string{"bash ls"}) {
		t.Errorf("progress %q", progress)
	}
}

func TestSubagentFailureKeepsStderrTail(t *testing.T) {
	r := &SubagentRunner{ReadOnly: func() bool { return false }, Label: func(b llm.Block) string { return b.Name }, Cwd: t.TempDir(), Exe: fakeChild(t, "", "qi: config: bad key\n", 1)}
	_, err := r.Run(context.Background(), tools.SubagentTask{Prompt: "x", Model: "m"}, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "exit status 1") || !strings.Contains(err.Error(), "qi: config: bad key") {
		t.Errorf("err %v", err)
	}
	r.Exe = fakeChild(t, entryLines(t, session.Entry{Type: "meta"}), "", 0)
	if answer, err := r.Run(context.Background(), tools.SubagentTask{Prompt: "x", Model: "m"}, func(string) {}); err != nil || answer != "(the subagent returned no answer)" {
		t.Errorf("no answer: %q %v", answer, err)
	}
}

func TestSubagentCancelWithGrandchildHoldingStdout(t *testing.T) {
	if _, err := exec.LookPath("perl"); err != nil {
		t.Skip("perl not installed")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "qi")
	body := "#!/bin/sh\nperl -MPOSIX -e 'fork and exit; POSIX::setsid(); sleep 30' &\necho started >&2\nexec sleep 30\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	r := &SubagentRunner{ReadOnly: func() bool { return false }, Label: func(b llm.Block) string { return b.Name }, Cwd: dir, Exe: script}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	start := time.Now()
	_, err := r.Run(ctx, tools.SubagentTask{Prompt: "x", Model: "m"}, func(string) {})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err %v", err)
	}
	if d := time.Since(start); d > subagentGrace+3*time.Second {
		t.Errorf("cancel took %v", d)
	}
}
