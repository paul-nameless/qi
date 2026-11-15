package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/paul-nameless/qi/internal/agent"
	"github.com/paul-nameless/qi/internal/llm"
	"github.com/paul-nameless/qi/internal/mcp"
	"github.com/paul-nameless/qi/internal/tools"
)

const keysText = `Keys:
  shift+tab          toggle read-only / yolo
  ctrl+g             edit the prompt in $EDITOR
  ctrl+r             pick a prompt from history
  @                  pick files to insert (at the start of a word)
  alt+enter, ctrl+j  newline
  ctrl+c, esc        cancel the running turn (ctrl+c twice at the prompt quits)
  typing during a turn queues a message that is sent after the next tool results`

type command struct {
	name    string
	args    string
	help    string
	short   string
	alias   bool
	run     func(r *repl, args string) (text string, quit bool)
	pickArg func(r *repl) (text string, submit bool)
}

var commandList []command

func init() {
	commandList = []command{
		{name: "help", help: "this help", short: "show commands", run: info(func(*repl) string { return helpText() })},
		{name: "keys", help: "key bindings", short: "show key bindings", run: info(func(*repl) string { return keysText })},
		{name: "config", help: "config file and external tools", short: "show the config file and external tools", run: info((*repl).configInfo)},
		{name: "clear", help: "start a new session", short: "start a new session", run: action((*repl).clear)},
		{name: "context", help: "show what takes space in the context", short: "show what takes space in the context", run: info(func(r *repl) string { return r.agent.ContextReport() })},
		{name: "reload", help: "re-read skills, commands and CLAUDE.md files (rebuilds the cached prompt)", short: "re-read skills, commands and CLAUDE.md files", run: action((*repl).reload)},
		{name: "compact", args: "[focus]", help: "summarize the conversation to free context (optional focus for the summary)", short: "summarize the conversation to free context", run: withArgs((*repl).compact)},
		{name: "resume", args: "[id]", help: "switch to another session of this directory (picker without id)", short: "switch to another session", run: withArgs(func(r *repl, args string) { r.report(r.resume(args)) })},
		{name: "history", help: "open the whole conversation in the pager", short: "open the conversation in the pager", run: action(func(r *repl) { r.report(r.showHistory()) })},
		{name: "copy", args: "[code]", help: "copy the last answer (or its last code block) to the clipboard", short: "copy the last answer to the clipboard", run: withArgs(func(r *repl, args string) { r.report(r.copyAnswer(args)) })},
		{name: "model", args: "[name]", help: "show or switch the model (switching loses the prompt cache)", short: "switch the model", run: withArgs((*repl).model), pickArg: func(r *repl) (string, bool) { return r.pickArg("model", r.modelItems()) }},
		{name: "profile", args: "[name]", help: "show or switch the profile", short: "switch the profile", run: withArgs((*repl).profile), pickArg: (*repl).pickProfile},
		{name: "effort", args: "[level]", help: "show or switch the reasoning effort (default, none, low, medium, high, xhigh, max)", short: "switch the reasoning effort", run: withArgs((*repl).effort), pickArg: func(r *repl) (string, bool) { return r.pickArg("effort", r.effortItems()) }},
		{name: "skills", help: "list skills (/<skill-name> [args] runs one)", short: "pick a skill", run: info((*repl).skillList), pickArg: (*repl).pickSkill},
		{name: "mcp", help: "start MCP servers and show their status and enabled tools", short: "show MCP servers and their enabled tools", run: action((*repl).showMCP)},
		{name: "exit", help: "quit", short: "quit", run: quit},
		{name: "quit", alias: true, run: quit},
	}
}

func info(text func(*repl) string) func(*repl, string) (string, bool) {
	return func(r *repl, _ string) (string, bool) {
		r.display.Info(text(r))
		return "", false
	}
}

func action(fn func(*repl)) func(*repl, string) (string, bool) {
	return func(r *repl, _ string) (string, bool) {
		fn(r)
		return "", false
	}
}

func withArgs(fn func(*repl, string)) func(*repl, string) (string, bool) {
	return func(r *repl, args string) (string, bool) {
		fn(r, args)
		return "", false
	}
}

func quit(*repl, string) (string, bool) { return "", true }

func findCommand(name string) *command {
	for i := range commandList {
		if commandList[i].name == name {
			return &commandList[i]
		}
	}
	return nil
}

func helpText() string {
	var b strings.Builder
	b.WriteString("Commands:")
	for _, c := range commandList {
		if !c.alias {
			fmt.Fprintf(&b, "\n  %-19s%s", strings.TrimSpace("/"+c.name+" "+c.args), c.help)
		}
	}
	return b.String()
}

func pickerItems() []pickItem {
	var items []pickItem
	for _, c := range commandList {
		if !c.alias {
			items = append(items, pickItem{c.name, c.short})
		}
	}
	return items
}

