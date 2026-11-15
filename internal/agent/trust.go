package agent

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/paul-nameless/qi/internal/config"
)

func trustFile() string {
	return filepath.Join(config.Dir(), "trusted")
}

func Trusted(dir string) bool {
	data, err := os.ReadFile(trustFile())
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == dir {
			return true
		}
	}
	return false
}

func Trust(dir string) error {
	if err := os.MkdirAll(config.Dir(), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(trustFile(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(dir + "\n")
	return err
}

func SplitHookFiles(files []string) (user, project []string) {
	userFiles := map[string]bool{
		filepath.Join(config.Home(), ".claude", "settings.json"):       true,
		filepath.Join(config.Home(), ".claude", "settings.local.json"): true,
		filepath.Join(config.Dir(), "settings.json"):                   true,
	}
	for _, f := range files {
		if userFiles[filepath.Clean(f)] {
			user = append(user, f)
		} else {
			project = append(project, f)
		}
	}
	return user, project
}

func HookCommands(files []string) []string {
	var out []string
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var s struct {
			Hooks map[string][]hookMatcher `json:"hooks"`
		}
		if json.Unmarshal(data, &s) != nil {
			continue
		}
		for ev, ms := range s.Hooks {
			for _, m := range ms {
				for _, c := range m.Hooks {
					if c.Command != "" {
						out = append(out, ev+": "+c.Command)
					}
				}
			}
		}
	}
	return out
}

func AskTrust(dir string, commands []string) bool {
	os.Stdout.WriteString("This project defines hooks that run shell commands:\n")
	for _, c := range commands {
		os.Stdout.WriteString("  " + strconv.Quote(c) + "\n")
	}
	os.Stdout.WriteString("Trust " + dir + " and run its hooks? [y/N] ")
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	if !approved(line) {
		return false
	}
	return Trust(dir) == nil
}

func SelectHookFiles(roots, configured []string, interactive bool, ask func(dir string, cmds []string) bool) (files []string, skipped bool) {
	user, project := SplitHookFiles(ExpandPaths(roots, configured))
	cmds := HookCommands(project)
	if len(cmds) == 0 || Trusted(roots[0]) || (interactive && ask(roots[0], cmds)) {
		return append(user, project...), false
	}
	return user, true
}

func approved(answer string) bool {
	return strings.ToLower(strings.TrimSpace(answer)) == "y"
}
