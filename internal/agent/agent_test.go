package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/paul-nameless/qi/internal/config"
	"github.com/paul-nameless/qi/internal/llm"
	"github.com/paul-nameless/qi/internal/tools"
)

type fakeProvider struct {
	mu       sync.Mutex
	replies  []llm.Message
	usage    llm.Usage
	requests [][]llm.Message
	onCall   func(n int)
}

func (f *fakeProvider) Stream(ctx context.Context, req llm.Request, onDelta func(llm.Delta)) (llm.Message, llm.Usage, error) {
	f.mu.Lock()
	n := len(f.requests)
	f.requests = append(f.requests, append([]llm.Message{}, req.Messages...))
	f.mu.Unlock()
	if f.onCall != nil {
		f.onCall(n)
	}
	if err := ctx.Err(); err != nil {
		return llm.Message{}, llm.Usage{}, err
	}
	if n >= len(f.replies) {
		return llm.Message{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockText, Text: "done"}}}, f.usage, nil
	}
	return f.replies[n], f.usage, nil
}

type nopDisplay struct{}

func (nopDisplay) ToolStart(string, string)      {}
func (nopDisplay) ToolProgress(string, string)   {}
func (nopDisplay) ToolDone(string, bool, string) {}
func (nopDisplay) Text(string)                   {}
func (nopDisplay) Reasoning(string)              {}
func (nopDisplay) EndBlock()                     {}
func (nopDisplay) Info(string)                   {}
func (nopDisplay) Error(string)                  {}

func runCall(id, code string) llm.Message {
	in, _ := json.Marshal(map[string]string{"code": code})
	return llm.Message{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockToolUse, ID: id, Name: "run", Input: in}}}
}

func toolCall(id, cmd string) llm.Message {
	q, _ := json.Marshal(cmd)
	return runCall(id, "return await bash("+string(q)+")")
}

func newAgent(t *testing.T, p *fakeProvider) *Agent {
	return New(Options{
		Config:   config.Config{CompactAt: config.Threshold{Tokens: 1 << 30}, Model: "m"},
		Cwd:      t.TempDir(),
		Provider: p,
		UI:       nopDisplay{},
		Tool:     tools.Run(tools.Bash(), tools.JobsFunc()),
		SpillDir: t.TempDir(),
	})
}

