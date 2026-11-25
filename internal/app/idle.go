package app

import (
	"context"
	"time"

	"github.com/paul-nameless/qi/internal/ui"
)

func (r *repl) armIdle() {
	ttl, err := time.ParseDuration(r.agent.Config().CacheTTL)
	if err != nil || ttl <= 5*time.Minute {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.idle != nil {
		r.idle.Stop()
	}
	r.idleGen++
	gen := r.idleGen
	r.idle = time.AfterFunc(ttl-5*time.Minute, func() { r.idleCompact(gen) })
}

func (r *repl) idleCompact(gen int) {
	defer ui.CleanupOnPanic()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	done := make(chan struct{})
	defer close(done)
	r.mu.Lock()
	if gen != r.idleGen {
		r.mu.Unlock()
		return
	}
	r.idleCancel, r.idleDone = cancel, done
	r.mu.Unlock()
	if err := r.agent.TryCompactIdle(ctx); err != nil && ctx.Err() == nil {
		r.display.Error("idle compaction failed: " + err.Error())
	}
}

func (r *repl) stopIdle() {
	r.mu.Lock()
	r.idleGen++
	if r.idle != nil {
		r.idle.Stop()
		r.idle = nil
	}
	if r.idleCancel != nil {
		r.idleCancel()
	}
	done := r.idleDone
	r.idleCancel, r.idleDone = nil, nil
	r.mu.Unlock()
	if done != nil {
		<-done
	}
}
