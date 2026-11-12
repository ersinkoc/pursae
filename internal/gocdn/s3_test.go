package gocdn_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"wpgo/internal/gocdn"
	"wpgo/internal/gocdn/s3fake"
)

func newFake(t *testing.T) *s3fake.Server {
	t.Helper()
	f := s3fake.New("AKTEST", "test-secret/key", "auto", "media")
	t.Cleanup(f.Close)
	return f
}

func pathClient(f *s3fake.Server) *gocdn.Client {
	return &gocdn.Client{Endpoint: f.URL, Region: "auto", Bucket: "media", AccessKey: "AKTEST", SecretKey: "test-secret/key", PathStyle: true}
}

func TestClientRoundTripPathStyle(t *testing.T) {
	f := newFake(t)
	c := pathClient(f)
	ctx := context.Background()
	key := "wp-content/uploads/2026/10/a photo+1.jpg"
	if err := c.Put(ctx, key, []byte("hello"), gocdn.PutOptions{ContentType: "image/jpeg", CacheControl: "public, max-age=31536000, immutable", ACL: "public-read"}); err != nil {
		t.Fatal(err)
	}
	o := f.Object("media", key)
	if o == nil || string(o.Body) != "hello" || o.ContentType != "image/jpeg" || o.ACL != "public-read" || !strings.Contains(o.CacheControl, "immutable") {
		t.Fatalf("stored object %+v", o)
	}
	h, err := c.Head(ctx, key)
	if err != nil || h.Size != 5 || h.ContentType != "image/jpeg" {
		t.Fatalf("head %+v %v", h, err)
	}
	rc, _, err := c.Get(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	if string(b) != "hello" {
		t.Fatalf("get %q", b)
	}
	part, err := c.GetRange(ctx, key, 1, 3)
	if err != nil || string(part) != "ell" {
		t.Fatalf("range %q %v", part, err)
	}
	if err := c.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Head(ctx, key); !gocdn.IsNotFound(err) {
		t.Fatalf("head after delete: %v", err)
	}
	if err := c.Delete(ctx, key); err != nil {
		t.Fatalf("deleting a missing key: %v", err)
	}
	if len(f.Errors) != 0 {
		t.Fatalf("signature errors: %v", f.Errors)
	}
	for _, r := range f.Requests() {
		if _, sh, _, ok := gocdn.ParseAuthorization(r.Header.Get("Authorization")); !ok || !strings.Contains(sh, "host") || !strings.Contains(sh, "x-amz-content-sha256") || !strings.Contains(sh, "x-amz-date") {
			t.Errorf("%s %s: Authorization %q", r.Method, r.Key, r.Header.Get("Authorization"))
		}
		if !strings.Contains(r.Header.Get("Authorization"), "/auto/s3/aws4_request") {
			t.Errorf("region auto not in the scope: %s", r.Header.Get("Authorization"))
		}
	}
}

func TestClientVirtualHostStyle(t *testing.T) {
	f := newFake(t)
	addr := strings.TrimPrefix(f.URL, "http://")
	_, port, _ := net.SplitHostPort(addr)
	c := pathClient(f)
	c.PathStyle = false
	c.Endpoint = "http://s3.fake.test:" + port
	c.HTTP = &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, addr) // every host name reaches the fake
	}}}
	if err := c.Put(context.Background(), "x/y.txt", []byte("v"), gocdn.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	reqs := f.Requests()
	if len(reqs) != 1 || reqs[0].Bucket != "media" || reqs[0].Key != "x/y.txt" {
		t.Fatalf("virtual-host request routed as %+v", reqs)
	}
	if f.Object("media", "x/y.txt") == nil {
		t.Fatal("object not stored")
	}
}

func TestClientErrors(t *testing.T) {
	f := newFake(t)
	c := pathClient(f)
	c.SecretKey = "wrong"
	err := c.Put(context.Background(), "a.txt", []byte("x"), gocdn.PutOptions{})
	var se *gocdn.Error
	if err == nil || !strings.Contains(err.Error(), "SignatureDoesNotMatch") {
		t.Fatalf("wrong secret: %v", err)
	}
	c = pathClient(f)
	c.AccessKey = "NOPE"
	if err := c.Put(context.Background(), "a.txt", []byte("x"), gocdn.PutOptions{}); err == nil || !strings.Contains(err.Error(), "InvalidAccessKeyId") {
		t.Fatalf("wrong key: %v", err)
	}
	c = pathClient(f)
	c.Bucket = "missing"
	if err := c.Put(context.Background(), "a.txt", []byte("x"), gocdn.PutOptions{}); err == nil || !strings.Contains(err.Error(), "NoSuchBucket") {
		t.Fatalf("missing bucket: %v", err)
	}
	_ = se
	c = pathClient(f)
	c.Bucket = ""
	if err := c.Put(context.Background(), "a.txt", nil, gocdn.PutOptions{}); err == nil {
		t.Fatal("no bucket accepted")
	}
	c = pathClient(f)
	c.Endpoint = "ftp://x"
	if err := c.Validate(); err == nil {
		t.Fatal("ftp endpoint accepted")
	}
}