func lastText(m llm.Message) string {
	var parts []string
	for _, b := range m.Blocks {
		if b.Type == llm.BlockText {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "|")
}

func TestTurnRunsToolsAndQueuesSteering(t *testing.T) {
	t.Parallel()
	p := &fakeProvider{replies: []llm.Message{toolCall("c1", "echo hi")}}
	a := newAgent(t, p)
	p.onCall = func(n int) {
		if n == 0 {
			a.Queue([]llm.Block{{Type: llm.BlockText, Text: "also check x"}})
		}
	}
	final, stats, err := a.Turn(context.Background(), []llm.Block{{Type: llm.BlockText, Text: "go"}})
	if err != nil || final != "done" || stats.Requests != 2 {
		t.Fatalf("final %q stats %+v err %v", final, stats, err)
	}
	second := p.requests[1]
	last := second[len(second)-1]
	if last.Blocks[0].Type != llm.BlockToolResult || !strings.Contains(last.Blocks[0].Text, "hi") {
		t.Errorf("tool result missing: %+v", last)
	}
	if !strings.Contains(lastText(last), "also check x") {
		t.Errorf("steering not delivered: %+v", last)
	}
}

func TestModeNoteSentOnChangeOnly(t *testing.T) {
	t.Parallel()
	p := &fakeProvider{}
	a := newAgent(t, p)
	a.SetReadOnly(true)
	_, _, _ = a.Turn(context.Background(), []llm.Block{{Type: llm.BlockText, Text: "one"}})
	_, _, _ = a.Turn(context.Background(), []llm.Block{{Type: llm.BlockText, Text: "two"}})
	a.SetReadOnly(false)
	_, _, _ = a.Turn(context.Background(), []llm.Block{{Type: llm.BlockText, Text: "three"}})
	a.SetMode(ModeProject)
	_, _, _ = a.Turn(context.Background(), []llm.Block{{Type: llm.BlockText, Text: "four"}})
	users := []string{}
	for _, m := range a.conv.messages {
		if m.Role == llm.RoleUser {
			users = append(users, lastText(m))
		}
	}
	if !strings.Contains(users[0], "Read-only mode is on") || strings.Contains(users[1], "<mode>") || !strings.Contains(users[2], "Yolo mode is on") || !strings.Contains(users[3], "Project mode is on") {
		t.Errorf("mode notes: %q", users)
	}
	if m, ok := lastModeNote([]llm.Message{llm.UserText(legacyYoloNote)}); !ok || m != ModeYolo {
		t.Errorf("sessions from before project mode end in yolo: %v %v", m, ok)
	}
}

func TestCancelDuringToolsKeepsLogValid(t *testing.T) {
	t.Parallel()
	p := &fakeProvider{replies: []llm.Message{toolCall("c1", "sleep 5")}}
	a := newAgent(t, p)
	ctx, cancel := context.WithCancel(context.Background())
	p.onCall = func(n int) {
		if n == 0 {
			go func() {
				time.Sleep(300 * time.Millisecond)
				cancel()
			}()
		}
	}
	_, _, err := a.Turn(ctx, []llm.Block{{Type: llm.BlockText, Text: "go"}})
	if err != context.Canceled {
		t.Fatalf("err %v", err)
	}
	last := a.conv.messages[len(a.conv.messages)-1]
	if last.Role != llm.RoleUser || len(last.Blocks) != 1 || last.Blocks[0].ToolUseID != "c1" || !last.Blocks[0].IsError {
		t.Fatalf("expected interrupted result, got %+v", last)
	}
}

func TestCompactBeforeNewPrompt(t *testing.T) {
	t.Parallel()
	p := &fakeProvider{replies: []llm.Message{{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockText, Text: "SUMMARY"}}}}}
	a := newAgent(t, p)
	a.live.cfg.CompactAt = config.Threshold{Tokens: 100}
	a.conv.messages = []llm.Message{llm.UserText("old"), {Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockText, Text: "old answer"}}}}
	a.SetContextTokens(500)
	if _, _, err := a.Turn(context.Background(), []llm.Block{{Type: llm.BlockText, Text: "new request"}}); err != nil {
		t.Fatal(err)
	}
	if len(p.requests[0]) != 3 || !strings.Contains(lastText(p.requests[0][2]), "Write a summary") {
		t.Fatalf("first request should be the compaction: %+v", p.requests[0])
	}
	sent := p.requests[1]
	if len(sent) != 3 || !strings.Contains(lastText(sent[0]), "SUMMARY") || lastText(sent[1]) != "old answer" || lastText(sent[2]) != "new request" {
		t.Fatalf("after compaction: %+v", sent)
	}
}

func TestCompactRejectsEmptySummary(t *testing.T) {
	t.Parallel()
	p := &fakeProvider{replies: []llm.Message{{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockToolUse, ID: "x", Name: "bash", Input: json.RawMessage(`{}`)}}}}}
	a := newAgent(t, p)
	a.conv.messages = []llm.Message{llm.UserText("old")}
	if err := a.Compact(context.Background(), ""); err == nil {
		t.Fatal("expected error")
	}
	if len(a.conv.messages) != 1 {
		t.Error("history replaced despite failed compaction")
	}
}

func TestStopHookContinuesOnceWithActiveFlag(t *testing.T) {
	t.Parallel()
	p := &fakeProvider{}
	a := newAgent(t, p)
	var actives []bool
	a.conv.onStop = func(ctx context.Context, last string, active bool) (bool, string) {
		actives = append(actives, active)
		if !active {
			a.Queue([]llm.Block{{Type: llm.BlockText, Text: "queued note"}})
			return true, "run the tests"
		}
		return false, ""
	}
	_, stats, err := a.Turn(context.Background(), []llm.Block{{Type: llm.BlockText, Text: "go"}})
	if err != nil || stats.Requests != 2 || len(actives) != 2 || actives[0] || !actives[1] {
		t.Fatalf("stats %+v actives %v err %v", stats, actives, err)
	}
	last := p.requests[1][len(p.requests[1])-1]
	if txt := lastText(last); !strings.Contains(txt, "run the tests") || !strings.Contains(txt, "queued note") {
		t.Errorf("continuation message %q", txt)
	}
}

