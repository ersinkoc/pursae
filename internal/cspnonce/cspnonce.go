// Package cspnonce holds the per-process CSP nonce placeholder. Code that writes a trusted
// <script> puts Attr() on it; the server's outermost handler swaps the placeholder for a fresh
// random nonce in every response (internal/server/csp_nonce.go). The placeholder is random, never
// leaves the server and changes on every start.
//
// Add the attribute only where trusted first-party code writes a script, never to markup that
// comes from plugins' HTML or a request, and never by rewriting finished HTML. Stored markup that
// only an unfiltered_html administrator can have written gets it through the server's
// cspTrustScripts, where the stored value is read (internal/server/csp_trusted.go).
package cspnonce

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
)

// Len is the length of a nonce and of the placeholder (16 random bytes, base64url, no padding).
const Len = 22

// Placeholder is this process's placeholder.
var Placeholder = New()

// New returns a fresh random nonce.
func New() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("cspnonce: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}

// Attr is the nonce attribute for a trusted <script>, with its leading space: "<script" + Attr() + ">".
func Attr() string { return ` nonce="` + Placeholder + `"` }

// Source is a response writer that knows the nonce its response's placeholder becomes: the
// server's outermost writer (internal/server/csp_nonce.go).
type Source interface{ CSPNonce() string }

// FromWriter returns the nonce that replaces the placeholder in w's response, following
// Unwrap through wrapping writers. ok is false when no writer on the chain swaps placeholders.
func FromWriter(w http.ResponseWriter) (nonce string, ok bool) {
	for w != nil {
		if s, is := w.(Source); is {
			return s.CSPNonce(), true
		}
		u, is := w.(interface{ Unwrap() http.ResponseWriter })
		if !is {
			return "", false
		}
		w = u.Unwrap()
	}
	return "", false
}
