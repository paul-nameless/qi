package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/paul-nameless/qi/internal/jsvm"
)

const (
	maxCompletedList = 30
	maxFailedList    = 10
)

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
		add(c.completed())
	default:
		add(value)
		add(c.failedNote())
	}
	if c.timerErr != nil {
		add("Uncaught error in a callback: " + errText(c.timerErr))
	}
	add(strings.Join(c.extras, "\n"))
	add(c.lateNote(end.lateStop))
	add(c.lateStopNote(end))
	if c.lostImgs > 0 {
		add(fmt.Sprintf("Note: only the first %d images are attached; %d more were dropped.", maxCellImages, c.lostImgs))
	}
	text := strings.Join(parts, "\n")
	if text == "" {
		text = "(no output)"
	}
	isErr := failure != "" || c.timerErr != nil
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
		return fmt.Sprintf("timed out after %ds", int(c.timeout/time.Second))
	case end.stopped:
		return "interrupted by user"
	case end.err != nil:
		return errText(end.err)
	}
	return ""
}

func errText(err error) string {
	var ex *jsvm.Exception
	if errors.As(err, &ex) {
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
		marks = marks[len(marks)-maxCompletedList:]
	}
	return head + "\n" + strings.Join(marks, "\n")
}

func (c *cell) lateNote(stopped bool) string {
	if c.late == 0 {
		return ""
	}
	waited := " and " + pluralVerb(c.late) + " waited for"
	if stopped {
		waited = ""
	}
	note := fmt.Sprintf("Note: %s %s still running when the code finished%s; await every call you start.", plural(c.late, "call"), pluralVerb(c.late), waited)
	if len(c.lateFails) == 0 {
		return note
	}
	return note + " Failed:\n" + strings.Join(c.lateFails, "\n")
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
	return "Note: the code finished and its result is kept, but waiting for the calls still running stopped: " + c.failure(cellEnd{stopped: true})
}

func pluralVerb(n int) string {
	if n == 1 {
		return "was"
	}
	return "were"
}

func (c *cell) summary(text, failure string) string {
	s := plural(strings.Count(text, "\n")+1, "line")
	if failure != "" {
		s = strings.SplitN(failure, "\n", 2)[0]
	}
	if n := len(c.records); n > 0 {
		s = plural(n, "call") + " · " + s
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
		return fmt.Sprintf("[the value could not be shown: converting it took longer than %ds or was interrupted]", int(formatTimeout/time.Second))
	case err == nil && s.Kind() == jsvm.KindString:
		return jsvm.ToString(s)
	}
	str, err := c.vm.ToString(v)
	if err != nil {
		return "[the value could not be shown: " + errText(err) + "]"
	}
	return str
}

func (c *cell) referenceHint(err error) string {
	var ex *jsvm.Exception
	if !errors.As(err, &ex) || ex.Name != "ReferenceError" {
		return ""
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