func (r *repl) handleInput(line string) (string, bool) {
	if !strings.HasPrefix(line, "/") || strings.HasPrefix(line, "//") {
		return strings.TrimPrefix(line, "/"), false
	}
	name, args, _ := strings.Cut(line[1:], " ")
	if strings.Contains(name, "/") {
		return line, false
	}
	args = strings.TrimSpace(args)
	if c := findCommand(name); c != nil {
		return c.run(r, args)
	}
	text, err := r.expand(name, args)
	switch {
	case err != nil:
		r.display.Error(err.Error())
	case text != "":
		return text, false
	default:
		r.display.Error("unknown command /" + name + " (try /help)")
	}
	return "", false
}

func (r *repl) clear() {
	if err := r.newSession(); err != nil {
		r.display.Error(err.Error())
		return
	}
	r.display.Info("new session " + r.agent.Session().ID)
}

func (r *repl) compact(focus string) {
	err := r.cancellable(func(ctx context.Context) error { return r.agent.Compact(ctx, focus) })
	if errors.Is(err, context.Canceled) {
		r.display.Info("compaction cancelled")
	} else if err != nil {
		r.display.Error(err.Error())
	}
}

func (r *repl) switchThen(args string, switchTo func(string) error, show func() string) {
	if args != "" {
		if err := switchTo(args); err != nil {
			r.display.Error(err.Error())
		}
	}
	r.display.Info(show())
}

func (r *repl) model(args string) { r.switchThen(args, r.switchModel, r.modelList) }

func (r *repl) profile(args string) { r.switchThen(args, r.switchProfile, r.modelList) }

func (r *repl) effort(args string) {
	r.switchThen(args, r.setEffort, func() string { return "effort: " + effortName(r.agent.Config().ReasoningEffort) })
}

