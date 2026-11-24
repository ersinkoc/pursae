package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"wpgo/internal/backupcrypt"
	"wpgo/internal/config"
	"wpgo/internal/gocdn"
	"wpgo/internal/gocdn/s3fake"
)

func TestBmgrRemoteCheckEndpoint(t *testing.T) {
	for _, tc := range []struct {
		url          string
		allowPrivate bool
		ok           bool
	}{
		{"https://s3.eu-central-1.amazonaws.com", false, true},
		{"https://abc123.r2.cloudflarestorage.com", false, true},
		{"https://s3.us-west-004.backblazeb2.com:443", false, true},
		{"https://8.8.8.8", false, true},
		{"http://s3.amazonaws.com", false, false}, // plain http
		{"ftp://s3.amazonaws.com", false, false},  // scheme
		{"https://user:pw@s3.amazonaws.com", false, false},
		{"https://s3.amazonaws.com/?x=1", false, false},
		{"https://s3.amazonaws.com/#frag", false, false},
		{"https://", false, false},
		{"s3.amazonaws.com", false, false},
		{"https://127.0.0.1:9000", false, false},
		{"https://localhost:9000", false, false},
		{"https://minio.localhost", false, false},
		{"https://10.0.0.5", false, false},
		{"https://[::1]:9000", false, false},
		{"https://192.168.1.10:9000", false, false},
		{"https://100.64.1.1", false, false},
		{"https://169.254.169.254", false, false},
		{"http://127.0.0.1:9000", true, true}, // a MinIO on this host, explicitly allowed
		{"http://10.0.0.5:9000", true, true},
		{"https://localhost:9000", true, true},
		{"http://169.254.169.254", true, false}, // metadata: never, not even with the override
		{"http://[fe80::1]:9000", true, false},
		{"http://100.100.100.200", true, false},
		{"http://[fd00:ec2::254]", true, false},
		{"https://0.0.0.0", true, false},
		{"https://s3.amazonaws.com:99999", false, false},
		{"https://8.8.8.8", true, true},
		{"http://[fc00::1]", true, true},        // IPv6 unique-local, a MinIO on the private network
		{"http://100.64.1.1", true, true},       // carrier-grade NAT
		{"https://168.63.129.16", false, false}, // Azure WireServer
		{"https://168.63.129.16", true, false},
		{"https://[::ffff:168.63.129.16]", true, false},
		{"https://[64:ff9b::a9fe:a9fe]", false, false}, // NAT64 of 169.254.169.254
		{"https://[64:ff9b::a9fe:a9fe]", true, false},
		{"https://[2002:a9fe:a9fe::]", true, false}, // 6to4 of 169.254.169.254
		{"https://[64:ff9b::808:808]", false, true}, // NAT64 of 8.8.8.8 stays reachable
		{"https://192.0.2.1", true, false},          // documentation range, not a private LAN
	} {
		err := bmgrRemoteCheckEndpoint(tc.url, tc.allowPrivate)
		if (err == nil) != tc.ok {
			t.Errorf("%s (allowPrivate=%v): err=%v, want ok=%v", tc.url, tc.allowPrivate, err, tc.ok)
		}
	}
}

// The storage client checks the address it connects to after DNS resolution, and follows no redirect.
func TestBmgrRemoteHTTPGuards(t *testing.T) {
	hits := 0
	inner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer inner.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, inner.URL+"/latest/meta-data/", http.StatusFound)
	}))
	defer redirect.Close()

	if _, err := bmgrRemoteHTTP(false).Get(inner.URL); err == nil || !strings.Contains(err.Error(), "private network") {
		t.Errorf("loopback reached without the override: %v", err)
	}
	resp, err := bmgrRemoteHTTP(true).Get(redirect.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || hits != 0 {
		t.Errorf("redirect followed: status %d, %d hits on the target", resp.StatusCode, hits)
	}
	// Through the S3 client a redirect is an error, not a success.
	cl := &gocdn.Client{Endpoint: redirect.URL, Region: "us-east-1", Bucket: "b", AccessKey: "AK", SecretKey: "SK", PathStyle: true, HTTP: bmgrRemoteHTTP(true)}
	if err := cl.Put(context.Background(), "k", []byte("x"), gocdn.PutOptions{}); err == nil {
		t.Error("a redirected PUT counted as success")
	}
	if hits != 0 {
		t.Errorf("the redirect target was reached %d times", hits)
	}
}

