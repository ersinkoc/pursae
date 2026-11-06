package server

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
)

// Helpers for JavaScript that the server prints or reads.

// scriptJSON marshals a value for an inline script without HTML escaping; "</" stays escaped so a
// string never ends the script.
func scriptJSON(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	out := strings.TrimSuffix(buf.String(), "\n")
	// </script> inside a string must not end the inline script.
	return strings.ReplaceAll(out, "</", `<\/`)
}

// String literals of a script that may be UI text: jsLiteralRe finds '…', "…" and `…` literals
// (no template substitutions), jsHasWordRe asks for a word, jsNotTextRe rejects code-like text.
var (
	jsLiteralRe = regexp.MustCompile(`'((?:[^'\\\n]|\\.){0,160})'|"((?:[^"\\\n]|\\.){0,160})"|` + "`([^`$\\n]{0,160})`")
	jsNotTextRe = regexp.MustCompile(`[{}<>=;]|^\.|^#|\\u|https?:|^[A-Z][a-z]+[A-Z]|^[A-Z_]+$`)
	jsHasWordRe = regexp.MustCompile(`[A-Za-z]{2}`)
)
