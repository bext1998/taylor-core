package pricing

import "testing"

func TestApplyDiscount(t *testing.T) {
	cases := []struct {
		cents, percent, want int
	}{
		{1000, 25, 750},
		{999, 10, 900},
		{500, 0, 500},
		{500, 100, 0},
	}
	for _, c := range cases {
		if got := ApplyDiscount(c.cents, c.percent); got != c.want {
			t.Errorf("ApplyDiscount(%d, %d) = %d, want %d", c.cents, c.percent, got, c.want)
		}
	}
}
