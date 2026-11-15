package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, dir, name, data string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func wantErr(t *testing.T, err error, parts ...string) {
	t.Helper()
	if err == nil {
		t.Errorf("no error, want one mentioning %q", parts)
		return
	}
	for _, p := range parts {
		if !strings.Contains(err.Error(), p) {
			t.Errorf("error %q does not mention %q", err, p)
		}
	}
}

func TestUnknownKeysAreErrors(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AICODE_CONFIG_DIR", dir)
	path := writeConfig(t, dir, "config.json", "{\n  \"model\": \"m\",\n  \"modle\": \"x\"\n}\n")
	_, err := Load(path, "")
	wantErr(t, err, path, `unknown key "modle"`)

	path = writeConfig(t, dir, "config.json", `{"profiles": {"good": {"model": "a"}, "bad": {"reasoning": "high"}}}`)
	_, err = Load(path, "good")
	wantErr(t, err, path, `profile "bad"`, `unknown key "reasoning"`)

	path = writeConfig(t, dir, "config.json", `{"providers": {"or": {"base_url": "u", "key": "k"}}}`)
	_, err = Load(path, "")
	wantErr(t, err, path, `unknown key "key"`)

	path = writeConfig(t, dir, "config.json", `{"model": "m"}`)
	file := writeConfig(t, dir, "extra.json", `{"model": "m", "debug": true}`)
	_, err = Load(path, "extra")
	wantErr(t, err, file, `unknown key "debug"`)
}

func TestJSONSyntaxAndTypeErrors(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AICODE_CONFIG_DIR", dir)
	cases := map[string][]string{
		"{\n  \"model\": \"m\",\n}\n":   {"line 3", "invalid JSON"},
		`{"max_tokens": "lots"}`:        {`key "max_tokens"`, "expected a number"},
		`{"read_only": "yes"}`:          {`key "read_only"`, "expected true or false"},
		`{"model": "m"} {"model": "n"}`: {"unexpected data after the JSON object"},
		"  \n":                          {"empty file"},
		`{"model": "m"`:                 {"unexpected end"},
	}
	for in, parts := range cases {
		path := writeConfig(t, dir, "config.json", in)
		_, err := Load(path, "")
		wantErr(t, err, append(parts, path)...)
	}
}

func TestSchemaKeyIsAccepted(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AICODE_CONFIG_DIR", dir)
	path := writeConfig(t, dir, "config.json", `{"$schema": "./schema.json", "model": "m", "profiles": {"p": {"$schema": "x", "model": "n"}}}`)
	writeConfig(t, dir, "f.json", `{"$schema": "x", "model": "f"}`)
	for profile, want := range map[string]string{"": "m", "p": "n", "f": "f"} {
		if c, err := Load(path, profile); err != nil || c.Model != want {
			t.Errorf("profile %q: model %q %v", profile, c.Model, err)
		}
	}
}

func TestProfileKeysReplaceWholeValues(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AICODE_CONFIG_DIR", dir)
	path := writeConfig(t, dir, "config.json", `{
  "providers": {"a": {"base_url": "http://a", "cache_control": true}, "b": {"base_url": "http://b"}},
  "mcp": {"x": {"command": "x", "args": ["1", "2"], "env": {"K": "v"}}},
  "subagent_models": [{"name": "s1", "model": "m1"}, {"name": "s2", "model": "m2"}],
  "read_only_allow": ["a", "b"],
  "provider": "a",
  "profiles": {
    "p": {
      "providers": {"a": {"base_url": "http://a2"}, "c": {"base_url": "http://c"}},
      "mcp": {"x": {"command": "x2"}},
      "subagent_models": [{"name": "s3", "model": "m3"}],
      "read_only_allow": ["c"],
      "cache_control": false
    },
    "q": {"model": "only-model"}
  }
}`)
	c, err := Load(path, "p")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Providers["b"]; ok || c.Providers["a"].BaseURL != "http://a2" || c.Providers["a"].CacheControl != nil || c.BaseURL != "http://a2" {
		t.Errorf("providers not replaced: %+v base %q", c.Providers, c.BaseURL)
	}
	if len(c.MCP) != 1 || c.MCP["x"].Command != "x2" || c.MCP["x"].Args != nil || c.MCP["x"].Env != nil {
		t.Errorf("mcp not replaced: %+v", c.MCP)
	}
	if len(c.SubagentModels) != 1 || !slices.Equal(c.ReadOnlyAllow, []string{"c"}) {
		t.Errorf("arrays not replaced: %+v %v", c.SubagentModels, c.ReadOnlyAllow)
	}
	if c.CacheControl == nil || *c.CacheControl {
		t.Errorf("profile cache_control: %v", c.CacheControl)
	}
	q, err := Load(path, "q")
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Providers) != 2 || len(q.MCP) != 1 || len(q.SubagentModels) != 2 || q.CacheControl == nil || !*q.CacheControl {
		t.Errorf("profile without those keys must keep the shared values: %+v", q)
	}
	if err := q.UseProvider("b"); err != nil || q.CacheControl != nil {
		t.Errorf("switching provider: %v %v", q.CacheControl, err)
	}
	if a := q.Providers["a"].CacheControl; a == nil || !*a {
		t.Errorf("provider a's cache_control changed through a shared pointer: %v", a)
	}
}

