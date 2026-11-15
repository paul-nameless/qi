package ui

import (
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
