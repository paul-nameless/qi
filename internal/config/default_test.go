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
		got, err := setTopLevelString([]byte(c.in), "default_profile", "opus")
		if err != nil || string(got) != c.want {
			t.Errorf("in %q:\n got %q, %v\nwant %q", c.in, got, err, c.want)
		}
	}
	if _, err := setTopLevelString([]byte("[1]"), "default_profile", "x"); err == nil {
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
