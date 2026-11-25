package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/paul-nameless/qi/internal/jsvm"
)

type CodeSpec struct {
	Name     string
	Params   []string
	Required int
	Options  []string
	Alias    map[string]string
	Defaults map[string]string
	Doc      string
}

type ctxKey int

const callKey ctxKey = 0

type callInfo struct {
	id string
	ui UI
}

func withCall(ctx context.Context, id string, ui UI) context.Context {
	return context.WithValue(ctx, callKey, callInfo{id: id, ui: ui})
}

func callFrom(ctx context.Context) (string, UI) {
	if c, ok := ctx.Value(callKey).(callInfo); ok && c.ui != nil {
		return c.id, c.ui
	}
	return RunName, silentUI{}
}

type silentUI struct{}

func (silentUI) ToolStart(string, string)      {}
func (silentUI) ToolProgress(string, string)   {}
func (silentUI) ToolDone(string, bool, string) {}

func codeInput(vm *jsvm.VM, t *Function, args []jsvm.Value) (json.RawMessage, error) {
	spec := &t.Code
	jsonArgs := make([]string, len(args))
	for i, a := range args {
		s, err := vm.JSONStringify(a)
		if err != nil {
			return nil, fmt.Errorf("%s: argument %d cannot be converted to JSON: %v", spec.Name, i+1, err)
		}
		jsonArgs[i] = s
	}
	for len(jsonArgs) > 0 && jsonArgs[len(jsonArgs)-1] == "" {
		jsonArgs = jsonArgs[:len(jsonArgs)-1]
	}
	fields := map[string]json.RawMessage{}
	switch {
	case len(jsonArgs) == 1 && isObjectJSON(jsonArgs[0]) && len(spec.Params) > 0 && spec.Params[0] != "arguments":
		if err := mergeOptions(fields, spec, jsonArgs[0]); err != nil {
			return nil, err
		}
	default:
		if err := positional(fields, spec, jsonArgs); err != nil {
			return nil, err
		}
	}
	if err := checkFields(t, fields); err != nil {
		return nil, err
	}
	applyDefaults(fields, spec)
	if err := checkRequired(t, fields); err != nil {
		return nil, err
	}
	return json.Marshal(fields)
}

func applyDefaults(fields map[string]json.RawMessage, spec *CodeSpec) {
	for name, v := range spec.Defaults {
		if cur, ok := fields[name]; !ok || string(cur) == "null" {
			fields[name] = json.RawMessage(v)
		}
	}
}

var jsArgNames = map[string]string{"file_path": "path", "old_string": "old", "new_string": "new", "arguments": "args"}

func checkRequired(t *Function, fields map[string]json.RawMessage) error {
	for _, name := range t.Code.Params[:min(t.Code.Required, len(t.Code.Params))] {
		v, ok := fields[name]
		switch {
		case !ok:
			return fmt.Errorf("%s: %s is undefined", t.Code.Name, jsArgName(name))
		case string(v) == "null":
			return fmt.Errorf("%s: %s is null", t.Code.Name, jsArgName(name))
		}
	}
	return nil
}

func jsArgName(name string) string {
	if js, ok := jsArgNames[name]; ok {
		return js + " (" + name + ")"
	}
	return name
}

func positional(fields map[string]json.RawMessage, spec *CodeSpec, args []string) error {
	for i, a := range args {
		switch {
		case i < len(spec.Params):
			if a != "" {
				fields[spec.Params[i]] = json.RawMessage(a)
			}
		case i == len(spec.Params) && isObjectJSON(a):
			if err := mergeOptions(fields, spec, a); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%s takes %s", spec.Name, usage(spec))
		}
	}
	return nil
}

func usage(spec *CodeSpec) string {
	if len(spec.Params) == 0 {
		return "only an options object"
	}
	return fmt.Sprintf("at most %d arguments (%s) and an options object", len(spec.Params), strings.Join(spec.Params, ", "))
}

func isObjectJSON(s string) bool {
	return strings.HasPrefix(s, "{")
}

func mergeOptions(fields map[string]json.RawMessage, spec *CodeSpec, obj string) error {
	var opts map[string]json.RawMessage
	if err := json.Unmarshal([]byte(obj), &opts); err != nil {
		return fmt.Errorf("%s: bad options object: %v", spec.Name, err)
	}
	if err := aliasConflict(spec, opts); err != nil {
		return err
	}
	for k, v := range opts {
		if canon, ok := spec.Alias[k]; ok {
			k = canon
		}
		fields[k] = v
	}
	return nil
}

func aliasConflict(spec *CodeSpec, opts map[string]json.RawMessage) error {
	aliases := make([]string, 0, len(spec.Alias))
	for alias := range spec.Alias {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	for _, alias := range aliases {
		_, a := opts[alias]
		_, c := opts[spec.Alias[alias]]
		if a && c {
			return fmt.Errorf("%s: both %s and %s given", spec.Name, alias, spec.Alias[alias])
		}
	}
	return nil
}

func checkFields(t *Function, fields map[string]json.RawMessage) error {
	var unknown []string
	for k := range fields {
		if !slices.Contains(t.Code.Params, k) && !slices.Contains(t.Code.Options, k) {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return fmt.Errorf("%s: unknown option %s (known: %s)", t.Code.Name, strings.Join(unknown, ", "), strings.Join(optionNames(&t.Code), ", "))
}

func optionNames(spec *CodeSpec) []string {
	known := slices.Sorted(slices.Values(append(slices.Clone(spec.Params), spec.Options...)))
	out := make([]string, 0, len(known))
	for _, k := range known {
		name := k
		for alias, canon := range spec.Alias {
			if canon == k {
				name = alias + " (" + k + ")"
			}
		}
		out = append(out, name)
	}
	return out
}
