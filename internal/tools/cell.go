package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/paul-nameless/qi/internal/jsvm"
	"github.com/paul-nameless/qi/internal/llm"
)

type callKind int

const (
	toolCall callKind = iota
	sleepCall
	timerCall
)

type pendingCall struct {
	kind      callKind
	resolve   func(jsvm.Value)
	reject    func(jsvm.Value)
	site      string
	fn        jsvm.Value
	args      []jsvm.Value
	stop      chan struct{}
	cancelled bool
	late      bool
}

type outcome struct {
	id     int
	label  string
	res    Result
	extra  string
	record llm.InnerCall
}

type answer struct {
	mark int
	text string
}

type cellEnd struct {
	value    jsvm.Value
	err      error
	stopped  bool
	lateStop bool
}

var errNeverSettles = errors.New("the code is waiting on a promise that can never settle")

type cell struct {
	pathMu    sync.Mutex
	paths     map[string]*sync.Mutex
	r         *runner
	vm        *jsvm.VM
	env       *Env
	parent    context.Context
	ctx       context.Context
	cancel    context.CancelFunc
	timedOut  atomic.Bool
	timeout   time.Duration
	clock     *cellClock
	parentID  string
	ui        UI
	progress  func(string)
	note      string
	outcomes  chan outcome
	pending   map[int]*pendingCall
	wg        sync.WaitGroup
	sem       chan struct{}
	seq       int
	toolCalls int
	subagents int
	cellReport
	timerErr error
	settled  bool
	finished bool
}

func newCell(ctx context.Context, r *runner, env *Env, progress func(string), timeout time.Duration) *cell {
	parentID, ui := callFrom(ctx)
	if progress == nil {
		progress = func(string) {}
	}
	c := &cell{
		r:        r,
		parent:   ctx,
		vm:       r.vm,
		env:      env,
		timeout:  timeout,
		parentID: parentID,
		ui:       ui,
		progress: progress,
		outcomes: make(chan outcome),
		pending:  map[int]*pendingCall{},
		sem:      make(chan struct{}, cellParallel),
	}
	c.ctx, c.cancel = context.WithCancel(ctx)
	c.clock = newClock(timeout, func() {
		c.timedOut.Store(true)
		c.cancel()
	})
	return c
}

func (c *cell) execute(code string) Result {
	stopWatch := c.watch(c.ctx.Done())
	end := c.evaluate(code)
	c.settled, c.finished = true, true
	stopWatch()
	c.clock.stop()
	c.vm.ClearInterrupt()
	if end.stopped || end.lateStop {
		c.abandon()
	}
	value, hint := "", ""
	switch {
	case end.stopped:
	case end.err != nil:
		hint = c.referenceHint(end.err)
	default:
		value = c.formatBounded(end.value, end.lateStop)
	}
	c.cancel()
	c.wg.Wait()
	return c.result(end, value, hint)
}

func (c *cell) formatBounded(v jsvm.Value, lateStop bool) string {
	base := c.parent
	if lateStop {
		base = context.WithoutCancel(c.parent)
	}
	ctx, cancel := context.WithTimeout(base, formatTimeout)
	defer cancel()
	stop := c.watch(ctx.Done())
	defer c.vm.ClearInterrupt()
	defer stop()
	text := c.format(v)
	c.runFormatJobs()
	return text
}

func (c *cell) runFormatJobs() {
	for c.vm.PendingJobs() > 0 {
		if err := c.vm.RunJobs(); errors.Is(err, jsvm.ErrInterrupted) {
			c.vm.AbandonPending()
			return
		}
	}
}

func (c *cell) watch(done <-chan struct{}) func() {
	stop, exited := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(exited)
		select {
		case <-done:
			c.vm.Interrupt()
		case <-stop:
		}
	}()
	return func() {
		close(stop)
		<-exited
	}
}

