package app

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/paul-nameless/qi/internal/config"
)

type cliArgs struct {
	opts        options
	show        string
	showVersion bool
	checkConfig bool
}

func Main(args []string, version string) int {
	c, err := parseArgs(args, os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		return 2
	}
	c.opts.version = buildVersion(version)
	switch {
	case c.showVersion:
		fmt.Println("qi", c.opts.version)
		return 0
	case c.show != "":
		return exitCode(showSession(os.Stdout, c.show))
	case c.checkConfig:
		if err := config.Check(c.opts.configPath, c.opts.profile); err != nil {
			return exitCode(err)
		}
		if c.opts.systemPromptFile != "" {
			if _, err := resolveConfig(c.opts); err != nil {
				return exitCode(err)
			}
		}
		fmt.Println("config ok")
		return 0
	}
	return exitCode(run(c.opts))
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	fmt.Fprintln(os.Stderr, "qi:", err)
	return 1
}

func parseArgs(args []string, out io.Writer) (cliArgs, error) {
	var c cliArgs
	fs := newFlagSet(&c, out)
	if err := fs.Parse(args); err != nil {
		return c, err
	}
	if c.opts.pickSession && c.opts.resumeID == "" && fs.NArg() > 0 {
		c.opts.resumeID = fs.Arg(0)
		if err := fs.Parse(fs.Args()[1:]); err != nil {
			return c, err
		}
	}
	return c, nil
}

type resumeFlag struct{ o *options }

func (f resumeFlag) String() string   { return "" }
func (f resumeFlag) IsBoolFlag() bool { return true }

func (f resumeFlag) Set(v string) error {
	f.o.pickSession = true
	if v != "true" {
		f.o.resumeID = v
	}
	return nil
}

func newFlagSet(c *cliArgs, out io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet("qi", flag.ContinueOnError)
	fs.SetOutput(out)
	o := &c.opts
	fs.StringVar(&o.query, "q", "", "run one `prompt` without the interactive UI and print the answer")
	fs.BoolVar(&o.json, "json", false, "with -q, stream every session entry to stdout as a JSON line (the session file's format) instead of the answer; a resumed session starts with its meta line, then this run's entries")
	fs.BoolVar(&o.cont, "c", false, "continue the last session in this directory")
	fs.StringVar(&o.profile, "p", "", "profile `name` from \"profiles\" in the config, or "+filepath.Join(config.ShortHome(config.Dir()), "<name>.json"))
	fs.StringVar(&o.provider, "provider", "", "provider `name` override (from \"providers\" in the config)")
	fs.StringVar(&o.configPath, "config", "", "JSON config `file` (default "+config.ShortHome(config.DefaultPath())+")")
	fs.StringVar(&o.model, "model", "", "`model` override")
	fs.StringVar(&o.effort, "effort", "", "reasoning `effort` override")
	fs.StringVar(&o.systemPromptFile, "system-prompt-file", "", "replace the built-in base prompt with this `file` for this run")
	fs.BoolVar(&o.readOnly, "read-only", false, "start in read-only mode")
	fs.StringVar(&o.sessionID, "session-id", "", "use this session `id`: continues it if it exists, creates it otherwise")
	fs.Var(resumeFlag{o}, "r", "pick a session of this directory to resume, or resume <id>")
	fs.StringVar(&c.show, "show", "", "print a `session` of this directory (id or path) as plain text and exit")
	fs.BoolVar(&c.showVersion, "version", false, "print version")
	fs.BoolVar(&c.showVersion, "v", false, "print version")
	fs.BoolVar(&c.checkConfig, "check-config", false, "load the config and every profile without calling any API, then exit")
	fs.Usage = func() { usage(fs) }
	return fs
}

func usage(fs *flag.FlagSet) {
	out := fs.Output()
	fmt.Fprintln(out, "Usage: qi [flags]")
	var rows [][2]string
	width := 0
	fs.VisitAll(func(f *flag.Flag) {
		if f.Name == "v" {
			return
		}
		arg, help := flag.UnquoteUsage(f)
		name := "-" + f.Name
		switch f.Name {
		case "r":
			arg = "[id]"
		case "version":
			name = "-v, -version"
		}
		if arg != "" {
			name += " " + arg
		}
		width = max(width, len(name))
		rows = append(rows, [2]string{name, help})
	})
	for _, r := range rows {
		fmt.Fprintf(out, "  %-*s  %s\n", width, r[0], r[1])
	}
}