func TestCompactRejectsToolCallsAndTriesOncePerTurn(t *testing.T) {
	t.Parallel()
	summaryWithTool := llm.Message{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockText, Text: "Let me check first."}, {Type: llm.BlockToolUse, ID: "x", Name: "bash", Input: json.RawMessage(`{"command":"ls"}`)}}}
	p := &fakeProvider{replies: []llm.Message{summaryWithTool, toolCall("c1", "echo a"), toolCall("c2", "echo b")}}
	a := newAgent(t, p)
	a.live.cfg.CompactAt = config.Threshold{Tokens: 10}
	a.conv.messages = []llm.Message{llm.UserText("old")}
	a.SetContextTokens(500)
	if _, _, err := a.Turn(context.Background(), []llm.Block{{Type: llm.BlockText, Text: "go"}}); err != nil {
		t.Fatal(err)
	}
	if a.conv.messages[0].Text() != "old" {
		t.Errorf("history replaced by a tool-calling reply: %q", a.conv.messages[0].Text())
	}
	compactions := 0
	for _, req := range p.requests {
		if strings.Contains(lastText(req[len(req)-1]), "Write a summary of the conversation") {
			compactions++
		}
	}
	if compactions > 2 {
		t.Errorf("compaction attempted %d times", compactions)
	}
}

func TestStopHookEmptyReason(t *testing.T) {
	t.Parallel()
	p := &fakeProvider{}
	a := newAgent(t, p)
	n := 0
	a.conv.onStop = func(context.Context, string, bool) (bool, string) {
		n++
		return n == 1, ""
	}
	if _, _, err := a.Turn(context.Background(), []llm.Block{{Type: llm.BlockText, Text: "go"}}); err != nil {
		t.Fatal(err)
	}
	last := p.requests[1][len(p.requests[1])-1]
	if last.Role != llm.RoleUser || lastText(last) == "" {
		t.Errorf("continuation message empty: %+v", last)
	}
}

func TestRestoreMode(t *testing.T) {
	t.Parallel()
	a := newAgent(t, &fakeProvider{})
	a.conv.messages = []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "x"}, {Type: llm.BlockText, Text: readOnlyNote}}}}
	a.restoreMode()
	if mb, ok := a.modeBlock(); !ok || mb.Text != yoloNote {
		t.Errorf("resumed yolo session after read-only should announce yolo: %v %v", mb, ok)
	}
}

type rejectProvider struct{ calls int }

func (r *rejectProvider) Stream(ctx context.Context, req llm.Request, onDelta func(llm.Delta)) (llm.Message, llm.Usage, error) {
	r.calls++
	return llm.Message{}, llm.Usage{}, &llm.APIError{Status: 400, Body: "image not supported"}
}

func TestRejectedPromptIsRolledBack(t *testing.T) {
	t.Parallel()
	a := newAgent(t, &fakeProvider{})
	a.live.provider = &rejectProvider{}
	a.conv.messages = []llm.Message{llm.UserText("earlier")}
	if _, _, err := a.Turn(context.Background(), []llm.Block{{Type: llm.BlockText, Text: "bad"}}); err == nil {
		t.Fatal("expected error")
	}
	if len(a.conv.messages) != 1 || a.conv.messages[0].Text() != "earlier" {
		t.Errorf("rejected prompt kept in history: %+v", a.conv.messages)
	}
}

