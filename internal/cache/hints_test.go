package cache

import (
	"math"
	"testing"
	"time"
)

func TestSecondsRoundsWithoutOverflow(t *testing.T) {
	for _, tc := range []struct {
		duration time.Duration
		want     int64
	}{
		{-1, 0}, {0, 0}, {1, 1}, {time.Second, 1}, {time.Second + 1, 2},
		{time.Duration(math.MaxInt64), 9223372037},
	} {
		if got := secs(tc.duration); got != tc.want {
			t.Errorf("secs(%v) = %d, want %d", tc.duration, got, tc.want)
		}
	}
}
