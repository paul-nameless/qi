package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/paul-nameless/aicode/internal/config"
)

const protocolVersion = "2025-06-18"

type Tool struct {
	Server      string          `json:"server"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type Content struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	Data     string `json:"data"`
	MimeType string `json:"mimeType"`
}

type CallResult struct {
	Content []Content `json:"content"`
	IsError bool      `json:"isError"`
}

type rpcMsg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  any             `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type client struct {
	name    string
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	mu      sync.Mutex
	writeMu sync.Mutex
	nextID  int64
	pending map[int64]chan rpcMsg
	done    chan struct{}
	tools   []Tool
	hidden  int
}

func start(name string, srv config.MCPServer) (*client, error) {
	cmd := exec.Command(srv.Command, srv.Args...)
	cmd.Env = os.Environ()
	for k, v := range srv.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stderr = io.Discard
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start mcp server %s: %w", name, err)
	}
	c := &client{name: name, cmd: cmd, stdin: stdin, pending: map[int64]chan rpcMsg{}, done: make(chan struct{})}
	go c.readLoop(stdout)
	return c, nil
}

func (c *client) readLoop(r io.Reader) {
	defer close(c.done)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 64<<20)
	for sc.Scan() {
		var m rpcMsg
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		if m.ID != nil && m.Method != "" {
			_ = c.send(rpcMsg{JSONRPC: "2.0", ID: m.ID, Error: &rpcError{Code: -32601, Message: "method not supported by aicode"}})
			continue
		}
		if m.ID == nil {
			continue
		}
		c.mu.Lock()
		ch := c.pending[*m.ID]
		delete(c.pending, *m.ID)
		c.mu.Unlock()
		if ch != nil {
			ch <- m
		}
	}
}

func (c *client) send(m rpcMsg) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = c.stdin.Write(append(data, '\n'))
	return err
}

