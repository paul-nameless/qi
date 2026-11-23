package tools

import (
	"encoding/json"
	"testing"
)

func BenchmarkArgChecks(b *testing.B) {
	read, edit := Read(), Edit()
	rf := map[string]json.RawMessage{"file_path": json.RawMessage(`"a.go"`)}
	ef := map[string]json.RawMessage{"file_path": json.RawMessage(`"a.go"`), "old_string": json.RawMessage(`"x"`), "new_string": json.RawMessage(`"y"`)}
	b.ReportAllocs()
	for b.Loop() {
		for _, c := range []struct {
			t *Tool
			f map[string]json.RawMessage
		}{{read, rf}, {edit, ef}} {
			if checkFields(c.t, c.f) != nil || checkRequired(c.t, c.f) != nil {
				b.Fatal("unexpected error")
			}
		}
	}
}