func TestBmgrRemoteRedact(t *testing.T) {
	st := bmgrRemoteSettings{AccessKey: "AKIAEXAMPLEKEY", SecretKey: "wJalrXUtnFEMI/K7MDENG", Passphrase: "a long passphrase"}
	err := st.redact(errors.New(`dial: wJalrXUtnFEMI/K7MDENG with a long passphrase for AKIAEXAMPLEKEY`))
	for _, secret := range []string{st.AccessKey, st.SecretKey, st.Passphrase} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("%q left in %q", secret, err)
		}
	}
	if st.redact(nil) != nil {
		t.Error("nil error changed")
	}
}

// A name that resolves to loopback or link-local is refused after DNS, including when the
// connected host is bucket.endpoint rather than the endpoint that was saved.
func TestBmgrRemoteHTTPBlocksResolvedLoopback(t *testing.T) {
	ips, err := net.LookupIP("127.0.0.1.nip.io")
	if err != nil || len(ips) == 0 || !ips[0].IsLoopback() {
		t.Skip("resolver did not map 127.0.0.1.nip.io to a loopback address")
	}
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer srv.Close()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatal(err)
	}
	target := "http://127.0.0.1.nip.io:" + port + "/latest/meta-data/"
	if _, err := bmgrRemoteHTTP(false).Get(target); err == nil || !strings.Contains(err.Error(), "private network") {
		t.Fatalf("loopback name reached: %v", err)
	}
	if hits != 0 {
		t.Fatalf("handler was called %d times", hits)
	}
	resp, err := bmgrRemoteHTTP(true).Get(target)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if hits != 1 {
		t.Fatalf("allow-private did not reach the loopback name, hits=%d", hits)
	}

	meta, err := net.LookupIP("169.254.169.254.nip.io")
	linkLocal := err == nil && len(meta) > 0
	for _, ip := range meta {
		if !ip.IsLinkLocalUnicast() {
			linkLocal = false
		}
	}
	if !linkLocal {
		t.Skip("resolver did not map 169.254.169.254.nip.io to only link-local addresses")
	}
	cl := &gocdn.Client{Endpoint: "https://nip.io", Region: "us-east-1", Bucket: "169.254.169.254", AccessKey: "AK", SecretKey: "SK",
		HTTP: bmgrRemoteHTTP(true)}
	if err := cl.Put(context.Background(), "k", []byte("x"), gocdn.PutOptions{}); err == nil || !strings.Contains(err.Error(), "link-local") {
		t.Fatalf("virtual-hosted metadata name was not refused: %v", err)
	}
}

// Two backups uploaded in the same second used to be ordered by name. The random suffix then
// deleted the object that had just been uploaded. The key passed as protect is the one kept.
func TestBmgrRemotePruneKeepsJustUploaded(t *testing.T) {
	fake := s3fake.New("AK", "SK", "us-east-1", "bk")
	defer fake.Close()
	cl := &gocdn.Client{Endpoint: fake.URL, Region: "us-east-1", Bucket: "bk", AccessKey: "AK", SecretKey: "SK", PathStyle: true,
		HTTP: bmgrRemoteHTTP(true)}
	st := bmgrRemoteSettings{Prefix: "p/"}
	put := func(name string, when time.Time) string {
		t.Helper()
		key := "p/" + name
		fake.PutObject("bk", key, []byte(name), "")
		fake.Object("bk", key).Modified = when
		return key
	}
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Same LastModified, as S3 reports for two uploads in one second. The greater name would win
	// the old sort; the upload that just finished is the lower name.
	high := put("backup-20260101-120000-ffffffff.zip.enc", t0)
	low := put("backup-20260101-120000-00000000.zip.enc", t0)
	if _, err := bmgrRemotePrune(context.Background(), cl, st, 1, low); err != nil {
		t.Fatal(err)
	}
	if fake.Object("bk", low) == nil || fake.Object("bk", high) != nil {
		t.Fatalf("timestamp tie kept the wrong object: %v", fake.Keys("bk"))
	}

	// The protected object is kept even when its timestamp is the oldest of the set, and the
	// next-newest of the others fills the remaining slots.
	fake = s3fake.New("AK", "SK", "us-east-1", "bk")
	defer fake.Close()
	cl.Endpoint = fake.URL
	oldest := put("backup-20260101-000000-00000000.zip.enc", t0)
	middle := put("backup-20260101-000001-aaaaaaaa.zip.enc", t0.Add(time.Second))
	newest := put("backup-20260101-000002-ffffffff.zip.enc", t0.Add(2*time.Second))
	if _, err := bmgrRemotePrune(context.Background(), cl, st, 2, oldest); err != nil {
		t.Fatal(err)
	}
	if fake.Object("bk", oldest) == nil || fake.Object("bk", newest) == nil || fake.Object("bk", middle) != nil {
		t.Fatalf("protected object not kept with the newest other: %v", fake.Keys("bk"))
	}
}

