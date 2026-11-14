package pricing

type Line struct {
	PriceCents int64
	Qty        int
}

func Subtotal(items []Line) int64 {
	var sum int64
	for _, it := range items {
		sum += it.PriceCents * int64(it.Qty)
	}
	return sum
}

// Total is subtotal plus tax plus shipping.
func Total(items []Line, taxRate float64, freeOver int64) int64 {
	sub := Subtotal(items)
	return ApplyTax(sub, taxRate) + Shipping(sub, freeOver)
}
