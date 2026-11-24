package metrics

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type failingTransportWriter struct {
	*httptest.ResponseRecorder
	err error
}

func (w failingTransportWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, w.err
}

func (w failingTransportWriter) FlushError() error { return w.err }
func (w failingTransportWriter) ReadFrom(io.Reader) (int64, error) {
	return 0, w.err
}

func TestStatusWriterPreservesTransportFailures(t *testing.T) {
	failure := errors.New("transport failure")
	for name, run := range map[string]func(*statusWriter) error{
		"hijack": func(w *statusWriter) error { _, _, err := w.Hijack(); return err },
		"flush":  func(w *statusWriter) error { return http.NewResponseController(w).Flush() },
		"copy":   func(w *statusWriter) error { _, err := w.ReadFrom(strings.NewReader("body")); return err },
	} {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			w := &statusWriter{ResponseWriter: failingTransportWriter{rec, failure}}
			if err := run(w); !errors.Is(err, failure) {
				t.Fatalf("error = %v", err)
			}
			w.WriteHeader(http.StatusBadGateway)
			if w.status != rec.Code || w.status != http.StatusBadGateway {
				t.Fatalf("wrapper status = %d, response status = %d", w.status, rec.Code)
			}
		})
	}
}
