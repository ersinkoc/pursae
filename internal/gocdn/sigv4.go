// Package gocdn holds the parts of the GoCDN plugin that do not need the server: an S3-compatible
// object storage client with AWS Signature Version 4 (AWS S3, Cloudflare R2, DigitalOcean Spaces,
// Backblaze B2, MinIO and other S3 APIs) and the image service's resize/crop/encode pipeline.
package gocdn

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Signature Version 4 constants.
const (
	sigAlgorithm     = "AWS4-HMAC-SHA256"
	amzDateFormat    = "20060102T150405Z"
	amzShortDate     = "20060102"
	UnsignedPayload  = "UNSIGNED-PAYLOAD"
	EmptyPayloadHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

// Signer signs requests with AWS Signature Version 4 for one set of credentials, region and
// service ("s3").
type Signer struct {
	AccessKey string
	SecretKey string
	Region    string // "auto" for Cloudflare R2
	Service   string // "s3" when empty
}

func (s Signer) service() string {
	if s.Service == "" {
		return "s3"
	}
	return s.Service
}

func (s Signer) region() string {
	if s.Region == "" {
		return "us-east-1"
	}
	return s.Region
}

// Scope is the credential scope: date/region/service/aws4_request.
func (s Signer) Scope(t time.Time) string {
	return t.UTC().Format(amzShortDate) + "/" + s.region() + "/" + s.service() + "/aws4_request"
}

// signingKey derives the day's signing key from the secret.
func (s Signer) signingKey(t time.Time) []byte {
	k := hmacSHA256([]byte("AWS4"+s.SecretKey), t.UTC().Format(amzShortDate))
	k = hmacSHA256(k, s.region())
	k = hmacSHA256(k, s.service())
	return hmacSHA256(k, "aws4_request")
}

// Sign adds x-amz-date, x-amz-content-sha256 and Authorization to req. payloadHash is the hex
// SHA-256 of the body (EmptyPayloadHash for none, UnsignedPayload to skip). Every header already
// on the request (plus Host) is signed, except a few that proxies rewrite.
func (s Signer) Sign(req *http.Request, payloadHash string, t time.Time) {
	t = t.UTC()
	if payloadHash == "" {
		payloadHash = EmptyPayloadHash
	}
	req.Header.Set("X-Amz-Date", t.Format(amzDateFormat))
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	names, canonHeaders := canonicalHeaders(req)
	creq := CanonicalRequest(req.Method, CanonicalURI(req.URL), CanonicalQuery(req.URL.Query()), canonHeaders, names, payloadHash)
	sig := s.signature(t, creq)
	req.Header.Set("Authorization", sigAlgorithm+" Credential="+s.AccessKey+"/"+s.Scope(t)+
		", SignedHeaders="+names+", Signature="+sig)
}

// Presign returns the URL of a query-string signed request (method GET usually) valid for
// expires. Only the Host header is signed and the payload is UNSIGNED-PAYLOAD.
func (s Signer) Presign(method string, u *url.URL, t time.Time, expires time.Duration) *url.URL {
	t = t.UTC()
	out := *u
	q := out.Query()
	q.Set("X-Amz-Algorithm", sigAlgorithm)
	q.Set("X-Amz-Credential", s.AccessKey+"/"+s.Scope(t))
	q.Set("X-Amz-Date", t.Format(amzDateFormat))
	q.Set("X-Amz-Expires", strconv.Itoa(int(expires/time.Second)))
	q.Set("X-Amz-SignedHeaders", "host")
	creq := CanonicalRequest(method, CanonicalURI(&out), CanonicalQuery(q), "host:"+out.Host+"\n", "host", UnsignedPayload)
	q.Set("X-Amz-Signature", s.signature(t, creq))
	out.RawQuery = CanonicalQuery(q)
	return &out
}

func (s Signer) signature(t time.Time, canonicalRequest string) string {
	sts := StringToSign(t, s.Scope(t), canonicalRequest)
	return hex.EncodeToString(hmacSHA256(s.signingKey(t), sts))
}

// SignatureFor recomputes the signature of a canonical request (used by the test fake server).
func (s Signer) SignatureFor(t time.Time, canonicalRequest string) string {
	return s.signature(t, canonicalRequest)
}

// StringToSign is the second step of SigV4.
func StringToSign(t time.Time, scope, canonicalRequest string) string {
	h := sha256.Sum256([]byte(canonicalRequest))
	return sigAlgorithm + "\n" + t.UTC().Format(amzDateFormat) + "\n" + scope + "\n" + hex.EncodeToString(h[:])
}

// CanonicalRequest joins the parts of a canonical request.
func CanonicalRequest(method, uri, query, headers, signedHeaders, payloadHash string) string {
	return method + "\n" + uri + "\n" + query + "\n" + headers + "\n" + signedHeaders + "\n" + payloadHash
}

// skipSigning are headers that are not signed: they may be changed on the way (or are the
// signature itself).
var skipSigning = map[string]bool{"authorization": true, "user-agent": true, "x-amzn-trace-id": true,
	"expect": true, "connection": true, "accept-encoding": true, "content-length": true}

// canonicalHeaders lists the signed header names and the canonical header block.
func canonicalHeaders(req *http.Request) (string, string) {
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	vals := map[string][]string{"host": {host}}
	for k, vs := range req.Header {
		lk := strings.ToLower(k)
		if skipSigning[lk] || lk == "host" {
			continue
		}
		vals[lk] = append(vals[lk], vs...)
	}
	return CanonicalHeaderBlock(vals)
}

// CanonicalHeaderBlock builds the sorted, trimmed header lines and the SignedHeaders list from
// lower-case header names.
func CanonicalHeaderBlock(vals map[string][]string) (string, string) {
	names := make([]string, 0, len(vals))
	for k := range vals {
		names = append(names, k)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, k := range names {
		trimmed := make([]string, len(vals[k]))
		for i, v := range vals[k] {
			trimmed[i] = strings.Join(strings.Fields(v), " ")
		}
		b.WriteString(k + ":" + strings.Join(trimmed, ",") + "\n")
	}
	return strings.Join(names, ";"), b.String()
}

// CanonicalURI is the URI-encoded path; S3 encodes every segment once, keeping the slashes.
func CanonicalURI(u *url.URL) string {
	p := u.EscapedPath()
	if p == "" {
		return "/"
	}
	// Re-encode from the decoded path so the result follows SigV4's rules exactly, whatever
	// escaping net/url chose.
	dec, err := url.PathUnescape(p)
	if err != nil {
		dec = u.Path
	}
	return EscapePath(dec)
}

// EscapePath URI-encodes every byte of a path except unreserved characters and "/".
func EscapePath(p string) string {
	return uriEncode(p, false)
}

// CanonicalQuery sorts and encodes the query parameters.
func CanonicalQuery(q url.Values) string {
	if len(q) == 0 {
		return ""
	}
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return uriEncode(keys[i], true) < uriEncode(keys[j], true) })
	var parts []string
	for _, k := range keys {
		vs := append([]string(nil), q[k]...)
		sort.Strings(vs)
		for _, v := range vs {
			parts = append(parts, uriEncode(k, true)+"="+uriEncode(v, true))
		}
	}
	return strings.Join(parts, "&")
}

// uriEncode is SigV4's UriEncode(): unreserved characters stay, everything else is %XX
// (upper-case); slashes are encoded only when encodeSlash is set.
func uriEncode(s string, encodeSlash bool) string {
	const hexd = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' || c == '~' || (c == '/' && !encodeSlash) {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hexd[c>>4])
		b.WriteByte(hexd[c&15])
	}
	return b.String()
}

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

// SHA256Hex is the hex SHA-256 of b.
func SHA256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// ParseAuthorization splits a SigV4 Authorization header into its credential, signed headers and
// signature; ok is false when the header does not have that shape.
func ParseAuthorization(h string) (credential, signedHeaders, signature string, ok bool) {
	rest, found := strings.CutPrefix(h, sigAlgorithm+" ")
	if !found {
		return "", "", "", false
	}
	for _, part := range strings.Split(rest, ",") {
		k, v, _ := strings.Cut(strings.TrimSpace(part), "=")
		switch k {
		case "Credential":
			credential = v
		case "SignedHeaders":
			signedHeaders = v
		case "Signature":
			signature = v
		}
	}
	ok = credential != "" && signedHeaders != "" && len(signature) == 64
	return
}
