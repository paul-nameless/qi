package mcp

import (
	"strings"
	"sync"
)

const (
	tailBytes = 4096
	tailLines = 5
)

type tailWriter struct {
	mu  sync.Mutex
	buf []byte
}

func (w *tailWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	if over := len(w.buf) - tailBytes; over > 0 {
		w.buf = append(w.buf[:0], w.buf[over:]...)
	}
	return len(p), nil
}

func (w *tailWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	lines := strings.Split(strings.TrimSpace(strings.ToValidUTF8(string(w.buf), "")), "\n")
	if len(lines) > tailLines {
		lines = lines[len(lines)-tailLines:]
	}
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	return strings.TrimSpace(strings.Join(lines, " | "))
}
