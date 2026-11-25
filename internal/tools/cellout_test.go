package tools

import (
	"context"
	"errors"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCellErrorsText(t *testing.T) {
	t.Parallel()
	k := newCellKit(t)
	k.wantErr("let x = ;", "SyntaxError: Unexpected token ';'\n    at cell.js:1:9")
	k.wantErr("1\nclass A {}", "SyntaxError: unsupported: classes\n    at cell.js:2:")
	k.wantErr("missing", "ReferenceError: missing is not defined", "No variables are defined in this VM yet.")
	k.cell("const alpha = 1; function beta() {}")
	k.wantErr("gamma", "ReferenceError: gamma is not defined\n    at cell.js:1:1", "Variables defined in this VM: ", "alpha", "beta")
	k.wantErr("await new Promise(r => setTimeout(r, 10))", "ReferenceError: setTimeout is not defined", "There are no timers: wait with await sleep(ms).")
	k.wantErr("clearInterval(1)", "ReferenceError: clearInterval is not defined", "There are no timers: wait with await sleep(ms).")
	if res := k.cell(`throw new ReferenceError("setTimeout wrapper failed")`); strings.Contains(res.Text, "no timers") {
		t.Errorf("a ReferenceError about something else got the timer hint: %q", res.Text)
	}
	k.file("f.txt", "x\n")
	res := k.wantErr(`await read("f.txt"); await tick(); null.x`, "TypeError", "Calls that completed:\n✓ read f.txt\n✓ tick")
	if res.Summary != "2 calls · TypeError: Cannot read properties of null (reading 'x')" {
		t.Errorf("summary %q", res.Summary)
	}
	k.wantErr("throw 5", "Uncaught 5")
}

func TestCellOutputCap(t *testing.T) {
	t.Parallel()
	k := newCellKit(t)
	res := k.cell(`for (let i = 0; i < 5000; i++) console.log("line " + i)`)
	for _, want := range []string{"line 0\n", "line 4999", "lines omitted. Full output: "} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("capped output lacks %q", want)
		}
	}
	if len(res.Text) > cellOutBytes+500 || strings.Count(res.Text, "\n") > cellOutLines {
		t.Errorf("output not capped: %d bytes", len(res.Text))
	}
}

func TestCellFailureSavesSubagentAnswers(t *testing.T) {
	t.Parallel()
	k := newCellKit(t, Subagent("m", nil))
	k.second(300 * time.Millisecond)
	failed := make(chan struct{})
	var failOnce sync.Once
	k.env.Subagent = func(ctx context.Context, task SubagentTask, progress func(string)) (string, error) {
		switch task.Prompt {
		case "fail":
			return "", errors.New("api error 402")
		case "slow":
			<-failed
		}
		return "report for " + task.Prompt, nil
	}
	code := `const q = subagent("quick"); const f = q.then(() => subagent("fail")).catch(e => { console.log("failing"); throw e }); await Promise.all([q, subagent("slow"), f])`
	res := k.cellProgress(k.ctx, code, 0, func(text string) {
		if text == "failing" {
			failOnce.Do(func() { close(failed) })
		}
	})
	for _, part := range []string{"ToolError: subagent failed: api error 402", "Note: the code failed while 1 call was still running; it was waited for."} {
		if !res.IsError || !strings.Contains(res.Text, part) {
			t.Errorf("failed cell: %q lacks %q", res.Text, part)
		}
	}
	if strings.Contains(res.Text, "await every call") {
		t.Errorf("failed cell blames unawaited calls: %q", res.Text)
	}
	for _, name := range []string{"quick", "slow"} {
		m := regexp.MustCompile(`✓ subagent m/default ` + name + ` → answer saved to (\S+)`).FindStringSubmatch(res.Text)
		if m == nil {
			t.Fatalf("%s: no saved answer in %q", name, res.Text)
		}
		if data, err := os.ReadFile(m[1]); err != nil || string(data) != "report for "+name {
			t.Errorf("%s: saved %q, %v", name, data, err)
		}
	}
	k.wantErr(`await subagent("first"); for (let i = 0; i < 35; i++) await tick(); null.x`, "(last 30 of 36):\n✓ subagent m/default first → answer saved to ")
	if res = k.cellCtx(context.Background(), `await subagent("quick"); while (true) {}`, 1); !strings.Contains(res.Text, "timed out after 1s") || !strings.Contains(res.Text, "✓ subagent m/default quick → answer saved to ") {
		t.Errorf("timeout: %q", res.Text)
	}
	k.env.SpillDir = t.TempDir()
	if res = k.cell(`const r = await Promise.allSettled([subagent("quick"), subagent("fail")]); r.map(x => x.status).join(" ")`); res.IsError || !strings.HasPrefix(res.Text, "fulfilled rejected\n") {
		t.Errorf("allSettled: %+v", res)
	}
	k.want(`(await Promise.all([subagent("a"), subagent("b")])).join(", ")`, "report for a, report for b")
	if files, _ := os.ReadDir(k.env.SpillDir); len(files) != 0 {
		t.Errorf("a successful cell saved %d answers", len(files))
	}
}

