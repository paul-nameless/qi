package app

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/paul-nameless/qi/internal/ui"
)

func pickMain(args []string, in io.Reader, out io.Writer) int {
	fs := flag.NewFlagSet("qi pick", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: qi pick [-p prompt] [-d] [-f] < lines\n\nPicks one line from stdin and prints it. A tab splits a line into a name and a\ndescription. Exit 1 when cancelled.")
		fs.PrintDefaults()
	}
	prompt := fs.String("p", "> ", "prompt")
	desc := fs.Bool("d", false, "search descriptions too")
	files := fs.Bool("f", false, "browse files from the current directory instead of reading stdin")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		fmt.Fprintln(os.Stderr, "qi pick: no terminal:", err)
		return 2
	}
	var lines []string
	if !*files {
		lines = readLines(in)
	}
	t, err := ui.OpenTerminalOn(tty, tty)
	if err != nil {
		fmt.Fprintln(os.Stderr, "qi pick:", err)
		return 2
	}
	ed := ui.NewEditor(t, ui.NewPrinter(tty), "")
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	defer signal.Stop(sig)
	go func() {
		for range sig {
			select {
			case ed.Interrupt <- struct{}{}:
			default:
			}
		}
	}()
	choice, ok := pickLine(ed, lines, *prompt, *desc, *files)
	t.Restore()
	if !ok {
		return 1
	}
	fmt.Fprintln(out, choice)
	return 0
}

func pickLine(ed *ui.Editor, lines []string, prompt string, desc, files bool) (string, bool) {
	if files {
		cwd, err := os.Getwd()
		if err != nil {
			return "", false
		}
		return browseFiles(ed, cwd)
	}
	items := make([]ui.PickItem, len(lines))
	for i, l := range lines {
		name, d, _ := strings.Cut(l, "\t")
		items[i] = ui.PickItem{Name: name, Desc: d}
	}
	i, ok := ed.Pick(ui.PickList{Prompt: prompt, Items: items}, ui.PickOptions{SearchDesc: desc})
	if !ok {
		return "", false
	}
	return lines[i], true
}

func readLines(in io.Reader) []string {
	var lines []string
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64<<10), 8<<20)
	for sc.Scan() {
		if l := strings.TrimRight(sc.Text(), "\r"); l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}
