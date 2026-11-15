package app

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/paul-nameless/qi/internal/config"
	"github.com/paul-nameless/qi/internal/session"
)

const previewResultLines = 6

func sessionPath(cwd, ref string) (string, error) {
	if strings.HasSuffix(ref, ".jsonl") {
		return ref, nil
	}
	if !session.ValidID(ref) {
		return "", fmt.Errorf("invalid session id %q", ref)
	}
	path := session.PathFor(cwd, ref)
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("no session %s in %s", ref, cwd)
	}
	return path, nil
}

func showSession(w io.Writer, ref string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	path, err := sessionPath(cwd, ref)
	if err != nil {
		return err
	}
	entries, err := session.Entries(path)
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, renderTranscript(transcript(entries), previewResultLines, 0))
	return err
}

func sessionItems(cwd, current string) []pickItem {
	var items []pickItem
	for _, s := range session.List(cwd, true) {
		desc := fmt.Sprintf("%s · %d msgs · %s", age(time.Since(s.Mod)), s.Messages, s.First)
		if s.ID == current {
			desc = "(current) " + desc
		}
		items = append(items, pickItem{s.ID, desc})
	}
	return items
}

func pickSession(run runner, cfg config.Config, cwd, current string) (string, error) {
	if !toolAvailable(cfg.SessionPicker, cfg.Picker) {
		return "", fmt.Errorf("session_picker is off (%s not found); pass a session id: /resume <id>, ids are in %s", firstWord(cfg.SessionPicker), session.Dir(cwd))
	}
	items := sessionItems(cwd, current)
	if len(items) == 0 {
		return "", errors.New("no sessions with messages in " + cwd)
	}
	id, ok := pick(run, cfg.SessionPicker, cfg.Picker, "resume ", items)
	if !ok {
		return "", nil
	}
	return session.PathFor(cwd, id), nil
}

func (r *repl) resume(ref string) error {
	path, err := r.resumeTarget(ref)
	if err != nil || path == "" {
		return err
	}
	if path == r.agent.Session.Path {
		r.display.Info("already in session " + r.agent.Session.ID)
		return nil
	}
	r.stopIdle()
	s, ld, err := session.Open(path)
	if err != nil {
		return err
	}
	r.agent.Load(s, ld)
	r.hooks.Session, r.hooks.SessionID = s.Path, s.ID
	r.display.Info("session " + s.ID)
	r.printHistory()
	return nil
}

func (r *repl) resumeTarget(ref string) (string, error) {
	if ref != "" {
		return sessionPath(r.agent.Cwd, ref)
	}
	return pickSession(r.runTTY, r.agent.Cfg, r.agent.Cwd, r.agent.Session.ID)
}
