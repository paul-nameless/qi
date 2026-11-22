package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/paul-nameless/qi/internal/jsvm/bench/harness"
)

type runOut struct {
	Recs   []harness.Rec
	Stdout string
	Err    string
}

func (r runOut) get(name string) (harness.Rec, bool) {
	for _, x := range r.Recs {
		if x.Name == name {
			return x, true
		}
	}
	return harness.Rec{}, false
}

func runJSON(bin string, timeout time.Duration, args ...string) runOut {
	cmd := exec.Command(bin, append([]string{"-json"}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		return runOut{Err: err.Error()}
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var werr error
	select {
	case werr = <-done:
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		<-done
		werr = fmt.Errorf("killed after %s", timeout)
	}
	out := runOut{Stdout: stdout.String()}
	for _, l := range strings.Split(stdout.String(), "\n") {
		if strings.HasPrefix(l, "@@") {
			var r harness.Rec
			if err := json.Unmarshal([]byte(l[2:]), &r); err != nil {
				out.Err = "bad json: " + err.Error()
				continue
			}
			out.Recs = append(out.Recs, r)
		}
	}
	if werr != nil {
		out.Err = strings.TrimSpace(werr.Error() + ": " + lastLines(stdout.String()+stderr.String(), 3))
	}
	return out
}

func lastLines(s string, n int) string {
	ls := strings.Split(strings.TrimSpace(s), "\n")
	if len(ls) > n {
		ls = ls[len(ls)-n:]
	}
	return strings.Join(ls, " | ")
}
