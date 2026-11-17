package main

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"wpgo/internal/config"
)

type setupTokenErrorWriter struct{ err error }

func (w setupTokenErrorWriter) Write([]byte) (int, error) { return 0, w.err }

func TestSetupTokenPropagatesOutputError(t *testing.T) {
	cfg := config.Config{SiteURL: "https://example.test", SetupToken: "fixture-token"}
	want := errors.New("output unavailable")
	if err := setupToken(context.Background(), cfg, setupTokenErrorWriter{want}); !errors.Is(err, want) {
		t.Fatalf("output error = %v, want %v", err, want)
	}
	var out bytes.Buffer
	if err := setupToken(context.Background(), cfg, &out); err != nil || out.Len() == 0 {
		t.Fatalf("normal output: %q, error: %v", out.String(), err)
	}
}
