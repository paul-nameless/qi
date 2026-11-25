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
	t.Setenv("QI_CONFIG_DIR", dir)
	path := writeConfig(t, dir, "config.json", "{\n  \"model\": \"m\",\n  \"modle\": \"x\"\n}\n")
	_, err := Load(path, "")
	wantErr(t, err, path, `unknown key "modle"`)

	path = writeConfig(t, dir, "config.json", `{"profiles": {"good": {"model": "a"}, "bad": {"reasoning": "high"}}}`)
	_, err = Load(path, "good")
	wantErr(t, err, path, `profile "bad"`, `unknown key "reasoning"`)

	path = writeConfig(t, dir, "config.json", `{"providers": {"or": {"base_url": "u", "key": "k"}}}`)
	_, err = Load(path, "")
	wantErr(t, err, path, `unknown key "providers.or.key"`)
}

func TestJSONSyntaxAndTypeErrors(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("QI_CONFIG_DIR", dir)
	cases := map[string][]string{
		"{\n  \"model\": \"m\",\n}\n":  {"line 2", "invalid JSON"},
		`{"max_tokens": "lots"}`:       {`key "max_tokens"`, "expected a whole number, got a string"},
		`{"max_tokens": 1.5}`:          {`key "max_tokens"`, "got the number 1.5"},
		`[1]`:                          {"expected a JSON object, got an array"},
		"{\"model\": \"m\"}\n\n  junk": {"line 3, column 3", "unexpected data after the JSON object"},
		`{"Model": "x"}`:               {`unknown key "Model"`},
		`{"model": "a", "model": "b"}`: {`duplicate key "model"`},
		`{"providers": {"a": {"base_url": "x", "base_url": "y"}}}`: {`duplicate key "providers.a.base_url"`},
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

func TestByteOrderMarkIsSkipped(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("QI_CONFIG_DIR", dir)
	path := writeConfig(t, dir, "config.json", "\xef\xbb\xbf{\"model\": \"m\"}")
	if c, err := Load(path, ""); err != nil || c.Model != "m" {
		t.Errorf("BOM: %q %v", c.Model, err)
	}
}

func TestProfileKeysAreCaseSensitive(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("QI_CONFIG_DIR", dir)
	path := writeConfig(t, dir, "config.json", `{"providers": {"a": {"base_url": "u"}}, "profiles": {"p": {"Providers": {"b": {"base_url": "v"}}}}}`)
	_, err := Load(path, "")
	wantErr(t, err, `profile "p"`, `unknown key "Providers"`)
}

func TestProfilesCannotNestProfiles(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("QI_CONFIG_DIR", dir)
	path := writeConfig(t, dir, "config.json", `{"profiles": {"p": {"profiles": {"q": {"modle": 1}}}}}`)
	_, err := Load(path, "")
	wantErr(t, err, `profile "p"`, `key "profiles" is not allowed in a profile`)
	path = writeConfig(t, dir, "config.json", `{"model": "m", "profiles": {"f": {"default_profile": "x"}}}`)
	_, err = Load(path, "")
	wantErr(t, err, `profile "f"`, `key "default_profile" is not allowed in a profile`)
}

func TestNullInProfileResetsToDefault(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("QI_CONFIG_DIR", dir)
	path := writeConfig(t, dir, "config.json", `{"max_tokens": 1000, "compact_at": "50k", "read_only_allow": ["x"], "profiles": {"p": {"max_tokens": null, "compact_at": null, "read_only_allow": null}}}`)
	c, err := Load(path, "p")
	if err != nil || c.MaxTokens != 32000 || c.CompactAt != (Threshold{Tokens: 150000}) || c.ReadOnlyAllow != nil {
		t.Errorf("null should reset to defaults: %d %+v %v %v", c.MaxTokens, c.CompactAt, c.ReadOnlyAllow, err)
	}
}

func TestBoolKeysProfileOverridesGlobal(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("QI_CONFIG_DIR", dir)
	keys := map[string]func(Config) bool{"terminal_images": Config.TerminalImagesOn, "run_code": Config.RunCodeOn}
	for key, on := range keys {
		path := writeConfig(t, dir, "config.json", strings.ReplaceAll(`{"KEY": false, "profiles": {"on": {"KEY": true}, "same": {"model": "m"}, "reset": {"KEY": null}}}`, "KEY", key))
		for profile, want := range map[string]bool{"": false, "on": true, "same": false, "reset": true} {
			if c, err := Load(path, profile); err != nil || on(c) != want {
				t.Errorf("%s profile %q: %v, want %v (%v)", key, profile, on(c), want, err)
			}
		}
		path = writeConfig(t, dir, "config.json", strings.ReplaceAll(`{"profiles": {"off": {"KEY": false}}}`, "KEY", key))
		for profile, want := range map[string]bool{"": true, "off": false} {
			if c, err := Load(path, profile); err != nil || on(c) != want {
				t.Errorf("%s profile %q: %v, want %v (%v)", key, profile, on(c), want, err)
			}
		}
	}
}

func TestValidateMirrorsSchema(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("QI_CONFIG_DIR", dir)
	cases := map[string][]string{
		`{"api": "antropic"}`:                   {`key "api"`, `"antropic" is not one of openai, responses, anthropic`},
		`{"cache_ttl": "2h"}`:                   {`key "cache_ttl"`},
		`{"max_tokens": -5}`:                    {`key "max_tokens"`, "at least 1, got -5"},
		`{"bash_timeout": -1}`:                  {`key "bash_timeout"`},
		`{"max_rounds": -1}`:                    {`key "max_rounds"`},
		`{"reasoning_effort": "huge"}`:          {`key "reasoning_effort"`},
		`{"subagent_efforts": ["low", "lots"]}`: {`key "subagent_efforts.1"`},
		`{"subagent_models": [{"model": "m"}]}`: {`key "subagent_models.0"`, "name is required"},
		`{"subagent_models": [{"name": "n"}]}`:  {"model is required"},
		`{"subagent_models": [{"name": "n", "model": "m", "provider": "x"}]}`: {`key "subagent_models.0.provider"`, `unknown provider "x"`},
		`{"mcp": {"gh": {"args": ["stdio"]}}}`:                                {`key "mcp.gh"`, "command is required"},
		`{"mcp": {"gh": {"command": "gh", "tools": ["list_[a"]}}}`:            {`key "mcp.gh.tools.0"`, `invalid glob pattern "list_[a"`},
		`{"mcp": {"gh": {"command": "gh", "exclude_tools": ["ok", ""]}}}`:     {`key "mcp.gh.exclude_tools.1"`, "empty pattern"},
		`{"mcp": {"gh": {"command": "gh", "tools": []}}}`:                     {`key "mcp.gh.tools"`, "omit the key to allow all tools"},
		`{"profiles": {"p": {"api": "nope"}}, "default_profile": "p"}`:        {`profile "p"`, `key "api"`},
	}
	for in, parts := range cases {
		path := writeConfig(t, dir, "config.json", in)
		_, err := Load(path, "")
		wantErr(t, err, append(parts, path)...)
	}
	path := writeConfig(t, dir, "config.json", `{"api": "anthropic", "cache_ttl": "5m", "reasoning_effort": "max", "max_rounds": 0, "subagent_models": [{"name": "n", "model": "m", "provider": "claude"}], "mcp": {"gh": {"command": "gh", "tools": ["get_*", "list_issues"], "exclude_tools": ["*delete*"]}}}`)
	if _, err := Load(path, ""); err != nil {
		t.Error(err)
	}
	_, err := Resolve(Options{Path: path, Effort: "turbo"})
	wantErr(t, err, `key "reasoning_effort"`)
}

func TestSchemaKeyIsAccepted(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("QI_CONFIG_DIR", dir)
	path := writeConfig(t, dir, "config.json", `{"$schema": "./schema.json", "model": "m", "profiles": {"p": {"$schema": "x", "model": "n"}}}`)
	for profile, want := range map[string]string{"": "m", "p": "n"} {
		if c, err := Load(path, profile); err != nil || c.Model != want {
			t.Errorf("profile %q: model %q %v", profile, c.Model, err)
		}
	}
}

func TestProfileKeysReplaceWholeValues(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("QI_CONFIG_DIR", dir)
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

func TestUnknownProfile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("QI_CONFIG_DIR", dir)
	path := writeConfig(t, dir, "config.json", `{"model": "m"}`)
	_, err := Load(path, "nope")
	wantErr(t, err, `unknown profile "nope"`)
}

func TestMissingDefaultConfigIsFine(t *testing.T) {
	t.Setenv("QI_CONFIG_DIR", t.TempDir())
	if c, err := Load("", ""); err != nil || c.Model == "" {
		t.Errorf("no config: %+v %v", c, err)
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing.json"), ""); err == nil {
		t.Error("missing -config file accepted")
	}
}

func TestCheckLoadsEveryProfile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("QI_CONFIG_DIR", dir)
	path := writeConfig(t, dir, "config.json", `{"profiles": {"ok": {"model": "m"}, "broken": {"provider": "nope"}}}`)
	wantErr(t, Check(path, ""), path, `profile "broken": key "provider": unknown provider "nope"`)

	path = writeConfig(t, dir, "config.json", `{"profiles": {"ok": {"model": "m"}}}`)
	writeConfig(t, dir, "settings.json", `{"hooks": {}}`)
	if err := Check(path, ""); err != nil {
		t.Error(err)
	}
	writeConfig(t, dir, "side.json", `{"max_tokens": 0.5}`)
	if err := Check(path, ""); err != nil {
		t.Errorf("other json files in the config dir are not profiles: %v", err)
	}
}

type schemaNode struct {
	Ref                  string                     `json:"$ref"`
	Description          string                     `json:"description"`
	Properties           map[string]schemaNode      `json:"properties"`
	AdditionalProperties json.RawMessage            `json:"additionalProperties"`
	Defs                 map[string]json.RawMessage `json:"$defs"`
	Items                *schemaNode                `json:"items"`
	Enum                 []string                   `json:"enum"`
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

func schemaDef(t *testing.T, root schemaNode, name string, v any) {
	t.Helper()
	if err := json.Unmarshal(root.Defs[name], v); err != nil {
		t.Fatalf("$defs.%s: %v", name, err)
	}
}

func TestSchemaMatchesConfig(t *testing.T) {
	var root schemaNode
	if err := json.Unmarshal(Schema, &root); err != nil {
		t.Fatalf("schema.json: %v", err)
	}
	checkObject(t, "root", root, reflect.TypeFor[Config]())
	defs := map[string]reflect.Type{"provider": reflect.TypeFor[Provider](), "mcp_server": reflect.TypeFor[MCPServer](), "subagent_model": reflect.TypeFor[SubagentModel]()}
	nodes := map[string]schemaNode{}
	for name, typ := range defs {
		var node schemaNode
		schemaDef(t, root, name, &node)
		checkObject(t, name, node, typ)
		nodes[name] = node
	}
	refs := map[string]string{"providers": "#/$defs/provider", "profiles": "#/$defs/profile"}
	for key, want := range refs {
		if got := additionalRef(t, root.Properties[key]); got != want {
			t.Errorf("%s values: $ref %q, want %q", key, got, want)
		}
	}
	var mcpValue struct {
		AnyOf []map[string]string `json:"anyOf"`
	}
	if err := json.Unmarshal(root.Properties["mcp"].AdditionalProperties, &mcpValue); err != nil || !reflect.DeepEqual(mcpValue.AnyOf, []map[string]string{{"$ref": "#/$defs/mcp_server"}, {"type": "null"}}) {
		t.Errorf("mcp values must be an mcp_server or null (to remove a global server in a profile): %+v %v", mcpValue, err)
	}
	if items := root.Properties["subagent_models"].Items; items == nil || items.Ref != "#/$defs/subagent_model" {
		t.Errorf("subagent_models items: %+v", items)
	}
	enums := map[string][2][]string{
		"api":              {root.Properties["api"].Enum, apis},
		"provider.api":     {nodes["provider"].Properties["api"].Enum, apis},
		"cache_ttl":        {root.Properties["cache_ttl"].Enum, cacheTTLs},
		"reasoning_effort": {root.Properties["reasoning_effort"].Enum, Efforts},
		"subagent_efforts": {root.Properties["subagent_efforts"].Items.Enum, Efforts},
		"mode":             {root.Properties["mode"].Enum, modeNames},
	}
	for key, e := range enums {
		if !slices.Equal(e[0], e[1]) {
			t.Errorf("%s: schema enum %v, code accepts %v", key, e[0], e[1])
		}
	}
	var profile struct {
		Ref        string          `json:"$ref"`
		Properties map[string]bool `json:"properties"`
	}
	schemaDef(t, root, "profile", &profile)
	if profile.Ref != "#" || len(profile.Properties) != 2 || profile.Properties["profiles"] || profile.Properties["default_profile"] {
		t.Errorf("profile definition must be the root minus profiles and default_profile: %+v", profile)
	}
}
