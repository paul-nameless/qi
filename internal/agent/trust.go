package agent

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/paul-nameless/qi/internal/config"
)

type AskFunc func(dir, header string, lines []string) bool

type hookEntry struct {
	id, full, label string
}

type hookSet struct {
	fingerprint string
	entries     []hookEntry
}

type trustEntry struct {
	dir         string
	fingerprint string
	pairs       map[string]bool
	ids         map[string]bool
}

func trustFile() string {
	return filepath.Join(config.Dir(), "trusted")
}

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:6])
}

func projectHookSet(files []HookFile) hookSet {
	byFull := map[string]hookEntry{}
	for _, f := range files {
		for ev, ms := range f.events {
			for _, m := range ms {
				for _, c := range m.Hooks {
					if c.Command == "" {
						continue
					}
					full := strings.Join([]string{ev, m.Matcher, c.Type, c.Command, strconv.Itoa(c.Timeout)}, "\x00")
					byFull[full] = hookEntry{id: shortHash(ev + "\x00" + c.Command), full: shortHash(full), label: ev + ": " + c.Command}
				}
			}
		}
	}
	fulls := make([]string, 0, len(byFull))
	for k := range byFull {
		fulls = append(fulls, k)
	}
	slices.Sort(fulls)
	set := hookSet{}
	for _, k := range fulls {
		set.entries = append(set.entries, byFull[k])
	}
	if len(fulls) > 0 {
		sum := sha256.Sum256([]byte(strings.Join(fulls, "\n")))
		set.fingerprint = hex.EncodeToString(sum[:])
	}
	return set
}

func (s hookSet) line(dir string) string {
	pairs := make([]string, len(s.entries))
	for i, e := range s.entries {
		pairs[i] = e.id + ":" + e.full
	}
	return dir + "\t" + s.fingerprint + "\t" + strings.Join(pairs, ",")
}

func parseTrustLine(line string) trustEntry {
	parts := strings.Split(line, "\t")
	n := len(parts)
	if n < 3 || !isSHA256(parts[n-2]) {
		return trustEntry{dir: strings.TrimSpace(line)}
	}
	e := trustEntry{dir: strings.Join(parts[:n-2], "\t"), fingerprint: parts[n-2], pairs: map[string]bool{}, ids: map[string]bool{}}
	for _, p := range strings.Split(parts[n-1], ",") {
		if id, _, ok := strings.Cut(p, ":"); ok {
			e.pairs[p], e.ids[id] = true, true
		}
	}
	return e
}

func isSHA256(s string) bool {
	_, err := hex.DecodeString(s)
	return len(s) == 64 && err == nil
}

func trustLines() ([]string, error) {
	data, err := os.ReadFile(trustFile())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return strings.Split(strings.TrimRight(string(data), "\n"), "\n"), nil
}

func lookupTrust(dir string) (trustEntry, bool) {
	lines, _ := trustLines()
	var found trustEntry
	ok := false
	for _, l := range lines {
		if e := parseTrustLine(l); e.dir == dir {
			found, ok = e, true
		}
	}
	return found, ok
}

func trust(dir string, set hookSet) error {
	lines, err := trustLines()
	if err != nil {
		return err
	}
	var keep []string
	for _, l := range lines {
		if e := parseTrustLine(l); e.dir != dir && e.dir != "" {
			keep = append(keep, l)
		}
	}
	keep = append(keep, set.line(dir))
	if err := os.MkdirAll(config.Dir(), 0o700); err != nil {
		return err
	}
	tmp := trustFile() + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(keep, "\n")+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, trustFile())
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

func splitHookFiles(files []HookFile) (user, project []HookFile) {
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.Path
	}
	userPaths, _ := SplitHookFiles(paths)
	for _, f := range files {
		if slices.Contains(userPaths, f.Path) {
			user = append(user, f)
		} else {
			project = append(project, f)
		}
	}
	return user, project
}

func describeChange(prev trustEntry, found bool, set hookSet) (string, []string) {
	var lines []string
	if !found || prev.fingerprint == "" {
		for _, e := range set.entries {
			lines = append(lines, strconv.Quote(e.label))
		}
		if !found {
			return "This project defines hooks that run shell commands:", lines
		}
		return "qi now remembers which hooks you trusted; confirm this project's hooks once more:", lines
	}
	current := map[string]bool{}
	for _, e := range set.entries {
		pair := e.id + ":" + e.full
		current[e.id] = true
		switch {
		case prev.pairs[pair]:
		case prev.ids[e.id]:
			lines = append(lines, "changed: "+strconv.Quote(e.label)+" (matcher or timeout)")
		default:
			lines = append(lines, "new: "+strconv.Quote(e.label))
		}
	}
	header := "This project's hooks changed since you trusted it:"
	removed := 0
	for p := range prev.pairs {
		if id, _, _ := strings.Cut(p, ":"); !current[id] {
			removed++
		}
	}
	if removed > 0 {
		header = fmt.Sprintf("This project's hooks changed since you trusted it (%d removed):", removed)
	}
	return header, lines
}

func TrustPrompt(in io.Reader, out io.Writer) AskFunc {
	return func(dir, header string, lines []string) bool {
		fmt.Fprintln(out, header)
		for _, l := range lines {
			fmt.Fprintln(out, "  "+l)
		}
		fmt.Fprint(out, "Trust "+dir+" and run its hooks? [y/N] ")
		line, _ := bufio.NewReader(in).ReadString('\n')
		return approved(line)
	}
}

func SelectHookFiles(root string, files []HookFile, interactive bool, ask AskFunc) (selected []HookFile, skipped bool, err error) {
	user, project := splitHookFiles(files)
	all := append(user, project...)
	set := projectHookSet(project)
	if set.fingerprint == "" {
		return all, false, nil
	}
	prev, found := lookupTrust(root)
	if found && prev.fingerprint == set.fingerprint {
		return all, false, nil
	}
	if !interactive {
		return user, true, nil
	}
	header, lines := describeChange(prev, found, set)
	if !ask(root, header, lines) {
		return user, true, nil
	}
	if err := trust(root, set); err != nil {
		return all, false, fmt.Errorf("could not save trust for %s: %w", root, err)
	}
	return all, false, nil
}

func approved(answer string) bool {
	return strings.ToLower(strings.TrimSpace(answer)) == "y"
}
