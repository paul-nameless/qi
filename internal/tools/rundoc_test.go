package tools

import (
	"strings"
	"testing"
)

func TestRunDescriptionReservedNames(t *testing.T) {
	t.Parallel()
	k := newCellKit(t)
	want := "- The names read, write, edit, bash, jobs, tick and sleep are set again at the start of every run call: don't use them for your own variables.\n"
	if desc := k.run.Def.Description; !strings.Contains(desc, want) || strings.Contains(desc, "subagent,") {
		t.Errorf("description:\n%s", desc)
	}
}
