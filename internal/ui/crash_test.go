package ui

import "testing"

func TestCleanupOnPanicRunsCleanupsAndRepanics(t *testing.T) {
	var order []int
	OnCrash(func() { order = append(order, 1) })
	OnCrash(func() { panic("cleanup failed") })
	OnCrash(func() { order = append(order, 2) })
	defer func() {
		if p := recover(); p != "boom" {
			t.Errorf("panic value %v", p)
		}
		if len(order) != 2 || order[0] != 1 || order[1] != 2 {
			t.Errorf("cleanups %v, want [1 2]", order)
		}
	}()
	func() {
		defer CleanupOnPanic()
		panic("boom")
	}()
}
