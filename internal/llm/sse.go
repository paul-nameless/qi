package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/paul-nameless/qi/internal/strutil"
)

var httpClient = &http.Client{Transport: &http.Transport{
	Proxy:                 http.ProxyFromEnvironment,
	ResponseHeaderTimeout: 3 * time.Minute,
	IdleConnTimeout:       90 * time.Second,
	TLSHandshakeTimeout:   15 * time.Second,
	ForceAttemptHTTP2:     true,
}}

const streamIdleTimeout = 5 * time.Minute

var ErrStalled = errors.New("the API stopped sending data for 5 minutes")

type idleBody struct {
	io.ReadCloser
	timer   *time.Timer
	cancel  context.CancelFunc
	stalled atomic.Bool
}

func streamErr(body io.ReadCloser, err error) error {
	if b, ok := body.(*idleBody); ok && b.stalled.Load() {
		return ErrStalled
	}
	return err
}

func (b *idleBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		b.timer.Reset(streamIdleTimeout)
	}
	return n, err
}

func (b *idleBody) Close() error {
	b.timer.Stop()
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}

type APIError struct {
	Status     int
	Body       string
	retryAfter time.Duration
}

func (e *APIError) Error() string {
	body := strings.TrimSpace(e.Body)
	if len(body) > 600 {
		body = strutil.CutStart(body, 600) + "…"
	}
	return fmt.Sprintf("api error %d: %s", e.Status, body)
}

func postStream(parent context.Context, url string, headers map[string]string, body any) (*http.Response, error) {
	ctx, cancel := context.WithCancel(parent)
	resp, err := postStreamCtx(ctx, url, headers, body)
	if err != nil {
		cancel()
		return nil, err
	}
	ib := &idleBody{ReadCloser: resp.Body, cancel: cancel}
	ib.timer = time.AfterFunc(streamIdleTimeout, func() {
		ib.stalled.Store(true)
		cancel()
	})
	resp.Body = ib
	return resp, nil
}

func postStreamCtx(ctx context.Context, url string, headers map[string]string, body any) (*http.Response, error) {
	if err := checkURL(url); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}
	var lastErr error
	var wait time.Duration
	for attempt := 0; attempt < requestAttempts; attempt++ {
		if attempt > 0 {
			if err := Sleep(ctx, max(wait, requestBackoff(attempt))); err != nil {
				return nil, err
			}
		}
		resp, err := sendRequest(ctx, url, headers, payload)
		if err == nil {
			return resp, nil
		}
		lastErr, wait = err, 0
		var apiErr *APIError
		if errors.As(err, &apiErr) {
			wait = apiErr.retryAfter
		}
		if ctx.Err() != nil || !Retryable(err) {
			return nil, err
		}
	}
	return nil, &exhaustedError{err: lastErr}
}

func sendRequest(ctx context.Context, url string, headers map[string]string, payload []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusOK {
		return resp, nil
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	resp.Body.Close()
	return nil, &APIError{Status: resp.StatusCode, Body: string(data), retryAfter: retryAfter(resp.Header)}
}

type sseEvent struct {
	Event string
	Data  []byte
}

func readSSE(r io.Reader, fn func(sseEvent) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	var ev sseEvent
	var data bytes.Buffer
	flush := func() error {
		if data.Len() == 0 {
			ev = sseEvent{}
			return nil
		}
		ev.Data = bytes.TrimSuffix(data.Bytes(), []byte("\n"))
		err := fn(ev)
		ev = sseEvent{}
		data.Reset()
		return err
	}
	for sc.Scan() {
		line := sc.Bytes()
		switch {
		case len(line) == 0:
			if err := flush(); err != nil {
				return err
			}
		case line[0] == ':':
		case bytes.HasPrefix(line, []byte("event:")):
			ev.Event = strings.TrimSpace(string(line[6:]))
		case bytes.HasPrefix(line, []byte("data:")):
			data.Write(bytes.TrimPrefix(line[5:], []byte(" ")))
			data.WriteByte('\n')
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return flush()
}

var errStreamDone = errors.New("stream done")
