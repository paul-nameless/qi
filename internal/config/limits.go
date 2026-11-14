package config

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"
)

type Tokens int

type Threshold struct {
	Tokens  int
	Percent float64
}

const FallbackWindow = 200000

var knownWindows = []struct {
	match  string
	tokens int
}{
	{"claude-3", 200000},
	{"haiku-4-5", 200000}, {"haiku-4.5", 200000},
	{"sonnet-4-5", 200000}, {"sonnet-4.5", 200000},
	{"opus-4-5", 200000}, {"opus-4.5", 200000},
	{"opus-4-1", 200000}, {"opus-4.1", 200000},
	{"claude", 1000000},
	{"gpt-6", 1050000},
	{"gemini", 1048576},
}

func ParseTokens(s string) (int, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	mult := 1.0
	switch {
	case strings.HasSuffix(s, "k"):
		mult, s = 1e3, strings.TrimSuffix(s, "k")
	case strings.HasSuffix(s, "m"):
		mult, s = 1e6, strings.TrimSuffix(s, "m")
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f <= 0 {
		return 0, fmt.Errorf("invalid token count %q (use 150000, 150k or 1.5m)", s)
	}
	return int(f * mult), nil
}

func ParseThreshold(s string) (Threshold, error) {
	s = strings.TrimSpace(s)
	if p, ok := strings.CutSuffix(s, "%"); ok {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil || f <= 0 || f > 100 {
			return Threshold{}, fmt.Errorf("invalid percentage %q (use 1%%–100%%)", s)
		}
		return Threshold{Percent: f}, nil
	}
	n, err := ParseTokens(s)
	if err != nil {
		return Threshold{}, err
	}
	return Threshold{Tokens: n}, nil
}

func scalar(b []byte) (string, error) {
	var v any
	if err := yaml.Unmarshal(b, &v); err != nil {
		return "", err
	}
	switch x := v.(type) {
	case string:
		return x, nil
	case uint64, int64, int, float64:
		return fmt.Sprint(x), nil
	}
	return "", fmt.Errorf("expected a number or string, got %s", strings.TrimSpace(string(b)))
}

func (t *Tokens) UnmarshalYAML(b []byte) error {
	s, err := scalar(b)
	if err != nil {
		return err
	}
	n, err := ParseTokens(s)
	*t = Tokens(n)
	return err
}

func (t *Threshold) UnmarshalYAML(b []byte) error {
	s, err := scalar(b)
	if err != nil {
		return err
	}
	*t, err = ParseThreshold(s)
	return err
}

func (t Threshold) String() string {
	if t.Percent > 0 {
		return strconv.FormatFloat(t.Percent, 'f', -1, 64) + "%"
	}
	return strconv.Itoa(t.Tokens)
}

func (c Config) Window() (int, bool) {
	if c.ContextWindow > 0 {
		return int(c.ContextWindow), true
	}
	m := strings.ToLower(c.Model)
	for _, w := range knownWindows {
		if strings.Contains(m, w.match) {
			return w.tokens, true
		}
	}
	return FallbackWindow, false
}

func (c Config) resolve(t Threshold) int {
	window, known := c.Window()
	n := t.Tokens
	if t.Percent > 0 {
		n = int(float64(window) * t.Percent / 100)
	}
	if room := window - c.MaxTokens; known && room > 0 && n > room {
		n = room
	}
	return n
}

func (c Config) CompactTokens() int {
	return c.resolve(c.CompactAt)
}

func (c Config) CompactIdleTokens() int {
	return min(c.resolve(c.CompactIdleAt), c.CompactTokens())
}

func (c Config) WindowGuessed() bool {
	_, known := c.Window()
	return !known && (c.CompactAt.Percent > 0 || c.CompactIdleAt.Percent > 0)
}
