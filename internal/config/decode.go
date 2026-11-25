package config

import (
	"bytes"
	_ "embed"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"
)

//go:embed schema.json
var Schema []byte

func decodeStrict(data []byte, v any) error {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if len(bytes.TrimSpace(data)) == 0 {
		return errors.New("empty file, expected a JSON object")
	}
	if err := json.Unmarshal(data, v, json.RejectUnknownMembers(true)); err != nil {
		return explain(data, err)
	}
	return nil
}

type badValue struct{ error }

func explain(data []byte, err error) error {
	var syntax *jsontext.SyntacticError
	var semantic *json.SemanticError
	switch {
	case errors.As(err, &syntax):
		return syntaxError(data, syntax)
	case errors.As(err, &semantic):
		return semanticError(semantic)
	}
	return err
}

func syntaxError(data []byte, e *jsontext.SyntacticError) error {
	line, col := position(data, e.ByteOffset)
	switch {
	case errors.Is(e.Err, jsontext.ErrDuplicateName):
		return fmt.Errorf("line %d: duplicate key %q", line, keyPath(e.JSONPointer))
	case errors.Is(e.Err, io.ErrUnexpectedEOF):
		return errors.New("invalid JSON: unexpected end of file")
	case strings.Contains(e.Err.Error(), "after top-level value"):
		return fmt.Errorf("line %d, column %d: unexpected data after the JSON object", line, col)
	}
	return fmt.Errorf("line %d, column %d: invalid JSON: %v", line, col, e.Err)
}

var removedKeys = map[string]string{
	"subagent_efforts": "subagents offer the same efforts as reasoning_effort",
	"subagent_models":  "replaced by subagent_profiles, a list of profile names, and use_for on those profiles",
}

func semanticError(e *json.SemanticError) error {
	key := keyPath(e.JSONPointer)
	var bad badValue
	switch {
	case errors.Is(e, json.ErrUnknownName):
		if why, ok := removedKeys[key]; ok {
			return fmt.Errorf("key %q was removed (%s): delete it", key, why)
		}
		return fmt.Errorf("unknown key %q", key)
	case key == "":
		return fmt.Errorf("expected a JSON object, got %s", kindName(e.JSONKind, nil))
	case errors.As(e.Err, &bad):
		return fmt.Errorf("key %q: %v", key, bad.error)
	}
	return fmt.Errorf("key %q: expected %s, got %s", key, goKind(e.GoType), kindName(e.JSONKind, e.JSONValue))
}

func keyPath(p jsontext.Pointer) string {
	return strings.Join(slices.Collect(p.Tokens()), ".")
}

func kindName(k jsontext.Kind, v jsontext.Value) string {
	switch k {
	case '"':
		return "a string"
	case '0':
		if len(v) > 0 {
			return "the number " + string(v)
		}
		return "a number"
	case 't', 'f':
		return "a boolean"
	case 'n':
		return "null"
	case '{':
		return "an object"
	case '[':
		return "an array"
	}
	return "an invalid value"
}

func position(data []byte, offset int64) (int, int) {
	prefix := data[:min(int(offset), len(data))]
	line := bytes.Count(prefix, []byte("\n")) + 1
	col := len(prefix) - bytes.LastIndexByte(prefix, '\n')
	return line, col
}

func goKind(t reflect.Type) string {
	if t == nil {
		return "another type"
	}
	switch t.Kind() {
	case reflect.String:
		return "a string"
	case reflect.Bool:
		return "true or false"
	case reflect.Int, reflect.Int64, reflect.Int32:
		return "a whole number"
	case reflect.Slice:
		return "an array"
	case reflect.Map, reflect.Struct:
		return "an object"
	case reflect.Pointer:
		return goKind(t.Elem())
	}
	return t.String()
}

func clearKeys(c *Config, raw []byte) error {
	keys, err := objectKeys(raw)
	if err != nil {
		return err
	}
	v := reflect.ValueOf(c).Elem()
	for _, f := range reflect.VisibleFields(v.Type()) {
		if _, ok := keys[jsonName(f)]; ok && f.IsExported() {
			v.FieldByIndex(f.Index).SetZero()
		}
	}
	return nil
}

func objectKeys(raw []byte) (map[string]jsontext.Value, error) {
	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
	var keys map[string]jsontext.Value
	if err := json.Unmarshal(raw, &keys); err != nil {
		return nil, explain(raw, err)
	}
	return keys, nil
}

func checkProfile(raw []byte) error {
	if err := decodeStrict(raw, &Config{}); err != nil {
		return err
	}
	keys, err := objectKeys(raw)
	if err != nil {
		return err
	}
	for _, k := range []string{"profiles", "default_profile"} {
		if _, ok := keys[k]; ok {
			return fmt.Errorf("key %q is not allowed in a profile", k)
		}
	}
	return nil
}

func jsonName(f reflect.StructField) string {
	name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
	return name
}
