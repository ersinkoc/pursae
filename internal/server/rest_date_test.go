package server

import (
	"testing"
	"time"
)

func TestRestParseDate(t *testing.T) {
	ist := time.FixedZone("IST", 3*3600)
	want := time.Date(2026, 12, 1, 10, 0, 0, 0, time.UTC)
	for _, v := range []string{"2026-12-01T13:00:00", "2026-12-01T10:00:00Z", "2026-12-01T13:00:00+03:00", "2026-12-01T10:00:00.000Z", "2026-12-01 13:00:00", "2026-12-01t10:00:00z"} {
		got, ok := restParseDate(v, ist)
		if !ok || !got.Equal(want) {
			t.Errorf("%s: %v %v", v, got, ok)
		}
	}
	for _, v := range []string{"tomorrow", "2026-12-01"} {
		if _, ok := restParseDate(v, ist); ok {
			t.Errorf("%s parsed", v)
		}
	}
}