func (c *cell) evaluate(code string) cellEnd {
	v, err := c.vm.RunCell("cell.js", code)
	if err != nil {
		return c.finishWith(cellEnd{err: err})
	}
	for {
		if c.ctx.Err() != nil {
			return cellEnd{stopped: true}
		}
		if err := c.vm.RunJobs(); err != nil {
			return c.finishWith(cellEnd{err: err})
		}
		switch st, val := c.vm.PromiseResult(v); st {
		case jsvm.PromiseFulfilled:
			return c.finishWith(cellEnd{value: val})
		case jsvm.PromiseRejected:
			return c.finishWith(cellEnd{err: c.vm.Exception(val)})
		}
		if len(c.pending) == 0 && c.vm.PendingJobs() == 0 {
			return cellEnd{err: errNeverSettles}
		}
		if !c.next() {
			return cellEnd{stopped: true}
		}
	}
}

func (c *cell) stoppedBy(err error) bool {
	return errors.Is(err, jsvm.ErrInterrupted) || c.ctx.Err() != nil
}

func (c *cell) finishWith(end cellEnd) cellEnd {
	if end.err != nil && c.stoppedBy(end.err) {
		return cellEnd{stopped: true}
	}
	c.settled = true
	c.cancelTimers()
	if !c.drainJobs() {
		return stopWaiting(end)
	}
	for _, p := range c.pending {
		if p.kind == toolCall {
			p.late = true
			c.late++
		}
	}
	for len(c.pending) > 0 {
		if !c.next() || c.ctx.Err() != nil || !c.drainJobs() {
			return stopWaiting(end)
		}
	}
	return end
}

func stopWaiting(end cellEnd) cellEnd {
	end.lateStop = true
	return end
}

func (c *cell) drainJobs() bool {
	err := c.vm.RunJobs()
	if err == nil {
		return true
	}
	if c.stoppedBy(err) {
		return false
	}
	c.noteTimerErr(err)
	return true
}

func (c *cell) cancelTimers() {
	for _, p := range c.pending {
		if p.kind != toolCall && !p.cancelled {
			p.cancelled = true
			close(p.stop)
		}
	}
}

func (c *cell) next() bool {
	select {
	case o := <-c.outcomes:
		if c.ctx.Err() != nil {
			c.discard(o)
			return false
		}
		c.settle(o)
		return true
	case <-c.ctx.Done():
		return false
	}
}

func (c *cell) abandon() {
	c.cancel()
	for len(c.pending) > 0 {
		c.discard(<-c.outcomes)
	}
	c.wg.Wait()
	c.vm.AbandonPending()
}

func (c *cell) add(p *pendingCall) int {
	c.seq++
	c.pending[c.seq] = p
	return c.seq
}

func (c *cell) admit(t *Tool) error {
	switch {
	case c.finished:
		return errors.New(codeFinished)
	case c.ctx.Err() != nil:
		return errors.New("interrupted by user")
	case c.toolCalls >= maxCellCalls:
		return fmt.Errorf("too many tool calls in one run call (limit %d); split the work across several run calls", maxCellCalls)
	case t.Def.Name == "subagent" && c.subagents >= maxCellSubagents:
		return fmt.Errorf("too many subagent calls in one run call (limit %d)", maxCellSubagents)
	}
	c.toolCalls++
	if t.Def.Name == "subagent" {
		c.subagents++
	}
	return nil
}

func (c *cell) startTool(t *Tool, input json.RawMessage, p *pendingCall) {
	id := c.add(p)
	subagent := t.Def.Name == "subagent"
	if subagent {
		c.clock.pause()
	}
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		o := outcome{id: id}
		defer func() {
			if v := recover(); v != nil {
				o.res = errResult("tool %s panicked: %v", t.Def.Name, v)
			}
			if subagent {
				c.clock.resume()
			}
			c.outcomes <- o
		}()
		o = c.runTool(id, t, input)
	}()
}

func (c *cell) runTool(id int, t *Tool, input json.RawMessage) outcome {
	innerID := fmt.Sprintf("%s.%d", c.parentID, id)
	call := llm.Block{ID: innerID, Name: t.Def.Name, Input: input}
	label := Label(t, c.env, call)
	o := outcome{id: id, label: label}
	unlock := c.lockPath(t, input)
	defer unlock()
	select {
	case c.sem <- struct{}{}:
	case <-c.ctx.Done():
		o.res = errResult("interrupted by user")
		return o
	}
	defer func() { <-c.sem }()
	c.ui.ToolStart(innerID, label)
	start := time.Now()
	o.res, o.extra = c.r.inner.invokeSplit(c.ctx, c.env, t, call, c.ui)
	c.ui.ToolDone(innerID, !o.res.IsError, DoneLine(label, o.res.Summary))
	o.record = llm.InnerCall{Tool: t.Def.Name, Label: label, OK: !o.res.IsError, Summary: o.res.Summary, Ms: time.Since(start).Milliseconds(), Input: recordInput(input)}
	return o
}

