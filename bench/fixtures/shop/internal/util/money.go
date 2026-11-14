package util

import (
	"fmt"
	"strconv"
	"strings"
)

// FormatMoney renders cents as "12.34 EUR".
func FormatMoney(cents int64, currency string) string {
	return fmt.Sprintf("%d.%02d %s", cents/100, cents%100, currency)
}

// FormatMoneyShort renders cents without a currency, "12.34".
func FormatMoneyShort(cents int64) string {
	return fmt.Sprintf("%d.%02d", cents/100, cents%100)
}

func ParseCents(s string) (int64, error) {
	whole, frac, _ := strings.Cut(s, ".")
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, err
	}
	if frac == "" {
		return w * 100, nil
	}
	f, err := strconv.ParseInt((frac + "0")[:2], 10, 64)
	if err != nil {
		return 0, err
	}
	return w*100 + f, nil
}
