package gocdn

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client talks to one bucket of an S3-compatible object storage.
type Client struct {
	Endpoint  string // https://s3.eu-central-1.amazonaws.com, https://<account>.r2.cloudflarestorage.com, http://127.0.0.1:9000 …
	Region    string // "auto" for R2
	Bucket    string
	AccessKey string
	SecretKey string
	PathStyle bool // https://endpoint/bucket/key instead of https://bucket.endpoint/key
	// SSECKey is the 256-bit customer key of SSE-C: sent with every request that reads or writes
	// object data (PUT, multipart, GET, HEAD, the source of a COPY). Objects written with it cannot
	// be read without it, nor through a public or presigned URL. Never logged.
	SSECKey []byte
	HTTP    *http.Client
	Now     func() time.Time
}

// Object is an entry of a listing (or the result of a HEAD).
type Object struct {
	Key          string
	Size         int64
	ETag         string
	LastModified time.Time
	ContentType  string
}

// ListResult is one page of ListObjectsV2.
type ListResult struct {
	Objects   []Object
	Prefixes  []string // "folders" when a delimiter was given
	NextToken string   // continuation token for the next page, "" at the end
	Truncated bool
}

// PutOptions are the optional headers of a PUT (and of CreateMultipartUpload / CopyObject).
type PutOptions struct {
	ContentType       string
	CacheControl      string
	ACL               string // "public-read" or "" (bucket default)
	SSE               string // x-amz-server-side-encryption: "AES256" (SSE-S3), "aws:kms" or "" (bucket default)
	CopySourceSSECKey []byte // Copy only: the SSE-C key of the source object, when it has one
	SSEKMSKeyID       string // x-amz-server-side-encryption-aws-kms-key-id with SSE "aws:kms" ("" = the account's aws/s3 key)
	StorageClass      string // x-amz-storage-class: STANDARD, STANDARD_IA, INTELLIGENT_TIERING… or ""
}

func (o PutOptions) headers() http.Header {
	h := http.Header{}
	if o.ContentType != "" {
		h.Set("Content-Type", o.ContentType)
	}
	if o.CacheControl != "" {
		h.Set("Cache-Control", o.CacheControl)
	}
	if o.ACL != "" {
		h.Set("X-Amz-Acl", o.ACL)
	}
	if o.SSE != "" {
		h.Set("X-Amz-Server-Side-Encryption", o.SSE)
	}
	if o.StorageClass != "" {
		h.Set("X-Amz-Storage-Class", o.StorageClass)
	}
	if o.SSE == "aws:kms" && o.SSEKMSKeyID != "" {
		h.Set("X-Amz-Server-Side-Encryption-Aws-Kms-Key-Id", o.SSEKMSKeyID)
	}
	return h
}

// ParseSSECKey reads a customer key given as 64 hex characters or as base64 of 32 bytes.
func ParseSSECKey(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if len(s) == 64 {
		if k, err := hex.DecodeString(s); err == nil {
			return k, nil
		}
	}
	if k, err := base64.StdEncoding.DecodeString(s); err == nil && len(k) == 32 {
		return k, nil
	}
	return nil, errors.New("the SSE-C key must be 32 bytes: 64 hex characters or 44 base64 characters")
}

// sseCHeaders sets the three SSE-C headers for key under the given header prefix
// ("X-Amz-Server-Side-Encryption-Customer-" or the "X-Amz-Copy-Source-…" variant).
func sseCHeaders(h http.Header, prefix string, key []byte) {
	sum := md5.Sum(key)
	h.Set(prefix+"Algorithm", "AES256")
	h.Set(prefix+"Key", base64.StdEncoding.EncodeToString(key))
	h.Set(prefix+"Key-Md5", base64.StdEncoding.EncodeToString(sum[:]))
}

const (
	sseCPrefix     = "X-Amz-Server-Side-Encryption-Customer-"
	sseCCopyPrefix = "X-Amz-Copy-Source-Server-Side-Encryption-Customer-"
)

// withSSEC adds the client's SSE-C headers to h (made when nil). Those replace any SSE-S3/KMS
// header: S3 refuses the combination.
func (c *Client) withSSEC(h http.Header) http.Header {
	if len(c.SSECKey) == 0 {
		return h
	}
	if h == nil {
		h = http.Header{}
	}
	h.Del("X-Amz-Server-Side-Encryption")
	h.Del("X-Amz-Server-Side-Encryption-Aws-Kms-Key-Id")
	sseCHeaders(h, sseCPrefix, c.SSECKey)
	return h
}

// Error is an S3 error response.
type Error struct {
	Status    int
	Code      string
	Message   string
	RequestID string
}

