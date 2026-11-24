package session

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/paul-nameless/qi/internal/config"
	"github.com/paul-nameless/qi/internal/llm"
)

const Version = 1

type Entry struct {
	Type          string        `json:"type"`
	ID            string        `json:"id"`
	Parent        string        `json:"parent,omitempty"`
	Time          string        `json:"time"`
	Version       int           `json:"version,omitempty"`
	Cwd           string        `json:"cwd,omitempty"`
	Kind          Kind          `json:"kind,omitempty"`
	ParentSession string        `json:"parent_session,omitempty"`
	Model         string        `json:"model,omitempty"`
	Message       *llm.Message  `json:"message,omitempty"`
	Usage         *llm.Usage    `json:"usage,omitempty"`
	Summary       string        `json:"summary,omitempty"`
	Kept          []llm.Message `json:"kept,omitempty"`
}

type Session struct {
	Path string
	ID   string
	mu   sync.Mutex
	f    *os.File
	tee  io.Writer
	last string
}

func Dir(cwd string) string {
	base := os.Getenv("QI_SESSION_DIR")
	if base == "" {
		base = filepath.Join(config.DataDir(), "sessions")
	}
	name := strings.NewReplacer("/", "-", "\\", "-", ":", "", ".", "-").Replace(cwd)
	dir := filepath.Join(base, name)
	old := filepath.Join(base, strings.Trim(strings.NewReplacer("/", "-", "\\", "-", ":", "").Replace(cwd), "-"))
	migrate(old, dir)
	return dir
}

func migrate(old, dir string) {
	if old == dir {
		return
	}
	if _, err := os.Stat(dir); err == nil {
		return
	}
	if _, err := os.Stat(old); err == nil {
		_ = os.Rename(old, dir)
	}
}

func NewID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := strings.ToUpper(hex.EncodeToString(b))
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

func ValidID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !(r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return false
		}
	}
	return true
}

func PathFor(cwd, id string) string {
	return filepath.Join(Dir(cwd), id+".jsonl")
}

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

type Kind string

const (
	Interactive Kind = "interactive"
	Headless    Kind = "headless"
)

type CreateOptions struct {
	Cwd           string
	ID            string
	Model         string
	Kind          Kind
	Subagent      bool
	ParentSession string
	Tee           io.Writer
}

func Create(o CreateOptions) (*Session, error) {
	dir := Dir(o.Cwd)
	if o.Subagent {
		dir = filepath.Join(dir, "subagents")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create session dir: %w", err)
	}
	id := o.ID
	if id == "" {
		id = NewID()
	}
	path := filepath.Join(dir, id+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create session file: %w", err)
	}
	if err := lock(f, path, id); err != nil {
		f.Close()
		return nil, err
	}
	s := &Session{Path: path, ID: id, f: f, tee: o.Tee}
	if err := s.write(Entry{Type: "meta", Version: Version, Cwd: o.Cwd, Model: o.Model, Kind: o.Kind, ParentSession: o.ParentSession}); err != nil {
		f.Close()
		return nil, err
	}
	return s, nil
}

type file struct {
	path string
	mod  time.Time
}

func sessionFiles(dir string) ([]file, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []file
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		if info, err := e.Info(); err == nil {
			files = append(files, file{filepath.Join(dir, e.Name()), info.ModTime()})
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	return files, nil
}

func Latest(cwd string, interactive bool) (string, error) {
	files, err := sessionFiles(Dir(cwd))
	if err != nil {
		return "", fmt.Errorf("no sessions for %s", cwd)
	}
	for _, f := range files {
		if hasMessages(f.path, interactive) {
			return f.path, nil
		}
	}
	return "", fmt.Errorf("no sessions with messages for %s", cwd)
}

func headlessMeta(line string) bool {
	return strings.HasPrefix(line, `{"type":"meta"`) && strings.Contains(line, `"kind":"headless"`)
}

func hasMessages(path string, interactive bool) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 256<<20)
	for sc.Scan() {
		line := sc.Text()
		if interactive && headlessMeta(line) {
			return false
		}
		if strings.Contains(line, `"role":"assistant"`) || strings.Contains(line, `"type":"compaction"`) {
			return true
		}
	}
	return false
}

