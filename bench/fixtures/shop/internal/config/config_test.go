package config

import "testing"

func TestDefault(t *testing.T) {
	if Default().Port != 8080 {
		t.Fatal("default port")
	}
}

func TestLoadMissing(t *testing.T) {
	if _, err := Load("nope.json"); err == nil {
		t.Fatal("expected error")
	}
}
