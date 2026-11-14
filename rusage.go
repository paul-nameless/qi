package main

import (
	"fmt"
	"runtime"
	"syscall"
	"time"
)

func resourceUsage() string {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return ""
	}
	cpu := time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
	rss := int64(ru.Maxrss)
	if runtime.GOOS != "darwin" {
		rss *= 1024
	}
	return fmt.Sprintf("cpu %.2fs · peak mem %.1fMB", cpu.Seconds(), float64(rss)/(1<<20))
}
