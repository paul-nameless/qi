package config

import (
	"os"
	"path/filepath"
	"strings"
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
		"0":      {},
		"0%":     {},
	}
	for in, want := range good {
		if got, err := ParseThreshold(in); err != nil || got != want {
			t.Errorf("%q: got %+v %v, want %+v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "abc", "-5k", "-1%", "120%", "k", "10x", "nan", "NaN%", "inf", "inf%", "1e400", "1e12"} {
		if _, err := ParseThreshold(in); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
}

func TestThresholdJSON(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AICODE_CONFIG_DIR", dir)
	path := filepath.Join(dir, "config.json")
	cfg := `{"compact_at": 120000, "compact_idle_at": "60k", "profiles": {"big": {"model": "some-model", "context_window": "2m", "compact_at": "50%"}, "num": {"context_window": 300000}}}`
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
	if c, err := Load(path, "num"); err != nil || c.ContextWindow != 300000 {
		t.Errorf("numeric context_window: %d %v", c.ContextWindow, err)
	}
}

func TestThresholdJSONErrors(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AICODE_CONFIG_DIR", dir)
	path := filepath.Join(dir, "config.json")
	cases := map[string][]string{
		`{"compact_at": "lots"}`:                                     {"compact_at", "invalid token count"},
		`{"compact_at": "lots", "model": "m", "context_window": 5}`:  {`key "compact_at"`},
		`{"profiles": {"p": {"context_window": "x", "model": "m"}}}`: {`profile "p"`, `key "context_window"`},
		`{"compact_idle_at": "120%"}`:                                {"compact_idle_at", "invalid percentage"},
		`{"context_window": true}`:                                   {"context_window", "a boolean"},
		`{"context_window": [1]}`:                                    {"context_window", "an array"},
		`{"profiles": {"p": {"model": "m", "compact_at": "-5k"}}}`:   {`profile "p"`, "compact_at"},
	}
	for in, want := range cases {
		if err := os.WriteFile(path, []byte(in), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := Load(path, "")
		if err == nil {
			t.Errorf("%s accepted", in)
			continue
		}
		for _, w := range append(want, path) {
			if !strings.Contains(err.Error(), w) {
				t.Errorf("%s: error %q does not mention %q", in, err, w)
			}
		}
	}
	if err := os.WriteFile(path, []byte(`{"compact_at": null}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, err := Load(path, ""); err != nil || c.CompactAt != (Threshold{Tokens: 150000}) {
		t.Errorf("null compact_at should keep the default: %+v %v", c.CompactAt, err)
	}
}
