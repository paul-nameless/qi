package tools

import (
	"fmt"
	"os"
	"sync"
	"time"
)

type fileTimes struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

func (f *fileTimes) remember(path string) {
	st, err := os.Stat(path)
	if err != nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.seen == nil {
		f.seen = map[string]time.Time{}
	}
	f.seen[path] = st.ModTime()
}

func (f *fileTimes) check(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if t, ok := f.seen[path]; ok && !st.ModTime().Equal(t) {
		return fmt.Errorf("%s changed on disk since you last read or wrote it (by the user, a formatter or a command); read it again before changing it", path)
	}
	return nil
}
