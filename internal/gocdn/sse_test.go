package gocdn_test

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"wpgo/internal/gocdn"
)

func testKey(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

func TestSSEKMS(t *testing.T) {
	f := newFake(t)
	c := pathClient(f)
	ctx := context.Background()
	o := gocdn.PutOptions{ContentType: "text/plain", SSE: "aws:kms", SSEKMSKeyID: "arn:aws:kms:eu-west-1:111122223333:key/abcd"}
	if err := c.Put(ctx, "k", []byte("hello"), o); err != nil {
		t.Fatal(err)
	}
	if got := f.Object("media", "k"); got.SSE != "aws:kms" || got.KMSKeyID != o.SSEKMSKeyID {
		t.Fatalf("stored %+v", got)
	}
	// Multipart and copy carry the key id too.
	big := bytes.Repeat([]byte("x"), 6<<20)
	if _, err := c.Upload(ctx, "big", bytes.NewReader(big), int64(len(big)), gocdn.UploadOptions{PutOptions: o, Threshold: 5 << 20, PartSize: 5 << 20}); err != nil {
		t.Fatal(err)
	}
	if got := f.Object("media", "big"); got.Parts < 2 || got.KMSKeyID != o.SSEKMSKeyID {
		t.Fatalf("multipart %+v", got)
	}
	if err := c.Copy(ctx, "media", "k", "k2", o); err != nil {
		t.Fatal(err)
	}
	if got := f.Object("media", "k2"); got.KMSKeyID != o.SSEKMSKeyID {
		t.Fatalf("copy %+v", got)
	}
	// SSE-KMS without a key id uses the account's default key: no key-id header.
	if err := c.Put(ctx, "k3", []byte("x"), gocdn.PutOptions{SSE: "aws:kms"}); err != nil || f.Object("media", "k3").KMSKeyID != "" {
		t.Fatalf("default kms key: %v", err)
	}
}

func TestSSEC(t *testing.T) {
	f := newFake(t)
	c := pathClient(f)
	c.SSECKey = testKey(7)
	ctx := context.Background()
	if err := c.Put(ctx, "a", []byte("secret"), gocdn.PutOptions{ContentType: "text/plain", SSE: "AES256"}); err != nil {
		t.Fatal(err) // SSE-S3 in the options is dropped for SSE-C
	}
	if f.Object("media", "a").SSECKeyMD5 == "" || f.Object("media", "a").SSE != "" {
		t.Fatalf("stored %+v", f.Object("media", "a"))
	}
	// GET, HEAD and ranged GET send the key.
	rc, _, err := c.Get(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	if string(b) != "secret" {
		t.Fatalf("get %q", b)
	}
	if h, err := c.Head(ctx, "a"); err != nil || h.Size != 6 {
		t.Fatalf("head %+v %v", h, err)
	}
	if part, err := c.GetRange(ctx, "a", 1, 3); err != nil || string(part) != "ecr" {
		t.Fatalf("range %q %v", part, err)
	}
	// Without the key, or with another one, the object cannot be read.
	plain := pathClient(f)
	if _, _, err := plain.Get(ctx, "a"); err == nil || !strings.Contains(err.Error(), "400") {
		t.Fatalf("get without key: %v", err)
	}
	wrong := pathClient(f)
	wrong.SSECKey = testKey(8)
	if _, _, err := wrong.Get(ctx, "a"); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("get with the wrong key: %v", err)
	}
	// Multipart: every part carries the key.
	big := bytes.Repeat([]byte("y"), 6<<20)
	if _, err := c.Upload(ctx, "big", bytes.NewReader(big), int64(len(big)), gocdn.UploadOptions{Threshold: 5 << 20, PartSize: 5 << 20}); err != nil {
		t.Fatal(err)
	}
	if got := f.Object("media", "big"); got.Parts < 2 || got.SSECKeyMD5 == "" {
		t.Fatalf("multipart %+v", got)
	}
	// Copy: source key for reading, destination key for writing (a key rotation).
	dst := pathClient(f)
	dst.SSECKey = testKey(9)
	if err := dst.Copy(ctx, "media", "a", "b", gocdn.PutOptions{CopySourceSSECKey: testKey(7)}); err != nil {
		t.Fatal(err)
	}
	if f.Object("media", "b").SSECKeyMD5 == f.Object("media", "a").SSECKeyMD5 {
		t.Error("the copy kept the old key")
	}
	if rc, _, err := dst.Get(ctx, "b"); err != nil {
		t.Fatalf("read the rotated copy: %v", err)
	} else {
		rc.Close()
	}
	if err := dst.Copy(ctx, "media", "a", "c", gocdn.PutOptions{}); err == nil {
		t.Error("copying an SSE-C source without its key should fail")
	}
	// Delete and listing need no key.
	if err := plain.Delete(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if res, err := c.List(ctx, "", "", "", 10); err != nil || len(res.Objects) == 0 {
		t.Fatalf("list %v", err)
	}
	// No presigned URLs (the headers cannot be put in a link), and no SSE-C over plain http away
	// from loopback.
	if _, err := c.PresignGet("b", 60); err == nil {
		t.Error("presign with SSE-C")
	}
	remote := &gocdn.Client{Endpoint: "http://s3.example.com", Region: "x", Bucket: "b", AccessKey: "a", SecretKey: "s", SSECKey: testKey(1)}
	if err := remote.Validate(); err == nil || !strings.Contains(err.Error(), "https") {
		t.Errorf("SSE-C over http: %v", err)
	}
}

func TestParseSSECKey(t *testing.T) {
	hexKey := strings.Repeat("ab", 32)
	if k, err := gocdn.ParseSSECKey(hexKey); err != nil || len(k) != 32 {
		t.Errorf("hex: %v", err)
	}
	if k, err := gocdn.ParseSSECKey("q83vEjRWeJqrze8SNFZ4mqvN7xI0VniaK83vEjRWeJo="); err != nil || len(k) != 32 {
		t.Errorf("base64: %v", err)
	}
	for _, bad := range []string{"", "short", strings.Repeat("zz", 32)} {
		if _, err := gocdn.ParseSSECKey(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestSSECLocalhostNamesAreCaseInsensitive(t *testing.T) {
	for _, endpoint := range []string{"http://LOCALHOST:9000", "http://Dev.LocalHost:9000", "http://localhost.:9000"} {
		c := &gocdn.Client{Endpoint: endpoint, Bucket: "bucket", AccessKey: "key", SecretKey: "secret", SSECKey: testKey(1)}
		if err := c.Validate(); err != nil {
			t.Errorf("%s: %v", endpoint, err)
		}
	}
}
