package jsvm

import (
	"os"
	"runtime"
	"runtime/pprof"
	"testing"
)

func TestProfScratch(t *testing.T) {
	src := os.Getenv("PROFJS")
	if src == "" {
		t.Skip()
	}
	b, _ := os.ReadFile(src)
	vm := New(Options{MemoryLimit: 1 << 30})
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	v, err := vm.RunCell("cell.js", string(b))
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	t.Logf("%v %v live=%dKB", ToString(v), err, (after.HeapAlloc-before.HeapAlloc)/1024)
	if out := os.Getenv("HEAPOUT"); out != "" {
		f, _ := os.Create(out)
		pprof.WriteHeapProfile(f)
		f.Close()
	}
	runtime.KeepAlive(vm)
}
