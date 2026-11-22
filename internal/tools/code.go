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
	Name   string
	Params []string
	Alias  map[string]string
	Doc    string
}

type ctxKey int

const (
	cellKey ctxKey = iota
	callKey
)

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
	return "run", silentUI{}
}

func withCell(ctx context.Context) context.Context {
	return context.WithValue(ctx, cellKey, true)
}

func inCell(ctx context.Context) bool {
	v, _ := ctx.Value(cellKey).(bool)
	return v
}

type silentUI struct{}

func (silentUI) ToolStart(string, string)      {}
func (silentUI) ToolProgress(string, string)   {}
func (silentUI) ToolDone(string, bool, string) {}

func codeInput(vm *jsvm.VM, t *Tool, args []jsvm.Value) (json.RawMessage, error) {
	spec := t.Code
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
	return json.Marshal(fields)
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
	for k, v := range opts {
		if canon, ok := spec.Alias[k]; ok {
			k = canon
		}
		fields[k] = v
	}
	return nil
}

func checkFields(t *Tool, fields map[string]json.RawMessage) error {
	known := schemaProps(t.Def.Schema)
	if known == nil {
		return nil
	}
	var unknown []string
	for k := range fields {
		if !slices.Contains(known, k) {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return fmt.Errorf("%s: unknown option %s (known: %s)", t.Code.Name, strings.Join(unknown, ", "), strings.Join(optionNames(t.Code, known), ", "))
}

func schemaProps(schema json.RawMessage) []string {
	var s struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if json.Unmarshal(schema, &s) != nil || s.Properties == nil {
		return nil
	}
	names := make([]string, 0, len(s.Properties))
	for k := range s.Properties {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

func optionNames(spec *CodeSpec, known []string) []string {
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
