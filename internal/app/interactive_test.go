package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/paul-nameless/qi/internal/agent"
	"github.com/paul-nameless/qi/internal/config"
	"github.com/paul-nameless/qi/internal/llm"
	"github.com/paul-nameless/qi/internal/ui"
)

type scriptProvider struct {
	mu       sync.Mutex
	requests []llm.Request
	reply    func(ctx context.Context, n int, req llm.Request) (llm.Message, error)
}

func (p *scriptProvider) Stream(ctx context.Context, req llm.Request, onDelta func(llm.Delta)) (llm.Message, llm.Usage, error) {
	p.mu.Lock()
	n := len(p.requests)
	p.requests = append(p.requests, req)
	p.mu.Unlock()
	msg, err := p.reply(ctx, n, req)
	if err == nil {
		onDelta(llm.Delta{Kind: llm.DeltaText, Text: msg.Text()})
	}
	return msg, llm.Usage{Input: 100, Output: 10}, err
}

func (p *scriptProvider) request(n int) llm.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.requests[n]
}

func textReply(s string) llm.Message {
	return llm.Message{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockText, Text: s}}}
}

func bashCall(id, command string) llm.Message {
	in, _ := json.Marshal(map[string]string{"command": command})
	return llm.Message{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockToolUse, ID: id, Name: "bash", Input: in}}}
}

type capture struct {
	t    *testing.T
	path string
}

func capturePrinter(t *testing.T) (*ui.Printer, capture) {
	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return ui.NewPrinter(f), capture{t, f.Name()}
}

func (c capture) String() string {
	data, err := os.ReadFile(c.path)
	if err != nil {
		c.t.Fatal(err)
	}
	return string(data)
}

func testRepl(t *testing.T, cfg config.Config, p llm.Provider) (*repl, capture) {
	t.Setenv("QI_SESSION_DIR", t.TempDir())
	display, out := capturePrinter(t)
	if cfg.CompactAt == (config.Threshold{}) {
		cfg.CompactAt = config.Threshold{Tokens: 1 << 30}
	}
	a := newTestAgentWith(t, cfg, t.TempDir(), p, display)
	r := &repl{agent: a, display: display, hooks: &agent.Hooks{}}
	if err := r.newSession(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.closeResources() })
	r.editor = ui.NewEditor(&ui.Terminal{Keys: make(chan ui.Key, 64)}, display, filepath.Join(t.TempDir(), "history"))
	return r, out
}

func writeSkill(t *testing.T, dir, name, body string) agent.Skill {
	path := filepath.Join(dir, name, "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("---\nname: "+name+"\ndescription: d\n---\n"+body), 0o600); err != nil {
		t.Fatal(err)
	}
	return agent.Skill{Name: name, Path: path}
}

