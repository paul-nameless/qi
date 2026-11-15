package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
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
	{"gpt-6", 1050000},
	{"gemini", 1048576},
}

var claudeVersion = regexp.MustCompile(`(opus|sonnet|haiku)-(\d)(?:[-.](\d)(?:\D|$))?`)

func claudeWindow(model string) int {
	if strings.Contains(model, "fable") || strings.Contains(model, "mythos") {
		return 1000000
	}
	v := claudeVersion.FindStringSubmatch(model)
	if v == nil {
		return 200000
	}
	major, minor := int(v[2][0]-'0'), 0
	if v[3] != "" {
		minor = int(v[3][0] - '0')
	}
	if major >= 5 || major == 4 && minor >= 6 {
		return 1000000
	}
	return 200000
}

func finite(f float64) bool {
	return !math.IsNaN(f) && !math.IsInf(f, 0)
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
	if err != nil || !finite(f) || f < 0 || f*mult > math.MaxInt32 {
		return 0, fmt.Errorf("invalid token count %q (use 150000, 150k or 1.5m)", s)
	}
	return int(f * mult), nil
}

func ParseThreshold(s string) (Threshold, error) {
	s = strings.TrimSpace(s)
	if p, ok := strings.CutSuffix(s, "%"); ok {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil || !finite(f) || f < 0 || f > 100 {
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

func scalar(b []byte) (string, bool, error) {
	b = bytes.TrimSpace(b)
	switch {
	case string(b) == "null":
		return "", false, nil
	case len(b) > 0 && b[0] == '"':
		var s string
		err := json.Unmarshal(b, &s)
		return s, true, err
	case len(b) > 0 && (b[0] == '-' || b[0] >= '0' && b[0] <= '9'):
		return string(b), true, nil
	}
	return "", false, fmt.Errorf("expected a number or a string, got %s", rawKind(b))
}

func rawKind(b []byte) string {
	switch {
	case len(b) == 0:
		return "nothing"
	case b[0] == '{':
		return "an object"
	case b[0] == '[':
		return "an array"
	case b[0] == 't' || b[0] == 'f':
		return "a boolean"
	}
	return string(b)
}

func (t *Tokens) UnmarshalJSON(b []byte) error {
	s, ok, err := scalar(b)
	if !ok || err != nil {
		return err
	}
	n, err := ParseTokens(s)
	*t = Tokens(n)
	return err
}

func (t *Threshold) UnmarshalJSON(b []byte) error {
	s, ok, err := scalar(b)
	if !ok || err != nil {
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
	if strings.Contains(m, "claude") {
		return claudeWindow(m), true
	}
	for _, w := range knownWindows {
		if strings.Contains(m, w.match) {
			return w.tokens, true
		}
	}
	return FallbackWindow, false
}

func (c *Config) SetModel(model string) {
	if model != c.Model {
		c.Model = model
		c.ContextWindow = 0
	}
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