func (e *Error) Error() string {
	msg := e.Code
	if msg == "" {
		msg = http.StatusText(e.Status)
	}
	if e.Message != "" {
		msg += ": " + e.Message
	}
	return fmt.Sprintf("S3 %d %s", e.Status, msg)
}

// ErrNotFound is returned (wrapped in *Error) for a missing object; use IsNotFound.
func IsNotFound(err error) bool {
	var e *Error
	return errors.As(err, &e) && (e.Status == http.StatusNotFound || e.Code == "NoSuchKey")
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 120 * time.Second}
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Client) signer() Signer {
	return Signer{AccessKey: c.AccessKey, SecretKey: c.SecretKey, Region: c.Region, Service: "s3"}
}

// Validate reports a configuration that cannot work.
func (c *Client) Validate() error {
	switch {
	case c.Bucket == "":
		return errors.New("no bucket name")
	case c.AccessKey == "" || c.SecretKey == "":
		return errors.New("no access key or secret key")
	case len(c.SSECKey) != 0 && len(c.SSECKey) != 32:
		return errors.New("the SSE-C key must be 32 bytes")
	}
	u, err := url.Parse(c.Endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("the endpoint must be an http(s) URL")
	}
	if len(c.SSECKey) != 0 && u.Scheme != "https" && !isLoopback(u.Hostname()) {
		return errors.New("SSE-C sends the key with every request and needs an https endpoint")
	}
	return nil
}

func isLoopback(host string) bool {
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// objectURL is the request URL of a key ("" for the bucket itself).
func (c *Client) objectURL(key string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimRight(c.Endpoint, "/"))
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("bad endpoint %q", c.Endpoint)
	}
	base := strings.TrimRight(u.Path, "/")
	p := base + "/"
	if c.PathStyle {
		p += c.Bucket + "/"
	} else {
		u.Host = c.Bucket + "." + u.Host
	}
	p += key
	u.Path = p
	u.RawPath = EscapePath(p)
	u.RawQuery = ""
	return u, nil
}

// do signs and sends a request; a non-2xx answer becomes an *Error.
func (c *Client) do(ctx context.Context, method, key string, query url.Values, body []byte, hdr http.Header) (*http.Response, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	u, err := c.objectURL(key)
	if err != nil {
		return nil, err
	}
	if len(query) > 0 {
		u.RawQuery = CanonicalQuery(query)
	}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rd)
	if err != nil {
		return nil, err
	}
	req.URL = u // keep our RawPath
	for k, vs := range hdr {
		req.Header[k] = vs
	}
	hash := EmptyPayloadHash
	if body != nil {
		sum := sha256.Sum256(body)
		hash = hex.EncodeToString(sum[:])
		req.ContentLength = int64(len(body))
	}
	c.signer().Sign(req, hash, c.now())
	resp, err := c.http().Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		defer resp.Body.Close()
		return nil, parseError(resp)
	}
	return resp, nil
}

func parseError(resp *http.Response) error {
	e := &Error{Status: resp.StatusCode, RequestID: resp.Header.Get("X-Amz-Request-Id")}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var x struct {
		Code      string `xml:"Code"`
		Message   string `xml:"Message"`
		RequestID string `xml:"RequestId"`
	}
	if len(data) > 0 && xml.Unmarshal(data, &x) == nil {
		e.Code, e.Message = x.Code, x.Message
		if x.RequestID != "" {
			e.RequestID = x.RequestID
		}
	}
	if e.Code == "" {
		switch resp.StatusCode {
		case http.StatusNotFound:
			e.Code = "NotFound"
		case http.StatusForbidden:
			e.Code = "AccessDenied"
		}
	}
	return e
}

// Put uploads an object.
func (c *Client) Put(ctx context.Context, key string, body []byte, o PutOptions) error {
	if body == nil {
		body = []byte{}
	}
	resp, err := c.do(ctx, http.MethodPut, key, nil, body, c.withSSEC(o.headers()))
	if err != nil {
		return err
	}
	io.Copy(io.Discard, resp.Body)
	return resp.Body.Close()
}

// Get downloads an object; the caller closes the body.
func (c *Client) Get(ctx context.Context, key string) (io.ReadCloser, Object, error) {
	resp, err := c.do(ctx, http.MethodGet, key, nil, nil, c.withSSEC(nil))
	if err != nil {
		return nil, Object{}, err
	}
	return resp.Body, objectFromHeaders(key, resp.Header, resp.ContentLength), nil
}

// Partial is the answer of GetRanged.
type Partial struct {
	Body         io.ReadCloser
	Object       Object // Size is the length of this answer
	Status       int    // 200, or 206 for a range
	ContentRange string // "bytes 0-99/1234" with 206
}

