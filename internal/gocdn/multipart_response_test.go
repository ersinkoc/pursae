package gocdn

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type failedResponseReader struct{}

func (failedResponseReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestErrorInBodyPreservesReadFailure(t *testing.T) {
	for _, prefix := range []string{"", "<Error><Code>", "<CopyObjectResult>"} {
		t.Run(prefix, func(t *testing.T) {
			body := io.NopCloser(io.MultiReader(strings.NewReader(prefix), failedResponseReader{}))
			defer body.Close()
			err := errorInBody(&http.Response{StatusCode: http.StatusOK, Body: body})
			if !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatalf("got %v, want unexpected EOF", err)
			}
		})
	}
}
