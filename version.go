package main

import (
	"regexp"
	"runtime/debug"
	"strings"
)

var pseudoVersion = regexp.MustCompile(`^v\d+\.\d+\.\d+-(?:[\w.]+\.)?0\.\d{14}-([0-9a-f]{12})(\+dirty)?$`)

func buildVersion() string {
	if version != "dev" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return version
	}
	return versionFrom(info)
}

func versionFrom(info *debug.BuildInfo) string {
	v := info.Main.Version
	if m := pseudoVersion.FindStringSubmatch(v); m != nil {
		return "dev-" + m[1][:7] + strings.Replace(m[2], "+", "-", 1)
	}
	if v != "" && v != "(devel)" {
		return strings.TrimSuffix(strings.TrimPrefix(v, "v"), "+dirty")
	}
	var rev, modified string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			modified = s.Value
		}
	}
	if rev == "" {
		return version
	}
	short := "dev-" + rev[:min(len(rev), 7)]
	if modified == "true" {
		short += "-dirty"
	}
	return short
}
