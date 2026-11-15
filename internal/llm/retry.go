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
	switch e.Type {
	case "overloaded_error", "api_error":
		return true
	}
	if n, err := strconv.Atoi(e.Code); err == nil && (n == 429 || n >= 500) {
		return true
	}
	return e.Code == "server_error" || strings.Contains(strings.ToLower(e.Message), "overloaded")
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
		return apiErr.Status == http.StatusTooManyRequests || apiErr.Status >= 500
	case errors.As(err, &streamErr):
		return streamErr.retryable()
	case errors.As(err, &dnsErr):
		return dnsErr.IsTemporary || dnsErr.IsTimeout
	case errors.Is(err, ErrStalled), errors.Is(err, ErrIncomplete), errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, syscall.ECONNRESET):
		return true
	case errors.As(err, &urlErr):
		return urlErr.Op != "parse"
	}
	return false
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
