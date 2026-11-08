package plugins

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type translatorHost struct {
	Host
	gotPlugin, gotText, gotContext string
}

func (h *translatorHost) Translate(_ context.Context, plugin, text, context string) string {
	h.gotPlugin, h.gotText, h.gotContext = plugin, text, context
	return "fr:" + text
}

func TestHostTranslate(t *testing.T) {
	th := &translatorHost{}
	p := &Plugin{manifest: &Manifest{Slug: "hello"}, host: th}
	res, err := hostTranslate(context.Background(), p, []byte(`{"text":"Read more","context":"link"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := res.(map[string]string)["text"]; got != "fr:Read more" {
		t.Fatalf("text = %q", got)
	}
	if th.gotPlugin != "hello" || th.gotContext != "link" {
		t.Fatalf("translator got plugin %q context %q", th.gotPlugin, th.gotContext)
	}

	// A host without Translator gives the text back.
	plain := &Plugin{manifest: &Manifest{Slug: "hello"}}
	res, err = hostTranslate(context.Background(), plain, []byte(`{"text":"Read more"}`))
	if err != nil || res.(map[string]string)["text"] != "Read more" {
		t.Fatalf("plain host: %v %v", res, err)
	}

	// Limits.
	var ce *CallError
	if _, err := hostTranslate(context.Background(), p, []byte(`{"text":"`+strings.Repeat("a", maxTranslateText+1)+`"}`)); !errors.As(err, &ce) || ce.Code != CodeInvalidRequest {
		t.Fatalf("long text: %v", err)
	}
	if _, err := hostTranslate(context.Background(), p, []byte(`{"text":"a","context":"`+strings.Repeat("c", maxTranslateContext+1)+`"}`)); !errors.As(err, &ce) {
		t.Fatalf("long context: %v", err)
	}
	if _, err := hostTranslate(context.Background(), p, []byte(`{`)); !errors.As(err, &ce) {
		t.Fatalf("malformed: %v", err)
	}
}
