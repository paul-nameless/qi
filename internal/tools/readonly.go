package tools

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

var DefaultAllow = []string{
	"ls", "cat", "head", "tail", "wc", "grep", "egrep", "rg", "fd", "find", "tree", "file", "stat",
	"du", "df", "pwd", "echo", "printf", "which", "type", "diff", "sort", "uniq", "cut", "tr", "nl",
	"basename", "dirname", "realpath", "readlink", "jq", "true", "false", "test",
	"git status", "git log", "git diff", "git show", "git blame", "git branch", "git rev-parse",
	"git ls-files", "git grep",
	"go list", "go vet", "go doc", "go version", "go env",
}

var forbiddenArgs = map[string][]string{
	"find":       {"-exec", "-execdir", "-delete", "-ok", "-okdir", "-fprint", "-fprint0", "-fprintf", "-fls"},
	"fd":         {"-x", "-X", "--exec", "--exec-batch"},
	"rg":         {"--pre", "--hostname-bin"},
	"sort":       {"-o", "--output", "--compress-program"},
	"tree":       {"-o"},
	"file":       {"-C", "--compile"},
	"git":        {"--output", "-O", "--open-files-in-pager", "--ext-diff", "--textconv"},
	"git branch": {"-d", "-D", "-m", "-M", "-c", "-C", "-f", "-u", "--delete", "--move", "--copy", "--force", "--set-upstream-to", "--unset-upstream", "--edit-description", "--track", "--no-track", "--create-reflog"},
	"go":         {"-toolexec", "-exec", "-vettool", "-modfile", "-overlay", "-mod"},
	"go env":     {"-w", "-u"},
}

var branchValueFlags = map[string]bool{"--contains": true, "--no-contains": true, "--merged": true, "--no-merged": true, "--points-at": true, "--sort": true, "--format": true}

func CheckReadOnly(command, cwd string, extra []string) error {
	allow := append(append([]string{}, DefaultAllow...), extra...)
	segments, err := splitCommand(command)
	if err != nil {
		return err
	}
	for _, words := range segments {
		for _, w := range words {
			if err := checkGlob(w, cwd); err != nil {
				return err
			}
		}
		if isAssignment(words[0]) {
			return errors.New("variable assignments are not allowed")
		}
		if strings.ContainsRune(words[0], '/') {
			return fmt.Errorf("%q: run commands by name, not by path", words[0])
		}
		sub := words[0]
		if len(words) > 1 {
			sub += " " + words[1]
		}
		if !allowed(words, allow) {
			return fmt.Errorf("%q is not in the read-only allowlist", sub)
		}
		for _, key := range []string{words[0], sub} {
			for _, arg := range words[1:] {
				if words[0] == "go" && strings.HasPrefix(arg, "--") {
					arg = arg[1:]
				}
				if bad := forbiddenArg(arg, forbiddenArgs[key]); bad != "" {
					return fmt.Errorf("%s %s can write or execute", words[0], bad)
				}
			}
		}
		if err := checkPositional(words, sub); err != nil {
			return err
		}
	}
	return nil
}

func checkGlob(word, cwd string) error {
	if !strings.ContainsAny(word, "*?[") {
		return nil
	}
	if strings.Contains(word, "[:") {
		return fmt.Errorf("glob %q is not allowed in read-only mode", word)
	}
	pattern := strings.ReplaceAll(word, "[!", "[^")
	if !filepath.IsAbs(pattern) {
		pattern = filepath.Join(cwd, pattern)
	}
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return fmt.Errorf("glob %q is not allowed in read-only mode", word)
	}
	for _, m := range matches {
		if rel, err := filepath.Rel(cwd, m); err == nil && strings.HasPrefix(rel, "-") {
			return fmt.Errorf("glob %q matches a file starting with '-' (%s)", word, rel)
		}
	}
	return nil
}

func forbiddenArg(arg string, bad []string) string {
	for _, b := range bad {
		if arg == b || strings.HasPrefix(arg, b+"=") {
			return b
		}
		if len(b) == 2 && b[0] == '-' && strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") && strings.ContainsRune(arg[1:], rune(b[1])) {
			return b
		}
		name, _, _ := strings.Cut(arg, "=")
		if strings.HasPrefix(b, "--") && len(name) > 2 && strings.HasPrefix(name, "--") && strings.HasPrefix(b, name) {
			return b
		}
	}
	return ""
}

