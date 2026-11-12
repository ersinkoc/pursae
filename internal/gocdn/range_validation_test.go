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
	return &http.Response{StatusCode: http.StatusPartialContent, Header: http.Header{"Content-Range": {"bytes 1-3/5"}}, Body: io.NopCloser(strings.NewReader("ell"))}, nil
}

type ignoredRangeTransport struct{}

func (ignoredRangeTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("hello"))}, nil
}

func TestGetRangeRejectsIgnoredRange(t *testing.T) {
	c := &gocdn.Client{Endpoint: "https://s3.example.test", Region: "auto", Bucket: "b", AccessKey: "a", SecretKey: "s", PathStyle: true, HTTP: &http.Client{Transport: ignoredRangeTransport{}}}
	if b, err := c.GetRange(context.Background(), "x", 1, 3); err == nil {
		t.Fatalf("ignored range returned %q without an error", b)
	}
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

type contentRangeTransport string

func (r contentRangeTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusPartialContent, Header: http.Header{"Content-Range": {string(r)}}, Body: io.NopCloser(strings.NewReader("abc"))}, nil
}

func TestGetRangeChecksContentRange(t *testing.T) {
	for _, tc := range []struct {
		value   string
		wantErr bool
	}{
		{"bytes 0-2/10", true},
		{"bytes 1-4/10", true},
		{"", true},
		{"bytes 1-3/10", false},
		{"bytes 1-2/3", false},
	} {
		c := &gocdn.Client{Endpoint: "https://s3.example.test", Bucket: "b", AccessKey: "a", SecretKey: "s", PathStyle: true, HTTP: &http.Client{Transport: contentRangeTransport(tc.value)}}
		if _, err := c.GetRange(context.Background(), "x", 1, 3); (err != nil) != tc.wantErr {
			t.Errorf("Content-Range %q: error = %v, want error %t", tc.value, err, tc.wantErr)
		}
	}
}

type shortRangeTransport string

func (body shortRangeTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusPartialContent, Header: http.Header{"Content-Range": {"bytes 1-3/10"}}, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
}

func TestGetRangeRejectsShortBody(t *testing.T) {
	for _, tc := range []struct {
		body    string
		wantErr bool
	}{{"", true}, {"ab", true}, {"abc", false}} {
		c := &gocdn.Client{Endpoint: "https://s3.example.test", Bucket: "b", AccessKey: "a", SecretKey: "s", PathStyle: true, HTTP: &http.Client{Transport: shortRangeTransport(tc.body)}}
		if _, err := c.GetRange(context.Background(), "x", 1, 3); (err != nil) != tc.wantErr {
			t.Errorf("body %q: error = %v, want error %t", tc.body, err, tc.wantErr)
		}
	}
}
