// Package s3fake is an in-process S3 server for tests: the subset GoCDN uses (PUT, GET, HEAD,
// DELETE object, ListObjectsV2 and presigned GETs), path-style and virtual-host style, with every
// request's Signature Version 4 checked: the Authorization header's shape, x-amz-date,
// x-amz-content-sha256 against the body, and the signature itself recomputed from the request
// as it arrived.
package s3fake

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"wpgo/internal/gocdn"
)

// Object is a stored object.
type Object struct {
	Body         []byte
	ContentType  string
	CacheControl string
	ACL          string
	Modified     time.Time
}

// Request is a logged request.
type Request struct {
	Method, Bucket, Key, Query string
	Header                     http.Header
}

// Server is the fake.
type Server struct {
	*httptest.Server
	AccessKey, SecretKey, Region string

	mu       sync.Mutex
	buckets  map[string]map[string]*Object
	requests []Request
	// FailNext makes the next n requests answer 500 InternalError (resilience tests).
	FailNext int
	// Errors are the signature/shape problems seen (each also answered with 403).
	Errors []string
}

// New starts a fake with one bucket and the given credentials.
func New(accessKey, secretKey, region string, buckets ...string) *Server {
	f := &Server{AccessKey: accessKey, SecretKey: secretKey, Region: region, buckets: map[string]map[string]*Object{}}
	for _, b := range buckets {
		f.buckets[b] = map[string]*Object{}
	}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	return f
}

// Requests returns a copy of the request log.
func (f *Server) Requests() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Request(nil), f.requests...)
}

// Object returns a stored object (nil when missing).
func (f *Server) Object(bucket, key string) *Object {
	f.mu.Lock()
	defer f.mu.Unlock()
	if b := f.buckets[bucket]; b != nil {
		return b[key]
	}
	return nil
}

// PutObject stores an object directly (test setup).
func (f *Server) PutObject(bucket, key string, body []byte, contentType string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.buckets[bucket] == nil {
		f.buckets[bucket] = map[string]*Object{}
	}
	f.buckets[bucket][key] = &Object{Body: body, ContentType: contentType, Modified: time.Now().UTC()}
}

