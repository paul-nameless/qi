package app

import (
	"runtime/debug"
	"testing"
)

func TestVersionFrom(t *testing.T) {
	cases := []struct {
		info debug.BuildInfo
		want string
	}{
		{debug.BuildInfo{Main: debug.Module{Version: "v1.2.3"}}, "1.2.3"},
		{debug.BuildInfo{Main: debug.Module{Version: "v0.6.1-0.20261115145730-f795dd20bd35+dirty"}}, "dev-f795dd2-dirty"},
		{debug.BuildInfo{Main: debug.Module{Version: "v1.0.1-0.20261115145730-f795dd20bd35"}}, "dev-f795dd2"},
		{debug.BuildInfo{Main: debug.Module{Version: "v1.1.0-rc.1.0.20261115145730-f795dd20bd35"}}, "dev-f795dd2"},
		{debug.BuildInfo{Main: debug.Module{Version: "(devel)"}, Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "f795dd2abcdef"}, {Key: "vcs.modified", Value: "true"}}}, "dev-f795dd2-dirty"},
		{debug.BuildInfo{Main: debug.Module{Version: "(devel)"}, Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "f795dd2abcdef"}}}, "dev-f795dd2"},
		{debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, "dev"},
	}
	for _, c := range cases {
		if got := versionFrom(&c.info, "dev"); got != c.want {
			t.Errorf("%+v: got %q, want %q", c.info.Main, got, c.want)
		}
	}
}
