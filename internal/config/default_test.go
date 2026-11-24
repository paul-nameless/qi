package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSetTopLevelString(t *testing.T) {
	cases := []struct{ in, want string }{
		{"{\n  \"model\": \"m\",\n  \"default_profile\": \"luna\"\n}\n", "{\n  \"model\": \"m\",\n  \"default_profile\": \"opus\"\n}\n"},
		{"{\n  \"model\": \"m\"\n}\n", "{\n  \"default_profile\": \"opus\",\n  \"model\": \"m\"\n}\n"},
		{"{}\n", "{\"default_profile\": \"opus\"}\n"},
		{"{\"profiles\": {\"p\": {\"default_profile\": \"x\"}}, \"default_profile\": \"luna\"}", "{\"profiles\": {\"p\": {\"default_profile\": \"x\"}}, \"default_profile\": \"opus\"}"},
	}
	for _, c := range cases {
		got, err := setTopLevel([]byte(c.in), "default_profile", []byte(`"opus"`))
		if err != nil || string(got) != c.want {
			t.Errorf("in %q:\n got %q, %v\nwant %q", c.in, got, err, c.want)
		}
	}
	if _, err := setTopLevel([]byte("[1]"), "default_profile", []byte(`"x"`)); err == nil {
		t.Error("non-object accepted")
	}
}

func TestSetDefaultProfileKeepsFileValid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	orig := "{\n  \"profiles\": {\n    \"luna\": {\"model\": \"a\"},\n    \"opus\": {\"model\": \"b\"}\n  },\n  \"default_profile\": \"luna\"\n}\n"
	if err := os.WriteFile(path, []byte(orig), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := SetDefaultProfile(path, "opus"); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path, "")
	if err != nil || c.Profile != "opus" || c.Model != "b" {
		t.Fatalf("after switch: profile %q model %q err %v", c.Profile, c.Model, err)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o640 {
		t.Errorf("perm changed to %v", st.Mode().Perm())
	}
}

func TestSetInWritesWhereTheKeyIsDefined(t *testing.T) {
	in := "{\n  \"run_code\": true,\n  \"profiles\": {\n    \"a\": {\"model\": \"m\", \"run_code\": true},\n    \"b\": {\"model\": \"m\"}\n  }\n}\n"
	cases := []struct {
		profile, key, want string
		inProfile          bool
	}{
		{"a", "run_code", "{\n  \"run_code\": true,\n  \"profiles\": {\n    \"a\": {\"model\": \"m\", \"run_code\": false},\n    \"b\": {\"model\": \"m\"}\n  }\n}\n", true},
		{"b", "run_code", "{\n  \"run_code\": false,\n  \"profiles\": {\n    \"a\": {\"model\": \"m\", \"run_code\": true},\n    \"b\": {\"model\": \"m\"}\n  }\n}\n", false},
		{"", "show_reasoning", "{\n  \"show_reasoning\": false,\n  \"run_code\": true,\n  \"profiles\": {\n    \"a\": {\"model\": \"m\", \"run_code\": true},\n    \"b\": {\"model\": \"m\"}\n  }\n}\n", false},
	}
	for _, c := range cases {
		got, inProfile, err := setIn([]byte(in), c.profile, c.key, []byte("false"))
		if err != nil || string(got) != c.want || inProfile != c.inProfile {
			t.Errorf("%s/%s: inProfile=%v err=%v\n%s", c.profile, c.key, inProfile, err, got)
		}
	}
}
