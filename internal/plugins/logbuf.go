package plugins

import (
	"bytes"
	"sync"
	"time"
)

// LogEntry is one line in a plugin's log.
type LogEntry struct {
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`  // debug, info, warning, error
	Source  string    `json:"source"` // plugin (wpgo.Log), stdout, stderr or runtime
	Message string    `json:"message"`
}

// maxLogMessage caps a single log message.
const maxLogMessage = 4 << 10

// logRing is a fixed-size ring buffer of log entries.
type logRing struct {
	mu      sync.Mutex
	entries []LogEntry
	next    int
	full    bool
	sink    func(LogEntry)
}

func newLogRing(size int, sink func(LogEntry)) *logRing {
	if size <= 0 {
		size = 1
	}
	return &logRing{entries: make([]LogEntry, size), sink: sink}
}

func (l *logRing) add(level, source, msg string) {
	if len(msg) > maxLogMessage {
		msg = msg[:maxLogMessage] + "…"
	}
	e := LogEntry{Time: time.Now(), Level: level, Source: source, Message: msg}
	l.mu.Lock()
	l.entries[l.next] = e
	l.next = (l.next + 1) % len(l.entries)
	if l.next == 0 {
		l.full = true
	}
	l.mu.Unlock()
	if l.sink != nil {
		l.sink(e)
	}
}

// list returns the entries oldest first.
func (l *logRing) list() []LogEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.full {
		return append([]LogEntry(nil), l.entries[:l.next]...)
	}
	return append(append([]LogEntry(nil), l.entries[l.next:]...), l.entries[:l.next]...)
}

// lineWriter turns a byte stream (guest stdout/stderr) into log entries, one
// per line. Partial lines are buffered up to maxLogMessage bytes.
type lineWriter struct {
	mu     sync.Mutex
	ring   *logRing
	level  string
	source string
	buf    []byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		w.ring.add(w.level, w.source, string(w.buf[:i]))
		w.buf = w.buf[i+1:]
	}
	if len(w.buf) > maxLogMessage {
		w.ring.add(w.level, w.source, string(w.buf))
		w.buf = nil
	}
	return len(p), nil
}
