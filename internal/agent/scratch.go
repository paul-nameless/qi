package agent

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

func ScratchDir(sessionID string) string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("qi-%d", os.Getuid()), sessionID)
}

func ensureScratch(dir string) string {
	if dir == "" {
		return ""
	}
	for _, d := range []string{filepath.Dir(dir), dir} {
		if err := os.Mkdir(d, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return ""
		}
		if !ownPrivateDir(d) {
			return ""
		}
	}
	return dir
}

func ownPrivateDir(path string) bool {
	st, err := os.Lstat(path)
	if err != nil || !st.IsDir() || st.Mode().Perm() != 0o700 {
		return false
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	return ok && int(sys.Uid) == os.Getuid()
}

func RemoveScratchIfEmpty(sessionID string) {
	_ = os.Remove(ScratchDir(sessionID))
}
