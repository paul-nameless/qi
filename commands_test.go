package main

import (
	"reflect"
	"testing"
)

func TestExpandArgs(t *testing.T) {
	cases := []struct {
		body, args string
		positional bool
		want       string
	}{
		{"run awk '{print $1}'", "", false, "run awk '{print $1}'"},
		{"run awk '{print $1}'", "x y", false, "run awk '{print $1}'\n\nArguments: x y"},
		{"Review $ARGUMENTS now", "the diff", false, "Review the diff now"},
		{"Review $ARGUMENTS now", "", false, "Review  now"},
		{"cp $1 $2", "a b", true, "cp a b"},
		{"plain", "a b", true, "plain\n\nArguments: a b"},
	}
	for _, c := range cases {
		if got := expandArgs(c.body, c.args, c.positional); got != c.want {
			t.Errorf("expandArgs(%q, %q) = %q, want %q", c.body, c.args, got, c.want)
		}
	}
}

func TestPathTokens(t *testing.T) {
	got := pathTokens(`look at '/a b.png' and c\ d.jpg`)
	want := []string{"look", "at", "/a b.png", "and", "c d.jpg"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q", got)
	}
}
