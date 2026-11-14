package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/paul-nameless/aicode/internal/config"
	"github.com/paul-nameless/aicode/internal/tools"
)

type infoer interface{ Info(string) }

func notifyAsync(cfg config.Config, cwd, msg string, ui infoer) {
	if !toolAvailable(cfg.NotifyCmd, cfg.Picker) {
		return
	}
	command := expandTool(cfg.NotifyCmd, cfg.Picker, map[string]string{"title": "aicode · " + filepath.Base(cwd), "msg": msg})
	go func() {
		if _, err := runQuiet(command, ""); err != nil {
			ui.Info("notify_cmd: " + err.Error())
		}
	}()
}

func notifyAfter(cfg config.Config) time.Duration {
	d, err := time.ParseDuration(cfg.NotifyAfter)
	if err != nil {
		return 20 * time.Second
	}
	return d
}

func turnMessage(final string, err error, took time.Duration) string {
	took = took.Round(time.Second)
	if err != nil {
		return fmt.Sprintf("failed after %s: %s", took, tools.OneLine(err.Error(), 120))
	}
	if s := strings.TrimSpace(final); s != "" {
		return fmt.Sprintf("done in %s: %s", took, tools.OneLine(s, 120))
	}
	return fmt.Sprintf("done in %s", took)
}

func (r *repl) notifyTurn(final string, err error, took time.Duration) {
	if took < notifyAfter(r.agent.Cfg) || (r.term != nil && r.term.Focused()) {
		return
	}
	notifyAsync(r.agent.Cfg, r.agent.Cwd, turnMessage(final, err, took), r.display)
}