func (c *cell) lockPath(t *Tool, input json.RawMessage) func() {
	if t.Path == nil {
		return func() {}
	}
	p := t.Path(c.env, input)
	if p == "" {
		return func() {}
	}
	c.pathMu.Lock()
	if c.paths == nil {
		c.paths = map[string]*sync.Mutex{}
	}
	m := c.paths[p]
	if m == nil {
		m = &sync.Mutex{}
		c.paths[p] = m
	}
	c.pathMu.Unlock()
	m.Lock()
	return m.Unlock
}

func (c *cell) startTimer(p *pendingCall, d time.Duration) int {
	id := c.add(p)
	stop := make(chan struct{})
	p.stop = stop
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-t.C:
		case <-stop:
		case <-c.ctx.Done():
		}
		c.outcomes <- outcome{id: id}
	}()
	return id
}

func (c *cell) clearTimer(id int) {
	p := c.pending[id]
	if p == nil || p.kind != timerCall || p.cancelled {
		return
	}
	p.cancelled = true
	close(p.stop)
}

func (c *cell) settle(o outcome) {
	p := c.pending[o.id]
	delete(c.pending, o.id)
	switch p.kind {
	case sleepCall:
		if !p.cancelled {
			p.resolve(jsvm.Undefined())
		}
	case timerCall:
		if !p.cancelled {
			if _, err := c.vm.CallValue(p.fn, jsvm.Undefined(), p.args...); err != nil && !c.stoppedBy(err) {
				c.noteTimerErr(err)
			}
		}
	default:
		c.record(o, p)
		c.collect(o)
		if rejects(o.res) {
			p.reject(toolError(c.vm, o.res.Text, o.res.Denied, p.site))
			return
		}
		p.resolve(c.jsValue(o.res))
	}
}

func (c *cell) discard(o outcome) {
	p := c.pending[o.id]
	delete(c.pending, o.id)
	if p.kind == toolCall && o.record.Tool != "" {
		c.record(o, p)
		if o.extra != "" {
			c.extras = append(c.extras, o.extra)
		}
	}
}

func (c *cell) record(o outcome, p *pendingCall) {
	if o.record.Tool == "" {
		return
	}
	c.records = append(c.records, o.record)
	mark := "✓ "
	if o.res.IsError {
		mark = "✗ "
	}
	if !rejects(o.res) {
		c.marks = append(c.marks, mark+o.label)
		c.keepAnswer(o)
		return
	}
	line := o.label + ": " + OneLine(o.res.Text, 200)
	if p.late {
		c.lateFails = append(c.lateFails, line)
		return
	}
	c.marks = append(c.marks, mark+o.label)
	c.fails = append(c.fails, mark+line)
}

func (c *cell) keepAnswer(o outcome) {
	if o.record.Tool == "subagent" && !o.res.IsError {
		c.answers = append(c.answers, answer{mark: len(c.marks) - 1, text: o.res.Text})
	}
}

func rejects(res Result) bool {
	return res.IsError && res.Value == nil && res.JS == nil
}

func (c *cell) collect(o outcome) {
	if o.extra != "" {
		c.extras = append(c.extras, o.extra)
	}
	for _, img := range o.res.Images {
		if len(c.images) >= maxCellImages {
			c.lostImgs++
			continue
		}
		c.images = append(c.images, img)
	}
}

func (c *cell) jsValue(res Result) jsvm.Value {
	if res.JS != nil {
		return res.JS(c.vm)
	}
	if res.Value != nil {
		if v, err := c.vm.JSONParse(string(res.Value)); err == nil {
			return v
		}
	}
	return jsvm.Str(res.Text)
}

func (c *cell) noteTimerErr(err error) {
	if c.timerErr == nil {
		c.timerErr = err
	}
}

func (c *cell) print(text string) {
	if c.out.Len() >= maxConsoleBytes {
		c.dropped += len(text) + 1
		return
	}
	c.out.WriteString(text)
	c.out.WriteByte('\n')
	c.progress(text)
}
