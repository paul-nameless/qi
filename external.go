package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/paul-nameless/aicode/internal/config"
	"github.com/paul-nameless/aicode/internal/tools"
)

type runner func(*exec.Cmd) error

var quietTimeout = 10 * time.Second

var shellWords = map[string]bool{"if": true, "for": true, "while": true, "until": true, "case": true, "exec": true, "command": true, "eval": true, ".": true, "{": true, "!": true, "[": true}

func toolAvailable(command, picker string) bool {
	if strings.TrimSpace(command) == "off" {
		return false
	}
	if strings.Contains(command, "{picker}") && !toolAvailable(picker, "") {
		return false
	}
	name := firstCommand(strings.ReplaceAll(command, "{picker}", picker))
	if name == "" {
		return false
	}
	if shellWords[name] {
		return true
	}
	_, err := exec.LookPath(name)
	return err == nil
}

func firstCommand(command string) string {
	for _, f := range strings.Fields(command) {
		f = strings.TrimLeft(f, "(")
		if f == "" || (strings.Contains(f, "=") && !strings.ContainsAny(f[:strings.Index(f, "=")], "/'\"")) {
			continue
		}
		if i := strings.IndexAny(f, "|;&<>)"); i >= 0 {
			f = f[:i]
		}
		f = strings.Trim(f, `'"`)
		if strings.HasPrefix(f, "~/") {
			f = config.ExpandHome(f)
		}
		return os.ExpandEnv(f)
	}
	return ""
}

func expandTool(command, picker string, vars map[string]string) (string, []string) {
	command = strings.ReplaceAll(command, "{picker}", picker)
	var env []string
	for k, v := range vars {
		env = append(env, varName(k)+"="+v)
	}
	var b strings.Builder
	quotes := []byte{0}
	for i := 0; i < len(command); i++ {
		c, quote := command[i], quotes[len(quotes)-1]
		if name := placeholder(command[i:], vars); name != "" {
			b.WriteString(varRef(varName(name), quote))
			i += len(name) + 1
			continue
		}
		switch {
		case c == '\\' && quote != '\'' && i+1 < len(command):
			b.WriteByte(c)
			i++
			c = command[i]
		case c == '$' && quote != '\'' && i+1 < len(command) && command[i+1] == '(':
			b.WriteByte(c)
			i++
			c = command[i]
			quotes = append(quotes, '(')
		case c == ')' && quote == '(':
			quotes = quotes[:len(quotes)-1]
		case (quote == 0 || quote == '(') && (c == '\'' || c == '"'):
			quotes = append(quotes, c)
		case quote == c && quote != '(':
			quotes = quotes[:len(quotes)-1]
		}
		b.WriteByte(c)
	}
	return b.String(), env
}

func placeholder(s string, vars map[string]string) string {
	end := strings.IndexByte(s, '}')
	if !strings.HasPrefix(s, "{") || end < 0 {
		return ""
	}
	if _, ok := vars[s[1:end]]; !ok {
		return ""
	}
	return s[1:end]
}

func varName(key string) string {
	return "AICODE_" + strings.ToUpper(key)
}

func varRef(name string, quote byte) string {
	if quote == '"' {
		return "${" + name + "}"
	}
	return `"${` + name + `}"`
}

func toolEnv(extra []string) []string {
	env := os.Environ()
	if self, err := os.Executable(); err == nil {
		env = append(env, "AICODE_BIN="+self)
	}
	return append(env, extra...)
}

func toolCmd(command, picker string, vars map[string]string) *exec.Cmd {
	line, env := expandTool(command, picker, vars)
	cmd := exec.Command("sh", "-c", line)
	cmd.Env = toolEnv(env)
	return cmd
}

