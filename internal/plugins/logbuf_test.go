package plugins

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestLogRingTruncatesAtUTF8Boundary(t *testing.T) {
	r := newLogRing(1, nil)
	r.add("info", "plugin", strings.Repeat("界", maxLogMessage))
	got := r.list()[0].Message
	if !utf8.ValidString(got) || !strings.HasSuffix(got, "…") {
		t.Fatalf("invalid truncated log: %q", got)
	}
}

func TestLineWriterKeepsLongLineAsOneEntry(t *testing.T) {
	r := newLogRing(10, nil)
	w := &lineWriter{ring: r}
	_, _ = w.Write([]byte(strings.Repeat("x", maxLogMessage+1)))
	_, _ = w.Write([]byte("tail\nnext\n"))
	logs := r.list()
	if len(logs) != 2 || !strings.HasSuffix(logs[0].Message, "…") || logs[1].Message != "next" {
		t.Fatalf("logs = %+v", logs)
	}
}
