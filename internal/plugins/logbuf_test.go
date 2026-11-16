package plugins

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"unicode/utf8"
)

// SetLogsQuiet sends a plugin's entries to the server log at debug level until it is cleared.
func TestSetLogsQuiet(t *testing.T) {
	var buf bytes.Buffer
	rt := &Runtime{cfg: Config{Logger: slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))}}
	sink := rt.logSink("demo")
	rt.SetLogsQuiet("demo", true)
	sink(LogEntry{Level: "error", Source: "plugin", Message: "add_hook the_content: permission_denied"})
	if !strings.Contains(buf.String(), "level=DEBUG") {
		t.Fatalf("quiet entry: %s", buf.String())
	}
	buf.Reset()
	rt.SetLogsQuiet("demo", false)
	sink(LogEntry{Level: "error", Source: "plugin", Message: "boom"})
	if !strings.Contains(buf.String(), "level=ERROR") {
		t.Fatalf("entry after quiet: %s", buf.String())
	}
}

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
