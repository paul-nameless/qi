package proc

import (
	"errors"
	"os"
	"os/exec"
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

func Terminate(pgid int, grace time.Duration) error {
	err := syscall.Kill(-pgid, syscall.SIGTERM)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	go killAfter(pgid, grace)
	return err
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

func alive(pgid int) bool {
	return syscall.Kill(-pgid, 0) == nil
}

func Stop(cmd *exec.Cmd, grace time.Duration) error {
	if cmd.Process == nil {
		return nil
	}
	if err := Terminate(cmd.Process.Pid, grace); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return cmd.Wait()
}
