package blocks

import (
	"context"
	"encoding/json"
	"testing"
)

func names(bs []*Block) []string {
	var out []string
	for _, b := range bs {
		out = append(out, b.Name())
	}
	return out
}

func TestParseNested(t *testing.T) {
	doc := "<!-- wp:paragraph -->\n<p>Hello</p>\n<!-- /wp:paragraph -->\n\n" +
		`<!-- wp:columns {"align":"wide"} --><div class="wp-block-columns"><!-- wp:column --><div class="wp-block-column"><!-- wp:image {"id":5} /--></div><!-- /wp:column --></div><!-- /wp:columns -->` +
		"\ntrailing <b>html</b>"
	got := Parse(doc)
	want := []string{"core/paragraph", "", "core/columns", ""}
	if g := names(got); len(g) != len(want) {
		t.Fatalf("got %v want %v", g, want)
	} else {
		for i := range g {
			if g[i] != want[i] {
				t.Fatalf("got %v want %v", g, want)
			}
		}
	}
	if got[0].InnerHTML != "\n<p>Hello</p>\n" {
		t.Errorf("paragraph innerHTML = %q", got[0].InnerHTML)
	}
	cols := got[2]
	if cols.Attrs["align"] != "wide" {
		t.Errorf("columns attrs = %v", cols.Attrs)
	}
	if len(cols.InnerBlocks) != 1 || cols.InnerBlocks[0].Name() != "core/column" {
		t.Fatalf("columns inner = %v", names(cols.InnerBlocks))
	}
	col := cols.InnerBlocks[0]
	if len(col.InnerBlocks) != 1 || col.InnerBlocks[0].Name() != "core/image" || col.InnerBlocks[0].Attrs["id"] != float64(5) {
		t.Fatalf("column inner = %+v", col.InnerBlocks)
	}
	// innerContent: html, placeholder, html
	if len(cols.InnerContent) != 3 || cols.InnerContent[1] != nil {
		t.Errorf("columns innerContent = %v", cols.InnerContent)
	}
	if got[3].InnerHTML != "\ntrailing <b>html</b>" {
		t.Errorf("trailing freeform = %q", got[3].InnerHTML)
	}
}

func TestParseNamespacedAndTrickyAttrs(t *testing.T) {
	doc := `<!-- wp:my-plugin/thing {"text":"a --> b","n":{"x":1}} /-->`
	got := Parse(doc)
	if len(got) != 1 || got[0].Name() != "my-plugin/thing" {
		t.Fatalf("got %v", names(got))
	}
	if got[0].Attrs["text"] != "a --> b" {
		t.Errorf("attrs = %v", got[0].Attrs)
	}
}

func TestParseNotADelimiter(t *testing.T) {
	doc := "<!-- a normal comment --><p>x</p><!--wp:paragraph-->"
	got := Parse(doc)
	if len(got) != 1 || got[0].BlockName != nil || got[0].InnerHTML != doc {
		t.Fatalf("expected one freeform block, got %+v", got)
	}
}

func TestParseUnclosed(t *testing.T) {
	got := Parse("<!-- wp:group --><div>open")
	if len(got) != 1 || got[0].Name() != "core/group" || got[0].InnerHTML != "<div>open" {
		t.Fatalf("got %+v", got)
	}
}

func TestJSONShape(t *testing.T) {
	raw, _ := json.Marshal(Parse("<!-- wp:separator /-->"))
	want := `[{"blockName":"core/separator","attrs":{},"innerBlocks":[],"innerHTML":"","innerContent":[]}]`
	if string(raw) != want {
		t.Errorf("got %s", raw)
	}
}

func TestRenderAndSerializeRoundTrip(t *testing.T) {
	doc := `<!-- wp:group {"layout":{"type":"constrained"}} --><div class="wp-block-group"><!-- wp:paragraph --><p>a</p><!-- /wp:paragraph --></div><!-- /wp:group -->`
	if out := Serialize(Parse(doc)); out != doc {
		t.Errorf("serialize round trip:\n got %s\nwant %s", out, doc)
	}
	if out := Render(context.Background(), doc); out != `<div class="wp-block-group"><p>a</p></div>` {
		t.Errorf("render = %s", out)
	}
	RegisterDynamic("core/test-dyn", func(_ context.Context, b *Block, _ string) string { return "<dyn/>" })
	if out := Render(context.Background(), "x<!-- wp:test-dyn /-->y"); out != "x<dyn/>y" {
		t.Errorf("dynamic render = %s", out)
	}
}

func TestAddClass(t *testing.T) {
	if got := AddClass(`<h2>Hi</h2>`, "wp-block-heading", "h2"); got != `<h2 class="wp-block-heading">Hi</h2>` {
		t.Errorf("no class: %s", got)
	}
	if got := AddClass(`<div class="wp-block-group"><p>x</p></div>`, "is-layout-flow wp-block-group"); got != `<div class="wp-block-group is-layout-flow"><p>x</p></div>` {
		t.Errorf("existing class: %s", got)
	}
}

func TestSerializeAttrsEscapes(t *testing.T) {
	got := serializeAttrs(map[string]any{"a": "x--><b>\"q\""})
	want := "{\"a\":\"x\\u002d\\u002d\\u003e\\u003cb\\u003e\\u0022q\\u0022\"}"
	if got != want {
		t.Errorf("got %s want %s", got, want)
	}
}
