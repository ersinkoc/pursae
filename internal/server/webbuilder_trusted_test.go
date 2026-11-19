package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"wpgo/internal/settings"
	"wpgo/internal/store"
)

// wbTrustedHTML is cspTrustContent of the stored snapshot, with and without placeholders, trusted
// or not.
func TestWBTrustedHTML(t *testing.T) {
	s := &Server{settings: settings.New(nil)}
	page := withRender(context.Background(), &renderCtx{page: true})
	feed := withRender(context.Background(), &renderCtx{page: true, feed: true})
	for i, html := range []string{
		`<div class="wb-page"><script>window.a=1</script><p>static</p></div>`,
		`<div class="wb-page"><script>window.b=1</script><p data-wb-slot="site.title">x</p></div>`,
		`<div class="wb-page"><p>no script</p></div>`,
	} {
		d := &store.WebBuilderDoc{PostID: int64(9300 + i), Rev: 2, UpdatedAt: time.Unix(1700000000, int64(i)), HTML: html}
		for _, ctx := range []context.Context{page, feed, context.Background()} {
			for n := 0; n < 2; n++ { // a miss, then a hit
				if got, want := s.wbTrustedHTML(ctx, d), s.cspTrustContent(ctx, d.HTML); got != want {
					t.Fatalf("doc %d: got %q, want %q", i, got, want)
				}
			}
		}
		if strings.Contains(html, "<script>") && !strings.Contains(s.wbTrustedHTML(page, d), "<script"+cspNonceAttr()+">") {
			t.Errorf("doc %d: trusted script without the placeholder", i)
		}
	}
}
