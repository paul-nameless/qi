package cart

import (
	"testing"

	"shop/internal/store"
)

func TestSummary(t *testing.T) {
	c := New()
	c.Add(store.Product{ID: "a", PriceCents: 1000}, 2)
	if got := c.Summary("EUR"); got != "1 items, 20.00 EUR" {
		t.Fatalf("got %q", got)
	}
}

func TestDescribe(t *testing.T) {
	if got := describe("TEN", 1000, "EUR"); got != "TEN: -1.00 EUR" {
		t.Fatalf("got %q", got)
	}
}
