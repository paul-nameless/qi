package agent

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/paul-nameless/qi/internal/config"
	"github.com/paul-nameless/qi/internal/llm"
)

const (
	sseStart     = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":1}}}\n\n"
	sseText      = "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n"
	sseThinking  = "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"hmm\"}}\n\n"
	sseEmptyText = "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"\"}}\n\n"
	sseStop      = "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	sseOverload  = "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"busy\"}}\n\n"
	sseTooLong   = "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"invalid_request_error\",\"message\":\"prompt is too long: 300000 tokens > 200000 maximum\"}}\n\n"
	statusPrefix = "status:"
)

func scriptedServer(t *testing.T, script func(n int) string) (string, *atomic.Int32) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply := script(int(n.Add(1)))
		var status int
		if _, err := fmt.Sscanf(reply, statusPrefix+"%d", &status); err == nil {
			w.WriteHeader(status)
			fmt.Fprint(w, `{"type":"error","error":{"type":"overloaded_error","message":"busy"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, reply)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, &n
}

func TestStreamRetryAttempts(t *testing.T) {
	cases := []struct {
		name     string
		hidden   bool
		headless bool
		script   func(n int) string
		requests int32
		sleeps   int
		check    func(error) bool
	}{
		{"persistent 529 is retried by the request layer only", false, false, func(int) string { return "status:529" }, 4, 3, func(err error) bool {
			var apiErr *llm.APIError
			return errors.As(err, &apiErr) && apiErr.Status == 529
		}},
		{"bad request is not retried", false, false, func(int) string { return "status:400" }, 1, 0, func(err error) bool { return err != nil }},
		{"stream cut before any delta", false, false, func(int) string { return sseStart }, 4, 3, func(err error) bool { return errors.Is(err, llm.ErrIncomplete) }},
		{"overloaded event", false, false, func(int) string { return sseStart + sseOverload }, 4, 3, func(err error) bool {
			var se *llm.StreamError
			return errors.As(err, &se) && se.Type == "overloaded_error"
		}},
		{"cut after text was shown", false, false, func(int) string { return sseStart + sseText }, 4, 3, func(err error) bool { return errors.Is(err, llm.ErrIncomplete) }},
		{"cut after shown thinking", false, false, func(int) string { return sseStart + sseThinking }, 4, 3, func(err error) bool { return errors.Is(err, llm.ErrIncomplete) }},
		{"cut after hidden thinking", true, false, func(int) string { return sseStart + sseThinking }, 4, 3, func(err error) bool { return errors.Is(err, llm.ErrIncomplete) }},
		{"cut after text in headless mode", false, true, func(int) string { return sseStart + sseText }, 4, 3, func(err error) bool { return errors.Is(err, llm.ErrIncomplete) }},
		{"cut after thinking in headless mode", false, true, func(int) string { return sseStart + sseThinking }, 4, 3, func(err error) bool { return errors.Is(err, llm.ErrIncomplete) }},
		{"cut after an empty text delta", false, false, func(int) string { return sseStart + sseEmptyText }, 4, 3, func(err error) bool { return errors.Is(err, llm.ErrIncomplete) }},
		{"context too long", false, false, func(int) string { return sseStart + sseTooLong }, 1, 0, contextTooLong},
		{"worst case interleaving", false, false, func(n int) string {
			if n%4 == 0 {
				return sseStart
			}
			return "status:503"
		}, 16, 15, func(err error) bool { return errors.Is(err, llm.ErrIncomplete) }},
		{"recovers", false, false, func(n int) string {
			if n == 1 {
				return sseStart
			}
			return sseStart + sseText + "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
		}, 2, 1, func(err error) bool { return err == nil }},
	}
	for _, c := range cases {
		var slept []time.Duration
		old := llm.Sleep
		llm.Sleep = func(ctx context.Context, d time.Duration) error {
			slept = append(slept, d)
			return ctx.Err()
		}
		url, n := scriptedServer(t, c.script)
		show := !c.hidden
		a := New(Options{Config: config.Config{Model: "claude-haiku-5-5", MaxTokens: 10, ShowReasoning: &show}, Provider: llm.NewAnthropic(url, "k"), UI: nopDisplay{}, Headless: c.headless})
		_, _, err := a.stream(context.Background())
		llm.Sleep = old
		if !c.check(err) || n.Load() != c.requests || len(slept) != c.sleeps {
			t.Errorf("%s: %d requests, %d sleeps, err %v; want %d requests, %d sleeps", c.name, n.Load(), len(slept), err, c.requests, c.sleeps)
		}
	}
}

func TestStreamRetryKeepsOnlyTheSuccessfulAttempt(t *testing.T) {
	old := llm.Sleep
	llm.Sleep = func(ctx context.Context, d time.Duration) error { return ctx.Err() }
	defer func() { llm.Sleep = old }()
	url, n := scriptedServer(t, func(n int) string {
		if n == 1 {
			return sseStart + sseThinking
		}
		return sseStart + sseText + sseStop
	})
	show := false
	a := New(Options{Config: config.Config{Model: "claude-haiku-5-5", MaxTokens: 10, ShowReasoning: &show}, Provider: llm.NewAnthropic(url, "k"), UI: nopDisplay{}})
	msg, _, err := a.stream(context.Background())
	if err != nil || n.Load() != 2 || len(msg.Blocks) != 1 || msg.Text() != "hi" {
		t.Fatalf("err %v, %d requests, blocks %+v", err, n.Load(), msg.Blocks)
	}
}

type infoDisplay struct {
	nopDisplay
	infos []string
}

func (d *infoDisplay) Info(s string) { d.infos = append(d.infos, s) }

func TestRetryAfterShownTextSaysItIsDiscarded(t *testing.T) {
	old := llm.Sleep
	llm.Sleep = func(ctx context.Context, d time.Duration) error { return ctx.Err() }
	defer func() { llm.Sleep = old }()
	url, n := scriptedServer(t, func(n int) string {
		if n == 1 {
			return sseStart + sseText
		}
		return sseStart + sseText + sseStop
	})
	ui := &infoDisplay{}
	a := New(Options{Config: config.Config{Model: "claude-haiku-5-5", MaxTokens: 10}, Provider: llm.NewAnthropic(url, "k"), UI: ui})
	msg, _, err := a.stream(context.Background())
	if err != nil || n.Load() != 2 || msg.Text() != "hi" {
		t.Fatalf("err %v, %d requests, text %q", err, n.Load(), msg.Text())
	}
	if len(ui.infos) != 1 || !strings.HasPrefix(ui.infos[0], "↻ retrying: ") || !strings.HasSuffix(ui.infos[0], "(the text above is discarded)") {
		t.Errorf("notes %q", ui.infos)
	}
}