func TestClientListPagination(t *testing.T) {
	f := newFake(t)
	c := pathClient(f)
	for i := 0; i < 7; i++ {
		f.PutObject("media", fmt.Sprintf("uploads/2026/%02d.jpg", i), []byte("x"), "image/jpeg")
	}
	f.PutObject("media", "uploads/2025/old.jpg", []byte("x"), "image/jpeg")
	f.PutObject("media", "uploads/2025/nested/deep.jpg", []byte("x"), "image/jpeg")
	f.PutObject("media", "root.txt", []byte("x"), "text/plain")

	top, err := c.List(context.Background(), "uploads/", "/", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(top.Prefixes) != 2 || top.Prefixes[0] != "uploads/2025/" || top.Prefixes[1] != "uploads/2026/" || len(top.Objects) != 0 {
		t.Fatalf("folders %+v", top)
	}
	var keys []string
	token := ""
	pages := 0
	for {
		res, err := c.List(context.Background(), "uploads/2026/", "/", token, 3)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		for _, o := range res.Objects {
			keys = append(keys, o.Key)
			if o.Size != 1 || o.LastModified.IsZero() {
				t.Errorf("object meta %+v", o)
			}
		}
		if !res.Truncated {
			break
		}
		token = res.NextToken
		if pages > 5 {
			t.Fatal("pagination does not end")
		}
	}
	if len(keys) != 7 || pages != 3 {
		t.Fatalf("paged keys %v in %d pages", keys, pages)
	}
	if len(f.Errors) != 0 {
		t.Fatalf("signature errors: %v", f.Errors)
	}
}

func TestClientPresignedGet(t *testing.T) {
	f := newFake(t)
	c := pathClient(f)
	f.PutObject("media", "private/doc.pdf", []byte("%PDF"), "application/pdf")
	u, err := c.PresignGet("private/doc.pdf", 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(b) != "%PDF" {
		t.Fatalf("presigned GET %d %q (%v)", resp.StatusCode, b, f.Errors)
	}
	resp, _ = http.Get(strings.Replace(u, "X-Amz-Signature=", "X-Amz-Signature=0", 1))
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("tampered presigned URL: %d", resp.StatusCode)
	}
}

type listResponseTransport string

func (body listResponseTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
}

func TestListRejectsUnexpectedResponseRoot(t *testing.T) {
	for _, tc := range []struct {
		body    string
		wantErr bool
	}{
		{`<Error><Code>InternalError</Code></Error>`, true},
		{`<Other/>`, true},
		{`<ListBucketResult>`, true},
		{`<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><IsTruncated>false</IsTruncated></ListBucketResult>`, false},
	} {
		c := &gocdn.Client{Endpoint: "https://example.test", Bucket: "bucket", AccessKey: "key", SecretKey: "secret", PathStyle: true, HTTP: &http.Client{Transport: listResponseTransport(tc.body)}}
		if _, err := c.List(context.Background(), "", "", "", 10); (err != nil) != tc.wantErr {
			t.Errorf("List response %q: error = %v, want error %t", tc.body, err, tc.wantErr)
		}
	}
}

func TestListRejectsTruncatedPageWithoutToken(t *testing.T) {
	for _, tc := range []struct {
		body    string
		wantErr bool
	}{
		{`<ListBucketResult><IsTruncated>true</IsTruncated></ListBucketResult>`, true},
		{`<ListBucketResult><IsTruncated>true</IsTruncated><NextContinuationToken></NextContinuationToken></ListBucketResult>`, true},
		{`<ListBucketResult><IsTruncated>true</IsTruncated><NextContinuationToken>next</NextContinuationToken></ListBucketResult>`, false},
		{`<ListBucketResult><IsTruncated>false</IsTruncated></ListBucketResult>`, false},
	} {
		c := &gocdn.Client{Endpoint: "https://example.test", Bucket: "bucket", AccessKey: "key", SecretKey: "secret", PathStyle: true, HTTP: &http.Client{Transport: listResponseTransport(tc.body)}}
		if _, err := c.List(context.Background(), "", "", "", 10); (err != nil) != tc.wantErr {
			t.Errorf("List response %q: error = %v, want error %t", tc.body, err, tc.wantErr)
		}
	}
}
