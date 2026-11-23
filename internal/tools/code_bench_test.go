package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/paul-nameless/qi/internal/jsvm"
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

func BenchmarkBashValue(b *testing.B) {
	vm := jsvm.New(jsvm.Options{MemoryLimit: 512 << 20})
	c := &cell{vm: vm}
	run := bashRun{output: strings.Repeat("line of build output with a path/to/file.go:123\n", 20000)}
	b.ReportAllocs()
	b.SetBytes(int64(len(run.output)))
	for b.Loop() {
		if v := c.jsValue(run.value()); v.IsUndefined() {
			b.Fatal("undefined")
		}
	}
}
