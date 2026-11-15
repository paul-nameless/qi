package main

import (
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/paul-nameless/aicode/internal/config"
)

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
		{"{picker} | head -1", "x", "alpha", true},
		{"sed -n 2p > {file}", "x", "beta", true},
	}
	for _, c := range cases {
		got, ok := pick(runPlain, c.command, "cat", c.prompt, items)
		if got != c.want || ok != c.ok {
			t.Errorf("pick(%q) = %q, %v; want %q, %v", c.command, got, ok, c.want, c.ok)
		}
	}
	if _, ok := pick(runPlain, "cat", "cat", "x", nil); ok {
		t.Error("empty list should not run the picker")
	}
}

func TestToolAvailable(t *testing.T) {
	t.Setenv("AICODE_TEST_SHELL", "/bin/sh")
	cases := []struct {
		command, picker string
		want            bool
	}{
		{"", "", false},
		{"off", "sh", false},
		{"no-such-tool-xyz --x", "", false},
		{"sh -c true", "", true},
		{"FOO=1 sh -c true", "", true},
		{"(git ls-files || find .) | {picker}", "sh", true},
		{"(sh -c true) | {picker}", "no-such-picker-xyz", false},
		{"{picker} --preview x", "sh", true},
		{"~/no-such-xyz/x {msg}", "", false},
		{"$NO_SUCH_VAR_XYZ/sh", "", false},
		{"'sh' -c true", "", true},
		{"sh|cat", "", true},
		{"sh; true", "", true},
		{"exec sh", "", true},
		{"if true; then :; fi", "", true},
		{"$AICODE_TEST_SHELL -c true", "", true},
	}
	for _, c := range cases {
		if got := toolAvailable(c.command, c.picker); got != c.want {
			t.Errorf("toolAvailable(%q, %q) = %v", c.command, c.picker, got)
		}
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	os.Mkdir(home+"/bin", 0o755)
	os.WriteFile(home+"/bin/notify", []byte("#!/bin/sh\n"), 0o755)
	if !toolAvailable("~/bin/notify {msg}", "") || !toolAvailable("$HOME/bin/notify", "") {
		t.Error("home-relative script reported off")
	}
}

func TestExpandTool(t *testing.T) {
	got, env := expandTool(`{picker} --preview 'x {1} {title}' -t {title} "text={msg} \"q\"" \'{msg}`, "fzf --prompt={prompt}", map[string]string{"prompt": "a b", "title": "it's", "msg": "{prompt}"})
	want := `fzf --prompt="${AICODE_PROMPT}" --preview 'x {1} "${AICODE_TITLE}"' -t "${AICODE_TITLE}" "text=${AICODE_MSG} \"q\"" \'"${AICODE_MSG}"`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	sort.Strings(env)
	if strings.Join(env, "|") != "AICODE_MSG={prompt}|AICODE_PROMPT=a b|AICODE_TITLE=it's" {
		t.Errorf("env %q", env)
	}
	evil := "`touch x` $(touch x) \" ' done"
	out, err := runQuiet(`printf '%s|' {msg} "q={msg}" "$(printf %s {msg})"; sh -c 'printf "%s|" {msg}'`, "", map[string]string{"msg": evil}, "")
	if err != nil || out != evil+"|q="+evil+"|"+evil+"|"+evil+"|" {
		t.Errorf("model text was not passed literally: %q %v", out, err)
	}
}

func TestRunQuiet(t *testing.T) {
	out, err := runQuiet("tr a-z A-Z", "", nil, "hello")
	if err != nil || out != "HELLO" {
		t.Errorf("runQuiet = %q, %v", out, err)
	}
	if _, err := runQuiet("echo boom >&2; exit 3", "", nil, ""); err == nil || err.Error() != "boom" {
		t.Errorf("error = %v, want boom", err)
	}
	old := quietTimeout
	quietTimeout = 200 * time.Millisecond
	defer func() { quietTimeout = old }()
	start := time.Now()
	_, err = runQuiet("sleep 5 & sleep 5", "", nil, "")
	if err == nil || !strings.Contains(err.Error(), "timed out") || time.Since(start) > 3*time.Second {
		t.Errorf("timeout: %v after %s", err, time.Since(start))
	}
	if out, _ := runQuiet(`printf %s "$AICODE_BIN"`, "", nil, ""); out != mustExecutable(t) {
		t.Errorf("AICODE_BIN = %q", out)
	}
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func mustExecutable(t *testing.T) string {
	p, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFilePicker(t *testing.T) {
	r := &repl{agent: newTestAgent(t, config.Config{}), display: devNullPrinter(t)}
	cwd := r.agent.Cwd
	cases := []struct{ command, want string }{
		{"printf '%s\\n' 'a b.txt' " + shellQuote(cwd+"/sub/x.go") + " /etc/hosts \"it's\"", `'a b.txt' sub/x.go /etc/hosts "it's" `},
		{"printf 'z.go\\n' > {file}", "z.go "},
		{"printf 'main.go\\tdesc\\n' | {picker}", "main.go "},
	}
	for _, c := range cases {
		r.agent.Cfg.FilePicker = c.command
		got, ok := r.filePicker()
		if !ok || got != c.want {
			t.Errorf("%s: got %q, %v; want %q", c.command, got, ok, c.want)
		}
	}
	r.agent.Cfg.FilePicker = "false"
	if _, ok := r.filePicker(); ok {
		t.Error("failing picker should cancel")
	}
}

func TestHistoryPicker(t *testing.T) {
	r := &repl{agent: newTestAgent(t, config.Config{}), display: devNullPrinter(t)}
	entries := []string{"old", "two\nlines\tx", "new", "old"}
	items, _ := historyItems(entries)
	var names []string
	for _, it := range items {
		names = append(names, it.name)
	}
	if strings.Join(names, "|") != "old|new|two⏎lines x" {
		t.Errorf("items %q", names)
	}
	r.agent.Cfg.HistoryPicker = "grep lines"
	if got, ok := r.historyPicker(entries); !ok || got != "two\nlines\tx" {
		t.Errorf("got %q, %v", got, ok)
	}
	r.agent.Cfg.HistoryPicker = "{picker} | head -1"
	if got, _ := r.historyPicker(entries); got != "old" {
		t.Errorf("newest first: got %q", got)
	}
}

func TestConfigInfo(t *testing.T) {
	r := &repl{agent: newTestAgent(t, config.Config{Viewer: "off", CopyCmd: "cat"}), opts: options{configPath: "/x/c.yml"}}
	got := r.configInfo()
	for _, want := range []string{"Config: /x/c.yml", "viewer          off  off", "copy_cmd        on   cat"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
}
