package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/paul-nameless/qi/internal/llm"
	"github.com/paul-nameless/qi/internal/session"
	"github.com/paul-nameless/qi/internal/strutil"
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
	t.Parallel()
	call := llm.Block{Type: llm.BlockToolUse, ID: "c1", Name: "bash", Input: json.RawMessage(`{"command":"ls"}`)}
	stream := entryLines(t,
		session.Entry{Type: "meta", Kind: session.Headless},
		session.Entry{Type: "message", Message: &llm.Message{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "count files"}}}},
		session.Entry{Type: "message", Message: &llm.Message{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockText, Text: "Looking."}, call}}},
		session.Entry{Type: "message", Message: &llm.Message{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockToolResult, ToolUseID: "c1", Text: "a\nb"}}}},
		session.Entry{Type: "message", Message: &llm.Message{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockText, Text: " There are 2 files. "}}}},
	) + "not json\n"
	r := &SubagentRunner{Mode: func() Mode { return ModeYolo }, Cwd: t.TempDir(), Exe: fakeChild(t, stream, "progress noise\n", 0)}
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

func TestSubagentFailureReportsFatalError(t *testing.T) {
	t.Parallel()
	long := "qi: api error 402: " + strings.Repeat("ж", 400)
	cases := []struct{ stderr, want string }{
		{"qi: config: bad key\n", "exit status 1: qi: config: bad key"},
		{"**Reasoning**\nqi: no stdin within 5s, running without it\nthinking\n✓ read a.go\n5 req · done\nqi: api error 402: budget\nlate  words", "exit status 1: qi: api error 402: budget late words"},
		{"thinking\nqi: api error 429: {\n  \"error\": {\n    \"message\": \"slow down\"\n  }\n}\n", `exit status 1: qi: api error 429: { "error": { "message": "slow down" } }`},
		{"thinking\nlast words\n\n", "exit status 1: last words"},
		{"", "exit status 1"},
		{long + "\n", "exit status 1: " + strutil.CutStart(long, maxCause) + "…"},
	}
	r := &SubagentRunner{Mode: func() Mode { return ModeYolo }, Label: func(b llm.Block) string { return b.Name }, Cwd: t.TempDir()}
	for _, c := range cases {
		r.Exe = fakeChild(t, "", c.stderr, 1)
		_, err := r.Run(context.Background(), tools.SubagentTask{Prompt: "x", Model: "m"}, func(string) {})
		if err == nil || err.Error() != c.want || !utf8.ValidString(err.Error()) {
			t.Errorf("stderr %q:\ngot  %v\nwant %s", c.stderr, err, c.want)
		}
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
	r := &SubagentRunner{Mode: func() Mode { return ModeYolo }, Label: func(b llm.Block) string { return b.Name }, Cwd: dir, Exe: script}
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

func TestSubagentEnvCarriesParentSession(t *testing.T) {
	r := &SubagentRunner{SessionID: func() string { return "ABC-1" }}
	env, err := r.Env()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(env, "QI_SUBAGENT=1") || !slices.Contains(env, ParentSessionEnv+"=ABC-1") {
		t.Errorf("env lacks the subagent markers: %q", env[len(env)-3:])
	}
}
