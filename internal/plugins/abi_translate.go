package plugins

import (
	"context"
	"unicode/utf8"
)

// Translator is an optional extension of Host: a host that implements it translates the strings
// plugins pass to the "translate" import into the language of the current request (Pursae Multi
// Language). The domain is always the plugin's slug.
type Translator interface {
	Translate(ctx context.Context, plugin, text, context string) string
}

// Limits of a translate request.
const (
	maxTranslateText    = 10000
	maxTranslateContext = 200
)

// hostTranslate serves "translate" {"text","context"} -> {"text"}. It needs no permission: the
// plugin only gets its own text back, in the request language when a translation exists (else
// unchanged, also on hosts without Translator).
func hostTranslate(ctx context.Context, p *Plugin, req []byte) (any, error) {
	var r struct {
		Text    string `json:"text"`
		Context string `json:"context"`
	}
	if err := decodeRequest(req, &r); err != nil {
		return nil, err
	}
	if len(r.Text) > maxTranslateText {
		return nil, invalid("text must be at most %d bytes", maxTranslateText)
	}
	if len(r.Context) > maxTranslateContext {
		return nil, invalid("context must be at most %d bytes", maxTranslateContext)
	}
	if !utf8.ValidString(r.Text) || !utf8.ValidString(r.Context) {
		return nil, invalid("text and context must be valid UTF-8")
	}
	out := r.Text
	if t, ok := p.host.(Translator); ok && r.Text != "" {
		out = t.Translate(ctx, p.Slug(), r.Text, r.Context)
	}
	return map[string]string{"text": out}, nil
}
