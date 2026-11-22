package ui

import (
	"regexp"
	"strings"
	"testing"
)

func threeRows(int, int) Frame {
	return Frame{Rows: []string{"❯ a", "  b", "  c"}, Row: 1, Col: 3}
}

func TestPrinterMultiRowPrompt(t *testing.T) {
	p, out := testPrinter(t, true)
	p.SetPrompt(threeRows, func(int) string { return "❯ a\n  b\n  c" })
	p.Line("x")
	p.RenderPrompt()
	p.FinishPrompt()
	p.EndLine()
	draw := "\r❯ a\r\n  b\r\n  c\x1b[1A\r\x1b[3C"
	clear := "\r\x1b[1A\x1b[2K\x1b[1B\x1b[J\x1b[1A"
	want := draw + clear + "x\n" + draw + clear + draw + clear + "❯ a\n  b\n  c\n"
	if got := out(); got != want {
		t.Errorf("output\n%q\nwant\n%q", got, want)
	}
}

func TestPrinterEndLineBelowPrompt(t *testing.T) {
	p, out := testPrinter(t, true)
	p.SetPrompt(threeRows, func(int) string { return "" })
	p.EndLine()
	if got := out(); !strings.HasSuffix(got, "\x1b[3C\x1b[1B\n") {
		t.Errorf("output %q", got)
	}
}

func TestPrinterClearScreenForgetsPrompt(t *testing.T) {
	p, out := testPrinter(t, true)
	p.SetPrompt(threeRows, func(int) string { return "" })
	p.ClearScreen()
	p.RenderPrompt()
	if got := out(); !strings.HasSuffix(got, "\x1b[2J\x1b[H\r❯ a\r\n  b\r\n  c\x1b[1A\r\x1b[3C") {
		t.Errorf("output %q", got)
	}
}

func TestPrinterDraftRows(t *testing.T) {
	p, out := testPrinter(t, true)
	p.SetDraft("a\nb")
	p.SetDraft("")
	want := "\r" + Cyan + "› " + Reset + "a\r\n  b  " + Dim + "(enter to queue)" + Reset + "\r\x1b[3C" + "\r\x1b[1A\x1b[2K\x1b[1B\x1b[J\x1b[1A"
	if got := out(); got != want {
		t.Errorf("output\n%q\nwant\n%q", got, want)
	}
}

func TestPrinterSingleRowClearKeepsLine(t *testing.T) {
	p, out := testPrinter(t, true)
	p.SetPrompt(func(int, int) Frame { return Frame{Rows: []string{"❯ a"}, Col: 3} }, func(int) string { return "❯ a" })
	p.FinishPrompt()
	if got, want := out(), "\r❯ a\r\x1b[3C\r\x1b[2K❯ a\n"; got != want {
		t.Errorf("output %q, want %q", got, want)
	}
}

func TestPrinterNestsInnerCalls(t *testing.T) {
	p, out := testPrinter(t, false)
	p.ToolStart("r1", "run await read('a')")
	p.ToolStart("r1.1", "read a")
	p.ToolStart("r1.2", "bash go test")
	p.ToolProgress("r1.2", "ok pkg")
	if got := stripANSI(p.statusText()); !strings.Contains(got, "1 running run await read('a') › read a, bash go test · ok pkg") {
		t.Errorf("status %q", got)
	}
	p.ToolDone("r1.1", true, "read a · 3 lines")
	p.ToolDone("r1.2", false, "bash go test · exit 1")
	p.ToolDone("r1", true, "run await read('a') · 2 calls · 1 line")
	p.ToolStart("r2", "run 1")
	p.ToolDone("r2", true, "run 1 · 1 line")
	got := regexp.MustCompile(` \d+ms\n`).ReplaceAllString(out(), "\n")
	want := "· run await read('a')\n  ✓ read a · 3 lines\n  ✗ bash go test · exit 1\n✓ run · 2 calls · 1 line\n✓ run 1 · 1 line\n"
	if got != want {
		t.Errorf("output\n%s\nwant\n%s", got, want)
	}
}
