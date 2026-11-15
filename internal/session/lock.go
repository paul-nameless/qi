package session

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

func pidFile(path string) string {
	return strings.TrimSuffix(path, ".jsonl") + ".pid"
}

func lock(f *os.File, path, id string) error {
	err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) {
		return fmt.Errorf("session %s is open in another qi%s", id, holder(path))
	}
	if err != nil {
		return fmt.Errorf("lock session %s: %w", id, err)
	}
	_ = os.WriteFile(pidFile(path), []byte(strconv.Itoa(os.Getpid())), 0o600)
	return nil
}

func holder(path string) string {
	data, err := os.ReadFile(pidFile(path))
	if pid, perr := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && perr == nil {
		return fmt.Sprintf(" (pid %d)", pid)
	}
	return ""
}

func unlock(f *os.File, path string) {
	_ = os.Remove(pidFile(path))
	_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