func TestRejectedPromptKeepsTurnNotes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := &fakeProvider{replies: []llm.Message{runCall("1", `return await read("a.txt")`)}}
	a := New(Options{Config: config.Config{CompactAt: config.Threshold{Tokens: 1 << 30}, Model: "m"}, Cwd: dir, Provider: p, UI: nopDisplay{}, Tool: tools.Run(tools.Read()), SpillDir: t.TempDir()})
	if _, _, err := a.Turn(context.Background(), []llm.Block{{Type: llm.BlockText, Text: "read it"}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	a.SetReadOnly(true)
	a.live.provider = &rejectProvider{}
	if _, _, err := a.Turn(context.Background(), []llm.Block{{Type: llm.BlockText, Text: "bad"}}); err == nil {
		t.Fatal("expected error")
	}
	a.live.provider = p
	if _, _, err := a.Turn(context.Background(), []llm.Block{{Type: llm.BlockText, Text: "again"}}); err != nil {
		t.Fatal(err)
	}
	last := p.requests[len(p.requests)-1]
	got := lastText(last[len(last)-1])
	if strings.Contains(got, "bad") || !strings.Contains(got, "+two") || !strings.Contains(got, readOnlyNote) {
		t.Errorf("next turn after a rejected prompt lost its notes:\n%s", got)
	}
}

func TestCancelledPromptIsKept(t *testing.T) {
	t.Parallel()
	p := &fakeProvider{}
	a := newAgent(t, p)
	ctx, cancel := context.WithCancel(context.Background())
	p.onCall = func(int) { cancel() }
	if _, _, err := a.Turn(ctx, []llm.Block{{Type: llm.BlockText, Text: "first"}}); err != context.Canceled {
		t.Fatalf("got %v", err)
	}
	p.onCall = nil
	if _, _, err := a.Turn(context.Background(), []llm.Block{{Type: llm.BlockText, Text: "second"}}); err != nil {
		t.Fatal(err)
	}
	last := llm.MergeConsecutive(p.requests[len(p.requests)-1])
	if len(last) != 1 || lastText(last[0]) != "first|"+llm.Interrupted+"|second" {
		t.Errorf("request after a cancelled prompt: %+v", last)
	}
}

func TestRejectionAfterInterruptDropsTheInterruptedPrompt(t *testing.T) {
	t.Parallel()
	p := &fakeProvider{}
	a := newAgent(t, p)
	a.conv.messages = []llm.Message{llm.UserText("earlier"), {Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockText, Text: "ok"}}}}
	ctx, cancel := context.WithCancel(context.Background())
	p.onCall = func(int) { cancel() }
	if _, _, err := a.Turn(ctx, []llm.Block{{Type: llm.BlockText, Text: "huge image"}}); err != context.Canceled {
		t.Fatalf("got %v", err)
	}
	a.live.provider = &rejectProvider{}
	if _, _, err := a.Turn(context.Background(), []llm.Block{{Type: llm.BlockText, Text: "next"}}); err == nil {
		t.Fatal("expected error")
	}
	if len(a.conv.messages) != 2 || a.conv.messages[1].Text() != "ok" {
		t.Errorf("history after a rejection that followed an interrupt: %+v", a.conv.messages)
	}
}

func TestThrottledPromptIsKept(t *testing.T) {
	old := llm.Sleep
	llm.Sleep = func(ctx context.Context, d time.Duration) error { return ctx.Err() }
	defer func() { llm.Sleep = old }()
	for _, e := range []*llm.APIError{{Status: 429}, {Status: 402, RetryAfter: time.Second}} {
		a := newAgent(t, &fakeProvider{})
		a.live.provider = &errProvider{err: e}
		a.conv.messages = []llm.Message{llm.UserText("earlier")}
		if _, _, err := a.Turn(context.Background(), []llm.Block{{Type: llm.BlockText, Text: "again"}}); err == nil {
			t.Fatal("expected error")
		}
		if len(a.conv.messages) != 2 {
			t.Errorf("status %d: throttled prompt dropped: %+v", e.Status, a.conv.messages)
		}
	}
}

type errProvider struct{ err error }

func (p *errProvider) Stream(context.Context, llm.Request, func(llm.Delta)) (llm.Message, llm.Usage, error) {
	return llm.Message{}, llm.Usage{}, p.err
}

func TestMaxRounds(t *testing.T) {
	t.Parallel()
	p := &fakeProvider{replies: []llm.Message{toolCall("1", "true"), toolCall("2", "true"), toolCall("3", "true")}}
	a := newAgent(t, p)
	a.live.cfg.MaxRounds = 2
	if _, _, err := a.Turn(context.Background(), []llm.Block{{Type: llm.BlockText, Text: "go"}}); err == nil || !strings.Contains(err.Error(), "max_rounds") {
		t.Errorf("expected max_rounds error, got %v", err)
	}
}

func TestBlockCharsCountsImagesBySize(t *testing.T) {
	t.Parallel()
	big := strings.Repeat("A", 2_000_000)
	blocks := []llm.Block{
		{Type: llm.BlockToolResult, Text: "ok", Images: []llm.Block{{Type: llm.BlockImage, Data: big}}},
		{Type: llm.BlockImage, Data: big},
	}
	if got := blockChars(blocks) / 4; got > 5000 {
		t.Errorf("two images estimated at %d tokens", got)
	}
}

