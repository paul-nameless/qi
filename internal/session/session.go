package session

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/paul-nameless/aicode/internal/config"
	"github.com/paul-nameless/aicode/internal/llm"
)

const Version = 1

type Entry struct {
	Type    string       `json:"type"`
	ID      string       `json:"id"`
	Parent  string       `json:"parent,omitempty"`
	Time    string       `json:"time"`
	Version int          `json:"version,omitempty"`
	Cwd     string       `json:"cwd,omitempty"`
	Model   string       `json:"model,omitempty"`
	Message *llm.Message `json:"message,omitempty"`
	Usage   *llm.Usage   `json:"usage,omitempty"`
	Summary string       `json:"summary,omitempty"`
}

type Session struct {
	Path string
	ID   string
	mu   sync.Mutex
	f    *os.File
	last string
}

func Dir(cwd string) string {
	base := os.Getenv("AICODE_SESSION_DIR")
	if base == "" {
		base = filepath.Join(config.Home(), ".local", "share", "aicode", "sessions")
	}
	name := strings.Trim(strings.NewReplacer("/", "-", "\\", "-", ":", "").Replace(cwd), "-")
	if name == "" {
		name = "root"
	}
	return filepath.Join(base, name)
}

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func Create(cwd, model string, subagent bool) (*Session, error) {
	dir := Dir(cwd)
	if subagent {
		dir = filepath.Join(dir, "subagents")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create session dir: %w", err)
	}
	id := time.Now().Format("20060102-150405") + "-" + newID()[:4]
	path := filepath.Join(dir, id+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create session file: %w", err)
	}
	s := &Session{Path: path, ID: id, f: f}
	if err := s.write(Entry{Type: "meta", Version: Version, Cwd: cwd, Model: model}); err != nil {
		f.Close()
		return nil, err
	}
	return s, nil
}

func Latest(cwd string) (string, error) {
	dir := Dir(cwd)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("no sessions for %s", cwd)
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".jsonl") {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return "", fmt.Errorf("no sessions for %s", cwd)
	}
	sort.Strings(names)
	return filepath.Join(dir, names[len(names)-1]), nil
}

type Loaded struct {
	Messages  []llm.Message
	LastID    string
	CtxTokens int
}

func Open(path string) (*Session, Loaded, error) {
	var ld Loaded
	f, err := os.Open(path)
	if err != nil {
		return nil, ld, fmt.Errorf("open session: %w", err)
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 256<<20)
	for sc.Scan() {
		var e Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			continue
		}
		ld.LastID = e.ID
		switch e.Type {
		case "message":
			if e.Message != nil {
				ld.Messages = append(ld.Messages, *e.Message)
			}
			if e.Usage != nil {
				ld.CtxTokens = e.Usage.Input + e.Usage.Output
			}
		case "compaction":
			ld.Messages = []llm.Message{llm.UserText(e.Summary)}
			ld.CtxTokens = len(e.Summary) / 4
		}
	}
	f.Close()
	if err := sc.Err(); err != nil {
		return nil, ld, fmt.Errorf("read session: %w", err)
	}
	ld.Messages = Repair(ld.Messages)
	wf, err := os.OpenFile(path, os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return nil, ld, fmt.Errorf("open session for append: %w", err)
	}
	if err := endWithNewline(wf); err != nil {
		wf.Close()
		return nil, ld, err
	}
	id := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	return &Session{Path: path, ID: id, f: wf, last: ld.LastID}, ld, nil
}

func Repair(msgs []llm.Message) []llm.Message {
	var out []llm.Message
	for i := 0; i < len(msgs); i++ {
		m := msgs[i]
		out = append(out, m)
		calls := m.ToolUses()
		if m.Role != "assistant" || len(calls) == 0 {
			continue
		}
		answered := map[string]bool{}
		var next *llm.Message
		if i+1 < len(msgs) && msgs[i+1].Role == "user" {
			next = &msgs[i+1]
			for _, b := range next.Blocks {
				if b.Type == llm.BlockToolResult {
					answered[b.ToolUseID] = true
				}
			}
		}
		var missing []llm.Block
		for _, c := range calls {
			if !answered[c.ID] {
				missing = append(missing, llm.Block{Type: llm.BlockToolResult, ToolUseID: c.ID, Text: "interrupted: aicode stopped before this tool finished", IsError: true})
			}
		}
		if len(missing) == 0 {
			continue
		}
		if next != nil {
			fixed := llm.Message{Role: "user", Blocks: append(missing, next.Blocks...)}
			out = append(out, fixed)
			i++
			continue
		}
		out = append(out, llm.Message{Role: "user", Blocks: missing})
	}
	return out
}

func endWithNewline(f *os.File) error {
	st, err := f.Stat()
	if err != nil || st.Size() == 0 {
		return err
	}
	last := make([]byte, 1)
	if _, err := f.ReadAt(last, st.Size()-1); err != nil {
		return fmt.Errorf("read session: %w", err)
	}
	if last[0] != '\n' {
		if _, err := f.Write([]byte("\n")); err != nil {
			return fmt.Errorf("repair session: %w", err)
		}
	}
	return nil
}

func (s *Session) write(e Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f == nil {
		return errors.New("session closed")
	}
	e.ID = newID()
	e.Parent = s.last
	e.Time = time.Now().UTC().Format(time.RFC3339)
	data, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("encode session entry: %w", err)
	}
	if _, err := s.f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write session: %w", err)
	}
	s.last = e.ID
	return nil
}

func (s *Session) Message(m llm.Message, model string, u *llm.Usage) error {
	return s.write(Entry{Type: "message", Message: &m, Model: model, Usage: u})
}

func (s *Session) Compaction(summary, model string) error {
	return s.write(Entry{Type: "compaction", Summary: summary, Model: model})
}

func (s *Session) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f != nil {
		s.f.Close()
		s.f = nil
	}
}
