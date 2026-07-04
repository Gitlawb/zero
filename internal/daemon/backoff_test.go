package daemon

import (
	"testing"
	"time"
)

func TestBackoffBaseExponentialAndCap(t *testing.T) {
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{0, 1 * time.Second},
		{1, 2 * time.Second},
		{2, 4 * time.Second},
		{3, 8 * time.Second},
		{20, backoffCap}, // far past the cap
	}
	for _, tc := range cases {
		if got := backoffBase(tc.attempt); got != tc.want {
			t.Fatalf("backoffBase(%d) = %s, want %s", tc.attempt, got, tc.want)
		}
	}
}

func TestBackoffWithJitterBounds(t *testing.T) {
	for attempt := 1; attempt <= 6; attempt++ {
		base := backoffBase(attempt)
		lo := time.Duration(float64(base) * 0.5)
		hi := time.Duration(float64(base) * 1.5)
		for range 200 {
			got := BackoffWithJitter(attempt)
			if got < lo || got >= hi {
				t.Fatalf("BackoffWithJitter(%d) = %s, want in [%s,%s)", attempt, got, lo, hi)
			}
		}
	}
}
