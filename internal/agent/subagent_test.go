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
	"strconv"
	"strings"
	"syscall"
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
	script := filepath.Join(t.TempDir(), "qi")
	body := "#!/bin/sh\ncase \" $* \" in *\" -json \"*) ;; *) echo 'no -json' >&2; exit 3;; esac\ncat \"$0.out\"\ncat \"$0.err\" >&2\nexit $(cat \"$0.code\")\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	setChild(t, script, stdout, stderr, code)
	return script
}

func setChild(t *testing.T, script, stdout, stderr string, code int) {
	for ext, data := range map[string]string{".out": stdout, ".err": stderr, ".code": strconv.Itoa(code)} {
		if err := os.WriteFile(script+ext, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
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
	r := &SubagentRunner{Mode: func() Mode { return ModeYolo }, Label: func(b llm.Block) string { return b.Name }, Cwd: t.TempDir(), Exe: fakeChild(t, "", "", 1)}
	for _, c := range cases {
		setChild(t, r.Exe, "", c.stderr, 1)
		_, err := r.Run(context.Background(), tools.SubagentTask{Prompt: "x", Model: "m"}, func(string) {})
		if err == nil || err.Error() != c.want || !utf8.ValidString(err.Error()) {
			t.Errorf("stderr %q:\ngot  %v\nwant %s", c.stderr, err, c.want)
		}
	}
	setChild(t, r.Exe, entryLines(t, session.Entry{Type: "meta"}), "", 0)
	if answer, err := r.Run(context.Background(), tools.SubagentTask{Prompt: "x", Model: "m"}, func(string) {}); err != nil || answer != "(the subagent returned no answer)" {
		t.Errorf("no answer: %q %v", answer, err)
	}
}

func TestSubagentCancelWithGrandchildHoldingStdout(t *testing.T) {
	if _, err := exec.LookPath("perl"); err != nil {
		t.Skip("perl not installed")
	}
	dir := t.TempDir()
	script, marker := filepath.Join(dir, "qi"), filepath.Join(dir, "grandchild")
	body := "#!/bin/sh\nperl -MPOSIX -e 'fork and exit; POSIX::setsid(); open(my $f, \">\", \"$ARGV[0].tmp\") or die; print $f $$; close $f; rename(\"$ARGV[0].tmp\", $ARGV[0]); sleep 30' '" + marker + "' &\necho started >&2\nexec sleep 30\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	r := &SubagentRunner{Mode: func() Mode { return ModeYolo }, Label: func(b llm.Block) string { return b.Name }, Cwd: dir, Exe: script, Grace: 100 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		if pid := waitPid(marker); pid > 0 {
			t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
		} else {
			t.Error("the grandchild never started")
		}
		cancel()
	}()
	start := time.Now()
	_, err := r.Run(ctx, tools.SubagentTask{Prompt: "x", Model: "m"}, func(string) {})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err %v", err)
	}
	if d := time.Since(start); d > r.Grace+3*time.Second {
		t.Errorf("cancel took %v", d)
	}
}

func TestSubagentTimeoutKeepsFinalAnswer(t *testing.T) {
	t.Parallel()
	call := llm.Block{Type: llm.BlockToolUse, ID: "c1", Name: "bash", Input: json.RawMessage(`{"command":"ls"}`)}
	for _, c := range []struct {
		name string
		last llm.Message
		want string
	}{
		{"final", llm.Message{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockText, Text: "Server checked."}}}, "Server checked.\n(the subagent hit its time limit after this answer"},
		{"mid-turn", llm.Message{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockText, Text: "Looking."}, call}}, ""},
		{"working again", llm.Message{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "<background-job id=\"1\">"}}}, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			script := filepath.Join(dir, "qi")
			answered := llm.Message{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockText, Text: "Server checked."}}}
			if err := os.WriteFile(script+".out", []byte(entryLines(t, session.Entry{Type: "message", Message: &answered}, session.Entry{Type: "message", Message: &c.last})), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(script, []byte("#!/bin/sh\ncat \"$0.out\"\nexec sleep 30\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			r := &SubagentRunner{Mode: func() Mode { return ModeYolo }, Label: func(b llm.Block) string { return b.Name }, Cwd: dir, Exe: script, Grace: 100 * time.Millisecond, Timeout: time.Second}
			answer, err := r.Run(context.Background(), tools.SubagentTask{Prompt: "x", Model: "m"}, func(string) {})
			if c.name == "final" {
				ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
				defer cancel()
				if _, perr := r.Run(ctx, tools.SubagentTask{Prompt: "x", Model: "m"}, func(string) {}); !errors.Is(perr, context.DeadlineExceeded) {
					t.Errorf("a cancelled parent got an answer: %v", perr)
				}
			}
			if c.want == "" {
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("answer %q err %v", answer, err)
				}
				return
			}
			if err != nil || !strings.HasPrefix(answer, c.want) {
				t.Fatalf("answer %q err %v", answer, err)
			}
		})
	}
}

func waitPid(path string) int {
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if data, err := os.ReadFile(path); err == nil {
			if pid, err := strconv.Atoi(string(data)); err == nil {
				return pid
			}
		}
	}
	return 0
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
