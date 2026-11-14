package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/paul-nameless/aicode/internal/tools"
)

type runner func(*exec.Cmd) error

var quietTimeout = 10 * time.Second

func toolAvailable(command, picker string) bool {
	if strings.TrimSpace(command) == "off" {
		return false
	}
	if strings.Contains(command, "{picker}") && !toolAvailable(picker, "") {
		return false
	}
	command = strings.ReplaceAll(command, "{picker}", picker)
	for _, f := range strings.Fields(command) {
		f = strings.TrimLeft(f, "(")
		if f == "" || strings.Contains(f, "=") {
			continue
		}
		_, err := exec.LookPath(f)
		return err == nil
	}
	return false
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func expandTool(command, picker string, vars map[string]string) string {
	command = strings.ReplaceAll(command, "{picker}", picker)
	var pairs []string
	for k, v := range vars {
		pairs = append(pairs, "{"+k+"}", shellQuote(v))
	}
	return strings.NewReplacer(pairs...).Replace(command)
}

func toolEnv() []string {
	env := os.Environ()
	if self, err := os.Executable(); err == nil {
		env = append(env, "AICODE_BIN="+self)
	}
	return env
}

func toolCmd(command string) *exec.Cmd {
	cmd := exec.Command("sh", "-c", command)
	cmd.Env = toolEnv()
	return cmd
}

func runQuiet(command, stdin string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), quietTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Env = toolEnv()
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
	case err != nil && strings.TrimSpace(errb.String()) != "":
		return out.String(), errors.New(firstLine(strings.TrimSpace(errb.String())))
	}
	return out.String(), err
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

func choose(run runner, command, picker, prompt string, stdin io.Reader) ([]string, bool) {
	vars := map[string]string{"prompt": prompt}
	var file string
	if strings.Contains(command, "{file}") {
		f, err := os.CreateTemp("", "aicode-choice-*")
		if err != nil {
			return nil, false
		}
		file = f.Name()
		f.Close()
		defer os.Remove(file)
		vars["file"] = file
	}
	cmd := toolCmd(expandTool(command, picker, vars))
	cmd.Env = append(cmd.Env, "FZF_DEFAULT_OPTS=", "FZF_DEFAULT_OPTS_FILE=", "AICODE_PICK_PROMPT="+prompt)
	cmd.Stdin = stdin
	var out bytes.Buffer
	if file == "" {
		cmd.Stdout = &out
	}
	if err := run(cmd); err != nil {
		return nil, false
	}
	if file != "" {
		data, err := os.ReadFile(file)
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
		{"paste_image_cmd", cfg.PasteImageCmd},
		{"session_picker", cfg.SessionPicker},
	}
}

func (r *repl) toolsHelp() string {
	var b strings.Builder
	b.WriteString("External tools:")
	for _, kv := range r.toolKeys() {
		state := "on "
		if !r.toolOn(kv[1]) {
			state = "off"
		}
		fmt.Fprintf(&b, "\n  %-15s %s  %s", kv[0], state, tools.OneLine(kv[1], 90))
	}
	return b.String()
}
