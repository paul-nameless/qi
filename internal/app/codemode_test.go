package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paul-nameless/qi/internal/agent"
	"github.com/paul-nameless/qi/internal/config"
	"github.com/paul-nameless/qi/internal/llm"
	"github.com/paul-nameless/qi/internal/session"
	"github.com/paul-nameless/qi/internal/ui"
)

func runCall(id, code string) llm.Message {
	in, _ := json.Marshal(map[string]string{"code": code})
	return llm.Message{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockToolUse, ID: id, Name: "run", Input: in}}}
}

func scripted(replies ...llm.Message) *scriptProvider {
	return &scriptProvider{reply: func(_ context.Context, n int, _ llm.Request) (llm.Message, error) {
		if n < len(replies) {
			return replies[n], nil
		}
		return textReply("done"), nil
	}}
}

func toolNames(req llm.Request) []string {
	var names []string
	for _, d := range req.Tools {
		names = append(names, d.Name)
	}
	return names
}

func lastResult(t *testing.T, req llm.Request) llm.Block {
	t.Helper()
	last := req.Messages[len(req.Messages)-1]
	if len(last.Blocks) == 0 || last.Blocks[0].Type != llm.BlockToolResult {
		t.Fatalf("last message is not a tool result: %+v", last)
	}
	return last.Blocks[0]
}

func goTurn(r *repl) {
	r.turn(func(context.Context) ([]llm.Block, error) {
		return []llm.Block{{Type: llm.BlockText, Text: "go"}}, nil
	}, "")
}

func TestCodeModeRunReadsAndEdits(t *testing.T) {
	p := scripted(runCall("c1", `const s = await read("a.txt"); await edit("a.txt", "old", "new"); return s.trim()`))
	r, out := testRepl(t, config.Config{Model: "m"}, p)
	path := filepath.Join(r.agent.Cwd(), "a.txt")
	if err := os.WriteFile(path, []byte("old text\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	goTurn(r)
	if got := toolNames(p.request(0)); strings.Join(got, ",") != "run" {
		t.Errorf("tools %v, want [run]", got)
	}
	if data, _ := os.ReadFile(path); string(data) != "new text\n" {
		t.Errorf("file %q", data)
	}
	res := lastResult(t, p.request(1))
	if res.IsError || res.Text != "old text" || len(res.Calls) != 2 || res.Calls[1].Tool != "edit" {
		t.Errorf("result %+v", res)
	}
	if got := out.String(); !strings.Contains(got, "· run\n    const s = await read(\"a.txt\"); await edit") || !strings.Contains(got, "  ✓ read a.txt") || !strings.Contains(got, "✓ run · 2 calls") {
		t.Errorf("output:\n%s", got)
	}
	entries, err := session.Entries(r.agent.Session().Path)
	if err != nil {
		t.Fatal(err)
	}
	uses, results := 0, 0
	for _, e := range entries {
		if e.Message == nil {
			continue
		}
		for _, b := range e.Message.Blocks {
			switch b.Type {
			case llm.BlockToolUse:
				uses++
			case llm.BlockToolResult:
				results++
				if len(b.Calls) != 2 {
					t.Errorf("stored result calls %+v", b.Calls)
				}
			}
		}
	}
	if uses != 1 || results != 1 {
		t.Errorf("session has %d tool uses, %d results", uses, results)
	}
}

func TestCodeModeVMNoteAfterLoadNotClear(t *testing.T) {
	cell := `typeof kept`
	p := scripted(runCall("c1", cell), textReply("ok"), runCall("c2", cell), textReply("ok"), runCall("c3", cell))
	r, _ := testRepl(t, config.Config{Model: "m"}, p)
	setMessages(r.agent, []llm.Message{llm.UserText("x"), runCall("old", "globalThis.kept = 1"), {Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockToolResult, ToolUseID: "old", Text: "(no output)"}}}, textReply("ok")})
	goTurn(r)
	if res := lastResult(t, p.request(1)); res.Text != "Note: this is a new VM; variables and functions from earlier run calls are gone.\nundefined" {
		t.Errorf("after load: %q", res.Text)
	}
	if err := r.newSession(); err != nil {
		t.Fatal(err)
	}
	goTurn(r)
	if res := lastResult(t, p.request(3)); res.Text != "undefined" {
		t.Errorf("after clear: %q", res.Text)
	}
	setMessages(r.agent, []llm.Message{llm.UserText("x"), bashCall("b", "ls"), {Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockToolResult, ToolUseID: "b", Text: "a"}}}, textReply("ok")})
	goTurn(r)
	if res := lastResult(t, p.request(5)); res.Text != "undefined" {
		t.Errorf("after loading a history without run: %q", res.Text)
	}
}

