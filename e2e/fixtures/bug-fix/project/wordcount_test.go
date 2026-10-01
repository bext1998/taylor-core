package wordcount

import "testing"

func TestCountWords(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"hello", 1},
		{"hello world", 2},
		{"  hello   world ", 2},
		{"a\tb\nc", 3},
	}
	for _, c := range cases {
		if got := CountWords(c.in); got != c.want {
			t.Errorf("CountWords(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}
