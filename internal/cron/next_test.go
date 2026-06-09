package cron

import (
	"testing"
	"time"
)

func mustParse(t *testing.T, expr string) Schedule {
	t.Helper()
	s, err := Parse(expr)
	if err != nil {
		t.Fatalf("Parse(%q): %v", expr, err)
	}
	return s
}

func TestNext(t *testing.T) {
	utc := time.UTC
	cases := []struct {
		expr  string
		after time.Time
		want  time.Time
	}{
		// every minute -> next minute
		{"* * * * *", time.Date(2026, 6, 9, 10, 0, 30, 0, utc), time.Date(2026, 6, 9, 10, 1, 0, 0, utc)},
		// every 15 min
		{"*/15 * * * *", time.Date(2026, 6, 9, 10, 7, 0, 0, utc), time.Date(2026, 6, 9, 10, 15, 0, 0, utc)},
		// hourly at :00 crossing the hour
		{"0 * * * *", time.Date(2026, 6, 9, 10, 30, 0, 0, utc), time.Date(2026, 6, 9, 11, 0, 0, 0, utc)},
		// daily 09:00 -> next day when already past
		{"0 9 * * *", time.Date(2026, 6, 9, 10, 0, 0, 0, utc), time.Date(2026, 6, 10, 9, 0, 0, 0, utc)},
		// month rollover: Jan 31 23:59 -> next match Feb? "0 0 1 * *" first of month
		{"0 0 1 * *", time.Date(2026, 1, 31, 23, 59, 0, 0, utc), time.Date(2026, 2, 1, 0, 0, 0, 0, utc)},
		// year rollover
		{"0 0 1 1 *", time.Date(2026, 6, 9, 0, 0, 0, 0, utc), time.Date(2027, 1, 1, 0, 0, 0, 0, utc)},
		// weekday: next Monday 00:00
		{"0 0 * * MON", time.Date(2026, 6, 9, 12, 0, 0, 0, utc), time.Date(2026, 6, 15, 0, 0, 0, 0, utc)}, // 2026-06-09 is a Tue
		// DOM/DOW OR: fires on the 1st OR any Monday
		{"0 0 1 * MON", time.Date(2026, 6, 9, 0, 0, 0, 0, utc), time.Date(2026, 6, 15, 0, 0, 0, 0, utc)}, // next Monday before next 1st
	}
	for _, c := range cases {
		got := mustParse(t, c.expr).Next(c.after)
		if !got.Equal(c.want) {
			t.Fatalf("Next(%q, %v) = %v want %v", c.expr, c.after, got, c.want)
		}
	}
}

func TestNextImpossibleReturnsZero(t *testing.T) {
	// Feb 30 never occurs.
	got := mustParse(t, "0 0 30 2 *").Next(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if !got.IsZero() {
		t.Fatalf("impossible schedule should return zero time, got %v", got)
	}
}

func TestNextIsStrictlyAfter(t *testing.T) {
	// Exactly on a match -> returns the NEXT one, not the same instant.
	at := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	got := mustParse(t, "0 * * * *").Next(at)
	if !got.After(at) {
		t.Fatalf("Next must be strictly after input; got %v for %v", got, at)
	}
}
