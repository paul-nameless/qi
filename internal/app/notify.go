package app

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/paul-nameless/qi/internal/config"
	"github.com/paul-nameless/qi/internal/tools"
)

type infoer interface{ Info(string) }

func notifyAsync(cfg config.Config, cwd, msg string, ui infoer) {
	if !toolAvailable(cfg.NotifyCmd, cfg.Picker) {
		return
	}
	vars := map[string]string{"title": "qi · " + filepath.Base(cwd), "msg": msg}
	go func() {
		if _, err := runQuiet(cfg.NotifyCmd, cfg.Picker, vars, ""); err != nil {
			ui.Info("notify_cmd: " + err.Error())
		}
	}()
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
	if r.term == nil || !r.term.Away() {
		return
	}
	notifyAsync(r.agent.Config(), r.agent.Cwd(), turnMessage(final, err, took), r.display)
}
