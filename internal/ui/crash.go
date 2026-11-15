package ui

import "sync"

var crash struct {
	sync.Mutex
	cleanups []func()
}

func OnCrash(fn func()) {
	crash.Lock()
	defer crash.Unlock()
	crash.cleanups = append(crash.cleanups, fn)
}

func CleanupOnPanic() {
	if p := recover(); p != nil {
		runCrashCleanups()
		panic(p)
	}
}

func runCrashCleanups() {
	crash.Lock()
	fns := crash.cleanups
	crash.cleanups = nil
	crash.Unlock()
	for _, fn := range fns {
		runCleanup(fn)
	}
}

func runCleanup(fn func()) {
	defer func() { _ = recover() }()
	fn()
}