func TestBmgrRemoteSealEncryptsPrefixedSecret(t *testing.T) {
	s := &Server{cfg: config.Config{Secret: "site-secret-for-remote-seal"}}
	const plain = "bkr:v1:this-is-a-real-passphrase"
	sealed, err := s.bmgrRemoteSeal(plain)
	if err != nil {
		t.Fatal(err)
	}
	if sealed == plain || !strings.HasPrefix(sealed, bmgrRemoteSealPfx) || strings.Contains(sealed, "this-is-a-real-passphrase") {
		t.Fatalf("secret stored so it can be read back: %q", sealed)
	}
	if got := s.bmgrRemoteReveal(sealed); got != plain {
		t.Fatalf("reveal = %q", got)
	}
	again, err := s.bmgrRemoteSeal(plain)
	if err != nil || again == sealed {
		t.Fatal("a second seal reused the nonce or failed")
	}
	prev := bmgrRand
	bmgrRand = failReader{}
	t.Cleanup(func() { bmgrRand = prev })
	if _, err := s.bmgrRemoteSeal("another-long-passphrase"); err == nil {
		t.Fatal("a seal with no entropy was stored")
	}
}

type failReader struct{}

func (failReader) Read([]byte) (int, error) { return 0, errors.New("no entropy") }

func TestBmgrRemoteCleanPrefix(t *testing.T) {
	for in, want := range map[string]string{"": "", "/": "", "backups": "backups/", "/a/b/": "a/b/", "a//b": "a/b/", " x ": "x/"} {
		if got := bmgrRemoteCleanPrefix(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

// bmgrRemotePutStream: one PUT for a small stream, a multipart upload with retried parts for a
// large one, and an aborted upload (no orphaned parts) when a part keeps failing.
func TestBmgrRemotePutStream(t *testing.T) {
	fake := s3fake.New("AK", "SK-secret", "us-east-1", "bk")
	defer fake.Close()
	fake.MinPartSize = 1024
	old := bmgrRemotePartSize
	bmgrRemotePartSize = 64 << 10
	defer func() { bmgrRemotePartSize = old }()
	failParts := &partFailer{next: bmgrRemoteHTTP(true).Transport}
	cl := &gocdn.Client{Endpoint: fake.URL, Region: "us-east-1", Bucket: "bk", AccessKey: "AK", SecretKey: "SK-secret", PathStyle: true,
		HTTP: &http.Client{Transport: failParts}}
	ctx := context.Background()

	small := []byte("tiny backup")
	if n, err := bmgrRemotePutStream(ctx, cl, "p/small", bytes.NewReader(small), 0, nil); err != nil || n != int64(len(small)) {
		t.Fatalf("small: %d %v", n, err)
	}
	if o := fake.Object("bk", "p/small"); o == nil || !bytes.Equal(o.Body, small) {
		t.Fatal("small object not stored")
	}

	big := make([]byte, 5*64<<10+777)
	_, _ = rand.Read(big)
	failParts.fail.Store(1) // one part fails once and is retried
	var last int64
	n, err := bmgrRemotePutStream(ctx, cl, "p/big", bytes.NewReader(big), int64(len(big)), func(done int64) { last = done })
	if err != nil || n != int64(len(big)) || last != n {
		t.Fatalf("big: %d %v (progress %d)", n, err, last)
	}
	if o := fake.Object("bk", "p/big"); o == nil || !bytes.Equal(o.Body, big) || o.Parts != 6 {
		t.Fatal("multipart object differs")
	}
	if fake.OpenUploads() != 0 {
		t.Errorf("open uploads=%d", fake.OpenUploads())
	}

	failParts.fail.Store(1000) // every part fails: the upload is aborted
	if _, err := bmgrRemotePutStream(ctx, cl, "p/fails", bytes.NewReader(big), 0, nil); err == nil {
		t.Fatal("an upload whose parts all fail succeeded")
	}
	failParts.fail.Store(0)
	if fake.Object("bk", "p/fails") != nil || fake.OpenUploads() != 0 {
		t.Errorf("a failed upload left an object or %d open uploads", fake.OpenUploads())
	}

	// A reader error mid-stream aborts too.
	r := io.MultiReader(bytes.NewReader(big[:200<<10]), iotestErrReader{})
	if _, err := bmgrRemotePutStream(ctx, cl, "p/broken", r, 0, nil); err == nil {
		t.Fatal("a broken stream was uploaded")
	}
	if fake.Object("bk", "p/broken") != nil || fake.OpenUploads() != 0 {
		t.Error("a broken stream left an object or an open upload")
	}
}

// partFailer answers UploadPart requests with 500 while fail is above zero.
type partFailer struct {
	next http.RoundTripper
	fail atomic.Int64
}

func (p *partFailer) RoundTrip(r *http.Request) (*http.Response, error) {
	if strings.Contains(r.URL.RawQuery, "partNumber=") && p.fail.Add(-1) >= 0 {
		if r.Body != nil {
			r.Body.Close()
		}
		return &http.Response{StatusCode: 500, Status: "500 Internal Server Error", Header: http.Header{}, Request: r,
			Body: io.NopCloser(strings.NewReader("<Error><Code>InternalError</Code></Error>"))}, nil
	}
	if p.fail.Load() < 0 {
		p.fail.Store(0)
	}
	return p.next.RoundTrip(r)
}

type iotestErrReader struct{}

func (iotestErrReader) Read([]byte) (int, error) { return 0, errors.New("disk read error") }

// An object uploaded through the encrypting pipe decrypts to the original.
func TestBmgrRemoteEncryptedPipe(t *testing.T) {
	fake := s3fake.New("AK", "SK", "us-east-1", "bk")
	defer fake.Close()
	fake.MinPartSize = 1024
	old := bmgrRemotePartSize
	bmgrRemotePartSize = 32 << 10
	defer func() { bmgrRemotePartSize = old }()
	cl := &gocdn.Client{Endpoint: fake.URL, Region: "us-east-1", Bucket: "bk", AccessKey: "AK", SecretKey: "SK", PathStyle: true, HTTP: bmgrRemoteHTTP(true)}
	plain := bytes.Repeat([]byte("PK\x03\x04 archive data "), 30000)
	pr, pw := io.Pipe()
	go func() {
		_, err := backupcrypt.EncryptStream(pw, bytes.NewReader(plain), []byte("passphrase-1234"))
		pw.CloseWithError(err)
	}()
	if _, err := bmgrRemotePutStream(context.Background(), cl, "x.zip.enc", pr, int64(len(plain)), nil); err != nil {
		t.Fatal(err)
	}
	o := fake.Object("bk", "x.zip.enc")
	if o == nil || bytes.Contains(o.Body, []byte("archive data")) {
		t.Fatal("stored object missing or readable")
	}
	var back bytes.Buffer
	if _, err := backupcrypt.DecryptStream(&back, bytes.NewReader(o.Body), []byte("passphrase-1234")); err != nil || !bytes.Equal(back.Bytes(), plain) {
		t.Fatalf("decrypt: %v", err)
	}
}
