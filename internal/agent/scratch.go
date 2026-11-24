package agent

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/paul-nameless/qi/internal/tools"
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
		if !tools.PrivateDir(d) {
			return ""
		}
	}
	return dir
}

func RemoveScratchIfEmpty(sessionID string) {
	_ = os.Remove(ScratchDir(sessionID))
}