func TestCodeModeCompactionKeepsVM(t *testing.T) {
	p := scripted(runCall("c1", "globalThis.kept = 7"), textReply("ok"), textReply("summary"), runCall("c2", "kept"))
	r, _ := testRepl(t, config.Config{Model: "m"}, p)
	goTurn(r)
	if err := r.agent.Compact(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	goTurn(r)
	if res := lastResult(t, p.request(4)); res.Text != "7" {
		t.Errorf("after compaction: %q", res.Text)
	}
}

func TestCodeModeSubagentRunHasNoSubagent(t *testing.T) {
	p := scripted()
	a := newTestAgentWith(t, config.Config{Model: "m"}, t.TempDir(), p, devNullPrinter(t))
	if _, _, err := a.Turn(context.Background(), []llm.Block{{Type: llm.BlockText, Text: "hi"}}); err != nil {
		t.Fatal(err)
	}
	req := p.request(0)
	if names := toolNames(req); len(names) != 1 || names[0] != "run" || strings.Contains(req.Tools[0].Description, "subagent(") {
		t.Errorf("subagent tools %v:\n%s", names, req.Tools[0].Description)
	}
}

func TestCodeModeOffKeepsDirectTools(t *testing.T) {
	p := scripted()
	cfg := config.Config{Model: "m", CodeMode: boolPtr(false)}
	cwd := t.TempDir()
	a, _ := buildAgent(cfg, options{}, cwd, p, devNullPrinter(t), true, false)
	if _, _, err := a.Turn(context.Background(), []llm.Block{{Type: llm.BlockText, Text: "hi"}}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(toolNames(p.request(0)), ","); got != "read,write,edit,bash,jobs,subagent" {
		t.Errorf("tools %s", got)
	}
	_, sys := agent.LoadPrompt(cwd, cfg, "")
	if strings.Contains(sys.Text, "Your only tool is run") || !strings.Contains(sys.Text, "Put independent tool calls in the same response") {
		t.Error("off mode did not get the direct-tools prompt")
	}
}

func TestCodeModeProfileSwitch(t *testing.T) {
	t.Setenv("QI_SESSION_DIR", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	data := `{"api_key": "k", "model": "m", "subagent_models": [{"name": "one", "model": "m1"}], "profiles": {"b": {"code_mode": false, "subagent_models": [{"name": "two", "model": "m2"}]}}}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Resolve(config.Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	p := scripted()
	display, out := capturePrinter(t)
	o := options{configPath: path}
	a, runner := buildAgent(cfg, o, t.TempDir(), p, display, true, false)
	r := &repl{agent: a, display: display, hooks: &agent.Hooks{}, runner: runner, opts: o}
	if err := r.newSession(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.closeResources() })
	r.editor = ui.NewEditor(&ui.Terminal{Keys: make(chan ui.Key, 64)}, display, filepath.Join(t.TempDir(), "history"))
	if err := r.switchProfile("b"); err != nil {
		t.Fatal(err)
	}
	if !r.agent.Config().CodeModeOn() || !strings.Contains(out.String(), "code_mode is false in the new settings; it applies after restarting qi") {
		t.Errorf("code mode %v, output:\n%s", r.agent.Config().CodeModeOn(), out.String())
	}
	if !strings.Contains(r.agent.System(), "Your only tool is run") {
		t.Error("the rebuilt prompt left code mode")
	}
	r.agent.Reconfigure(r.agent.Config(), p)
	goTurn(r)
	req := p.request(0)
	if names := toolNames(req); len(names) != 1 || names[0] != "run" {
		t.Fatalf("tools %v", names)
	}
	if desc := req.Tools[0].Description; !strings.Contains(desc, "  - two (m2)") || strings.Contains(desc, "one (m1)") {
		t.Errorf("subagent menu not regenerated:\n%s", desc)
	}
}

func TestRenderRunCell(t *testing.T) {
	use := runCall("c1", "const s = await read(\"a.txt\")\nawait edit(\"a.txt\", \"x\", \"y\")\nreturn s")
	res := llm.Message{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockToolResult, ToolUseID: "c1", Text: "x", Calls: []llm.InnerCall{
		{Tool: "read", Label: "read a.txt", OK: true, Summary: "1 lines"},
		{Tool: "edit", Label: "edit a.txt", Summary: "old_string not found", Input: json.RawMessage(`"{\"file_path\":…"`)},
	}}}}
	got := renderTranscript([]llm.Message{use, res}, 0, 0)
	want := "· run\n    const s = await read(\"a.txt\")\n    await edit(\"a.txt\", \"x\", \"y\")\n    return s\n  ⎿ x\n    ✓ read a.txt · 1 lines\n    ✗ edit a.txt · old_string not found\n"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	if other := renderTranscript([]llm.Message{bashCall("b", "ls")}, 0, 0); other != "· bash {\"command\":\"ls\"}\n" {
		t.Errorf("bash: %q", other)
	}
}

func TestConfigTogglesRunCode(t *testing.T) {
	p := scripted(runCall("c1", "await read(\"a.txt\")\nreturn 1"), textReply("one"), runCall("c2", "await read(\"a.txt\")\nreturn 2"))
	r, out := testRepl(t, config.Config{Model: "m", Picker: "grep '^run_code'"}, p)
	if err := os.WriteFile(filepath.Join(r.agent.Cwd(), "a.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	goTurn(r)
	full := out.String()
	if !strings.Contains(full, "· run\n    await read(\"a.txt\")\n    return 1\n  ✓ read a.txt") || !strings.Contains(full, "✓ run · 1 call · 1 line") {
		t.Errorf("full view:\n%s", full)
	}
	editor := r.editor
	r.editor = nil
	r.handleInput("/config")
	r.editor = editor
	if got := out.String()[len(full):]; !strings.Contains(got, "run_code        full ") || !strings.Contains(got, "run_code: compact for this session") || r.agent.Config().RunCodeOn() {
		t.Fatalf("toggle printed %q, run_code %v", got, r.agent.Config().RunCodeOn())
	}
	before := len(out.String())
	goTurn(r)
	if got := out.String()[before:]; !strings.Contains(got, "· run await read(\"a.txt\") return 2\n  ✓ read a.txt") || strings.Contains(got, "    return 2") {
		t.Errorf("compact view:\n%s", got)
	}
	if !strings.Contains(full, "    return 1") {
		t.Error("earlier output changed")
	}
	before = len(out.String())
	r.handleInput("/config run_code")
	r.handleInput("/config show_reasoning")
	r.handleInput("/config nope")
	if got := out.String()[before:]; !strings.Contains(got, "run_code: full for this session") || !strings.Contains(got, "show_reasoning: off for this session") || !strings.Contains(got, `unknown setting "nope" (switchable: run_code, show_reasoning)`) || !r.agent.Config().RunCodeOn() || r.agent.Config().ShowReasoningOn() {
		t.Errorf("typed toggle printed %q", got)
	}
}

func TestRunCodeToggleSurvivesProfileSwitch(t *testing.T) {
	t.Setenv("QI_SESSION_DIR", t.TempDir())
	path := filepath.Join(t.TempDir(), "config.json")
	data := `{"api_key": "k", "model": "m", "profiles": {"quiet": {"run_code": false}, "loud": {"run_code": true, "show_reasoning": false}}}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Resolve(config.Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	display, _ := capturePrinter(t)
	o := options{configPath: path}
	a, runner := buildAgent(cfg, o, t.TempDir(), scripted(), display, true, false)
	r := &repl{agent: a, display: display, hooks: &agent.Hooks{}, runner: runner, opts: o}
	if err := r.newSession(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.closeResources() })
	if err := r.switchProfile("quiet"); err != nil || r.agent.Config().RunCodeOn() {
		t.Fatalf("without a toggle the profile's run_code applies: %v %v", r.agent.Config().RunCodeOn(), err)
	}
	r.report(r.toggleSetting("run_code"))
	if err := r.switchProfile("loud"); err != nil || !r.agent.Config().RunCodeOn() || r.agent.Config().ShowReasoningOn() {
		t.Fatalf("after the switch: run_code %v show_reasoning %v %v", r.agent.Config().RunCodeOn(), r.agent.Config().ShowReasoningOn(), err)
	}
	r.report(r.toggleSetting("run_code"))
	if err := r.switchProfile("loud"); err != nil || r.agent.Config().RunCodeOn() {
		t.Errorf("the session toggle should win over the profile: %v %v", r.agent.Config().RunCodeOn(), err)
	}
	if err := r.switchModel("other"); err != nil || r.agent.Config().RunCodeOn() || r.agent.Model() != "other" {
		t.Errorf("/model kept the toggle: %v %v", r.agent.Config().RunCodeOn(), err)
	}
}
