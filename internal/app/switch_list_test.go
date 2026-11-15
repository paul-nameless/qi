package app

import (
	"strings"
	"testing"

	"github.com/paul-nameless/qi/internal/config"
)

func TestSwitchShowsSummaryOnly(t *testing.T) {
	r, out := testRepl(t, config.Config{Model: "m"}, nopProvider{})
	r.switchThen("x", func(string) error { return nil }, func() string { return "model: m · profile x\n  /profile x\n  /profile y" })
	if got := out.String(); !strings.Contains(got, "model: m · profile x") || strings.Contains(got, "/profile y") {
		t.Errorf("after a switch: %q", got)
	}
	r2, out2 := testRepl(t, config.Config{Model: "m"}, nopProvider{})
	r2.switchThen("", func(string) error { return nil }, func() string { return "model: m\n  /profile y" })
	if !strings.Contains(out2.String(), "/profile y") {
		t.Errorf("without args the list should show: %q", out2.String())
	}
}