func (c *client) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	ch := make(chan rpcMsg, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()
	if err := c.send(rpcMsg{JSONRPC: "2.0", ID: &id, Method: method, Params: params}); err != nil {
		return nil, fmt.Errorf("mcp %s: %w", c.name, err)
	}
	select {
	case m := <-ch:
		if m.Error != nil {
			return nil, fmt.Errorf("mcp %s: %s", c.name, m.Error.Message)
		}
		return m.Result, nil
	case <-c.done:
		return nil, fmt.Errorf("mcp server %s exited", c.name)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *client) initialize(ctx context.Context) error {
	params := map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "aicode", "version": "2"},
	}
	if _, err := c.request(ctx, "initialize", params); err != nil {
		return err
	}
	if err := c.send(rpcMsg{JSONRPC: "2.0", Method: "notifications/initialized"}); err != nil {
		return err
	}
	cursor := ""
	for {
		p := map[string]any{}
		if cursor != "" {
			p["cursor"] = cursor
		}
		raw, err := c.request(ctx, "tools/list", p)
		if err != nil {
			return err
		}
		var res struct {
			Tools      []Tool `json:"tools"`
			NextCursor string `json:"nextCursor"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			return fmt.Errorf("mcp %s: bad tools/list: %w", c.name, err)
		}
		for _, t := range res.Tools {
			t.Server = c.name
			c.tools = append(c.tools, t)
		}
		if res.NextCursor == "" {
			return nil
		}
		cursor = res.NextCursor
	}
}

func (c *client) filter(srv config.MCPServer) {
	var kept []Tool
	for _, t := range c.tools {
		if srv.ToolEnabled(t.Name) {
			kept = append(kept, t)
		} else {
			c.hidden++
		}
	}
	c.tools = kept
}

func (c *client) has(tool string) bool {
	return slices.ContainsFunc(c.tools, func(t Tool) bool { return t.Name == tool })
}

func (c *client) toolNames() []string {
	names := make([]string, len(c.tools))
	for i, t := range c.tools {
		names[i] = t.Name
	}
	return names
}

func (c *client) close() {
	c.stdin.Close()
	select {
	case <-c.done:
	case <-time.After(500 * time.Millisecond):
	}
	if c.cmd.Process != nil {
		_ = syscall.Kill(-c.cmd.Process.Pid, syscall.SIGKILL)
	}
	_ = c.cmd.Wait()
}

type Manager struct {
	servers map[string]config.MCPServer
	mu      sync.Mutex
	clients map[string]*client
	errs    map[string]error
}

func NewManager(servers map[string]config.MCPServer) *Manager {
	return &Manager{servers: servers, clients: map[string]*client{}, errs: map[string]error{}}
}

func (m *Manager) Names() []string {
	var names []string
	for n := range m.servers {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func (m *Manager) Description(name string) string {
	return m.servers[name].Description
}

func (m *Manager) server(name string) (config.MCPServer, error) {
	srv, ok := m.servers[name]
	if !ok {
		return srv, fmt.Errorf("unknown mcp server %q (servers: %s)", name, strings.Join(m.Names(), ", "))
	}
	return srv, nil
}

func (m *Manager) get(ctx context.Context, name string) (*client, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.clients[name]; ok {
		select {
		case <-c.done:
			c.close()
			delete(m.clients, name)
		default:
			return c, nil
		}
	}
	if err, ok := m.errs[name]; ok {
		return nil, err
	}
	srv, err := m.server(name)
	if err != nil {
		return nil, err
	}
	c, err := start(name, srv)
	if err == nil {
		ictx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err = c.initialize(ictx)
		cancel()
		if err != nil {
			c.close()
		}
	}
	if err != nil {
		if ctx.Err() == nil && !errors.Is(err, context.DeadlineExceeded) {
			m.errs[name] = err
		}
		return nil, err
	}
	c.filter(srv)
	m.clients[name] = c
	return c, nil
}

type Listing struct {
	Tools  []Tool
	Hidden map[string]int
	Errs   []error
}

func (l *Listing) add(c *client, query string) {
	l.Tools = append(l.Tools, rank(c.tools, query)...)
	if c.hidden > 0 {
		l.Hidden[c.name] = c.hidden
	}
}

func (m *Manager) Search(ctx context.Context, query string) Listing {
	l := Listing{Hidden: map[string]int{}}
	for _, name := range m.Names() {
		c, err := m.get(ctx, name)
		if err != nil {
			l.Errs = append(l.Errs, err)
			continue
		}
		l.add(c, query)
	}
	l.Tools = rank(l.Tools, query)
	return l
}

func (m *Manager) ServerTools(ctx context.Context, server, query string) (Listing, error) {
	c, err := m.get(ctx, server)
	if err != nil {
		return Listing{}, err
	}
	l := Listing{Hidden: map[string]int{}}
	l.add(c, query)
	return l, nil
}

func rank(tools []Tool, query string) []Tool {
	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 {
		return slices.Clone(tools)
	}
	type scored struct {
		t     Tool
		score int
	}
	var out []scored
	for _, t := range tools {
		if score := toolScore(t, words); score > 0 {
			out = append(out, scored{t, score})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].score > out[j].score })
	res := make([]Tool, len(out))
	for i, s := range out {
		res[i] = s.t
	}
	return res
}

func toolScore(t Tool, words []string) int {
	name := strings.ToLower(t.Server + " " + t.Name)
	desc := strings.ToLower(t.Description)
	score := 0
	for _, w := range words {
		if strings.Contains(name, w) {
			score += 3
		}
		if strings.Contains(desc, w) {
			score++
		}
	}
	return score
}

func (m *Manager) Call(ctx context.Context, server, tool string, args json.RawMessage) (CallResult, error) {
	srv, err := m.server(server)
	if err != nil {
		return CallResult{}, err
	}
	if !srv.ToolEnabled(tool) {
		return CallResult{}, fmt.Errorf("tool %q on server %q is disabled by the user; don't try to work around this", tool, server)
	}
	c, err := m.get(ctx, server)
	if err != nil {
		return CallResult{}, err
	}
	if !c.has(tool) {
		return CallResult{}, fmt.Errorf("unknown tool %q on server %q; use tool_search with server %q to list its tools", tool, server, server)
	}
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	raw, err := c.request(ctx, "tools/call", map[string]any{"name": tool, "arguments": args})
	if err != nil {
		return CallResult{}, err
	}
	var res CallResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return CallResult{}, errors.New("mcp: bad tools/call result")
	}
	return res, nil
}

type State int

const (
	NotStarted State = iota
	Started
	Failed
)

func (s State) String() string {
	return [...]string{"not started", "started", "failed"}[s]
}

type Status struct {
	Name   string
	State  State
	Err    error
	Tools  []string
	Hidden int
}

func (m *Manager) Start(ctx context.Context, name string) error {
	m.mu.Lock()
	delete(m.errs, name)
	m.mu.Unlock()
	_, err := m.get(ctx, name)
	return err
}

func (m *Manager) Status() []Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Status
	for _, name := range m.Names() {
		out = append(out, m.status(name))
	}
	return out
}

func (m *Manager) status(name string) Status {
	if err, ok := m.errs[name]; ok {
		return Status{Name: name, State: Failed, Err: err}
	}
	c, ok := m.clients[name]
	if !ok {
		return Status{Name: name, State: NotStarted}
	}
	return Status{Name: name, State: Started, Tools: c.toolNames(), Hidden: c.hidden}
}

func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.clients {
		c.close()
	}
	m.clients = map[string]*client{}
}
