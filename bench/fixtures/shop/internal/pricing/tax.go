package pricing

import "math"

const flatShipping = 499

func ApplyTax(cents int64, rate float64) int64 {
	return cents + int64(math.Round(float64(cents)*rate))
}

// Shipping is free once the subtotal reaches freeOver.
func Shipping(subtotal, freeOver int64) int64 {
	if subtotal >= freeOver {
		return 0
	}
	return flatShipping
}
