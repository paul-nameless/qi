package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseThreshold(t *testing.T) {
	good := map[string]Threshold{
		"150000": {Tokens: 150000},
		"150k":   {Tokens: 150000},
		"150K":   {Tokens: 150000},
		"1.5m":   {Tokens: 1500000},
		"80%":    {Percent: 80},
		" 2.5% ": {Percent: 2.5},
	}
	for in, want := range good {
		if got, err := ParseThreshold(in); err != nil || got != want {
			t.Errorf("%q: got %+v %v, want %+v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "abc", "0", "-5k", "0%", "120%", "k", "10x"} {
		if _, err := ParseThreshold(in); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
}

func TestThresholdYAML(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AICODE_CONFIG_DIR", dir)
	path := filepath.Join(dir, "config.yml")
	cfg := "compact_at: 120000\ncompact_idle_at: 60k\nprofiles:\n  big:\n    model: some-model\n    context_window: 2m\n    compact_at: \"50%\"\n"
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if c.CompactAt != (Threshold{Tokens: 120000}) || c.CompactIdleAt != (Threshold{Tokens: 60000}) {
		t.Errorf("top level: %+v %+v", c.CompactAt, c.CompactIdleAt)
	}
	c, err = Load(path, "big")
	if err != nil {
		t.Fatal(err)
	}
	if c.CompactAt != (Threshold{Percent: 50}) || c.ContextWindow != 2000000 || c.CompactTokens() != 1000000 {
		t.Errorf("profile: %+v window %d tokens %d", c.CompactAt, c.ContextWindow, c.CompactTokens())
	}
	if err := os.WriteFile(path, []byte("compact_at: lots\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, ""); err == nil {
		t.Error("invalid compact_at accepted")
	}
}

func TestResolveThresholds(t *testing.T) {
	defaults := Config{Model: "openai/gpt-6-luna"}
	defaults.applyDefaults()
	if defaults.CompactTokens() != 150000 || defaults.CompactIdleTokens() != 100000 {
		t.Errorf("defaults: %d %d", defaults.CompactTokens(), defaults.CompactIdleTokens())
	}
	cases := []struct {
		name      string
		cfg       Config
		at, idle  int
		guessWarn bool
	}{
		{"percent of table window", Config{Model: "claude-opus-5-5", MaxTokens: 32000, CompactAt: Threshold{Percent: 50}, CompactIdleAt: Threshold{Percent: 10}}, 500000, 100000, false},
		{"old claude is 200k", Config{Model: "claude-haiku-4-5", MaxTokens: 32000, CompactAt: Threshold{Percent: 50}, CompactIdleAt: Threshold{Tokens: 100000}}, 100000, 100000, false},
		{"clamped to window minus output", Config{Model: "x", ContextWindow: 128000, MaxTokens: 32000, CompactAt: Threshold{Tokens: 150000}, CompactIdleAt: Threshold{Tokens: 100000}}, 96000, 96000, false},
		{"no clamp against the guess", Config{Model: "mystery", MaxTokens: 32000, CompactAt: Threshold{Tokens: 500000}, CompactIdleAt: Threshold{Tokens: 100000}}, 500000, 100000, false},
		{"percent of the guess", Config{Model: "mystery", MaxTokens: 32000, CompactAt: Threshold{Percent: 3}, CompactIdleAt: Threshold{Tokens: 100000}}, 6000, 6000, true},
	}
	for _, tc := range cases {
		if got := tc.cfg.CompactTokens(); got != tc.at {
			t.Errorf("%s: compact %d, want %d", tc.name, got, tc.at)
		}
		if got := tc.cfg.CompactIdleTokens(); got != tc.idle {
			t.Errorf("%s: idle %d, want %d", tc.name, got, tc.idle)
		}
		if got := tc.cfg.WindowGuessed(); got != tc.guessWarn {
			t.Errorf("%s: guessed %v", tc.name, got)
		}
	}
}
