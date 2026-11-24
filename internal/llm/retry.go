package llm

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var (
	ErrIncomplete = errors.New("stream ended before the response was complete")
	ErrInvalidURL = errors.New("invalid API URL")
)

const requestAttempts = 4

var requestBackoff = func(attempt int) time.Duration {
	return time.Duration(1<<attempt)*time.Second + time.Duration(rand.Intn(500))*time.Millisecond
}

var Sleep = func(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

type StreamError struct {
	Type    string
	Code    string
	Message string
}

func (e *StreamError) Error() string {
	switch {
	case e.Type != "":
		return fmt.Sprintf("stream error: %s: %s", e.Type, e.Message)
	case e.Message != "":
		return "stream error: " + e.Message
	}
	return "stream error"
}

func (e *StreamError) retryable() bool {
	kind := strings.ToLower(e.Type + " " + e.Code)
	msg := strings.ToLower(e.Message)
	if n, err := strconv.Atoi(e.Code); err == nil && (n == 408 || n == 429 || n >= 500) {
		return true
	}
	if e.clientError(kind) {
		return false
	}
	for _, k := range []string{"overloaded", "api_error", "server_error", "unavailable", "timeout", "rate_limit"} {
		if strings.Contains(kind, k) {
			return !strings.Contains(msg, "too large")
		}
	}
	if strings.Contains(msg, "retry the request") || strings.Contains(msg, "retry your request") {
		return true
	}
	if strings.Contains(kind, "invalid") {
		return false
	}
	for _, hint := range []string{"overloaded", "try again later", "temporarily unavailable"} {
		if strings.Contains(msg, hint) {
			return true
		}
	}
	return false
}

func (e *StreamError) clientError(kind string) bool {
	if n, err := strconv.Atoi(e.Code); err == nil && n >= 400 && n < 500 {
		return true
	}
	for _, k := range []string{"authentication", "permission", "billing", "insufficient", "content", "policy", "safety", "moderation", "invalid_prompt", "context_length", "not_found"} {
		if strings.Contains(kind, k) {
			return true
		}
	}
	return false
}

var transientHTTP2 = []string{"INTERNAL_ERROR", "REFUSED_STREAM", "ENHANCE_YOUR_CALM", "NO_ERROR"}

func http2Transient(err error) bool {
	s := err.Error()
	switch {
	case strings.Contains(s, "GOAWAY"):
		return true
	case !strings.Contains(s, "received from peer"):
		return false
	}
	for _, code := range transientHTTP2 {
		if strings.Contains(s, "; "+code+";") {
			return true
		}
	}
	return false
}

type exhaustedError struct {
	err error
}

func (e *exhaustedError) Error() string { return e.err.Error() }
func (e *exhaustedError) Unwrap() error { return e.err }

func Retryable(err error) bool {
	var exhausted *exhaustedError
	var apiErr *APIError
	var streamErr *StreamError
	var dnsErr *net.DNSError
	var urlErr *url.Error
	switch {
	case err == nil, errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded),
		errors.As(err, &exhausted), errors.Is(err, ErrInvalidURL), certError(err):
		return false
	case errors.As(err, &apiErr):
		return apiErr.Throttled() || apiErr.Status == http.StatusRequestTimeout || apiErr.Status >= 500
	case errors.As(err, &streamErr):
		return streamErr.retryable()
	case errors.As(err, &dnsErr):
		return dnsErr.IsTemporary || dnsErr.IsTimeout
	case errors.Is(err, ErrStalled), errors.Is(err, ErrIncomplete), errors.Is(err, io.ErrUnexpectedEOF),
		errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.EPIPE), errors.Is(err, syscall.ETIMEDOUT), errors.Is(err, syscall.ECONNABORTED),
		errors.Is(err, syscall.ENETDOWN), errors.Is(err, syscall.ENETUNREACH), errors.Is(err, syscall.EHOSTUNREACH),
		http2Transient(err), netTimeout(err):
		return true
	case errors.As(err, &urlErr):
		return urlErr.Op != "parse"
	}
	return false
}

func netTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

func certError(err error) bool {
	var verify *tls.CertificateVerificationError
	var unknown x509.UnknownAuthorityError
	var host x509.HostnameError
	var invalid x509.CertificateInvalidError
	var record tls.RecordHeaderError
	return errors.As(err, &verify) || errors.As(err, &unknown) || errors.As(err, &host) || errors.As(err, &invalid) || errors.As(err, &record)
}

func checkURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%w %q: want http(s)://host/path", ErrInvalidURL, raw)
	}
	return nil
}

func retryAfter(h http.Header) time.Duration {
	if secs, err := strconv.Atoi(h.Get("Retry-After")); err == nil && secs > 0 && secs <= 120 {
		return time.Duration(secs) * time.Second
	}
	return 0
}
