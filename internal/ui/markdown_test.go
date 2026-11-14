package ui

import (
	"os"
	"strings"
	"testing"
)

func render(in string, width int) string {
	m := markdown{width: width}
	return m.feed(in) + m.flush()
}

func TestMarkdown(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain text\n", "plain text\n"},
		{"a **bold** b\n", "a " + Reset + Bold + "bold" + Reset + " b\n"},
		{"use `go test` now\n", "use " + Reset + Cyan + "go test" + Reset + " now\n"},
		{"## Title\n", Reset + Bold + "Title" + Reset + "\n"},
		{"## Heading **x**\n", Reset + Bold + "Heading x" + Reset + "\n"},
		{"#hashtag\n", "#hashtag\n"},
		{"- one\n  * two\n", "• one\n  • two\n"},
		{"**lead** in\n", Reset + Bold + "lead" + Reset + " in\n"},
		{"```go\nx := `a`\n```\nafter\n", Reset + Dim + "```go" + Reset + "\n" + Reset + Cyan + "x := `a`" + Reset + "\n" + Reset + Dim + "```" + Reset + "\nafter\n"},
		{"````md\n```go\nx\n```\n````\nafter **b**\n", Reset + Dim + "````md" + Reset + "\n" + Reset + Cyan + "```go" + Reset + "\n" + Reset + Cyan + "x" + Reset + "\n" + Reset + Cyan + "```" + Reset + "\n" + Reset + Dim + "````" + Reset + "\nafter " + Reset + Bold + "b" + Reset + "\n"},
		{"```go\ncode", Reset + Dim + "```go" + Reset + "\n" + Reset + Cyan + "code" + Reset},
		{"`**x**` y\n", Reset + Cyan + "**x**" + Reset + " y\n"},
		{"**a** `b", Reset + Bold + "a" + Reset + " `b"},
		{"open **bold\nnext\n", "open **bold\nnext\n"},
		{"f(**kwargs) and 2**10\n", "f(**kwargs) and 2**10\n"},
		{"match **/*.go\n", "match **/*.go\n"},
		{"***\n", "***\n"},
		{"2 * 3\n", "2 * 3\n"},
		{"tail *", "tail *"},
		{"-", "-"},
	}
	for _, c := range cases {
		if got := render(c.in, 0); got != c.want {
			t.Errorf("%q:\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

func TestMarkdownStreaming(t *testing.T) {
	cases := []struct{ in, want string }{
		{"# Plan\n\n- **step** one with `code`\n- two\n\n```sh\ngo test ./...\n```\ndone *", "Plan\n\n• step one with code\n• two\n\n```sh\ngo test ./...\n```\ndone *"},
		{"text ** and ``` mid\n   ```\nraw **x**\n```\n", "text ** and ``` mid\n   ```\nraw **x**\n```\n"},
		{"| a | **b** |\n|---|:-:|\n| 1 | `2` |\ntail\n| x |", "a │ b\n──┼──\n1 │ 2\ntail\n| x |\n"},
		{"**日本** 🙂 `é`\n| 日本 | ✅ |\n|---|---|\n| x | y |\n", "日本 🙂 é\n日本 │ ✅\n─────┼───\nx    │ y\n"},
	}
	for _, c := range cases {
		whole := render(c.in, 0)
		var m markdown
		got := ""
		for i := 0; i < len(c.in); i++ {
			got += m.feed(c.in[i : i+1])
		}
		got += m.flush()
		if got != whole {
			t.Errorf("%q:\n byte-by-byte %q\n whole        %q", c.in, got, whole)
		}
		if plain := stripANSI(whole); plain != c.want {
			t.Errorf("%q:\n got  %q\n want %q", c.in, plain, c.want)
		}
	}
}

func TestMarkdownTable(t *testing.T) {
	cases := []struct {
		name, in, want string
		width          int
	}{
		{"aligned", "Before\n| Name | Qty |\n|:-----|----:|\n| **apple** | 3 |\n| 日本 | 12 |\n\nAfter\n", "Before\nName  │ Qty\n──────┼────\napple │   3\n日本  │  12\n\nAfter\n", 0},
		{"short row padded", "| a | b |\n|---|---|\n| 1 |\n", "a │ b\n──┼──\n1 │\n", 0},
		{"short separator", "| a | b |\n|:-:|\n| 1 | 2 |\n", "a │ b\n──┼──\n1 │ 2\n", 0},
		{"exact width", "| ab | cd |\n|---|---|\n", "ab │ cd\n───┼───\n", 7},
	}
	for _, c := range cases {
		if got := stripANSI(render(c.in, c.width)); got != c.want {
			t.Errorf("%s: got\n%s\nwant\n%s", c.name, got, c.want)
		}
	}
}

func TestMarkdownTableFallbacks(t *testing.T) {
	cases := []struct {
		name, in string
		width    int
	}{
		{"no separator", "| a | b |\n| c | d |\n", 0},
		{"too wide", "| a | b |\n|---|---|\n| long cell | x |\n", 8},
		{"extra cells", "| a | b |\n|---|---|\n| 1 | 2 | 3 |\n", 0},
		{"pipe text", "|x\n", 0},
	}
	for _, c := range cases {
		if got := render(c.in, c.width); got != c.in {
			t.Errorf("%s: got %q want raw %q", c.name, got, c.in)
		}
	}
}

func TestSplitRow(t *testing.T) {
	got := splitRow(`| a | b \| c | |`)
	want := []string{"a", "b | c", ""}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestDisplayWidth(t *testing.T) {
	cases := map[string]int{"abc": 3, "日本": 4, "é": 1, "e\u0301": 1, "🙂": 2, "✓": 1, "✅": 2, "🟢": 2, "⚠️": 2, Bold + "日本" + Reset: 4}
	for s, want := range cases {
		if got := VisibleLen(s); got != want {
			t.Errorf("%q: got %d want %d", s, got, want)
		}
	}
}

func TestTruncateVisible(t *testing.T) {
	cases := []struct {
		in    string
		width int
		want  string
	}{
		{"short", 10, "short"},
		{"abcd日", 5, "abcd…"},
		{"ab日本語", 5, "ab日…"},
		{Bold + "abcdef" + Reset, 4, "abc…"},
	}
	for _, c := range cases {
		got := truncateVisible(c.in, c.width)
		if plain := stripANSI(got); plain != c.want {
			t.Errorf("%q/%d: got %q want %q", c.in, c.width, plain, c.want)
		}
		if VisibleLen(got) > c.width {
			t.Errorf("%q/%d: %q wider than limit", c.in, c.width, got)
		}
	}
}

func testPrinter(t *testing.T, tty bool) (*Printer, func() string) {
	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	p := NewPrinter(f)
	p.TTY = tty
	return p, func() string {
		data, err := os.ReadFile(f.Name())
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
}

func TestPrinterRawWithoutTTY(t *testing.T) {
	p, out := testPrinter(t, false)
	p.Text("# **a** `b`\n| x |\n")
	p.EndBlock()
	if got := out(); got != "# **a** `b`\n| x |\n" {
		t.Errorf("got %q", got)
	}
}

func TestPrinterFlushesBeforeToolLine(t *testing.T) {
	p, out := testPrinter(t, true)
	p.Text("hi\n| a |\n|---|\n| 1 |")
	p.ToolStart("t1", "bash")
	p.ToolDone("t1", true, "bash done")
	got := stripANSI(out())
	table, tool := strings.Index(got, "─"), strings.Index(got, "bash done")
	if table < 0 || tool < 0 || table > tool {
		t.Errorf("table must print before tool line: %q", got)
	}
}

func TestPrinterEndBlockFlushesHeld(t *testing.T) {
	p, out := testPrinter(t, true)
	p.Text("x\n-")
	p.EndBlock()
	if got := stripANSI(out()); got != "x\n-\n" {
		t.Errorf("got %q", got)
	}
}

func TestPrinterInfoKeepsFence(t *testing.T) {
	p, out := testPrinter(t, true)
	p.Text("```go\nx")
	p.Info("queued: hello")
	p.Text("\n- y\n```\nafter\n")
	p.EndBlock()
	got := stripANSI(out())
	if !strings.Contains(got, "- y") || strings.Contains(got, "• y") || !strings.Contains(got, "queued: hello") {
		t.Errorf("fence state lost after info line: %q", got)
	}
	if strings.Contains(out(), Cyan+"after") {
		t.Errorf("prose after closing fence is styled as code: %q", out())
	}
}

func TestMarkdownChunkIndependent(t *testing.T) {
	inputs := []string{
		"The **key**s and **API**s\n",
		"```go\nx\n``` js\n# still code\n```\nafter\n",
		"# x ******|y**z\n",
		"- ```js\n# not heading\n* x\n```\nafter **b**\n",
		"1. ```bash\nls\n```\n2024 was **good**\n",
		"\t```go\n# c\n\t```\ndone\n",
		"see `foo` and ``` run\n",
	}
	for _, in := range inputs {
		whole := render(in, 0)
		for size := 1; size <= 4; size++ {
			var m markdown
			got := ""
			for i := 0; i < len(in); i += size {
				got += m.feed(in[i:min(i+size, len(in))])
			}
			got += m.flush()
			if got != whole {
				t.Errorf("%q split %d:\n got  %q\n whole %q", in, size, got, whole)
			}
		}
	}
}

func TestMarkdownFenceAfterListMarker(t *testing.T) {
	cases := []struct{ in, want string }{
		{"- ```js\n# not heading\n```\nafter **b**\n", Reset + Dim + "- ```js" + Reset + "\n" + Reset + Cyan + "# not heading" + Reset + "\n" + Reset + Dim + "```" + Reset + "\nafter " + Reset + Bold + "b" + Reset + "\n"},
		{"\t```go\n# c\n\t```\n", Reset + Dim + "\t```go" + Reset + "\n" + Reset + Cyan + "# c" + Reset + "\n" + Reset + Dim + "\t```" + Reset + "\n"},
		{"The **key**s\n", "The **key**s\n"},
		{"2024 was\n1.5 x\n", "2024 was\n1.5 x\n"},
	}
	for _, c := range cases {
		if got := render(c.in, 0); got != c.want {
			t.Errorf("%q:\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

func TestPrinterBreakLineEndsMarkdownLine(t *testing.T) {
	p, out := testPrinter(t, true)
	p.Text("Done.")
	p.Info("queued")
	p.Text("## Plan\nsee `foo")
	p.Info("queued")
	p.Text("bar` ok\n")
	p.EndBlock()
	got := stripANSI(out())
	want := "Done.\nqueued\nPlan\nsee `foo\nqueued\nbar` ok\n"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}
