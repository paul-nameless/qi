package jsvm

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func withLocal(t *testing.T, name string) {
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Skipf("no tzdata for %s: %v", name, err)
	}
	old := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = old })
}

func TestDateOracle(t *testing.T) {
	withLocal(t, "Europe/Berlin")
	b, err := os.ReadFile("testdata/date_oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct{ Code, Out, Err, Skip string }
	if err := json.Unmarshal(b, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		if c.Skip != "" {
			continue
		}
		s := newSession(t)
		out, err := s.run(c.Code, hangGuard)
		switch {
		case c.Err != "":
			if err == nil || strings.SplitN(err.Error(), ":", 2)[0] != c.Err {
				t.Errorf("%s\n  want %s, got %q %v", c.Code, c.Err, out, err)
			}
		case err != nil || out != c.Out:
			t.Errorf("%s\n  want %q\n   got %q %v", c.Code, c.Out, out, err)
		}
	}
}
