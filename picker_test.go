package main

import "testing"

func TestPick(t *testing.T) {
	items := []pickItem{{"alpha", "first one"}, {"beta", "second\tone"}}
	cases := []struct {
		command, prompt, want string
		ok                    bool
	}{
		{"grep '^beta'", "x", "beta", true},
		{"false", "x", "", false},
		{"cat >/dev/null", "x", "", false},
		{`printf '%s\tdesc\n' {prompt}`, "it's $HOME", "it's $HOME", true},
		{`printf '%s\n' "$AICODE_PICK_PROMPT"`, "model ", "model ", true},
	}
	for _, c := range cases {
		got, ok := pick(c.command, c.prompt, items)
		if got != c.want || ok != c.ok {
			t.Errorf("pick(%q) = %q, %v; want %q, %v", c.command, got, ok, c.want, c.ok)
		}
	}
	if _, ok := pick("cat", "x", nil); ok {
		t.Error("empty list should not run the picker")
	}
	if pickerAvailable("") || pickerAvailable("no-such-picker-xyz --x") || !pickerAvailable("sh -c true") {
		t.Error("pickerAvailable")
	}
}