func TestSystemPromptStaticPartIsDirectoryFree(t *testing.T) {
	t.Parallel()
	home, proj := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(home, "CLAUDE.md"), []byte("global rules"), 0o600)
	os.WriteFile(filepath.Join(proj, "CLAUDE.md"), []byte("project rules"), 0o600)
	skills := []Skill{{Name: "g", Description: "global", Path: filepath.Join(home, "skills/g/SKILL.md")}, {Name: "p", Description: "proj", Path: filepath.Join(proj, ".claude/skills/p/SKILL.md")}}
	built := BuildSystemPrompt(proj, "", "", []string{filepath.Join(home, "CLAUDE.md"), filepath.Join(proj, "CLAUDE.md")}, skills)
	static, rest := built.Text[:built.Static], built.Text[built.Static:]
	total := 0
	var names []string
	for _, sec := range built.Sections {
		total += sec.Chars
		names = append(names, sec.Name)
	}
	if total != len(built.Text) || len(names) != 6 || names[0] != "base prompt" || names[2] != "skills (1 global)" || names[4] != "skills (1 in project)" || names[5] != "environment" {
		t.Errorf("sections %v cover %d of %d chars", names, total, len(built.Text))
	}
	if strings.Contains(static, proj) || !strings.Contains(static, "global rules") || !strings.Contains(static, "- [g](") || !strings.Contains(static, ") - global") {
		t.Errorf("static part: %q", static)
	}
	if !strings.Contains(rest, proj) || !strings.Contains(rest, "project rules") || !strings.Contains(rest, "- [p](") || !strings.Contains(rest, ") - proj") {
		t.Errorf("directory part: %q", rest)
	}
	if env := built.Text[built.Session:]; !strings.HasPrefix(env, "\n\n<env>") || strings.Contains(env, "project rules") {
		t.Errorf("session tail: %q", env)
	}
}

func TestFmtTokens(t *testing.T) {
	t.Parallel()
	cases := map[int]string{42: "42", 5234: "5.2k", 200000: "200k", 1000000: "1M", 1050000: "1.05M"}
	for n, want := range cases {
		if got := FmtTokens(n); got != want {
			t.Errorf("FmtTokens(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestContextUsageBeforeFirstRequest(t *testing.T) {
	t.Parallel()
	a := New(Options{Config: config.Config{Model: "claude-opus-5-5", ContextWindow: 200000}})
	a.SetSystem(SystemPrompt{Text: strings.Repeat("x", 40000)})
	if got, want := a.ContextUsage(), "10.0k/200k (5.0%)"; got != want {
		t.Errorf("ContextUsage() = %q, want %q", got, want)
	}
}

func TestContextReport(t *testing.T) {
	t.Parallel()
	a := New(Options{Tool: tools.Run(tools.Read()), Config: config.Config{Model: "claude-opus-5-5", ContextWindow: 200000}})
	a.SetSystem(SystemPrompt{Text: strings.Repeat("s", 4000), Sections: []Section{{"base prompt", 1000}, {"environment", 3000}}})
	a.conv.messages = []llm.Message{
		llm.UserText(strings.Repeat("u", 400)),
		{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockToolUse, ID: "1", Name: "read", Input: json.RawMessage(`{"file_path":"a"}`)}}},
		{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockToolResult, ToolUseID: "1", Text: strings.Repeat("r", 2000)}}},
	}
	got := a.ContextReport()
	for _, want := range []string{"context 2.5k/200k (1.2%)", "system prompt", "base prompt", "environment", "tools (1)", "messages (3)", "user text", "read results", "500", "free"} {
		if !strings.Contains(got, want) {
			t.Errorf("report misses %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "assistant text") {
		t.Errorf("empty rows should be left out:\n%s", got)
	}
}

func toolResultOf(t *testing.T, a *Agent, p *fakeProvider, cmd string) string {
	p.mu.Lock()
	for len(p.replies) < len(p.requests) {
		p.replies = append(p.replies, llm.Message{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockText, Text: "done"}}})
	}
	p.replies = append(p.replies[:len(p.requests)], toolCall("c", cmd))
	p.mu.Unlock()
	if _, _, err := a.Turn(context.Background(), []llm.Block{{Type: llm.BlockText, Text: "go"}}); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	last := p.requests[len(p.requests)-1]
	return last[len(last)-1].Blocks[0].Text
}

