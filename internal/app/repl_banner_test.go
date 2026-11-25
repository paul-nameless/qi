package app

import (
	"testing"

	"github.com/paul-nameless/qi/internal/config"
)

func TestBannerVersion(t *testing.T) {
	for in, want := range map[string]string{"1.0.0": "1.0.0", "dev-55430f1": "dev", "dev-55430f1-dirty": "dev", "dev": "dev"} {
		if got := bannerVersion(in); got != want {
			t.Errorf("bannerVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSubagentsLine(t *testing.T) {
	choices := []config.SubagentChoice{{Name: "haiku", Model: "claude-haiku-5-5", Effort: "low", UseFor: "x"}, {Name: "opus", Model: "claude-opus-5-5"}}
	if got := subagentsLine(choices); got != "subagents: haiku (claude-haiku-5-5, effort low), opus (claude-opus-5-5)" {
		t.Errorf("got %q", got)
	}
	if got := subagentsLine(config.Config{Model: "m"}.SubagentChoices()); got != "" {
		t.Errorf("self choice printed %q", got)
	}
}
