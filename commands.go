package main

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

	"github.com/paul-nameless/aicode/internal/agent"
	"github.com/paul-nameless/aicode/internal/llm"
	"github.com/paul-nameless/aicode/internal/tools"
)

const helpText = `Commands:
  /help              this help
  /clear             start a new session
  /context           show what takes space in the context
  /reload            re-read skills, commands and CLAUDE.md files (rebuilds the cached prompt)
  /compact [focus]   summarize the conversation to free context (optional focus for the summary)
  /model [name]      show or switch the model (switching loses the prompt cache)
  /profile [name]    show or switch the profile
  /effort [level]    show or switch the reasoning effort (default, none, low, medium, high, xhigh, max)
  /skills            list skills (/<skill-name> [args] runs one)
  /exit              quit
Keys:
  shift+tab          toggle read-only / yolo
  ctrl+g             edit the prompt in $EDITOR
  alt+enter, ctrl+j  newline
  ctrl+c, esc        cancel the running turn (ctrl+c twice at the prompt quits)
  typing during a turn queues a message that is sent after the next tool results`

func (r *repl) handleInput(line string) (string, bool) {
	if !strings.HasPrefix(line, "/") || strings.HasPrefix(line, "//") {
		return strings.TrimPrefix(line, "/"), false
	}
	name, args, _ := strings.Cut(line[1:], " ")
	if strings.Contains(name, "/") {
		return line, false
	}
	r.stopIdle()
	args = strings.TrimSpace(args)
	switch name {
	case "exit", "quit":
		return "", true
	case "help":
		r.display.Info(helpText + "\n" + r.toolsHelp())
	case "clear":
		if err := r.newSession(); err != nil {
			r.display.Error(err.Error())
		} else {
			r.display.Info("new session " + r.agent.Session.ID)
		}
	case "reload":
		r.reload()
	case "context":
		r.display.Info(r.agent.ContextReport())
	case "compact":
		err := r.cancellable(func(ctx context.Context) error { return r.agent.Compact(ctx, args) })
		if errors.Is(err, context.Canceled) {
			r.display.Info("compaction cancelled")
		} else if err != nil {
			r.display.Error(err.Error())
		}
	case "model":
		if args != "" {
			if err := r.switchModel(args); err != nil {
				r.display.Error(err.Error())
			}
		}
		r.display.Info(r.modelList())
	case "profile":
		if args != "" {
			if err := r.switchProfile(args); err != nil {
				r.display.Error(err.Error())
			}
		}
		r.display.Info(r.modelList())
	case "effort":
		if args != "" {
			if err := r.setEffort(args); err != nil {
				r.display.Error(err.Error())
			}
		}
		r.display.Info("effort: " + effortName(r.agent.Cfg.ReasoningEffort))
	case "skills":
		var lines []string
		for _, s := range r.skills {
			lines = append(lines, "  /"+s.Name)
		}
		for _, c := range r.commands {
			lines = append(lines, "  /"+c.Name+" (command)")
		}
		sort.Strings(lines)
		r.display.Info(strings.Join(lines, "\n"))
	default:
		if text, err := r.expand(name, args); err != nil {
			r.display.Error(err.Error())
		} else if text != "" {
			return text, false
		} else {
			r.display.Error("unknown command /" + name + " (try /help)")
		}
	}
	return "", false
}

func (r *repl) reload() {
	before := r.agent.System
	skills, sys := agent.LoadPrompt(r.agent.Cwd, r.agent.Cfg)
	r.agent.SetSystem(sys)
	r.skills = skills
	r.commands = agent.LoadCommands(agent.ProjectRoots(r.agent.Cwd))
	note := "unchanged"
	if sys.Text != before {
		note = "changed; the next request rewrites the prompt cache"
	}
	r.display.Info(fmt.Sprintf("reloaded %d skills, %d commands; system prompt %s · %s", len(skills), len(r.commands), note, r.agent.ContextUsage()))
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

func userBlocks(a *agent.Agent, text string, images []string) []llm.Block {
	blocks := []llm.Block{{Type: llm.BlockText, Text: text}}
	for _, tok := range pathTokens(text) {
		if tools.ImageType(tok) == "" {
			continue
		}
		p := tok
		if !filepath.IsAbs(p) {
			p = filepath.Join(a.Cwd, p)
		}
		if img, note, err := attachImage(a, p); err == nil {
			blocks = append(blocks, img, note)
		}
	}
	for _, p := range images {
		img, note, err := attachImage(a, p)
		if err != nil {
			a.UI.Error("image " + p + ": " + err.Error())
			continue
		}
		blocks = append(blocks, img, note)
	}
	return blocks
}

func attachImage(a *agent.Agent, path string) (llm.Block, llm.Block, error) {
	img, note, err := tools.LoadImage(keepTempImage(a, path))
	if err != nil {
		return llm.Block{}, llm.Block{}, err
	}
	info := "attached " + filepath.Base(path)
	if img.Width > 0 {
		info += fmt.Sprintf(" (%d×%d)", img.Width, img.Height)
	}
	a.UI.Info(info)
	return img, llm.Block{Type: llm.BlockText, Text: "<attached-image>" + note + "</attached-image>"}, nil
}

func keepTempImage(a *agent.Agent, path string) string {
	tmp, err := filepath.EvalSymlinks(os.TempDir())
	src, err2 := filepath.EvalSymlinks(path)
	if a.Session == nil || err != nil || err2 != nil || !strings.HasPrefix(src, tmp+string(filepath.Separator)) {
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
	dir := strings.TrimSuffix(a.Session.Path, ".jsonl") + "-images"
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
