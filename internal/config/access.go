package config

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"os"
)

const AccessEnv = "QI_ACCESS"

type Access struct {
	ProjectWritable []string `json:"project_writable"`
	ReadOnlyAllow   []string `json:"read_only_allow"`
	HookFiles       []string `json:"hook_files"`
}

func EncodeAccess(a Access) (string, error) {
	data, err := json.Marshal(a, json.Deterministic(true), json.FormatNilSliceAsNull(true))
	if err != nil {
		return "", fmt.Errorf("encode access: %w", err)
	}
	return string(data), nil
}

func AccessFromEnv() (Access, bool, error) {
	raw, ok := os.LookupEnv(AccessEnv)
	if !ok {
		return Access{}, false, nil
	}
	_ = os.Unsetenv(AccessEnv)
	a, err := decodeAccess(raw)
	if err != nil {
		return Access{}, true, fmt.Errorf("%s from the parent agent: %w", AccessEnv, err)
	}
	return a, true, nil
}

func decodeAccess(raw string) (Access, error) {
	var a Access
	var keys map[string]jsontext.Value
	if err := json.Unmarshal([]byte(raw), &keys); err != nil {
		return a, err
	}
	if keys == nil {
		return a, fmt.Errorf("want an object, got %q", raw)
	}
	for _, k := range []string{"project_writable", "read_only_allow", "hook_files"} {
		if _, ok := keys[k]; !ok {
			return a, fmt.Errorf("key %q is missing", k)
		}
	}
	if err := json.Unmarshal([]byte(raw), &a, json.RejectUnknownMembers(true)); err != nil {
		return a, err
	}
	if err := (Config{ProjectWritable: a.ProjectWritable}).checkWritable(); err != nil {
		return a, err
	}
	return a, nil
}

func (c *Config) ApplyAccess(a Access) {
	c.ProjectWritable, c.ReadOnlyAllow, c.HookFiles = a.ProjectWritable, a.ReadOnlyAllow, a.HookFiles
}