func TestReconfigureUpdatesBashAllowlistAndTimeout(t *testing.T) {
	t.Parallel()
	p := &fakeProvider{}
	a := newAgent(t, p)
	a.SetReadOnly(true)
	if got := toolResultOf(t, a, p, "uname"); !strings.Contains(got, "not in the read-only allowlist") {
		t.Fatalf("uname before reconfigure: %q", got)
	}
	cfg := a.Config()
	cfg.ReadOnlyAllow = []string{"uname"}
	a.Reconfigure(cfg, p)
	if got := toolResultOf(t, a, p, "uname"); strings.Contains(got, "read-only mode") {
		t.Errorf("uname after allowing it: %q", got)
	}
	a.SetReadOnly(false)
	cfg.BashTimeout = 1
	a.Reconfigure(cfg, p)
	if got := toolResultOf(t, a, p, "sleep 5"); !strings.Contains(got, `"timed_out": true`) {
		t.Errorf("bash timeout after reconfigure: %q", got)
	}
}

func TestAccessorsDuringTurn(t *testing.T) {
	t.Parallel()
	inTurn, release := make(chan struct{}), make(chan struct{})
	p := &fakeProvider{onCall: func(n int) {
		if n == 0 {
			close(inTurn)
			<-release
		}
	}}
	a := newAgent(t, p)
	turnDone := make(chan struct{})
	go func() {
		defer close(turnDone)
		_, _, _ = a.Turn(context.Background(), []llm.Block{{Type: llm.BlockText, Text: "go"}})
	}()
	<-inTurn
	reads := make(chan struct{})
	go func() {
		defer close(reads)
		_ = a.Config()
		_ = a.Session()
		_ = a.System()
		_ = a.ContextTokens()
		_ = a.Model()
		a.SetContextTokens(5)
		a.Queue([]llm.Block{{Type: llm.BlockText, Text: "q"}})
	}()
	select {
	case <-reads:
	case <-time.After(2 * time.Second):
		t.Fatal("mu-only accessors blocked behind the turn")
	}
	writes := make(chan string, 2)
	go func() {
		a.SetEffort("low")
		writes <- "effort"
	}()
	go func() {
		a.Reconfigure(a.Config(), p)
		writes <- "reconfigure"
	}()
	select {
	case w := <-writes:
		t.Fatalf("%s did not wait for the turn", w)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	<-turnDone
	for range 2 {
		select {
		case <-writes:
		case <-time.After(2 * time.Second):
			t.Fatal("writer still blocked after the turn")
		}
	}
}

func TestTurnStatsLine(t *testing.T) {
	s := TurnStats{Requests: 1}
	done := time.Date(2026, 11, 15, 21, 11, 5, 0, time.Local)
	if got := s.String(usageOf(8400, 1000000, true), 11400*time.Millisecond, done); got != "8.4k/1M (0.8%) · cooked for 11s · done 21:11" {
		t.Errorf("stats %q", got)
	}
	for jobs, want := range map[int]string{1: "cooked for 11s · done 21:11 · 1 bg job", 3: "cooked for 11s · done 21:11 · 3 bg jobs"} {
		s.Jobs = jobs
		if got := s.String("x", 11*time.Second, done); !strings.HasSuffix(got, want) {
			t.Errorf("%d jobs: %q", jobs, got)
		}
	}
	for d, want := range map[time.Duration]string{400 * time.Millisecond: "0s", 65 * time.Second: "1m 5s", 3*time.Hour + 4*time.Minute: "3h 4m"} {
		if got := fmtElapsed(d); got != want {
			t.Errorf("fmtElapsed(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestSystemPromptReplacesBase(t *testing.T) {
	t.Parallel()
	sys := BuildSystemPrompt(t.TempDir(), "", "custom base", nil, nil)
	if !strings.HasPrefix(sys.Text, "custom base\n\n<env>") || strings.Contains(sys.Text, "You are qi") || sys.Sections[0].Name != "base prompt (system_prompt_file)" {
		t.Fatalf("got %q %+v", sys.Text, sys.Sections)
	}
}

func TestSkillLinkTarget(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"/a/b/SKILL.md":         "/a/b/SKILL.md",
		"/My Skills/x/SKILL.md": "</My Skills/x/SKILL.md>",
		"/a/(old)/SKILL.md":     "</a/(old)/SKILL.md>",
	} {
		if got := linkTarget(in); got != want {
			t.Errorf("linkTarget(%q) = %q, want %q", in, got, want)
		}
	}
}
