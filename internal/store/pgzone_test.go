package store

import (
	"testing"
	"time"
)

func TestPGZone(t *testing.T) {
	ist, _ := time.LoadLocation("Europe/Istanbul")
	for _, c := range []struct {
		loc  *time.Location
		want string
	}{
		{nil, "UTC"},
		{time.UTC, "UTC"},
		{ist, "Europe/Istanbul"},
		{time.FixedZone("UTC+3:00", 3*3600), "UTC-03:00"},
		{time.FixedZone("UTC-5:30", -(5*3600 + 1800)), "UTC+05:30"},
	} {
		if got := PGZone(c.loc); got != c.want {
			t.Errorf("PGZone(%v) = %q, want %q", c.loc, got, c.want)
		}
	}
}
