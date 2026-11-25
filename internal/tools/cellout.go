package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/paul-nameless/qi/internal/jsvm"
	"github.com/paul-nameless/qi/internal/llm"
	"github.com/paul-nameless/qi/internal/strutil"
)

const (
	maxCompletedList = 30
	maxFailedList    = 10
)

type cellReport struct {
	out       strings.Builder
	dropped   int
	records   []llm.InnerCall
	marks     []string
	answers   []answer
	images    []llm.Block
	lostImgs  int
	extras    []string
	late      int
	lateFails []string
	fails     []string
}

func (c *cell) result(end cellEnd, value, hint string) Result {
	var parts []string
	add := func(s string) {
		if s != "" {
			parts = append(parts, s)
		}
	}
	add(c.note)
	add(strings.TrimRight(c.out.String(), "\n"))
	if c.dropped > 0 {
		add(fmt.Sprintf("[%d bytes of console output dropped past %d MB]", c.dropped, maxConsoleBytes>>20))
	}
	failure := c.failure(end)
	switch {
	case failure != "":
		add(failure)
		add(hint)
		c.saveAnswers()
		add(c.completed())
	default:
		add(value)
		add(c.failedNote())
	}
	add(strings.Join(c.extras, "\n"))
	add(c.lateNote(end))
	add(c.lateStopNote(end))
	if c.lostImgs > 0 {
		add(fmt.Sprintf("Note: only the first %d images are attached; %d more were dropped.", maxCellImages, c.lostImgs))
	}
	text := strings.Join(parts, "\n")
	if text == "" {
		text = "(no output)"
	}
	isErr := failure != ""
	return Result{
		Text:    c.env.capText(text, cellOutLines, cellOutBytes),
		IsError: isErr,
		Images:  c.images,
		Summary: c.summary(text, failure),
		Calls:   c.records,
	}
}

func (c *cell) failure(end cellEnd) string {
	switch {
	case end.stopped && c.timedOut.Load():
		return fmt.Sprintf("timed out after %ds", int(c.timeout/c.r.unit()))
	case end.stopped:
		return "interrupted by user"
	case end.err != nil:
		return errText(end.err)
	}
	return ""
}

func errText(err error) string {
	if ex, ok := errors.AsType[*jsvm.Exception](err); ok {
		return ex.Error()
	}
	return err.Error()
}

func (c *cell) completed() string {
	if len(c.marks) == 0 {
		return ""
	}
	marks := c.marks
	head := "Calls that completed:"
	if len(marks) > maxCompletedList {
		head = fmt.Sprintf("Calls that completed (last %d of %d):", maxCompletedList, len(marks))
		cut := len(marks) - maxCompletedList
		marks = append(c.answerMarks(cut), marks[cut:]...)
	}
	return head + "\n" + strings.Join(marks, "\n")
}

func (c *cell) answerMarks(before int) []string {
	var out []string
	for _, a := range c.answers {
		if a.mark < before {
			out = append(out, c.marks[a.mark])
		}
	}
	return out
}

func (c *cell) saveAnswers() {
	for _, a := range c.answers {
		path, err := c.env.Spill(a.text)
		if err != nil {
			c.marks[a.mark] += " → answer not saved: " + err.Error()
			continue
		}
		c.marks[a.mark] += " → answer saved to " + path
	}
}

func (c *cell) lateNote(end cellEnd) string {
	if c.late == 0 {
		return ""
	}
	note := c.lateHead(end)
	if len(c.lateFails) == 0 {
		return note
	}
	return note + " Failed:\n" + strings.Join(c.lateFails, "\n")
}

func (c *cell) lateHead(end cellEnd) string {
	running := fmt.Sprintf("%s %s still running", strutil.Plural(c.late, "call"), pluralVerb(c.late))
	if end.err != nil {
		if end.lateStop {
			return fmt.Sprintf("Note: the code failed while %s.", running)
		}
		return fmt.Sprintf("Note: the code failed while %s; %s waited for.", running, pronounWas(c.late))
	}
	waited := " and " + pluralVerb(c.late) + " waited for"
	if end.lateStop {
		waited = ""
	}
	return fmt.Sprintf("Note: %s when the code finished%s; await every call you start.", running, waited)
}

