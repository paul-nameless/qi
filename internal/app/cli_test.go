package app

import (
	"bytes"
	"errors"
	"flag"
	"strings"
	"testing"

	"github.com/paul-nameless/qi/internal/config"
)

func TestParseArgs(t *testing.T) {
	var out bytes.Buffer
	c, err := parseArgs([]string{"-p", "luna", "-q", "hi", "-read-only"}, &out)
	if err != nil || c.opts.profile != "luna" || c.opts.query != "hi" || !c.opts.readOnly || c.checkConfig {
		t.Errorf("-p luna -q hi -read-only: %+v %v", c, err)
	}
	c, err = parseArgs([]string{"-check-config", "-config", "x.json"}, &out)
	if err != nil || !c.checkConfig || c.opts.configPath != "x.json" {
		t.Errorf("-check-config: %+v %v", c, err)
	}
	if out.Len() != 0 {
		t.Errorf("output on valid args: %q", out.String())
	}
	if _, err := parseArgs([]string{"-nope"}, &out); err == nil || errors.Is(err, flag.ErrHelp) {
		t.Errorf("unknown flag: %v", err)
	}
}

func TestUsageListsSingleDashFlags(t *testing.T) {
	var out bytes.Buffer
	if _, err := parseArgs([]string{"-h"}, &out); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("-h: %v", err)
	}
	help := out.String()
	if !strings.HasPrefix(help, "Usage: qi [flags]\n") {
		t.Errorf("usage header: %q", help)
	}
	for _, want := range []string{"\n  -check-config ", "\n  -v, -version ", "\n  -p name "} {
		if !strings.Contains(help, want) {
			t.Errorf("help lacks %q:\n%s", want, help)
		}
	}
	if strings.Contains(help, "--") {
		t.Errorf("double dash in help:\n%s", help)
	}
}

func TestVersionAlias(t *testing.T) {
	var out bytes.Buffer
	for _, args := range [][]string{{"-v"}, {"-version"}} {
		if c, err := parseArgs(args, &out); err != nil || !c.showVersion {
			t.Errorf("%v: %+v %v", args, c, err)
		}
	}
	if _, err := parseArgs([]string{"-h"}, &out); !errors.Is(err, flag.ErrHelp) {
		t.Fatal(err)
	}
	if help := out.String(); !strings.Contains(help, "\n  -v, -version ") || strings.Contains(help, "\n  -v ") {
		t.Errorf("help does not merge -v into -version:\n%s", help)
	}
}

func TestResumeFlag(t *testing.T) {
	cases := []struct {
		args       []string
		pick       bool
		id, query  string
		positional int
	}{
		{[]string{"-r"}, true, "", "", 0},
		{[]string{"-r", "abc123"}, true, "abc123", "", 0},
		{[]string{"-r=abc123"}, true, "abc123", "", 0},
		{[]string{"-r", "abc123", "-q", "hi"}, true, "abc123", "hi", 0},
		{[]string{"-q", "hi", "-r"}, true, "", "hi", 0},
		{[]string{"-q", "hi", "stray"}, false, "", "hi", 1},
	}
	for _, c := range cases {
		var out bytes.Buffer
		got, err := parseArgs(c.args, &out)
		if err != nil || got.opts.pickSession != c.pick || got.opts.resumeID != c.id || got.opts.query != c.query {
			t.Errorf("%v: pick %v id %q query %q err %v", c.args, got.opts.pickSession, got.opts.resumeID, got.opts.query, err)
		}
	}
	var out bytes.Buffer
	if _, err := parseArgs([]string{"-h"}, &out); !errors.Is(err, flag.ErrHelp) {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "\n  -r [id] ") || !strings.Contains(out.String(), "or resume <id>") {
		t.Errorf("help for -r:\n%s", out.String())
	}
}

func TestResumeByID(t *testing.T) {
	cwd := t.TempDir()
	s := writeSession(t, cwd, "hello")
	path, cancelled, err := resumePath(options{pickSession: true, resumeID: s.ID}, config.Config{}, cwd, true)
	if err != nil || cancelled || path != s.Path {
		t.Errorf("-r %s: %q %v %v", s.ID, path, cancelled, err)
	}
	if _, _, err := resumePath(options{pickSession: true, resumeID: "nope"}, config.Config{}, cwd, true); err == nil || !strings.Contains(err.Error(), "no session nope in "+cwd) {
		t.Errorf("unknown id: %v", err)
	}
	if _, _, err := resumePath(options{pickSession: true, resumeID: "a/b"}, config.Config{}, cwd, true); err == nil || !strings.Contains(err.Error(), "invalid session id") {
		t.Errorf("bad id: %v", err)
	}
	if _, _, err := resumePath(options{pickSession: true}, config.Config{}, cwd, true); err == nil || !strings.Contains(err.Error(), "-r needs a terminal") {
		t.Errorf("picker without a terminal: %v", err)
	}
}