func checkPositional(words []string, sub string) error {
	var positional []string
	for i := 1; i < len(words); i++ {
		w := words[i]
		if strings.HasPrefix(w, "-") {
			continue
		}
		if sub == "git branch" && (i == 1 || branchValueFlags[words[i-1]]) {
			continue
		}
		positional = append(positional, w)
	}
	switch {
	case sub == "git branch" && len(positional) > 0 && !contains(words, "--list") && !contains(words, "-l"):
		return errors.New("git branch with a name creates a branch")
	case words[0] == "uniq" && (len(positional) > 1 || (len(positional) == 1 && strings.ContainsAny(positional[0], "*?["))):
		return errors.New("uniq with two files writes the second")
	}
	return nil
}

func contains(ws []string, s string) bool {
	for _, w := range ws {
		if w == s {
			return true
		}
	}
	return false
}

func allowed(words, allow []string) bool {
	for _, a := range allow {
		parts := strings.Fields(a)
		if len(parts) == 0 || len(words) < len(parts) {
			continue
		}
		if strings.Join(words[:len(parts)], " ") == strings.Join(parts, " ") {
			return true
		}
	}
	return false
}

func isAssignment(w string) bool {
	i := strings.IndexByte(w, '=')
	if i <= 0 {
		return false
	}
	for _, r := range w[:i] {
		if !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func splitCommand(s string) ([][]string, error) {
	var segments [][]string
	var words []string
	var cur strings.Builder
	inWord := false
	endWord := func() {
		if inWord {
			words = append(words, cur.String())
			cur.Reset()
			inWord = false
		}
	}
	endSegment := func() {
		endWord()
		if len(words) > 0 {
			segments = append(segments, words)
		}
		words = nil
	}
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case r == '\\':
			if i+1 >= len(rs) || rs[i+1] == '\n' {
				return nil, errors.New("line continuations are not allowed")
			}
			cur.WriteRune(rs[i+1])
			inWord = true
			i++
		case r == '\'':
			j := i + 1
			for j < len(rs) && rs[j] != '\'' {
				j++
			}
			if j >= len(rs) {
				return nil, errors.New("unterminated quote")
			}
			cur.WriteString(string(rs[i+1 : j]))
			inWord = true
			i = j
		case r == '"':
			j := i + 1
			for j < len(rs) && rs[j] != '"' {
				switch rs[j] {
				case '\\':
					j++
				case '`', '$':
					return nil, errors.New("expansions ($, `) are not allowed in read-only mode")
				}
				j++
			}
			if j >= len(rs) {
				return nil, errors.New("unterminated quote")
			}
			cur.WriteString(string(rs[i+1 : j]))
			inWord = true
			i = j
		case r == '`' || r == '$':
			return nil, errors.New("expansions ($, `) are not allowed in read-only mode")
		case r == '{' || r == '}':
			return nil, errors.New("brace expansion is not allowed in read-only mode")
		case r == '(' || r == ')':
			return nil, errors.New("subshells are not allowed in read-only mode")
		case r == '>':
			if inWord && !isDigits(cur.String()) {
				return nil, errors.New("output redirection is not allowed")
			}
			cur.Reset()
			inWord = false
			next, err := checkRedirect(rs, i)
			if err != nil {
				return nil, err
			}
			i = next - 1
		case r == '&' && i+1 < len(rs) && rs[i+1] == '>':
			endWord()
			next, err := checkRedirect(rs, i+1)
			if err != nil {
				return nil, err
			}
			i = next - 1
		case r == '|' || r == ';' || r == '&' || r == '\n':
			endSegment()
			if i+1 < len(rs) && (rs[i+1] == '|' || rs[i+1] == '&') {
				i++
			}
		case r == ' ' || r == '\t':
			endWord()
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	endSegment()
	return segments, nil
}

func checkRedirect(rs []rune, i int) (int, error) {
	j := i + 1
	if j < len(rs) && rs[j] == '>' {
		return 0, errors.New("output redirection is not allowed")
	}
	if j < len(rs) && rs[j] == '&' {
		k := j + 1
		for k < len(rs) && rs[k] >= '0' && rs[k] <= '9' {
			k++
		}
		if k == j+1 || (k < len(rs) && !strings.ContainsRune(" \t|;&\n", rs[k])) {
			return 0, errors.New("output redirection is not allowed")
		}
		return k, nil
	}
	for j < len(rs) && (rs[j] == ' ' || rs[j] == '\t') {
		j++
	}
	k := j
	for k < len(rs) && !strings.ContainsRune(" \t|;&\n", rs[k]) {
		k++
	}
	if string(rs[j:k]) != "/dev/null" {
		return 0, errors.New("output redirection is only allowed to /dev/null")
	}
	return k, nil
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