func TestHandleInput(t *testing.T) {
	r, out := testRepl(t, config.Config{Model: "m"}, nopProvider{})
	dir := t.TempDir()
	r.skills = []agent.Skill{writeSkill(t, dir, "review", "Review $ARGUMENTS carefully")}
	cmdPath := filepath.Join(dir, "deploy.md")
	if err := os.WriteFile(cmdPath, []byte("deploy $1 to $2"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.commands = []agent.Command{{Name: "deploy", Path: cmdPath}}
	cases := []struct {
		line, text string
		quit       bool
		printed    string
	}{
		{"hello", "hello", false, ""},
		{"//etc/hosts is odd", "/etc/hosts is odd", false, ""},
		{"/usr/bin/env", "/usr/bin/env", false, ""},
		{"/exit", "", true, ""},
		{"/quit", "", true, ""},
		{"/help", "", false, "/compact [focus]"},
		{"/keys", "", false, "shift+tab"},
		{"/nope", "", false, "unknown command /nope (try /help)"},
		{"/effort low", "", false, "effort: low"},
		{"/effort bogus", "", false, `unknown effort "bogus"`},
		{"/skills", "", false, "/deploy (command)"},
		{"/copy", "", false, "no answer to copy"},
		{"/copy x", "", false, "usage: /copy or /copy code"},
		{"/compact", "", false, "nothing to compact"},
		{"/mcp", "", false, "no MCP servers configured"},
		{"/context", "", false, "compacts at"},
		{"/model", "", false, "model: m"},
		{"/deploy web prod", "deploy web to prod", false, ""},
	}
	for _, c := range cases {
		before := len(out.String())
		text, quit := r.handleInput(c.line)
		printed := out.String()[before:]
		if text != c.text || quit != c.quit || !strings.Contains(printed, c.printed) {
			t.Errorf("%q: text %q quit %v printed %q", c.line, text, quit, printed)
		}
	}
	text, _ := r.handleInput("/review the diff")
	if !strings.HasPrefix(text, `<skill name="review">`) || !strings.Contains(text, "Review the diff carefully") || !strings.Contains(text, "Base directory for this skill: "+filepath.Join(dir, "review")) {
		t.Errorf("skill expansion: %q", text)
	}
	if got := r.agent.Model(); got != "m" {
		t.Errorf("model changed: %q", got)
	}
}

func TestHandleInputClearStartsNewSession(t *testing.T) {
	r, out := testRepl(t, config.Config{Model: "m"}, nopProvider{})
	old := r.agent.Session().ID
	r.handleInput("/clear")
	if id := r.agent.Session().ID; id == old || r.hooks.SessionID != id || !strings.Contains(out.String(), "new session "+id) {
		t.Errorf("clear: old %s new %s hooks %s", old, id, r.hooks.SessionID)
	}
}

func TestTurnRunsToolsAndPrintsStats(t *testing.T) {
	p := &scriptProvider{reply: func(_ context.Context, n int, _ llm.Request) (llm.Message, error) {
		if n == 0 {
			return bashCall("c1", "echo hi"), nil
		}
		return textReply("all done"), nil
	}}
	r, out := testRepl(t, config.Config{Model: "m"}, p)
	draft := r.turn(func(context.Context) ([]llm.Block, error) {
		return []llm.Block{{Type: llm.BlockText, Text: "go"}}, nil
	}, "kept draft")
	if draft != "kept draft" || r.lastCancelled {
		t.Errorf("draft %q cancelled %v", draft, r.lastCancelled)
	}
	got := out.String()
	if !strings.Contains(got, "✓ bash echo hi") || !strings.Contains(got, "2 req · in 200") {
		t.Errorf("output:\n%s", got)
	}
	results := p.request(1).Messages
	if last := results[len(results)-1]; last.Blocks[0].Type != llm.BlockToolResult || !strings.Contains(last.Blocks[0].Text, "hi") {
		t.Errorf("tool result not sent: %+v", last)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestTurnQueuesTypedMessages(t *testing.T) {
	var r *repl
	var out capture
	p := &scriptProvider{reply: func(_ context.Context, n int, _ llm.Request) (llm.Message, error) {
		if n == 0 {
			for _, c := range "also this" {
				r.editor.T.Keys <- ui.Key{Kind: ui.KeyRune, Rune: c}
			}
			r.editor.T.Keys <- ui.Key{Kind: ui.KeyEnter}
			waitFor(t, func() bool { return strings.Contains(out.String(), "queued: also this") })
			return bashCall("c1", "true"), nil
		}
		return textReply("ok"), nil
	}}
	r, out = testRepl(t, config.Config{Model: "m"}, p)
	r.turn(func(context.Context) ([]llm.Block, error) {
		return []llm.Block{{Type: llm.BlockText, Text: "go"}}, nil
	}, "")
	msgs := p.request(1).Messages
	last := msgs[len(msgs)-1]
	var texts []string
	for _, b := range last.Blocks {
		if b.Type == llm.BlockText {
			texts = append(texts, b.Text)
		}
	}
	if strings.Join(texts, "|") != "The user sent this while you were working:|also this" {
		t.Errorf("queued message not sent with the tool results: %q", texts)
	}
	if !strings.Contains(out.String(), "queued: also this") {
		t.Errorf("output:\n%s", out.String())
	}
}

func TestTurnCancel(t *testing.T) {
	started := make(chan struct{})
	p := &scriptProvider{reply: func(ctx context.Context, n int, _ llm.Request) (llm.Message, error) {
		close(started)
		<-ctx.Done()
		return llm.Message{}, ctx.Err()
	}}
	r, out := testRepl(t, config.Config{Model: "m"}, p)
	if r.cancelTurn() {
		t.Error("cancelTurn reported a turn before one started")
	}
	go func() {
		<-started
		if !r.cancelTurn() {
			t.Error("cancelTurn found no running turn")
		}
	}()
	r.turn(func(context.Context) ([]llm.Block, error) {
		return []llm.Block{{Type: llm.BlockText, Text: "go"}}, nil
	}, "")
	if !r.lastCancelled || !strings.Contains(out.String(), "interrupted") {
		t.Errorf("cancelled %v output:\n%s", r.lastCancelled, out.String())
	}
	if n := len(r.agent.Messages()); n != 0 {
		t.Errorf("cancelled first request left %d messages", n)
	}
	if r.cancelTurn() {
		t.Error("cancelTurn still finds the finished turn")
	}
}

func TestTurnReportsPrepareAndProviderErrors(t *testing.T) {
	p := &scriptProvider{reply: func(context.Context, int, llm.Request) (llm.Message, error) {
		return llm.Message{}, &llm.APIError{Status: 400, Body: "bad request"}
	}}
	r, out := testRepl(t, config.Config{Model: "m"}, p)
	r.turn(func(context.Context) ([]llm.Block, error) { return nil, errors.New("prompt blocked by hook: no") }, "")
	r.turn(func(context.Context) ([]llm.Block, error) {
		return []llm.Block{{Type: llm.BlockText, Text: "go"}}, nil
	}, "")
	got := out.String()
	if !strings.Contains(got, "prompt blocked by hook: no") || !strings.Contains(got, "api error 400: bad request") || r.lastCancelled {
		t.Errorf("output:\n%s", got)
	}
}

func TestCancellable(t *testing.T) {
	r, _ := testRepl(t, config.Config{Model: "m"}, nopProvider{})
	err := r.cancellable(func(ctx context.Context) error {
		if !r.cancelTurn() {
			t.Error("no cancel func while running")
		}
		<-ctx.Done()
		return ctx.Err()
	})
	if !errors.Is(err, context.Canceled) || r.cancelTurn() {
		t.Errorf("err %v", err)
	}
}

func TestIdleTimer(t *testing.T) {
	r, _ := testRepl(t, config.Config{Model: "m", CacheTTL: "5m"}, nopProvider{})
	r.armIdle()
	if r.idle != nil {
		t.Error("idle compaction armed for a 5m cache")
	}
	called := make(chan struct{}, 1)
	p := &scriptProvider{reply: func(context.Context, int, llm.Request) (llm.Message, error) {
		called <- struct{}{}
		return textReply("summary"), nil
	}}
	r, _ = testRepl(t, config.Config{Model: "m", CacheTTL: "1h", CompactAt: config.Threshold{Tokens: 1000}, CompactIdleAt: config.Threshold{Tokens: 10}}, p)
	setMessages(r.agent, []llm.Message{llm.UserText("hello"), textReply("hi")})
	r.agent.SetContextTokens(500)
	r.armIdle()
	if r.idle == nil {
		t.Fatal("idle compaction not armed for a 1h cache")
	}
	gen := r.idleGen
	r.stopIdle()
	r.idleCompact(gen)
	select {
	case <-called:
		t.Error("a stale idle timer compacted")
	default:
	}
	r.idleCompact(r.idleGen)
	select {
	case <-called:
	default:
		t.Error("idle compaction did not run")
	}
	if msgs := r.agent.Messages(); !strings.HasPrefix(msgs[0].Text(), "This session was compacted") {
		t.Errorf("messages after idle compaction: %+v", msgs)
	}
}

func headlessServer(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, anthropicReply)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func withStdio(t *testing.T, fn func()) string {
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer devNull.Close()
	rd, wr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldIn, oldOut := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = devNull, wr
	done := make(chan string)
	go func() {
		data, _ := io.ReadAll(rd)
		done <- string(data)
	}()
	defer func() { os.Stdin, os.Stdout = oldIn, oldOut }()
	fn()
	wr.Close()
	return <-done
}

func headlessConfig(t *testing.T, url string) string {
	home := t.TempDir()
	t.Chdir(t.TempDir())
	t.Setenv("HOME", home)
	t.Setenv("QI_SESSION_DIR", filepath.Join(home, "sessions"))
	path := filepath.Join(home, "config.json")
	data := fmt.Sprintf(`{"api": "anthropic", "base_url": %q, "api_key": "k", "model": "claude-haiku-5-5", "builtin_skills": false, "instruction_files": ["none.md"], "skill_dirs": ["none"], "hook_files": ["none.json"]}`, url)
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestHeadlessPrintsAnswer(t *testing.T) {
	srv := headlessServer(t)
	path := headlessConfig(t, srv.URL)
	var err error
	stdout := withStdio(t, func() { err = run(options{query: "say ok", configPath: path}) })
	if err != nil || stdout != "ok\n" {
		t.Errorf("stdout %q err %v", stdout, err)
	}
	if _, err := headlessPrompt("", false); err == nil {
		t.Error("empty prompt accepted")
	}
}

func TestQueuedPromptHookDoesNotBlockKeys(t *testing.T) {
	cancelled := make(chan time.Duration, 1)
	var r *repl
	p := &scriptProvider{reply: func(ctx context.Context, n int, _ llm.Request) (llm.Message, error) {
		if n > 0 {
			return textReply("ok"), nil
		}
		start := time.Now()
		for _, k := range []ui.Key{{Kind: ui.KeyRune, Rune: 'x'}, {Kind: ui.KeyEnter}, {Kind: ui.KeyEsc}} {
			r.editor.T.Keys <- k
		}
		select {
		case <-ctx.Done():
			cancelled <- time.Since(start)
		case <-time.After(3 * time.Second):
			cancelled <- time.Hour
		}
		return llm.Message{}, ctx.Err()
	}}
	r, _ = testRepl(t, config.Config{Model: "m"}, p)
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(`{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"sleep 1"}]}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	hooks, err := agent.LoadHooks(dir, []string{path})
	if err != nil {
		t.Fatal(err)
	}
	r.hooks = hooks
	r.turn(func(context.Context) ([]llm.Block, error) {
		return []llm.Block{{Type: llm.BlockText, Text: "go"}}, nil
	}, "")
	if d := <-cancelled; d > 700*time.Millisecond {
		t.Errorf("esc waited %v for the UserPromptSubmit hook of a queued message", d)
	}
	if !r.lastCancelled {
		t.Error("turn not cancelled")
	}
}

func TestHeadlessJSONStreamsSessionEntries(t *testing.T) {
	srv := headlessServer(t)
	path := headlessConfig(t, srv.URL)
	var err error
	stdout := withStdio(t, func() { err = run(options{query: "say ok", configPath: path, json: true}) })
	if err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(os.Getenv("QI_SESSION_DIR"), "*", "*.jsonl"))
	if len(files) != 1 {
		t.Fatalf("session files %v", files)
	}
	data, _ := os.ReadFile(files[0])
	if stdout != string(data) {
		t.Errorf("stdout differs from the session file:\nstdout %s\nfile   %s", stdout, data)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], `{"type":"meta"`) || !strings.Contains(lines[2], `"role":"assistant"`) || strings.Contains(stdout, "\nok\n") {
		t.Errorf("stream:\n%s", stdout)
	}
}

func TestBannerLine(t *testing.T) {
	r, _ := testRepl(t, config.Config{Model: "claude-opus-5-5", ContextWindow: 200000}, nopProvider{})
	r.opts.version = "dev-x"
	id := r.agent.Session().ID
	got := r.bannerLine(id)
	if !strings.HasPrefix(got, "qi dev-x · claude-opus-5-5 · yolo · ") || !strings.HasSuffix(got, "/200k ("+strings.Split(strings.Split(got, "(")[1], ")")[0]+") · "+id) || strings.Contains(got, "context") || strings.Contains(got, "~") || strings.Contains(got, "session") {
		t.Errorf("banner %q", got)
	}
	r2, _ := testRepl(t, config.Config{Model: "unknown-model"}, nopProvider{})
	if got := r2.bannerLine("ID"); strings.Contains(got, "/") || !strings.HasSuffix(got, " · ID") {
		t.Errorf("banner with an unknown window %q", got)
	}
}

func TestHeadlessJSONResumeStreamsMetaAndNewTail(t *testing.T) {
	srv := headlessServer(t)
	path := headlessConfig(t, srv.URL)
	if err := run(options{query: "first", configPath: path, sessionID: "S1"}); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(os.Getenv("QI_SESSION_DIR"), "*", "S1.jsonl"))
	if len(files) != 1 {
		t.Fatalf("session files %v", files)
	}
	for _, o := range []options{{sessionID: "S1"}, {cont: true}, {pickSession: true, resumeID: "S1"}} {
		before, _ := os.ReadFile(files[0])
		o.query, o.configPath, o.json = "again", path, true
		var err error
		stdout := withStdio(t, func() { err = run(o) })
		after, _ := os.ReadFile(files[0])
		meta, _, _ := strings.Cut(string(before), "\n")
		if err != nil || stdout != meta+"\n"+string(after[len(before):]) || strings.Count(stdout, "\n") != 3 {
			t.Errorf("%+v: err %v\nstdout %s", o, err, stdout)
		}
	}
}

func TestJSONNeedsHeadless(t *testing.T) {
	if err := checkJSON(options{json: true}, false); err == nil || !strings.Contains(err.Error(), "-json works only with -q") {
		t.Errorf("interactive -json: %v", err)
	}
	if checkJSON(options{json: true, query: "x"}, true) != nil || checkJSON(options{}, false) != nil {
		t.Error("valid combinations rejected")
	}
}
