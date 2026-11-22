package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

type result struct {
	id, ok, val string
}

type Answer struct {
	OK    bool
	Val   string
	Delay int
}

type Resolver func(name, args string) Answer

type queued struct {
	id   string
	seq  int
	due  int
	resp Answer
}

type Session struct {
	vm      VM
	out     strings.Builder
	done    bool
	doneOK  bool
	doneVal string
	pending int
	calls   int
	names   []string
	results chan result
	tools   func(name string, args []json.RawMessage) (any, error)
	Mode    string
	epoch   int
	Off     int
	Det     Resolver
	queue   []queued
	seq     int
	clock   int
	lastOut string
	lastVal string
}

func NewSession(vm VM) (*Session, error) {
	s := &Session{vm: vm, results: make(chan result, 1024), tools: fakeTools, Mode: "iife"}
	err := vm.Register("__host", func(op, a, b, c string) {
		switch op {
		case "out":
			s.out.WriteString(a + "\n")
		case "done":
			s.done, s.doneOK, s.doneVal = true, a == "1", b
		case "call":
			s.pending++
			s.calls++
			s.names = append(s.names, b)
			if s.Det != nil {
				s.enqueue(a, b, c)
				return
			}
			ep := s.epoch
			go func() {
				var args []json.RawMessage
				_ = json.Unmarshal([]byte(c), &args)
				v, err := s.tools(b, args)
				if err != nil {
					s.results <- result{fmt.Sprint(ep, ":", a), "0", err.Error()}
					return
				}
				j, _ := json.Marshal(v)
				s.results <- result{fmt.Sprint(ep, ":", a), "1", string(j)}
			}()
		}
	})
	if err != nil {
		return nil, err
	}
	if err := vm.Eval(prelude, false); err != nil {
		return nil, fmt.Errorf("prelude: %w", err)
	}
	return s, nil
}

func (s *Session) VM() VM                      { return s.vm }
func (s *Session) Calls() int                  { return s.calls }
func (s *Session) Names() []string             { return s.names }
func (s *Session) ResetNames()                 { s.names = nil }
func (s *Session) LastParts() (string, string) { return s.lastOut, s.lastVal }

func (s *Session) enqueue(id, name, args string) {
	resp := s.Det(name, args)
	s.seq++
	s.queue = append(s.queue, queued{id: id, seq: s.seq, due: s.clock + resp.Delay, resp: resp})
}

func (s *Session) nextQueued() queued {
	sort.SliceStable(s.queue, func(i, j int) bool {
		if s.queue[i].due != s.queue[j].due {
			return s.queue[i].due < s.queue[j].due
		}
		return s.queue[i].seq < s.queue[j].seq
	})
	q := s.queue[0]
	s.queue = s.queue[1:]
	if q.due > s.clock {
		s.clock = q.due
	}
	return q
}

var errTimeout = errors.New("timed out")

func (s *Session) Run(ctx context.Context, code string) (string, error) {
	if s.Mode == "repl" {
		src, async, fallback := s.vm.Prepare(code)
		s.Off = 1
		if async {
			s.Off = 0
		}
		out, err := s.exec(ctx, code, src, async)
		if err != nil && fallback != "" && strings.Contains(err.Error(), "return not in a function") {
			s.Off = 1
			return s.exec(ctx, code, fallback, false)
		}
		return out, err
	}
	s.Off = 0
	src, async := Wrap(code, s.Mode)
	return s.exec(ctx, code, src, async)
}

func (s *Session) exec(ctx context.Context, code, src string, async bool) (string, error) {
	s.out.Reset()
	s.done, s.pending = false, 0
	s.doneVal, s.lastOut, s.lastVal = "", "", ""
	s.queue = nil
	s.epoch++
	stop := make(chan struct{})
	interrupted, finished := false, false
	var imu sync.Mutex
	go func() {
		select {
		case <-ctx.Done():
			imu.Lock()
			if !finished {
				interrupted = true
				s.vm.Interrupt()
			}
			imu.Unlock()
		case <-stop:
		}
	}()
	defer func() {
		imu.Lock()
		finished = true
		imu.Unlock()
		close(stop)
		s.vm.Reset()
		s.lastOut = s.out.String()
	}()
	wasInterrupted := func() bool { imu.Lock(); defer imu.Unlock(); return interrupted }
	if s.Mode == "native" && !s.vm.NativeAsync() {
		if strings.Contains(code, "await") {
			return "", errors.New("top-level await is not supported in a script")
		}
		async = false
	}
	err := s.vm.Eval(src, async)
	if err != nil {
		if wasInterrupted() {
			return s.out.String(), errTimeout
		}
		return s.out.String(), err
	}
	if s.Mode == "native" && !async {
		return s.out.String(), nil
	}
	for {
		if err := s.vm.Drain(); err != nil && !s.done {
			if wasInterrupted() {
				return s.out.String(), errTimeout
			}
			return s.out.String(), err
		}
		if s.done {
			break
		}
		if s.pending == 0 {
			return s.out.String(), errors.New("the script is waiting on a promise that can never settle")
		}
		if s.Det != nil {
			if wasInterrupted() {
				return s.out.String(), errTimeout
			}
			q := s.nextQueued()
			s.pending--
			ok := "0"
			if q.resp.OK {
				ok = "1"
			}
			if err := s.vm.Call("__settle", q.id, ok, q.resp.Val); err != nil {
				if wasInterrupted() {
					return s.out.String(), errTimeout
				}
				return s.out.String(), err
			}
			continue
		}
		select {
		case r := <-s.results:
			ep, id, _ := strings.Cut(r.id, ":")
			if ep != fmt.Sprint(s.epoch) {
				continue
			}
			s.pending--
			if err := s.vm.Call("__settle", id, r.ok, r.val); err != nil {
				if wasInterrupted() {
					return s.out.String(), errTimeout
				}
				return s.out.String(), err
			}
		case <-ctx.Done():
			s.vm.Reset()
			_ = s.vm.Call("__abort", "cancelled")
			_ = s.vm.Drain()
			return s.out.String(), errTimeout
		}
	}
	text := s.out.String() + s.doneVal
	if !s.doneOK {
		return s.out.String(), errors.New(s.doneVal)
	}
	s.lastVal = s.doneVal
	return text, nil
}
