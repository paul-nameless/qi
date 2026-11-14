package util

import "testing"

func TestFormatMoney(t *testing.T) {
	if got := FormatMoney(1234, "EUR"); got != "12.34 EUR" {
		t.Fatalf("got %q", got)
	}
}

func TestFormatMoneyShort(t *testing.T) {
	if got := FormatMoneyShort(5); got != "0.05" {
		t.Fatalf("got %q", got)
	}
}

func TestSlug(t *testing.T) {
	if Slug(" Red Shoes ") != "red-shoes" {
		t.Fatal("slug")
	}
}
