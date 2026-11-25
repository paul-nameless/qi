package app

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/paul-nameless/qi/internal/ui"
	"golang.org/x/sys/unix"
)

func (r *repl) handleSignals() {
	ch := make(chan os.Signal, 4)
	signal.Notify(ch, os.Interrupt, syscall.SIGTSTP, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	go func() {
		defer ui.CleanupOnPanic()
		for s := range ch {
			switch s {
			case os.Interrupt:
				if !r.cancelTurn() {
					select {
					case r.editor.Interrupt <- struct{}{}:
					default:
					}
				}
			case syscall.SIGTSTP:
				r.stop()
			default:
				r.shutdown()
			}
		}
	}()
}

func (r *repl) handleResize() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	go func() {
		defer ui.CleanupOnPanic()
		for range ch {
			r.display.Redraw()
		}
	}()
}

func jobControl() bool {
	self, err := unix.Getsid(0)
	if err != nil {
		return false
	}
	parent, err := unix.Getsid(os.Getppid())
	return err == nil && parent == self
}

func (r *repl) stop() {
	if !jobControl() {
		return
	}
	child := r.term.ChildRunning()
	if !child {
		r.display.ClearFooter()
		r.term.Suspend()
	}
	stopSelf()
	if !child {
		r.term.Resume()
		r.display.RenderPrompt()
	}
}

func stopSelf() {
	cont := make(chan os.Signal, 1)
	signal.Notify(cont, syscall.SIGCONT)
	defer signal.Stop(cont)
	if syscall.Kill(os.Getpid(), syscall.SIGSTOP) == nil {
		<-cont
	}
}
