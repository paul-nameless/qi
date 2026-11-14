package cart

import "shop/internal/util"

// Discount returns the discount in cents for a code.
func Discount(code string, subtotal int64) int64 {
	switch code {
	case "TEN":
		return subtotal / 10
	case "FIVER":
		return 500
	}
	return 0
}

func describe(code string, subtotal int64, currency string) string {
	return code + ": -" + util.FormatMoney(Discount(code, subtotal), currency)
}
