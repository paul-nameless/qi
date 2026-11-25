package app

import (
	"os"
	"time"

	"github.com/paul-nameless/qi/internal/proc"
)

func (r *repl) shutdown() {
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.quit()
		proc.Settle(time.Second)
		r.resumeHint()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		if r.term != nil {
			r.term.Restore()
		}
	}
	r.display.EndLine()
	os.Exit(1)
}

func (r *repl) quit() {
	r.settle(3 * time.Second)
	r.closeResources()
	if r.term != nil {
		r.term.Restore()
	}
}

func (r *repl) settle(limit time.Duration) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.stopIdle()
		r.mu.Lock()
		turnDone := r.turnDone
		r.mu.Unlock()
		if r.cancelTurn() && turnDone != nil {
			<-turnDone
		}
	}()
	select {
	case <-done:
	case <-time.After(limit):
	}
}

func (r *repl) closeResources() {
	r.agent.Jobs().KillAll()
	if r.mcp != nil {
		r.mcp.Close()
	}
	if s := r.agent.Session(); s != nil {
		s.Close()
	}
}
