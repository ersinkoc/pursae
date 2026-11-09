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

func TestErrorInBodyParsesXMLRoot(t *testing.T) {
	for _, tc := range []struct {
		body string
		code string
		fail bool
	}{
		{`<Error xmlns="urn:s3"><Code>InternalError</Code></Error>`, "InternalError", true},
		{`<s:Error xmlns:s="urn:s3"><s:Code>SlowDown</s:Code></s:Error>`, "SlowDown", true},
		{`<Error request="123"><Code>AccessDenied</Code></Error>`, "AccessDenied", true},
		{`<CopyObjectResult>`, "", true},
		{"", "", true},
		{`<CopyObjectResult><ETag>ok</ETag></CopyObjectResult>`, "", false},
		{`<CompleteMultipartUploadResult xmlns="urn:s3"><ETag>ok</ETag></CompleteMultipartUploadResult>`, "", false},
	} {
		t.Run(tc.body, func(t *testing.T) {
			body := io.NopCloser(strings.NewReader(tc.body))
			defer body.Close()
			err := errorInBody(&http.Response{StatusCode: http.StatusOK, Body: body})
			if (err != nil) != tc.fail {
				t.Fatalf("error = %v, want failure %v", err, tc.fail)
			}
			if tc.code != "" {
				var s3err *Error
				if !errors.As(err, &s3err) || s3err.Code != tc.code {
					t.Fatalf("got %v, want S3 code %s", err, tc.code)
				}
			}
		})
	}
}
