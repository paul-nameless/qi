package config

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

//go:embed schema.json
var Schema []byte

func decodeStrict(data []byte, v any) error {
	if len(bytes.TrimSpace(data)) == 0 {
		return errors.New("empty file, expected a JSON object")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return explain(data, err)
	}
	if _, err := dec.Token(); err != io.EOF {
		line, _ := position(data, dec.InputOffset())
		return fmt.Errorf("line %d: unexpected data after the JSON object", line)
	}
	return nil
}

func explain(data []byte, err error) error {
	var syntax *json.SyntaxError
	var typ *json.UnmarshalTypeError
	switch {
	case errors.As(err, &syntax):
		line, col := position(data, syntax.Offset-1)
		return fmt.Errorf("line %d, column %d: invalid JSON: %s", line, col, syntax.Error())
	case errors.As(err, &typ):
		return fmt.Errorf("key %q: expected %s, got %s", typ.Field, jsonKind(typ.Type), typ.Value)
	case errors.Is(err, io.ErrUnexpectedEOF):
		return errors.New("invalid JSON: unexpected end of file")
	}
	msg := strings.TrimPrefix(err.Error(), "json: ")
	if field, ok := strings.CutPrefix(msg, "unknown field "); ok {
		return fmt.Errorf("unknown key %s", field)
	}
	if key := failingKey(data); key != "" {
		return fmt.Errorf("key %q: %s", key, msg)
	}
	return errors.New(msg)
}

var unmarshalerType = reflect.TypeFor[json.Unmarshaler]()

func failingKey(data []byte) string {
	var keys map[string]json.RawMessage
	if json.Unmarshal(data, &keys) != nil {
		return ""
	}
	for f := range reflect.TypeFor[Config]().Fields() {
		raw, ok := keys[jsonName(f)]
		if !ok || !reflect.PointerTo(f.Type).Implements(unmarshalerType) {
			continue
		}
		if json.Unmarshal(raw, reflect.New(f.Type).Interface()) != nil {
			return jsonName(f)
		}
	}
	return ""
}

func position(data []byte, offset int64) (int, int) {
	prefix := data[:min(int(offset), len(data))]
	line := bytes.Count(prefix, []byte("\n")) + 1
	col := len(prefix) - bytes.LastIndexByte(prefix, '\n')
	return line, col
}

func jsonKind(t reflect.Type) string {
	switch t.Kind() {
	case reflect.String:
		return "a string"
	case reflect.Bool:
		return "true or false"
	case reflect.Int, reflect.Int64, reflect.Int32, reflect.Float64:
		return "a number"
	case reflect.Slice:
		return "an array"
	case reflect.Map, reflect.Struct:
		return "an object"
	case reflect.Pointer:
		return jsonKind(t.Elem())
	}
	return t.String()
}

func clearKeys(c *Config, raw []byte) error {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		return explain(raw, err)
	}
	v := reflect.ValueOf(c).Elem()
	t := v.Type()
	for i := range t.NumField() {
		f := t.Field(i)
		if _, ok := keys[jsonName(f)]; ok && f.IsExported() {
			v.Field(i).SetZero()
		}
	}
	return nil
}

func jsonName(f reflect.StructField) string {
	name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
	return name
}

func rejectYAML(path string) error {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".yml", ".yaml":
		return fmt.Errorf("%s: YAML config is no longer supported; %s", ShortHome(path), convertHint(path))
	}
	return nil
}

func legacyYAML(path string) error {
	if filepath.Ext(path) != ".json" {
		return nil
	}
	yml := strings.TrimSuffix(path, ".json") + ".yml"
	if _, err := os.Stat(yml); err != nil {
		return nil
	}
	return fmt.Errorf("found %s but no %s: aicode now reads JSON config; %s", ShortHome(yml), ShortHome(path), convertHint(yml))
}

func convertHint(yml string) string {
	out := strings.TrimSuffix(yml, filepath.Ext(yml)) + ".json"
	return fmt.Sprintf("convert it with the same keys, e.g. yq -o json %s > %s (keys aicode no longer knows are now errors)", ShortHome(yml), ShortHome(out))
}