type Loaded struct {
	Messages  []llm.Message
	LastID    string
	CtxTokens int
}

func Entries(path string) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open session: %w", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 256<<20)
	var out []Entry
	for sc.Scan() {
		var e Entry
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			out = append(out, e)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read session: %w", err)
	}
	return out, nil
}

func Load(path string) (Loaded, error) {
	var ld Loaded
	entries, err := Entries(path)
	if err != nil {
		return ld, err
	}
	for _, e := range entries {
		ld.LastID = e.ID
		switch e.Type {
		case "message":
			if e.Message != nil {
				ld.Messages = append(ld.Messages, *e.Message)
			}
			if e.Usage != nil {
				ld.CtxTokens = e.Usage.Input + e.Usage.Output
			}
		case "drop":
			if n := len(ld.Messages); n > 0 {
				ld.Messages = ld.Messages[:n-1]
			}
		case "compaction":
			ld.Messages = append([]llm.Message{llm.UserText(e.Summary)}, e.Kept...)
			ld.CtxTokens = 0
		}
	}
	ld.Messages = Repair(ld.Messages)
	return ld, nil
}

func Open(path string) (*Session, Loaded, error) {
	ld, err := Load(path)
	if err != nil {
		return nil, ld, err
	}
	wf, err := os.OpenFile(path, os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return nil, ld, fmt.Errorf("open session for append: %w", err)
	}
	id := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	if err := lock(wf, path, id); err != nil {
		wf.Close()
		return nil, ld, err
	}
	if err := endWithNewline(wf); err != nil {
		unlock(wf, path)
		wf.Close()
		return nil, ld, err
	}
	return &Session{Path: path, ID: id, f: wf, last: ld.LastID}, ld, nil
}

func Repair(msgs []llm.Message) []llm.Message {
	var out []llm.Message
	for i := 0; i < len(msgs); i++ {
		m := msgs[i]
		out = append(out, m)
		calls := m.ToolUses()
		if m.Role != llm.RoleAssistant || len(calls) == 0 {
			continue
		}
		answered := map[string]bool{}
		var next *llm.Message
		if i+1 < len(msgs) && msgs[i+1].Role == llm.RoleUser {
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
				missing = append(missing, llm.Block{Type: llm.BlockToolResult, ToolUseID: c.ID, Text: "interrupted: qi stopped before this tool finished", IsError: true})
			}
		}
		if len(missing) == 0 {
			continue
		}
		if next != nil {
			fixed := llm.Message{Role: llm.RoleUser, Blocks: append(missing, next.Blocks...)}
			out = append(out, fixed)
			i++
			continue
		}
		out = append(out, llm.Message{Role: llm.RoleUser, Blocks: missing})
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
	line := append(data, '\n')
	if _, err := s.f.Write(line); err != nil {
		return fmt.Errorf("write session: %w", err)
	}
	s.last = e.ID
	if s.tee != nil {
		if _, err := s.tee.Write(line); err != nil {
			return fmt.Errorf("write session to stdout: %w", err)
		}
	}
	return nil
}

func (s *Session) Tee(w io.Writer) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, err := firstLine(s.Path)
	if err != nil {
		return err
	}
	if strings.HasPrefix(meta, `{"type":"meta"`) {
		if _, err := io.WriteString(w, meta+"\n"); err != nil {
			return fmt.Errorf("write session to stdout: %w", err)
		}
	}
	s.tee = w
	return nil
}

func firstLine(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open session: %w", err)
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 64<<10)
	line, err := r.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("read session: %w", err)
	}
	return strings.TrimSuffix(line, "\n"), nil
}

func (s *Session) Message(m llm.Message, model string, u *llm.Usage) error {
	return s.write(Entry{Type: "message", Message: &m, Model: model, Usage: u})
}

func (s *Session) Drop() error {
	return s.write(Entry{Type: "drop"})
}

func (s *Session) Compaction(summary string, kept []llm.Message, model string, u *llm.Usage) error {
	return s.write(Entry{Type: "compaction", Summary: summary, Kept: kept, Model: model, Usage: u})
}

func (s *Session) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f != nil {
		unlock(s.f, s.Path)
		s.f.Close()
		s.f = nil
	}
}
