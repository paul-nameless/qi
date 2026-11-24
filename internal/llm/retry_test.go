package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestRetryableClassification(t *testing.T) {
	post := func(err error) error { return &url.Error{Op: "Post", URL: "https://api.example.com", Err: err} }
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"400", &APIError{Status: 400}, false},
		{"429", &APIError{Status: 429}, true},
		{"402 out of credits", &APIError{Status: 402}, false},
		{"402 with retry-after", &APIError{Status: 402, RetryAfter: time.Minute}, true},
		{"503", &APIError{Status: 503}, true},
		{"cancelled", post(context.Canceled), false},
		{"deadline", post(context.DeadlineExceeded), false},
		{"dns not found", post(&net.OpError{Op: "dial", Err: &net.DNSError{Err: "no such host", Name: "x", IsNotFound: true}}), false},
		{"dns temporary", post(&net.OpError{Op: "dial", Err: &net.DNSError{Err: "server misbehaving", Name: "x", IsTemporary: true}}), true},
		{"refused", post(&net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}), true},
		{"keep-alive EOF", post(io.EOF), true},
		{"transport timeout", post(&net.OpError{Op: "read", Err: os.ErrDeadlineExceeded}), true},
		{"parse", &url.Error{Op: "parse", URL: "::", Err: errors.New("missing scheme")}, false},
		{"invalid url", fmt.Errorf("%w %q", ErrInvalidURL, "ftp://x"), false},
		{"reset mid-stream", fmt.Errorf("read: %w", syscall.ECONNRESET), true},
		{"unexpected EOF", io.ErrUnexpectedEOF, true},
		{"incomplete", ErrIncomplete, true},
		{"stalled", ErrStalled, true},
		{"overloaded event", &StreamError{Type: "overloaded_error", Message: "busy"}, true},
		{"api_error event", &StreamError{Type: "api_error", Message: "oops"}, true},
		{"invalid request event", &StreamError{Type: "invalid_request_error", Message: "prompt is too long"}, false},
		{"openrouter 502", &StreamError{Code: "502", Message: "provider disconnected"}, true},
		{"openrouter overloaded", &StreamError{Message: "Provider overloaded"}, true},
		{"openrouter 400", &StreamError{Code: "400", Message: "bad"}, false},
		{"already retried", &exhaustedError{err: &APIError{Status: 503}}, false},
		{"refusal", errors.New("the model declined to answer (stop_reason: refusal)"), false},
		{"decode", fmt.Errorf("decode stream chunk: %w", errors.New("bad json")), false},
		{"408", &APIError{Status: 408}, true},
		{"http2 reset by the server", fmt.Errorf("read stream: %w", errors.New("stream error: stream ID 63; INTERNAL_ERROR; received from peer")), true},
		{"http2 refused stream", errors.New("stream error: stream ID 5; REFUSED_STREAM; received from peer"), true},
		{"http2 goaway", errors.New("http2: server sent GOAWAY and closed the connection; LastStreamID=7, ErrCode=NO_ERROR, debug=\"\""), true},
		{"http2 protocol error from us", errors.New("stream error: stream ID 3; PROTOCOL_ERROR"), false},
		{"http2 protocol error from the server", errors.New("stream error: stream ID 3; PROTOCOL_ERROR; received from peer"), false},
		{"http2 cancel from the server", errors.New("stream error: stream ID 3; CANCEL; received from peer"), false},
		{"http2 calm down", errors.New("stream error: stream ID 9; ENHANCE_YOUR_CALM; received from peer"), true},
		{"etimedout", fmt.Errorf("read: %w", syscall.ETIMEDOUT), true},
		{"econnaborted", fmt.Errorf("read: %w", syscall.ECONNABORTED), true},
		{"timeout code", &StreamError{Code: "timeout", Message: "upstream timed out"}, true},
		{"rate limit code", &StreamError{Code: "rate_limit_exceeded", Message: "slow down"}, true},
		{"408 event", &StreamError{Code: "408", Message: "request timeout"}, true},
		{"temporarily unavailable", &StreamError{Message: "Service temporarily unavailable"}, true},
		{"overflow asking to try again", &StreamError{Code: "context_length_exceeded", Message: "Your input exceeds the context window of this model. Please adjust your input and try again."}, false},
		{"content policy asking to retry", &StreamError{Code: "invalid_prompt", Message: "Invalid prompt: flagged. Please try again with a different prompt and retry the request."}, false},
		{"400 asking to retry", &StreamError{Code: "400", Message: "Bad input, retry the request"}, false},
		{"anthropic timeout event", &StreamError{Type: "timeout_error", Message: "timed out"}, true},
		{"openai server_error type", &StreamError{Type: "server_error", Message: "The server had an error while processing your request. Sorry about that!"}, true},
		{"request too large", &StreamError{Code: "rate_limit_exceeded", Message: "Request too large for gpt: must be reduced"}, false},
		{"http2 truncated body", errors.New("stream error: stream ID 7; NO_ERROR; received from peer"), true},
		{"http2 graceful goaway", errors.New("http2: Transport received Server's graceful shutdown GOAWAY"), true},
		{"network unreachable mid-stream", fmt.Errorf("read: %w", syscall.ENETUNREACH), true},
		{"empty reply", errEmpty, true},
		{"empty at max tokens", errEmptyAtMaxToken, false},
		{"read timeout mid-stream", fmt.Errorf("read: %w", &net.OpError{Op: "read", Err: os.ErrDeadlineExceeded}), true},
		{"broken pipe", fmt.Errorf("write: %w", syscall.EPIPE), true},
		{"rate limit event", &StreamError{Type: "rate_limit_error", Message: "slow down"}, true},
		{"routeway unparsable tool call", &StreamError{Message: "The model produced a tool call that could not be parsed. Retry the request."}, true},
		{"try again later event", &StreamError{Message: "Upstream busy, please try again later"}, true},
		{"plain bad request event", &StreamError{Code: "400", Message: "messages: field required"}, false},
	}
	for _, c := range cases {
		if got := Retryable(c.err); got != c.want {
			t.Errorf("%s (%v): got %v, want %v", c.name, c.err, got, c.want)
		}
	}
}

