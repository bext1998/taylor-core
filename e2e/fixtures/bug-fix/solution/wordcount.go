// Package wordcount counts words in text.
package wordcount

import "strings"

// CountWords returns the number of words in s. Words are separated by any
// run of whitespace.
func CountWords(s string) int {
	return len(strings.Fields(s))
}