// GetRanged is Get with an HTTP Range header ("bytes=…", "" for the whole object) passed to the
// storage, for proxying media to players that seek (and Safari, which plays video only from
// servers that answer ranges). The caller closes the body.
func (c *Client) GetRanged(ctx context.Context, key, rng string) (*Partial, error) {
	h := c.withSSEC(http.Header{})
	if rng != "" {
		h.Set("Range", rng)
	}
	resp, err := c.do(ctx, http.MethodGet, key, nil, nil, h)
	if err != nil {
		return nil, err
	}
	return &Partial{Body: resp.Body, Object: objectFromHeaders(key, resp.Header, resp.ContentLength), Status: resp.StatusCode,
		ContentRange: resp.Header.Get("Content-Range")}, nil
}

// GetRange downloads part of an object (first..last bytes, inclusive).
func (c *Client) GetRange(ctx context.Context, key string, first, last int64) ([]byte, error) {
	if first < 0 || last < first {
		return nil, errors.New("invalid byte range")
	}
	h := c.withSSEC(http.Header{})
	h.Set("Range", "bytes="+strconv.FormatInt(first, 10)+"-"+strconv.FormatInt(last, 10))
	resp, err := c.do(ctx, http.MethodGet, key, nil, nil, h)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, last-first+1))
}

// Head returns an object's metadata.
func (c *Client) Head(ctx context.Context, key string) (Object, error) {
	resp, err := c.do(ctx, http.MethodHead, key, nil, nil, c.withSSEC(nil))
	if err != nil {
		return Object{}, err
	}
	resp.Body.Close()
	return objectFromHeaders(key, resp.Header, resp.ContentLength), nil
}

// Delete removes an object; deleting a missing object is not an error (S3 answers 204 too).
func (c *Client) Delete(ctx context.Context, key string) error {
	resp, err := c.do(ctx, http.MethodDelete, key, nil, nil, nil)
	if err != nil {
		if IsNotFound(err) {
			return nil
		}
		return err
	}
	io.Copy(io.Discard, resp.Body)
	return resp.Body.Close()
}

// List is ListObjectsV2: one page of keys under prefix. With delimiter "/" the sub-folders come
// back as Prefixes. token is the NextToken of the previous page.
func (c *Client) List(ctx context.Context, prefix, delimiter, token string, max int) (*ListResult, error) {
	q := url.Values{"list-type": {"2"}}
	if prefix != "" {
		q.Set("prefix", prefix)
	}
	if delimiter != "" {
		q.Set("delimiter", delimiter)
	}
	if token != "" {
		q.Set("continuation-token", token)
	}
	if max > 0 {
		q.Set("max-keys", strconv.Itoa(max))
	}
	resp, err := c.do(ctx, http.MethodGet, "", q, nil, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var x struct {
		IsTruncated           bool   `xml:"IsTruncated"`
		NextContinuationToken string `xml:"NextContinuationToken"`
		Contents              []struct {
			Key          string `xml:"Key"`
			Size         int64  `xml:"Size"`
			ETag         string `xml:"ETag"`
			LastModified string `xml:"LastModified"`
		} `xml:"Contents"`
		CommonPrefixes []struct {
			Prefix string `xml:"Prefix"`
		} `xml:"CommonPrefixes"`
	}
	if err := xml.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(&x); err != nil {
		return nil, fmt.Errorf("ListObjectsV2: %w", err)
	}
	out := &ListResult{Truncated: x.IsTruncated, NextToken: x.NextContinuationToken}
	for _, o := range x.Contents {
		t, _ := time.Parse(time.RFC3339, o.LastModified)
		out.Objects = append(out.Objects, Object{Key: o.Key, Size: o.Size, ETag: strings.Trim(o.ETag, `"`), LastModified: t})
	}
	for _, p := range x.CommonPrefixes {
		out.Prefixes = append(out.Prefixes, p.Prefix)
	}
	if !out.Truncated {
		out.NextToken = ""
	}
	return out, nil
}

// PresignGet is a time-limited GET URL for a private object.
func (c *Client) PresignGet(key string, expires time.Duration) (string, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	if len(c.SSECKey) != 0 {
		return "", errors.New("objects encrypted with a customer key (SSE-C) cannot be fetched through a presigned URL")
	}
	u, err := c.objectURL(key)
	if err != nil {
		return "", err
	}
	return c.signer().Presign(http.MethodGet, u, c.now(), expires).String(), nil
}

func objectFromHeaders(key string, h http.Header, length int64) Object {
	o := Object{Key: key, Size: length, ETag: strings.Trim(h.Get("ETag"), `"`), ContentType: h.Get("Content-Type")}
	if t, err := http.ParseTime(h.Get("Last-Modified")); err == nil {
		o.LastModified = t
	}
	return o
}
