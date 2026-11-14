package tools

import (
	"errors"
	"os"
	"os/exec"
	"strings"
)

func (e *Env) diff(path, old string) string {
	if e.OnDiff == nil {
		return ""
	}
	f, err := os.CreateTemp("", "aicode-old-*")
	if err != nil {
		return ""
	}
	defer os.Remove(f.Name())
	_, err = f.WriteString(old)
	if cerr := f.Close(); err != nil || cerr != nil {
		return ""
	}
	rel := e.rel(path)
	out, err := exec.Command("diff", "-u", "-L", "a/"+rel, "-L", "b/"+rel, f.Name(), path).Output()
	var exit *exec.ExitError
	if err != nil && !(errors.As(err, &exit) && exit.ExitCode() == 1) {
		return ""
	}
	return cleanDiff(string(out))
}

func cleanDiff(s string) string {
	return strings.Map(func(r rune) rune {
		if (r < 0x20 && r != '\t' && r != '\n') || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return -1
		}
		return r
	}, s)
}
