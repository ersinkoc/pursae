package server

import (
	"bytes"
	"net/http"
	"strings"

	"wpgo/internal/cspnonce"
)

// CSP script nonces.
//
// Everything that emits a trusted <script> (templates, Go emitters, plugin script tags, the page
// cache's loaders) writes the per-process placeholder cspPH, via cspNonceAttr / the cspNonce
// template function. The outermost handler (Handler) swaps the placeholder for a fresh random
// nonce in every response, headers and text bodies alike, so:
//
//   - every response, a page-cache hit included, carries its own nonce: the cache stores the
//     placeholder, never a nonce (its entries remember which placeholder they hold, and entries of
//     another process are not served);
//   - the placeholder never leaves the server, so content cannot carry a <script nonce="…"> that
//     would be promoted to a valid nonce. A nonce is generated at the outermost layer, after all
//     content was rendered, and nothing that renders content knows it.
//
// The nonce is added only where a trusted script is written, never by rewriting the finished page:
// that would also bless a script injected through stored content.

// cspPH is this process's placeholder (internal/cspnonce).
var cspPH = cspnonce.Placeholder

func newCSPNonce() string { return cspnonce.New() }

// cspNonceAttr is the nonce attribute for a trusted <script> (with its leading space).
func cspNonceAttr() string { return cspnonce.Attr() }

// cspNonceSource is the CSP source expression that matches the response's nonce.
func cspNonceSource() string { return "'nonce-" + cspPH + "'" }

// cspHeaders are the policy headers whose nonce must match the body.
var cspHeaders = []string{"Content-Security-Policy", "Content-Security-Policy-Report-Only"}

// nonceWriter swaps the placeholder for the response's nonce in the headers and in text bodies.
type nonceWriter struct {
	w           http.ResponseWriter
	ph, nonce   []byte
	wroteHeader bool
	swap        bool
	tail        []byte // bytes held back because they may be the start of a placeholder
}

func newNonceWriter(w http.ResponseWriter) *nonceWriter {
	return &nonceWriter{w: w, ph: []byte(cspPH), nonce: []byte(newCSPNonce())}
}

func (nw *nonceWriter) Header() http.Header { return nw.w.Header() }

// Unwrap lets http.ResponseController reach the underlying writer.
func (nw *nonceWriter) Unwrap() http.ResponseWriter { return nw.w }

func (nw *nonceWriter) WriteHeader(code int) {
	if nw.wroteHeader {
		return
	}
	nw.wroteHeader = true
	h := nw.w.Header()
	ph, nonce := string(nw.ph), string(nw.nonce)
	for k, vs := range h {
		for i, v := range vs {
			if strings.Contains(v, ph) {
				vs[i] = strings.ReplaceAll(v, ph, nonce)
			}
		}
		h[k] = vs
	}
	if code == http.StatusNotModified {
		// The browser keeps the body it has, with the nonce it was sent; a 304's headers replace
		// the stored ones, so a new nonce here would no longer match that body.
		for _, k := range cspHeaders {
			h.Del(k)
		}
	}
	nw.swap = h.Get("Content-Encoding") == "" && nonceSwapType(h.Get("Content-Type"))
	nw.w.WriteHeader(code)
}

// nonceSwapType reports whether a body of this type may hold a placeholder: markup, scripts and
// JSON (rendered fragments). Media and archives are passed through untouched.
func nonceSwapType(ct string) bool {
	if ct == "" {
		return true // sniffed later: assume text
	}
	return compressibleType(ct) && !strings.HasPrefix(strings.ToLower(ct), "text/event-stream")
}

func (nw *nonceWriter) Write(b []byte) (int, error) {
	if !nw.wroteHeader {
		nw.WriteHeader(http.StatusOK)
	}
	if !nw.swap {
		return nw.w.Write(b)
	}
	data := b
	if len(nw.tail) > 0 {
		data = append(nw.tail, b...)
		nw.tail = nil
	}
	if bytes.Contains(data, nw.ph) {
		data = bytes.ReplaceAll(data, nw.ph, nw.nonce)
	}
	keep := partialSuffix(data, nw.ph)
	if keep > 0 {
		nw.tail = append([]byte(nil), data[len(data)-keep:]...)
		data = data[:len(data)-keep]
	}
	if len(data) > 0 {
		if _, err := nw.w.Write(data); err != nil {
			return 0, err
		}
	}
	return len(b), nil
}

// partialSuffix is the length of the longest proper prefix of ph that data ends with.
func partialSuffix(data, ph []byte) int {
	n := len(ph) - 1
	if n > len(data) {
		n = len(data)
	}
	for ; n > 0; n-- {
		if bytes.HasSuffix(data, ph[:n]) {
			return n
		}
	}
	return 0
}

// Flush sends what was written so far. Up to len(placeholder)-1 trailing bytes stay held back
// when they could be the start of a placeholder.
func (nw *nonceWriter) Flush() {
	if !nw.wroteHeader {
		nw.WriteHeader(http.StatusOK)
	}
	if f, ok := nw.w.(http.Flusher); ok {
		f.Flush()
	}
}

// close writes the held-back bytes at the end of the response.
func (nw *nonceWriter) close() {
	if !nw.wroteHeader { // a handler that wrote nothing: its headers still need the swap
		nw.WriteHeader(http.StatusOK)
	}
	if len(nw.tail) > 0 {
		_, _ = nw.w.Write(nw.tail)
		nw.tail = nil
	}
}

// CSP modes (the csp_mode option).
const (
	cspModeOff     = "off"     // the old policy only: 'self' 'unsafe-inline'
	cspModeReport  = "report"  // the old policy, plus the nonce policy as Content-Security-Policy-Report-Only
	cspModeEnforce = "enforce" // the nonce policy is enforced
)

// cspReportPath receives the browsers' violation reports (csp_report.go).
const cspReportPath = "/wpgo-csp-report"

func (s *Server) cspMode() string {
	switch m := s.settings.String("csp_mode"); m {
	case cspModeOff, cspModeEnforce:
		return m
	}
	return cspModeReport
}

// cspNonceScriptSrc is the nonce policy's script-src: only scripts carrying the response's nonce, and
// what they load ('strict-dynamic' makes browsers ignore 'self' and host lists; 'self' stays for
// browsers without it). wp-admin's underscore templates still need 'unsafe-eval'.
func cspNonceScriptSrc(admin bool) string {
	src := cspNonceSource() + " 'strict-dynamic' 'self'"
	if admin {
		src += " 'unsafe-eval'"
	}
	return src
}

// cspScriptSrc is the enforced policy's script-src for mode.
func cspScriptSrc(mode string, admin bool) string {
	if mode == cspModeEnforce {
		return cspNonceScriptSrc(admin)
	}
	if admin {
		return "'self' 'unsafe-inline' 'unsafe-eval'"
	}
	return "'self' 'unsafe-inline'"
}

// cspReportOnly adds the nonce policy as Content-Security-Policy-Report-Only in report mode, and the
// report endpoint to either nonce policy.
func cspReportOnly(h http.Header, mode string, admin bool) {
	switch mode {
	case cspModeReport:
		h.Set("Content-Security-Policy-Report-Only", "script-src "+cspNonceScriptSrc(admin)+"; object-src 'none'; base-uri 'self'; report-uri "+cspReportPath)
	case cspModeEnforce:
		if p := h.Get("Content-Security-Policy"); p != "" {
			h.Set("Content-Security-Policy", p+"; report-uri "+cspReportPath)
		}
	}
}