func pronounWas(n int) string {
	if n == 1 {
		return "it was"
	}
	return "they were"
}

func (c *cell) failedNote() string {
	if len(c.fails) == 0 {
		return ""
	}
	list, more := c.fails, ""
	if len(list) > maxFailedList {
		list, more = list[:maxFailedList], fmt.Sprintf("\nand %d more", len(list)-maxFailedList)
	}
	return "Failed calls:\n" + strings.Join(list, "\n") + more
}

func (c *cell) lateStopNote(end cellEnd) string {
	if !end.lateStop {
		return ""
	}
	stopped := c.failure(cellEnd{stopped: true})
	if end.err != nil {
		return "Note: waiting for the calls still running stopped: " + stopped
	}
	return "Note: the code finished and its result is kept, but waiting for the calls still running stopped: " + stopped
}

func pluralVerb(n int) string {
	if n == 1 {
		return "was"
	}
	return "were"
}

func (c *cell) summary(text, failure string) string {
	s := strutil.Plural(strings.Count(text, "\n")+1, "line")
	if failure != "" {
		s = strings.SplitN(failure, "\n", 2)[0]
	}
	if n := len(c.records); n > 0 {
		s = strutil.Plural(n, "call") + " · " + s
	}
	return s
}

func (c *cell) format(v jsvm.Value) string {
	switch v.Kind() {
	case jsvm.KindUndefined:
		return ""
	case jsvm.KindString:
		return jsvm.ToString(v)
	}
	s, err := c.vm.CallValue(c.r.stringify, c.r.json, v, jsvm.Null(), jsvm.Number(1))
	switch {
	case errors.Is(err, jsvm.ErrInterrupted):
		return fmt.Sprintf("[the value could not be shown: converting it took longer than %ds or was interrupted]", formatSeconds)
	case err == nil && s.Kind() == jsvm.KindString:
		return jsvm.ToString(s)
	}
	str, err := c.vm.ToString(v)
	if err != nil {
		return "[the value could not be shown: " + errText(err) + "]"
	}
	return str
}

var timerNames = []string{"setTimeout", "clearTimeout", "setInterval", "clearInterval", "setImmediate"}

func (c *cell) referenceHint(err error) string {
	var ex *jsvm.Exception
	if !errors.As(err, &ex) || ex.Name != "ReferenceError" {
		return ""
	}
	if name, ok := strings.CutSuffix(ex.Message, " is not defined"); ok && slices.Contains(timerNames, name) {
		return "There are no timers: wait with await sleep(ms)."
	}
	keys, kerr := c.vm.CallValue(c.r.keys, c.r.object, c.r.global)
	if kerr != nil {
		return ""
	}
	raw, kerr := c.vm.JSONStringify(keys)
	var names []string
	if kerr != nil || json.Unmarshal([]byte(raw), &names) != nil {
		return ""
	}
	if len(names) == 0 {
		return "No variables are defined in this VM yet."
	}
	return "Variables defined in this VM: " + strings.Join(names, ", ")
}

type cellClock struct {
	mu        sync.Mutex
	timer     *time.Timer
	remaining time.Duration
	armed     time.Time
	paused    int
	done      bool
	fire      func()
}

func newClock(d time.Duration, fire func()) *cellClock {
	return &cellClock{timer: time.AfterFunc(d, fire), remaining: d, armed: time.Now(), fire: fire}
}

func (k *cellClock) pause() {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.paused++
	if k.paused > 1 || k.done {
		return
	}
	if !k.timer.Stop() {
		k.done = true
		return
	}
	k.remaining -= time.Since(k.armed)
}

func (k *cellClock) resume() {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.paused--
	if k.paused > 0 || k.done {
		return
	}
	k.armed = time.Now()
	k.timer = time.AfterFunc(max(k.remaining, 0), k.fire)
}

func (k *cellClock) stop() {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.done = true
	k.timer.Stop()
}
