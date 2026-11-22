package ui

import (
	"fmt"
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

func runCell(p *Printer, code string) {
	p.ToolStartCode("r1", "run "+strings.ReplaceAll(code, "\n", " "), code)
	p.ToolStart("r1.1", "read a")
	p.ToolStart("r1.2", "bash go test")
	p.ToolDone("r1.1", true, "read a · 3 lines")
	p.ToolDone("r1.2", false, "bash go test · exit 1")
	p.ToolDone("r1", true, "run "+strings.ReplaceAll(code, "\n", " ")+" · 2 calls · 1 line")
	p.ToolStartCode("r2", "run 1", "1")
	p.ToolDone("r2", true, "run 1 · 1 line")
}

func withoutTimes(s string) string {
	return regexp.MustCompile(` \d+ms\n`).ReplaceAllString(s, "\n")
}

func TestPrinterRunCodeFull(t *testing.T) {
	p, out := testPrinter(t, false)
	runCell(p, "\nconst a = await read('a')\nawait bash('go test')\n")
	want := "· run\n    const a = await read('a')\n    await bash('go test')\n  ✓ read a · 3 lines\n  ✗ bash go test · exit 1\n✓ run · 2 calls · 1 line\n· run\n    1\n✓ run · 1 line\n"
	if got := withoutTimes(out()); got != want {
		t.Errorf("output\n%s\nwant\n%s", got, want)
	}
}

func TestPrinterRunCodeCompact(t *testing.T) {
	p, out := testPrinter(t, false)
	p.SetRunCode(false)
	runCell(p, "await read('a')")
	want := "· run await read('a')\n  ✓ read a · 3 lines\n  ✗ bash go test · exit 1\n✓ run · 2 calls · 1 line\n✓ run 1 · 1 line\n"
	if got := withoutTimes(out()); got != want {
		t.Errorf("output\n%s\nwant\n%s", got, want)
	}
}

func TestPrinterRunCodeCap(t *testing.T) {
	p, out := testPrinter(t, false)
	lines := make([]string, 250)
	for i := range lines {
		lines[i] = fmt.Sprintf("line%d()", i+1)
	}
	p.ToolStartCode("r1", "run x", strings.Join(lines, "\n"))
	p.ToolDone("r1", true, "run x · 1 line")
	got := withoutTimes(out())
	if !strings.Contains(got, "    line200()\n    … 50 more lines\n✓ run · 1 line\n") || strings.Contains(got, "line201()") || strings.Count(got, "\n") != 203 {
		t.Errorf("output\n%s", got)
	}
}

func TestPrinterRunCodeLongLineKeepsStatus(t *testing.T) {
	p, out := testPrinter(t, true)
	long := strings.Repeat("x", 300)
	p.ToolStartCode("r1", "run "+long, "a()\n"+long)
	got := out()
	body := Dim + "· run\n    a()\n    " + long + Reset + "\n"
	i := strings.Index(got, body)
	if i < 0 {
		t.Fatalf("code not printed whole:\n%q", got)
	}
	rest := got[i+len(body):]
	if !strings.HasPrefix(rest, "\r") || !strings.Contains(rest, "1 running") || !strings.Contains(rest, "…") {
		t.Errorf("status after the code %q", rest)
	}
	p.ToolDone("r1", true, "run "+long+" · 1 line")
	if got := stripANSI(out()); !strings.HasSuffix(withoutTimes(got), "✓ run · 1 line\n") {
		t.Errorf("done line %q", got[len(got)-40:])
	}
}
