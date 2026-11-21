package proc

import (
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

type Mode int

const (
	Session Mode = iota
	Group
)

const (
	Grace     = 2 * time.Second
	pollEvery = 20 * time.Millisecond
)

func Detach(cmd *exec.Cmd, mode Mode) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: mode == Session, Setpgid: mode == Group}
}

func Setup(cmd *exec.Cmd, mode Mode, grace time.Duration) {
	Detach(cmd, mode)
	cmd.Cancel = func() error { return Terminate(cmd.Process.Pid, grace) }
	cmd.WaitDelay = grace + time.Second
}

var escalations sync.WaitGroup

func Terminate(pgid int, grace time.Duration) error {
	err := syscall.Kill(-pgid, syscall.SIGTERM)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	escalations.Go(func() { killAfter(pgid, grace) })
	return err
}

func Settle(limit time.Duration) bool {
	done := make(chan struct{})
	go func() {
		escalations.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(limit):
		return false
	}
}

func killAfter(pgid int, grace time.Duration) {
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if !alive(pgid) {
			return
		}
		time.Sleep(pollEvery)
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
}

func Alive(pgid int) bool {
	return alive(pgid)
}

func alive(pgid int) bool {
	return syscall.Kill(-pgid, 0) == nil
}

func Stop(cmd *exec.Cmd, grace time.Duration) error {
	if cmd.Process == nil {
		return nil
	}
	pgid := cmd.Process.Pid
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	deadline := time.Now().Add(grace)
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	select {
	case err := <-waited:
		killAfter(pgid, time.Until(deadline))
		return err
	case <-time.After(grace):
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		return <-waited
	}
}