// Keys lists a bucket's keys, sorted.
func (f *Server) Keys(bucket string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for k := range f.buckets[bucket] {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (f *Server) fail(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>%s</Code><Message>%s</Message><RequestId>fake</RequestId></Error>`, code, xmlEscape(msg))
}

func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// route splits bucket and key: virtual-host style when the Host starts with "<bucket>.", else the
// first path segment is the bucket.
func (f *Server) route(r *http.Request) (bucket, key string) {
	host := r.Host
	if i := strings.Index(host, ":"); i >= 0 {
		host = host[:i]
	}
	p := strings.TrimPrefix(r.URL.Path, "/")
	if i := strings.Index(host, "."); i > 0 && net127(host) == false {
		return host[:i], p
	}
	bucket, key, _ = strings.Cut(p, "/")
	return bucket, key
}

// net127 reports whether host is a bare IP address (no bucket in it).
func net127(host string) bool {
	for _, c := range host {
		if (c < '0' || c > '9') && c != '.' {
			return false
		}
	}
	return true
}

func (f *Server) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	bucket, key := f.route(r)
	f.mu.Lock()
	f.requests = append(f.requests, Request{Method: r.Method, Bucket: bucket, Key: key, Query: r.URL.RawQuery, Header: r.Header.Clone()})
	failing := f.FailNext > 0
	if failing {
		f.FailNext--
	}
	f.mu.Unlock()
	if failing {
		f.fail(w, http.StatusInternalServerError, "InternalError", "We encountered an internal error. Please try again.")
		return
	}
	if msg := f.verify(r, body); msg != "" {
		f.mu.Lock()
		f.Errors = append(f.Errors, msg)
		f.mu.Unlock()
		code := "SignatureDoesNotMatch"
		if strings.HasPrefix(msg, "InvalidAccessKeyId") {
			code = "InvalidAccessKeyId"
		}
		f.fail(w, http.StatusForbidden, code, msg)
		return
	}
	f.mu.Lock()
	objects, ok := f.buckets[bucket]
	f.mu.Unlock()
	if !ok {
		f.fail(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist")
		return
	}
	switch {
	case key == "" && r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "2":
		f.list(w, r, objects)
	case key == "":
		f.fail(w, http.StatusNotImplemented, "NotImplemented", "bucket operation not supported by the fake")
	case r.Method == http.MethodPut:
		f.mu.Lock()
		objects[key] = &Object{Body: body, ContentType: r.Header.Get("Content-Type"), CacheControl: r.Header.Get("Cache-Control"),
			ACL: r.Header.Get("X-Amz-Acl"), Modified: time.Now().UTC()}
		f.mu.Unlock()
		w.Header().Set("ETag", `"`+etag(body)+`"`)
		w.WriteHeader(http.StatusOK)
	case r.Method == http.MethodGet || r.Method == http.MethodHead:
		f.mu.Lock()
		o := objects[key]
		f.mu.Unlock()
		if o == nil {
			if r.Method == http.MethodHead {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			f.fail(w, http.StatusNotFound, "NoSuchKey", "The specified key does not exist.")
			return
		}
		data := o.Body
		status := http.StatusOK
		if rg := r.Header.Get("Range"); rg != "" && r.Method == http.MethodGet {
			var a, b int
			if _, err := fmt.Sscanf(rg, "bytes=%d-%d", &a, &b); err == nil && a <= b && a < len(data) {
				if b >= len(data) {
					b = len(data) - 1
				}
				w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", a, b, len(data)))
				data = data[a : b+1]
				status = http.StatusPartialContent
			}
		}
		w.Header().Set("Content-Type", o.ContentType)
		if o.CacheControl != "" {
			w.Header().Set("Cache-Control", o.CacheControl)
		}
		w.Header().Set("ETag", `"`+etag(o.Body)+`"`)
		w.Header().Set("Last-Modified", o.Modified.Format(http.TimeFormat))
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.WriteHeader(status)
		if r.Method == http.MethodGet {
			w.Write(data)
		}
	case r.Method == http.MethodDelete:
		f.mu.Lock()
		delete(objects, key)
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	default:
		f.fail(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func etag(b []byte) string {
	s := md5.Sum(b)
	return hex.EncodeToString(s[:])
}

func (f *Server) list(w http.ResponseWriter, r *http.Request, objects map[string]*Object) {
	q := r.URL.Query()
	prefix, delim := q.Get("prefix"), q.Get("delimiter")
	max := 1000
	if v, err := strconv.Atoi(q.Get("max-keys")); err == nil && v > 0 && v < max {
		max = v
	}
	start := q.Get("continuation-token")
	f.mu.Lock()
	var keys []string
	for k := range objects {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	f.mu.Unlock()
	sort.Strings(keys)
	type content struct {
		Key          string `xml:"Key"`
		LastModified string `xml:"LastModified"`
		ETag         string `xml:"ETag"`
		Size         int    `xml:"Size"`
	}
	type cp struct {
		Prefix string `xml:"Prefix"`
	}
	res := struct {
		XMLName               xml.Name  `xml:"ListBucketResult"`
		Name                  string    `xml:"Name"`
		Prefix                string    `xml:"Prefix"`
		KeyCount              int       `xml:"KeyCount"`
		MaxKeys               int       `xml:"MaxKeys"`
		IsTruncated           bool      `xml:"IsTruncated"`
		NextContinuationToken string    `xml:"NextContinuationToken,omitempty"`
		Contents              []content `xml:"Contents"`
		CommonPrefixes        []cp      `xml:"CommonPrefixes"`
	}{Prefix: prefix, MaxKeys: max}
	seen := map[string]bool{}
	n := 0
	for _, k := range keys {
		entry := k
		isPrefix := false
		if delim != "" {
			if i := strings.Index(k[len(prefix):], delim); i >= 0 {
				entry = k[:len(prefix)+i+len(delim)]
				isPrefix = true
			}
		}
		if start != "" && entry <= start {
			continue
		}
		if seen[entry] {
			continue
		}
		if n == max {
			res.IsTruncated = true
			break
		}
		seen[entry] = true
		n++
		if isPrefix {
			res.CommonPrefixes = append(res.CommonPrefixes, cp{Prefix: entry})
		} else {
			f.mu.Lock()
			o := objects[k]
			f.mu.Unlock()
			res.Contents = append(res.Contents, content{Key: k, LastModified: o.Modified.Format(time.RFC3339), ETag: `"` + etag(o.Body) + `"`, Size: len(o.Body)})
		}
		res.NextContinuationToken = entry
	}
	if !res.IsTruncated {
		res.NextContinuationToken = ""
	}
	res.KeyCount = n
	w.Header().Set("Content-Type", "application/xml")
	_ = xml.NewEncoder(w).Encode(res)
}

// verify checks the request's SigV4 signature (header or presigned query); "" when it is right.
func (f *Server) verify(r *http.Request, body []byte) string {
	q := r.URL.Query()
	signer := gocdn.Signer{AccessKey: f.AccessKey, SecretKey: f.SecretKey, Region: f.Region}
	if q.Get("X-Amz-Algorithm") != "" { // presigned URL
		t, err := time.Parse("20060102T150405Z", q.Get("X-Amz-Date"))
		if err != nil {
			return "bad X-Amz-Date"
		}
		exp, _ := strconv.Atoi(q.Get("X-Amz-Expires"))
		if time.Now().After(t.Add(time.Duration(exp) * time.Second)) {
			return "Request has expired"
		}
		if cred := q.Get("X-Amz-Credential"); cred != f.AccessKey+"/"+signer.Scope(t) {
			if !strings.HasPrefix(cred, f.AccessKey+"/") {
				return "InvalidAccessKeyId: " + cred
			}
			return "credential scope mismatch: " + cred
		}
		sig := q.Get("X-Amz-Signature")
		q.Del("X-Amz-Signature")
		creq := gocdn.CanonicalRequest(r.Method, gocdn.CanonicalURI(r.URL), gocdn.CanonicalQuery(q), "host:"+r.Host+"\n", "host", gocdn.UnsignedPayload)
		if signer.SignatureFor(t, creq) != sig {
			return "presigned signature does not match"
		}
		return ""
	}
	cred, signed, sig, ok := gocdn.ParseAuthorization(r.Header.Get("Authorization"))
	if !ok {
		return "missing or malformed Authorization header: " + r.Header.Get("Authorization")
	}
	t, err := time.Parse("20060102T150405Z", r.Header.Get("X-Amz-Date"))
	if err != nil {
		return "missing x-amz-date"
	}
	if d := time.Since(t); d > 15*time.Minute || d < -15*time.Minute {
		return "RequestTimeTooSkewed"
	}
	if !strings.HasPrefix(cred, f.AccessKey+"/") {
		return "InvalidAccessKeyId: the access key does not exist"
	}
	if cred != f.AccessKey+"/"+signer.Scope(t) {
		return "credential scope mismatch: " + cred
	}
	ph := r.Header.Get("X-Amz-Content-Sha256")
	if ph == "" {
		return "missing x-amz-content-sha256"
	}
	if ph != gocdn.UnsignedPayload && ph != gocdn.SHA256Hex(body) {
		return "XAmzContentSHA256Mismatch"
	}
	vals := map[string][]string{}
	for _, name := range strings.Split(signed, ";") {
		if name == "host" {
			vals["host"] = []string{r.Host}
			continue
		}
		v := r.Header.Values(name)
		if len(v) == 0 {
			return "signed header missing: " + name
		}
		vals[name] = v
	}
	if _, ok := vals["host"]; !ok {
		return "host is not signed"
	}
	if _, ok := vals["x-amz-date"]; !ok {
		return "x-amz-date is not signed"
	}
	names, block := gocdn.CanonicalHeaderBlock(vals)
	creq := gocdn.CanonicalRequest(r.Method, gocdn.CanonicalURI(r.URL), gocdn.CanonicalQuery(r.URL.Query()), block, names, ph)
	if signer.SignatureFor(t, creq) != sig {
		return "The request signature we calculated does not match the signature you provided."
	}
	return ""
}

// URL helpers for tests.

// Endpoint is the fake's base URL (path-style).
func (f *Server) Endpoint() string { return f.URL }

// ParseURL is url.Parse that panics (test convenience).
func ParseURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}
