package oracle

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
)

var (
	notDefinedRe = regexp.MustCompile(`(?:^|ReferenceError: |[\s:])'?([A-Za-z_$][\w$]*)'? is not defined`)
	notFuncRe    = regexp.MustCompile(`(?:^|TypeError: |\s)(?:[\w$.\[\]'"()]*\.)?([A-Za-z_$][\w$]*) is not a function`)
)

func missingGlobal(line string) bool {
	m := notDefinedRe.FindStringSubmatch(line)
	return m != nil && builtinGlobals[m[1]]
}

func missingBuiltin(line string) bool {
	if m := notDefinedRe.FindStringSubmatch(line); m != nil {
		return builtinGlobals[m[1]]
	}
	if m := notFuncRe.FindStringSubmatch(line); m != nil {
		return builtinMethods[m[1]] || builtinGlobals[m[1]]
	}
	return false
}

func loudLine(line string) bool {
	return strings.Contains(line, "unsupported:") || missingBuiltin(line)
}

func Loud(exp, alt, act string) bool {
	le, lx := strings.Split(exp, "\n"), strings.Split(act, "\n")
	la := strings.Split(alt, "\n")
	for i, l := range lx {
		if !loudLine(l) {
			continue
		}
		for j := 0; j < i; j++ {
			if j >= len(le) {
				return false
			}
			if lx[j] != le[j] && (alt == "" || j >= len(la) || lx[j] != la[j]) {
				return false
			}
		}
		return true
	}
	return false
}

type Allowlist struct {
	Tags map[string]string `json:"tags"`
	IDs  map[string]string `json:"ids"`
}

func LoadAllowlist(path string) (*Allowlist, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var a Allowlist
	if err := json.Unmarshal(b, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

func (a *Allowlist) Bucket(id string, tags []string) string {
	if a == nil {
		return ""
	}
	if b, ok := a.IDs[id]; ok {
		return b
	}
	for _, t := range tags {
		if b, ok := a.Tags[t]; ok {
			return b
		}
	}
	return ""
}
