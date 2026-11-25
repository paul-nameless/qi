package config

import "testing"

func TestModeNames(t *testing.T) {
	order := []Mode{ModeYolo, ModeReadOnly, ModeProject, ModeYolo}
	for i, m := range order[:3] {
		if got := ParseMode(m.String()); got != m {
			t.Errorf("%v parses back as %v", m, got)
		}
		if got := m.Next(); got != order[i+1] {
			t.Errorf("%v.Next() = %v, want %v", m, got, order[i+1])
		}
	}
	if ParseMode("bogus") != ModeYolo || Mode(-1).String() != "yolo" {
		t.Error("unknown modes must read as yolo")
	}
}
