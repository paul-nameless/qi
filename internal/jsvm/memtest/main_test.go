package memtest

import (
	"errors"
	"os"
	"runtime"
	"runtime/metrics"
	"testing"
	"time"

	"github.com/paul-nameless/qi/internal/jsvm"
	"github.com/paul-nameless/qi/internal/testenv"
)

func TestMain(m *testing.M) {
	os.Exit(testenv.Run(m))
}

var hangGuard = 60 * time.Second * time.Duration(1+4*raceFactor)

func heapBytes() int64 {
	s := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}, {Name: "/memory/classes/heap/stacks:bytes"}}
	metrics.Read(s)
	var total int64
	for _, v := range s {
		if v.Value.Kind() == metrics.KindUint64 {
			total += int64(v.Value.Uint64())
		}
	}
	return total
}

func limitAbove(n int64) int64 {
	runtime.GC()
	return heapBytes() + n
}

func errName(err error) string {
	if ex, ok := errors.AsType[*jsvm.Exception](err); ok {
		return ex.Name
	}
	if err == nil {
		return ""
	}
	return "go:" + err.Error()
}

func runGuarded(limit int64, src string) (string, error) {
	vm := jsvm.New(jsvm.Options{MemoryLimit: limit})
	defer func() {
		vm.Close()
		runtime.GC()
	}()
	stop := vm.InterruptAfter(hangGuard)
	v, err := vm.RunCell("cell.js", src)
	stop()
	if err != nil {
		return "", err
	}
	return jsvm.ToString(v), nil
}
