package blocks

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestParseCloserTokens covers the closer fast path of finishToken: a closer's name and attrs are
// not built, but a "/wp:" delimiter that ends in "/-->" is still a void block with both.
func TestParseCloserTokens(t *testing.T) {
	for doc, want := range map[string]string{
		`<!-- wp:ns/a {"x":1} --><p>i</p><!-- /wp:ns/a {"x":2} -->`: `[{"blockName":"ns/a","attrs":{"x":1},"innerBlocks":[],"innerHTML":"<p>i</p>","innerContent":["<p>i</p>"]}]`,
		`<!-- /wp:b {"y":[1]} /-->`:                                 `[{"blockName":"core/b","attrs":{"y":[1]},"innerBlocks":[],"innerHTML":"","innerContent":[]}]`,
		`<!-- /wp:c /-->`:                                           `[{"blockName":"core/c","attrs":{},"innerBlocks":[],"innerHTML":"","innerContent":[]}]`,
		`x<!-- /wp:d -->y`:                                          `[{"blockName":null,"attrs":{},"innerBlocks":[],"innerHTML":"x<!-- /wp:d -->y","innerContent":["x<!-- /wp:d -->y"]}]`,
	} {
		var buf strings.Builder
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(Parse(doc))
		if got := strings.TrimSpace(buf.String()); got != want {
			t.Errorf("%s\n got %s\nwant %s", doc, got, want)
		}
	}
}

func benchDoc() string {
	var sb strings.Builder
	for i := 0; i < 20; i++ {
		sb.WriteString(`<!-- wp:group {"layout":{"type":"constrained"}} --><div class="wp-block-group"><!-- wp:columns --><div class="wp-block-columns"><!-- wp:column --><div class="wp-block-column"><!-- wp:paragraph --><p>` + strings.Repeat("Lorem ipsum dolor sit amet ", 20) + `</p><!-- /wp:paragraph --><!-- wp:heading --><h2 class="wp-block-heading">Heading</h2><!-- /wp:heading --></div><!-- /wp:column --><!-- wp:column --><div class="wp-block-column"><!-- wp:image {"id":12} --><figure class="wp-block-image"><img src="/wp-content/uploads/a.jpg" alt=""/></figure><!-- /wp:image --></div><!-- /wp:column --></div><!-- /wp:columns --></div><!-- /wp:group -->`)
	}
	return sb.String()
}

func BenchmarkRenderStatic(b *testing.B) {
	doc := benchDoc()
	ctx := context.Background()
	b.SetBytes(int64(len(doc)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = Render(ctx, doc)
	}
}

func BenchmarkParseOnly(b *testing.B) {
	doc := benchDoc()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = Parse(doc)
	}
}
