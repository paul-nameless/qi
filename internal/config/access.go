package config

import (
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
	var a Access
	raw, ok := os.LookupEnv(AccessEnv)
	if !ok {
		return a, false, nil
	}
	_ = os.Unsetenv(AccessEnv)
	if err := json.Unmarshal([]byte(raw), &a, json.RejectUnknownMembers(true)); err != nil {
		return a, true, fmt.Errorf("%s from the parent agent: %w", AccessEnv, err)
	}
	if err := (Config{ProjectWritable: a.ProjectWritable}).checkWritable(); err != nil {
		return a, true, fmt.Errorf("%s from the parent agent: %w", AccessEnv, err)
	}
	return a, true, nil
}

func (c *Config) ApplyAccess(a Access) {
	c.ProjectWritable, c.ReadOnlyAllow, c.HookFiles = a.ProjectWritable, a.ReadOnlyAllow, a.HookFiles
}
