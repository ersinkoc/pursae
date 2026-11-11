package gocdn

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// The examples of AWS' "Signature Calculations for the Authorization Header: Transferring Payload
// in a Single Chunk" and "Query String Authentication" (S3 API reference, Signature Version 4).
var (
	awsExampleKey    = "AKIAIOSFODNN7EXAMPLE"
	awsExampleSecret = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
	awsExampleTime   = time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)
	awsExampleSigner = Signer{AccessKey: awsExampleKey, SecretKey: awsExampleSecret, Region: "us-east-1"}
)

func awsSignature(t *testing.T, req *http.Request, payload string) string {
	t.Helper()
	awsExampleSigner.Sign(req, payload, awsExampleTime)
	_, _, sig, ok := ParseAuthorization(req.Header.Get("Authorization"))
	if !ok {
		t.Fatalf("malformed Authorization: %s", req.Header.Get("Authorization"))
	}
	return sig
}

func TestSigV4GetObjectVector(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://examplebucket.s3.amazonaws.com/test.txt", nil)
	req.Header.Set("Range", "bytes=0-9")
	if sig := awsSignature(t, req, EmptyPayloadHash); sig != "f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41" {
		t.Errorf("GET object signature %s", sig)
	}
	want := "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request, SignedHeaders=host;range;x-amz-content-sha256;x-amz-date, Signature="
	if !strings.HasPrefix(req.Header.Get("Authorization"), want) {
		t.Errorf("Authorization %s", req.Header.Get("Authorization"))
	}
}

func TestSigV4PutObjectVector(t *testing.T) {
	body := "Welcome to Amazon S3."
	req, _ := http.NewRequest(http.MethodPut, "https://examplebucket.s3.amazonaws.com/test$file.text", strings.NewReader(body))
	req.Header.Set("Date", "Fri, 24 May 2013 00:00:00 GMT")
	req.Header.Set("X-Amz-Storage-Class", "REDUCED_REDUNDANCY")
	if got := SHA256Hex([]byte(body)); got != "44ce7dd67c959e0d3524ffac1771dfbba87d2b6b4b4e99e42034a8b803f8b072" {
		t.Fatalf("payload hash %s", got)
	}
	if sig := awsSignature(t, req, SHA256Hex([]byte(body))); sig != "98ad721746da40c64f1a55b78f14c238d841ea1380cd77a1b5971af0ece108bd" {
		t.Errorf("PUT object signature %s", sig)
	}
}

func TestSigV4GetLifecycleVector(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://examplebucket.s3.amazonaws.com/?lifecycle", nil)
	if sig := awsSignature(t, req, EmptyPayloadHash); sig != "fea454ca298b7da1c68078a5d1bdbfbbe0d65c699e0f91ac7a200a0136783543" {
		t.Errorf("GET lifecycle signature %s", sig)
	}
}

func TestSigV4ListObjectsVector(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://examplebucket.s3.amazonaws.com/?max-keys=2&prefix=J", nil)
	if sig := awsSignature(t, req, EmptyPayloadHash); sig != "34b48302e7b5fa45bde8084f4b7868a86f0a534bc59db6670ed5711ef69dc6f7" {
		t.Errorf("list objects signature %s", sig)
	}
}

func TestSigV4PresignVector(t *testing.T) {
	u, _ := url.Parse("https://examplebucket.s3.amazonaws.com/test.txt")
	got := awsExampleSigner.Presign(http.MethodGet, u, awsExampleTime, 86400*time.Second)
	if sig := got.Query().Get("X-Amz-Signature"); sig != "aeeed9bbccd4d02ee5c0109b86d86835f995330da4c265957d157751f604d404" {
		t.Errorf("presigned signature %s (%s)", sig, got)
	}
	if got.Query().Get("X-Amz-Credential") != "AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request" || got.Query().Get("X-Amz-Expires") != "86400" {
		t.Errorf("presigned query %s", got.RawQuery)
	}
}

func TestPresignReplacesOldSignature(t *testing.T) {
	u, _ := url.Parse("https://examplebucket.s3.amazonaws.com/test.txt?versionId=1")
	first := awsExampleSigner.Presign(http.MethodGet, u, awsExampleTime, time.Minute)
	again := awsExampleSigner.Presign(http.MethodGet, first, awsExampleTime, time.Minute)
	if again.String() != first.String() {
		t.Fatalf("re-signed URL differs: %s", again)
	}
}

func TestURIEncoding(t *testing.T) {
	for in, want := range map[string]string{
		"/a b/c+d$e.txt": "/a%20b/c%2Bd%24e.txt",
		"/ü/~x_y-z.":     "/%C3%BC/~x_y-z.",
		"/":              "/",
	} {
		if got := EscapePath(in); got != want {
			t.Errorf("EscapePath(%q) = %q, want %q", in, got, want)
		}
	}
	q := url.Values{"prefix": {"a/b c"}, "list-type": {"2"}, "continuation-token": {"x=y"}}
	if got := CanonicalQuery(q); got != "continuation-token=x%3Dy&list-type=2&prefix=a%2Fb%20c" {
		t.Errorf("CanonicalQuery = %q", got)
	}
}

func TestParseAuthorization(t *testing.T) {
	if _, _, _, ok := ParseAuthorization("AWS AKID:abc"); ok {
		t.Error("SigV2 header accepted")
	}
	c, sh, sig, ok := ParseAuthorization("AWS4-HMAC-SHA256 Credential=AK/20200101/auto/s3/aws4_request, SignedHeaders=host;x-amz-date, Signature=" + strings.Repeat("a", 64))
	if !ok || c != "AK/20200101/auto/s3/aws4_request" || sh != "host;x-amz-date" || len(sig) != 64 {
		t.Errorf("parse: %v %q %q %q", ok, c, sh, sig)
	}
}

func TestCanonicalQuerySortsEncodedNames(t *testing.T) {
	for _, tc := range []struct {
		query url.Values
		want  string
	}{
		{url.Values{"é": {"value"}, "z": {"last"}}, "%C3%A9=value&z=last"},
		{url.Values{"aé": {"x"}, "a~": {"y"}}, "a%C3%A9=x&a~=y"},
		{url.Values{"a": {"2", "1"}, "b": {""}}, "a=1&a=2&b="},
	} {
		if got := CanonicalQuery(tc.query); got != tc.want {
			t.Errorf("CanonicalQuery(%v) = %q, want %q", tc.query, got, tc.want)
		}
	}
}