func TestCertificateErrorNotRetryable(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	slept := fakeSleep(t)
	_, err := postStreamCtx(context.Background(), srv.URL, nil, map[string]any{})
	if err == nil || !certError(err) || Retryable(err) || len(*slept) != 0 {
		t.Errorf("certificate error: %v, slept %v", err, *slept)
	}
}

func fakeSleep(t *testing.T) *[]time.Duration {
	var slept []time.Duration
	oldSleep, oldBackoff := Sleep, requestBackoff
	Sleep = func(ctx context.Context, d time.Duration) error {
		slept = append(slept, d)
		return ctx.Err()
	}
	requestBackoff = func(attempt int) time.Duration { return time.Duration(attempt) * time.Second }
	t.Cleanup(func() { Sleep, requestBackoff = oldSleep, oldBackoff })
	return &slept
}

func statusServer(t *testing.T, status int, header map[string]string) (*httptest.Server, *atomic.Int32) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		for k, v := range header {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
		fmt.Fprint(w, `{"error":"x"}`)
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

func TestRequestAttempts(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		header   map[string]string
		requests int32
		slept    []time.Duration
	}{
		{"server error", 503, nil, 4, []time.Duration{time.Second, 2 * time.Second, 3 * time.Second}},
		{"retry-after", 429, map[string]string{"Retry-After": "30"}, 4, []time.Duration{30 * time.Second, 30 * time.Second, 30 * time.Second}},
		{"bad request", 400, nil, 1, nil},
		{"out of credits", 402, nil, 1, nil},
		{"in-flight budget", 402, map[string]string{"Retry-After": "120"}, 4, []time.Duration{120 * time.Second, 120 * time.Second, 120 * time.Second}},
		{"retry-after past the cap", 402, map[string]string{"Retry-After": "300"}, 1, nil},
		{"retry-after zero", 402, map[string]string{"Retry-After": "0"}, 1, nil},
		{"retry-after date", 402, map[string]string{"Retry-After": "Wed, 21 Oct 2026 07:28:00 GMT"}, 1, nil},
	}
	for _, c := range cases {
		slept := fakeSleep(t)
		srv, n := statusServer(t, c.status, c.header)
		_, err := postStreamCtx(context.Background(), srv.URL, nil, map[string]any{})
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.Status != c.status || Retryable(err) {
			t.Errorf("%s: error %v", c.name, err)
		}
		if n.Load() != c.requests || fmt.Sprint(*slept) != fmt.Sprint(c.slept) {
			t.Errorf("%s: %d requests, slept %v; want %d, %v", c.name, n.Load(), *slept, c.requests, c.slept)
		}
	}
	slept := fakeSleep(t)
	for _, u := range []string{"ftp://example.com/v1", "http:///v1", "example.com/v1"} {
		if _, err := postStreamCtx(context.Background(), u, nil, map[string]any{}); !errors.Is(err, ErrInvalidURL) || len(*slept) != 0 {
			t.Errorf("%s: %v, slept %v", u, err, *slept)
		}
	}
}
