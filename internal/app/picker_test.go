package app

import (
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/paul-nameless/qi/internal/config"
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
		{`printf '%s\n' "$QI_PICK_PROMPT"`, "model ", "model ", true},
		{"{picker} | head -1", "x", "alpha", true},
		{"sed -n 2p > {file}", "x", "beta", true},
	}
	for _, c := range cases {
		got, ok := pick(runPlain, nil, c.command, "cat", c.prompt, items, false)
		if got != c.want || ok != c.ok {
			t.Errorf("pick(%q) = %q, %v; want %q, %v", c.command, got, ok, c.want, c.ok)
		}
	}
	if _, ok := pick(runPlain, nil, "cat", "cat", "x", nil, false); ok {
		t.Error("empty list should not run the picker")
	}
}

func TestToolAvailable(t *testing.T) {
	t.Setenv("QI_TEST_SHELL", "/bin/sh")
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
		{"$QI_TEST_SHELL -c true", "", true},
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
	want := `fzf --prompt="${QI_PROMPT}" --preview 'x {1} "${QI_TITLE}"' -t "${QI_TITLE}" "text=${QI_MSG} \"q\"" \'"${QI_MSG}"`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	sort.Strings(env)
	if strings.Join(env, "|") != "QI_MSG={prompt}|QI_PROMPT=a b|QI_TITLE=it's" {
		t.Errorf("env %q", env)
	}
	if got, _ := expandTool("fd | {picker}", "builtin", map[string]string{"prompt": "@ "}); got != `fd | "$QI_BIN" pick -p "${QI_PROMPT}"` {
		t.Errorf("a pipeline into the builtin picker runs qi pick: %s", got)
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
	if out, _ := runQuiet(`printf %s "$QI_BIN"`, "", nil, ""); out != mustExecutable(t) {
		t.Errorf("QI_BIN = %q", out)
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
	cwd := r.agent.Cwd()
	cases := []struct{ command, want string }{
		{"printf '%s\\n' 'a b.txt' " + shellQuote(cwd+"/sub/x.go") + " /etc/hosts \"it's\"", `'a b.txt' sub/x.go /etc/hosts "it's" `},
		{"printf 'z.go\\n' > {file}", "z.go "},
		{"printf 'main.go\\tdesc\\n' | {picker}", "main.go "},
	}
	for _, c := range cases {
		editConfig(r.agent, func(cfg *config.Config) { cfg.FilePicker = c.command })
		got, ok := r.filePicker()
		if !ok || got != c.want {
			t.Errorf("%s: got %q, %v; want %q", c.command, got, ok, c.want)
		}
	}
	editConfig(r.agent, func(c *config.Config) { c.FilePicker = "false" })
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
	editConfig(r.agent, func(c *config.Config) { c.HistoryPicker = "grep lines" })
	if got, ok := r.historyPicker(entries); !ok || got != "two\nlines\tx" {
		t.Errorf("got %q, %v", got, ok)
	}
	editConfig(r.agent, func(c *config.Config) { c.HistoryPicker = "{picker} | head -1" })
	if got, _ := r.historyPicker(entries); got != "old" {
		t.Errorf("newest first: got %q", got)
	}
}

func TestConfigInfo(t *testing.T) {
	r := &repl{agent: newTestAgent(t, config.Config{Pager: "off", CopyCmd: "cat"}), opts: options{configPath: "/x/c.json"}}
	got := r.configInfo()
	for _, want := range []string{"Config: /x/c.json", "pager           off  off", "copy_cmd        on   cat"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
}

func writeFiles(t *testing.T, dir string, names ...string) {
	for _, n := range names {
		p := filepath.Join(dir, n)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestListFiles(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, "z.txt", "a/b.txt", "a/c/d.txt", ".git/HEAD")
	if got := strings.Join(listFiles(dir, 10), " "); got != "z.txt a/b.txt a/c/d.txt" {
		t.Errorf("breadth-first without git: %q", got)
	}
	if got := listFiles(dir, 2); len(got) != 2 {
		t.Errorf("limit: %q", got)
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	repo := t.TempDir()
	writeFiles(t, repo, "deep/x/y.go", ".gitignore", "top.txt", "ignored/i.txt")
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("ignored/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "deep"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	if got := strings.Join(listFiles(repo, 10), " "); got != ".gitignore top.txt deep/x/y.go ignored/i.txt" {
		t.Errorf("git files first, then the rest: %q", got)
	}
	r := &repl{agent: newTestAgentWith(t, config.Config{FilePicker: "{picker} | tail -1"}, repo, nopProvider{}, devNullPrinter(t)), display: devNullPrinter(t)}
	if got, ok := r.filePicker(); !ok || got != "ignored/i.txt " {
		t.Errorf("file picker gets the list on stdin: %q %v", got, ok)
	}
}

func TestMissingToolNamesTheResolvedCommand(t *testing.T) {
	cases := []struct{ command, picker, want string }{
		{"{picker} --with-nth=2..", "no-such-fzf-xyz --height=40%", "no-such-fzf-xyz"},
		{"no-such-pager-xyz -R", "cat", "no-such-pager-xyz"},
		{"FOO=1 no-such-copy-xyz", "cat", "no-such-copy-xyz"},
		{"", "cat", "command"},
	}
	for _, c := range cases {
		if got := missingTool(c.command, c.picker); got != c.want {
			t.Errorf("missingTool(%q, %q) = %q, want %q", c.command, c.picker, got, c.want)
		}
	}
	r := &repl{agent: newTestAgent(t, config.Config{Picker: "no-such-fzf-xyz --height=40%", SessionPicker: "{picker} --nth=1"}), opts: options{configPath: "/x/c.json"}}
	if got := r.configInfo(); !strings.Contains(got, "session_picker  off  no-such-fzf-xyz --height=40% --nth=1  (no-such-fzf-xyz not found)") {
		t.Errorf("config info:\n%s", got)
	}
	if _, err := pickSession(runPlain, nil, r.agent.Config(), t.TempDir(), ""); err == nil || !strings.Contains(err.Error(), "session_picker is off (no-such-fzf-xyz not found)") {
		t.Errorf("session picker error: %v", err)
	}
}

func TestEffortItemsListConfigEfforts(t *testing.T) {
	r, _ := testRepl(t, config.Config{Model: "m", ReasoningEffort: "minimal"}, nopProvider{})
	var names []string
	for _, it := range r.effortItems() {
		names = append(names, it.name)
		if want := map[bool]string{true: "current"}[it.name == "minimal"]; it.desc != want {
			t.Errorf("%s: desc %q, want %q", it.name, it.desc, want)
		}
	}
	if want := append([]string{"default"}, config.Efforts...); !slices.Equal(names, want) {
		t.Errorf("effort items %v, want %v", names, want)
	}
}

func TestToolKeysMatchToolDefaults(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "wl-copy"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path, "")
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, kv := range (&repl{agent: newTestAgent(t, cfg)}).toolKeys() {
		listed[kv[0]] = true
	}
	defaulted := map[string]bool{}
	v := reflect.ValueOf(cfg)
	for i := range v.NumField() {
		f := v.Type().Field(i)
		if f.IsExported() && f.Type.Kind() == reflect.String && v.Field(i).String() != "" {
			defaulted[strings.Split(f.Tag.Get("json"), ",")[0]] = true
		}
	}
	delete(defaulted, "api")
	delete(defaulted, "model")
	if !maps.Equal(listed, defaulted) {
		t.Errorf("/config lists tools %v, config defaults %v (other string defaults: update this test)", slices.Sorted(maps.Keys(listed)), slices.Sorted(maps.Keys(defaulted)))
	}
}
