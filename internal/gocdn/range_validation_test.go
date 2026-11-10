package gocdn_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"wpgo/internal/gocdn"
)

type rangeTransport struct{ calls int }

func (r *rangeTransport) RoundTrip(*http.Request) (*http.Response, error) {
	r.calls++
	return &http.Response{StatusCode: http.StatusPartialContent, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("ell"))}, nil
}

func TestGetRangeRejectsInvalidBoundsWithoutRequest(t *testing.T) {
	transport := &rangeTransport{}
	c := &gocdn.Client{Endpoint: "https://s3.example.test", Region: "auto", Bucket: "b", AccessKey: "a", SecretKey: "s", PathStyle: true, HTTP: &http.Client{Transport: transport}}
	for _, bounds := range [][2]int64{{-1, 2}, {3, 2}} {
		if _, err := c.GetRange(context.Background(), "x", bounds[0], bounds[1]); err == nil {
			t.Fatalf("GetRange(%d,%d) succeeded", bounds[0], bounds[1])
		}
	}
	if transport.calls != 0 {
		t.Fatalf("invalid ranges made %d requests", transport.calls)
	}
	b, err := c.GetRange(context.Background(), "x", 1, 3)
	if err != nil || string(b) != "ell" || transport.calls != 1 {
		t.Fatalf("valid range: body=%q error=%v calls=%d", b, err, transport.calls)
	}
}
