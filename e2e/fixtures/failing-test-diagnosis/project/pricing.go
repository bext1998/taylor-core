// Package pricing computes prices in whole cents.
package pricing

// ApplyDiscount returns cents reduced by percent (0-100), rounded down to a
// whole cent.
func ApplyDiscount(cents, percent int) int {
	return cents - cents*(percent/100)
}