func runQuiet(command, picker string, vars map[string]string, stdin string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), quietTimeout)
	defer cancel()
	line, env := expandTool(command, picker, vars)
	cmd := exec.CommandContext(ctx, "sh", "-c", line)
	cmd.Env = toolEnv(env)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	cmd.Stdin = strings.NewReader(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	switch {
	case ctx.Err() != nil:
		return out.String(), fmt.Errorf("timed out after %s", quietTimeout)
	case errors.Is(err, exec.ErrWaitDelay):
		return out.String(), nil
	case err != nil && strings.TrimSpace(errb.String()) != "":
		return out.String(), errors.New(firstLine(strings.TrimSpace(errb.String())))
	}
	return out.String(), err
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

func tempFile(ext, content string) (string, error) {
	f, err := os.CreateTemp("", "aicode-*"+ext)
	if err != nil {
		return "", err
	}
	_, err = f.WriteString(content)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

func choose(run runner, command, picker, prompt string, stdin io.Reader) ([]string, bool) {
	vars := map[string]string{"prompt": prompt}
	if strings.Contains(command, "{file}") {
		file, err := tempFile("", "")
		if err != nil {
			return nil, false
		}
		defer os.Remove(file)
		vars["file"] = file
	}
	cmd := toolCmd(command, picker, vars)
	cmd.Env = append(cmd.Env, "FZF_DEFAULT_OPTS=", "FZF_DEFAULT_OPTS_FILE=", "AICODE_PICK_PROMPT="+prompt)
	cmd.Stdin = stdin
	var out bytes.Buffer
	if vars["file"] == "" {
		cmd.Stdout = &out
	}
	if err := run(cmd); err != nil {
		return nil, false
	}
	if vars["file"] != "" {
		data, err := os.ReadFile(vars["file"])
		if err != nil {
			return nil, false
		}
		out.Write(data)
	}
	var lines []string
	for _, l := range strings.Split(out.String(), "\n") {
		if l = strings.TrimRight(l, "\r"); l != "" {
			lines = append(lines, l)
		}
	}
	return lines, len(lines) > 0
}

func runPlain(cmd *exec.Cmd) error {
	if cmd.Stdin == nil {
		cmd.Stdin = os.Stdin
	}
	if cmd.Stdout == nil {
		cmd.Stdout = os.Stdout
	}
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (r *repl) runTTY(cmd *exec.Cmd) error {
	if r.editor == nil {
		return runPlain(cmd)
	}
	return r.editor.RunTTY(cmd)
}

func (r *repl) view(key, command, content, ext string) error {
	if !r.toolOn(command) {
		return fmt.Errorf("%s is off (%s not found)", key, firstWord(command))
	}
	vars := map[string]string{}
	var stdin io.Reader = strings.NewReader(content)
	if strings.Contains(command, "{file}") {
		file, err := tempFile(ext, content)
		if err != nil {
			return err
		}
		defer os.Remove(file)
		vars["file"], stdin = file, nil
	}
	cmd := toolCmd(command, r.agent.Cfg.Picker, vars)
	cmd.Stdin = stdin
	if err := r.runTTY(cmd); err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	return nil
}

func firstWord(command string) string {
	f := strings.Fields(command)
	if len(f) == 0 {
		return "command"
	}
	return f[0]
}

func (r *repl) toolOn(command string) bool {
	return toolAvailable(command, r.agent.Cfg.Picker)
}

func (r *repl) toolKeys() [][2]string {
	cfg := r.agent.Cfg
	return [][2]string{
		{"picker", cfg.Picker},
		{"notify_cmd", cfg.NotifyCmd},
		{"file_picker", cfg.FilePicker},
		{"history_picker", cfg.HistoryPicker},
		{"session_picker", cfg.SessionPicker},
		{"pager", cfg.Pager},
		{"viewer", cfg.Viewer},
		{"copy_cmd", cfg.CopyCmd},
	}
}

func (r *repl) configInfo() string {
	path := r.opts.configPath
	if path == "" {
		path = filepath.Join(config.Dir(), "config.yml")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Config: %s\nExternal tools:", path)
	for _, kv := range r.toolKeys() {
		state := "on "
		if !r.toolOn(kv[1]) {
			state = "off"
		}
		fmt.Fprintf(&b, "\n  %-15s %s  %s", kv[0], state, tools.OneLine(kv[1], 90))
	}
	return b.String()
}