func TestYAMLConfigGivesMigrationHint(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AICODE_CONFIG_DIR", dir)
	writeConfig(t, dir, "config.yml", "model: m\n")
	_, err := Load("", "")
	wantErr(t, err, "config.yml", "config.json", "yq -o json")

	_, err = Load(filepath.Join(dir, "config.yml"), "")
	wantErr(t, err, "YAML config is no longer supported", "yq -o json")

	path := writeConfig(t, dir, "config.json", `{"model": "m"}`)
	writeConfig(t, dir, "commit.yml", "model: c\n")
	_, err = Load(path, "commit")
	wantErr(t, err, "commit.yml", "commit.json")

	_, err = Load(path, "nope")
	wantErr(t, err, `unknown profile "nope"`)
}

func TestMissingDefaultConfigIsFine(t *testing.T) {
	t.Setenv("AICODE_CONFIG_DIR", t.TempDir())
	if c, err := Load("", ""); err != nil || c.Model == "" {
		t.Errorf("no config: %+v %v", c, err)
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing.json"), ""); err == nil {
		t.Error("missing -config file accepted")
	}
}

func TestCheckLoadsEveryProfile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AICODE_CONFIG_DIR", dir)
	path := writeConfig(t, dir, "config.json", `{"profiles": {"ok": {"model": "m"}, "broken": {"provider": "nope"}}}`)
	wantErr(t, Check(path, ""), `profile "broken"`, `unknown provider "nope"`)
	path = writeConfig(t, dir, "config.json", `{"profiles": {"ok": {"model": "m"}}}`)
	if err := Check(path, ""); err != nil {
		t.Error(err)
	}
}

type schemaNode struct {
	Ref                  string                `json:"$ref"`
	Description          string                `json:"description"`
	Properties           map[string]schemaNode `json:"properties"`
	AdditionalProperties json.RawMessage       `json:"additionalProperties"`
	Defs                 map[string]schemaNode `json:"$defs"`
	Items                *schemaNode           `json:"items"`
}

func jsonFields(t reflect.Type) []string {
	var out []string
	for f := range t.Fields() {
		if name := jsonName(f); f.IsExported() && name != "-" && name != "" {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func checkObject(t *testing.T, where string, node schemaNode, typ reflect.Type) {
	t.Helper()
	var props []string
	for name, p := range node.Properties {
		props = append(props, name)
		if p.Description == "" {
			t.Errorf("%s.%s has no description", where, name)
		}
	}
	sort.Strings(props)
	if fields := jsonFields(typ); !slices.Equal(fields, props) {
		t.Errorf("%s: struct %s fields %v, schema properties %v", where, typ.Name(), fields, props)
	}
	if string(node.AdditionalProperties) != "false" {
		t.Errorf("%s must set additionalProperties to false", where)
	}
}

func additionalRef(t *testing.T, node schemaNode) string {
	t.Helper()
	var ref schemaNode
	if err := json.Unmarshal(node.AdditionalProperties, &ref); err != nil {
		t.Fatal(err)
	}
	return ref.Ref
}

func TestSchemaMatchesConfig(t *testing.T) {
	var root schemaNode
	if err := json.Unmarshal(Schema, &root); err != nil {
		t.Fatalf("schema.json: %v", err)
	}
	checkObject(t, "root", root, reflect.TypeFor[Config]())
	checkObject(t, "provider", root.Defs["provider"], reflect.TypeFor[Provider]())
	checkObject(t, "mcp_server", root.Defs["mcp_server"], reflect.TypeFor[MCPServer]())
	checkObject(t, "subagent_model", root.Defs["subagent_model"], reflect.TypeFor[SubagentModel]())
	refs := map[string]string{"providers": "#/$defs/provider", "mcp": "#/$defs/mcp_server", "profiles": "#"}
	for key, want := range refs {
		if got := additionalRef(t, root.Properties[key]); got != want {
			t.Errorf("%s values: $ref %q, want %q", key, got, want)
		}
	}
	if items := root.Properties["subagent_models"].Items; items == nil || items.Ref != "#/$defs/subagent_model" {
		t.Errorf("subagent_models items: %+v", items)
	}
}
