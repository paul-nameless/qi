package jsvm

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestOracleCases(t *testing.T) {
	t.Parallel()
	runOracleFile(t, "testdata/oracle.json")
	runOracleFile(t, "testdata/holes_oracle.json")
}

func runOracleFile(t *testing.T, file string) {
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Code string `json:"code"`
		Out  string `json:"out"`
		Err  string `json:"err"`
		Skip string `json:"skip"`
	}
	if err := json.Unmarshal(b, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		if c.Skip != "" {
			continue
		}
		s := newSession(t)
		out, err := s.run(c.Code, hangGuard)
		s.vm.Close()
		switch {
		case c.Err != "":
			if err == nil {
				t.Errorf("%s\n  want %s, got output %q", c.Code, c.Err, out)
			} else if name := strings.SplitN(err.Error(), ":", 2)[0]; name != c.Err {
				t.Errorf("%s\n  want %s, got %v", c.Code, c.Err, err)
			}
		case err != nil:
			t.Errorf("%s\n  want %q, got error %v", c.Code, c.Out, err)
		case out != c.Out:
			t.Errorf("%s\n  want %q\n   got %q", c.Code, c.Out, out)
		}
	}
}
