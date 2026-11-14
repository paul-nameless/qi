package pricing

import "testing"

func TestSubtotal(t *testing.T) {
	if got := Subtotal([]Line{{100, 2}, {50, 1}}); got != 250 {
		t.Fatalf("got %d", got)
	}
}

func TestApplyTax(t *testing.T) {
	if got := ApplyTax(1000, 0.2); got != 1200 {
		t.Fatalf("got %d", got)
	}
}

func TestShippingFreeAtThreshold(t *testing.T) {
	if got := Shipping(5000, 5000); got != 0 {
		t.Fatalf("shipping at threshold: got %d, want 0", got)
	}
	if got := Shipping(4999, 5000); got != 499 {
		t.Fatalf("shipping below threshold: got %d", got)
	}
}

func TestTotal(t *testing.T) {
	if got := Total([]Line{{2500, 2}}, 0.2, 5000); got != 6000 {
		t.Fatalf("got %d", got)
	}
}