func (r *repl) skillList() string {
	var lines []string
	for _, s := range r.skills {
		lines = append(lines, "  /"+s.Name)
	}
	for _, c := range r.commands {
		lines = append(lines, "  /"+c.Name+" (command)")
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func (r *repl) showMCP() {
	if r.mcp == nil {
		r.display.Info(r.noMCP())
		return
	}
	var errs map[string]error
	_ = r.cancellable(func(ctx context.Context) error {
		errs = startMCP(ctx, r.mcp, r.display.Info)
		return nil
	})
	r.display.Info(mcpReport(r.mcp.Status(), errs))
}

func (r *repl) noMCP() string {
	if len(r.agent.Config().MCP) > 0 {
		return "qi started without MCP servers; restart to use the ones in this profile"
	}
	return "no MCP servers configured (add \"mcp\" to " + r.configFile() + ")"
}

func startMCP(ctx context.Context, m *mcp.Manager, info func(string)) map[string]error {
	errs := map[string]error{}
	for _, name := range m.Names() {
		if ctx.Err() != nil {
			errs[name] = ctx.Err()
			continue
		}
		info("starting " + name + "…")
		if err := m.Start(ctx, name); err != nil {
			errs[name] = err
		}
	}
	return errs
}

func mcpReport(servers []mcp.Status, errs map[string]error) string {
	lines := make([]string, len(servers))
	for i, s := range servers {
		lines[i] = mcpLine(s, errs[s.Name])
	}
	return strings.Join(lines, "\n")
}

func mcpLine(s mcp.Status, startErr error) string {
	switch {
	case errors.Is(startErr, context.Canceled):
		return s.Name + ": cancelled"
	case errors.Is(startErr, context.DeadlineExceeded):
		return s.Name + ": timed out"
	case startErr != nil:
		return fmt.Sprintf("%s: failed: %v", s.Name, startErr)
	case s.Err != nil:
		return fmt.Sprintf("%s: failed: %v", s.Name, s.Err)
	case s.State != mcp.Started:
		return s.Name + ": " + s.State.String()
	}
	line := fmt.Sprintf("%s: started · %d tools", s.Name, len(s.Tools))
	if len(s.Tools) > 0 {
		line += ": " + strings.Join(s.Tools, ", ")
	}
	if s.Hidden > 0 {
		line += fmt.Sprintf(" · %d hidden", s.Hidden)
	}
	return line
}

func (r *repl) reload() {
	before := r.agent.System()
	skills, sys := agent.LoadPrompt(r.agent.Cwd(), r.agent.Config())
	r.agent.SetSystem(sys)
	r.skills = skills
	r.commands = agent.LoadCommands(agent.ProjectRoots(r.agent.Cwd()))
	note := "unchanged"
	if sys.Text != before {
		note = "changed; the next request rewrites the prompt cache"
	}
	r.display.Info(fmt.Sprintf("reloaded %d skills, %d commands; system prompt %s · %s", len(skills), len(r.commands), note, r.agent.ContextUsage()))
}

func (r *repl) report(err error) {
	if err != nil {
		r.display.Error(err.Error())
	}
}

func (r *repl) expand(name, args string) (string, error) {
	for _, s := range r.skills {
		if s.Name == name {
			body, err := readBody(s.Path)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("<skill name=%q>\nBase directory for this skill: %s\n\n%s\n</skill>", s.Name, filepath.Dir(s.Path), expandArgs(body, args, false)), nil
		}
	}
	for _, c := range r.commands {
		if c.Name == name {
			body, err := readBody(c.Path)
			if err != nil {
				return "", err
			}
			return expandArgs(body, args, true), nil
		}
	}
	return "", nil
}

func readBody(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	_, body := agent.ParseFrontmatter(string(data))
	return strings.TrimSpace(body), nil
}

func expandArgs(body, args string, positional bool) string {
	placeholder := strings.Contains(body, "$ARGUMENTS")
	body = strings.ReplaceAll(body, "$ARGUMENTS", args)
	if positional && args != "" {
		fields := strings.Fields(args)
		for i := 9; i >= 1; i-- {
			key := fmt.Sprintf("$%d", i)
			if strings.Contains(body, key) {
				placeholder = true
				v := ""
				if i <= len(fields) {
					v = fields[i-1]
				}
				body = strings.ReplaceAll(body, key, v)
			}
		}
	}
	if args != "" && !placeholder {
		body += "\n\nArguments: " + args
	}
	return body
}

type notifier interface {
	Info(string)
	Error(string)
}

func userBlocks(cwd, sessionPath string, ui notifier, text string, images []string) []llm.Block {
	blocks := []llm.Block{{Type: llm.BlockText, Text: text}}
	for _, tok := range pathTokens(text) {
		if tools.ImageType(tok) == "" {
			continue
		}
		p := tok
		if !filepath.IsAbs(p) {
			p = filepath.Join(cwd, p)
		}
		if img, note, err := attachImage(sessionPath, ui, p); err == nil {
			blocks = append(blocks, img, note)
		}
	}
	for _, p := range images {
		img, note, err := attachImage(sessionPath, ui, p)
		if err != nil {
			ui.Error("image " + p + ": " + err.Error())
			continue
		}
		blocks = append(blocks, img, note)
	}
	return blocks
}

func attachImage(sessionPath string, ui notifier, path string) (llm.Block, llm.Block, error) {
	img, note, err := tools.LoadImage(keepTempImage(sessionPath, path))
	if err != nil {
		return llm.Block{}, llm.Block{}, err
	}
	info := "attached " + filepath.Base(path)
	if img.Width > 0 {
		info += fmt.Sprintf(" (%d×%d)", img.Width, img.Height)
	}
	ui.Info(info)
	return img, llm.Block{Type: llm.BlockText, Text: llm.AttachedImage(note)}, nil
}

func keepTempImage(sessionPath, path string) string {
	tmp, err := filepath.EvalSymlinks(os.TempDir())
	src, err2 := filepath.EvalSymlinks(path)
	if sessionPath == "" || err != nil || err2 != nil || !strings.HasPrefix(src, tmp+string(filepath.Separator)) {
		return path
	}
	if st, err := os.Stat(src); err != nil || !st.Mode().IsRegular() || st.Size() > 50<<20 {
		return path
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return path
	}
	sum := sha256.Sum256(data)
	dir := strings.TrimSuffix(sessionPath, ".jsonl") + "-images"
	dst := filepath.Join(dir, hex.EncodeToString(sum[:6])+strings.ToLower(filepath.Ext(path)))
	if os.MkdirAll(dir, 0o700) != nil || os.WriteFile(dst, data, 0o600) != nil {
		return path
	}
	return dst
}

func imagePaths(cwd, text string) []string {
	toks := pathTokens(strings.TrimSpace(text))
	if len(toks) == 0 {
		return nil
	}
	var out []string
	for _, tok := range toks {
		p := tok
		if !filepath.IsAbs(p) {
			p = filepath.Join(cwd, p)
		}
		st, err := os.Stat(p)
		if tools.ImageType(p) == "" || err != nil || !st.Mode().IsRegular() {
			return nil
		}
		out = append(out, p)
	}
	return out
}

func pathTokens(s string) []string {
	var out []string
	var cur strings.Builder
	quote := rune(0)
	rs := []rune(s)
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
				flush()
			} else {
				cur.WriteRune(c)
			}
		case c == '\'' || c == '"':
			flush()
			quote = c
		case c == '\\' && i+1 < len(rs) && rs[i+1] == ' ':
			cur.WriteRune(' ')
			i++
		case c == ' ' || c == '\n' || c == '\t':
			flush()
		default:
			cur.WriteRune(c)
		}
	}
	flush()
	return out
}