func TestCellFailedCallsNote(t *testing.T) {
	t.Parallel()
	k := newCellKit(t)
	k.file("a.go", "package a\n")
	res := k.cell(`edit("a.go", "x", "y"); await bash("sleep 0.3"); "done"`)
	if res.IsError || !strings.HasPrefix(res.Text, "done\nFailed calls:\n✗ edit a.go: old not found") || strings.Contains(res.Text, "still running") {
		t.Errorf("unawaited failure: %+v", res)
	}
	res = k.cell(`for (let i = 0; i < 12; i++) { try { await read("missing" + i) } catch {} } "ok"`)
	if !strings.HasSuffix(res.Text, "\nand 2 more") || strings.Count(res.Text, "✗ read missing") != 10 {
		t.Errorf("failed list cap: %q", res.Text)
	}
	k.want(`(await bash("exit 3")).exit_code`, "3")
	res = k.cell(`edit("missing.txt", "a", "b"); "r"`)
	if strings.Count(res.Text, "edit missing.txt") != 1 || !strings.Contains(res.Text, "still running") {
		t.Errorf("late failure listed twice or not at all: %q", res.Text)
	}
}

func TestCellErrorKeptWhenWaitStops(t *testing.T) {
	t.Parallel()
	k := newCellKit(t)
	k.second(500 * time.Millisecond)
	res := k.cellCtx(context.Background(), `await Promise.all([bash("sleep 100", {timeout: 600}), read("nope")])`, 1)
	for _, want := range []string{"ToolError: stat ", "Calls that completed:\n✗ read nope\nNote: the code failed while 1 call was still running. Failed:\nbash sleep 100: ", "Note: waiting for the calls still running stopped: timed out after 1s"} {
		if !res.IsError || !strings.Contains(res.Text, want) {
			t.Errorf("timeout: %q lacks %q", res.Text, want)
		}
	}
	if !strings.HasPrefix(res.Text, "ToolError: stat ") || strings.Contains(res.Text, "waited for") || strings.Contains(res.Text, "await every call") || strings.Contains(res.Text, "✗ bash") {
		t.Errorf("timeout: %q", res.Text)
	}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	res = k.cellCtx(ctx, `bash("sleep 100", {timeout: 600}); undefinedName`, 0)
	if !res.IsError || !strings.HasPrefix(res.Text, "ReferenceError: undefinedName is not defined") || !strings.Contains(res.Text, "No variables are defined in this VM yet.") || !strings.HasSuffix(res.Text, "Note: waiting for the calls still running stopped: interrupted by user") {
		t.Errorf("interrupt: %q", res.Text)
	}
}
