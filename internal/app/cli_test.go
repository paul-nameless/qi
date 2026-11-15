package app

import (
	"bytes"
	"errors"
	"flag"
	"strings"
	"testing"
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
	for _, want := range []string{"\n  -check-config ", "\n  -version ", "\n  -p name "} {
		if !strings.Contains(help, want) {
			t.Errorf("help lacks %q:\n%s", want, help)
		}
	}
	if strings.Contains(help, "--") {
		t.Errorf("double dash in help:\n%s", help)
	}
}
