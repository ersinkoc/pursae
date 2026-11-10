package blocks

import (
	"context"
	"strings"
	"testing"
)

func TestContainerBlockRendersInnerPerItem(t *testing.T) {
	RegisterContainer("test/repeat", func(ctx context.Context, b *Block, _ string) string {
		var sb strings.Builder
		for i := 0; i < 2; i++ {
			sb.WriteString("[" + RenderInner(ctx, b) + "]")
		}
		return sb.String()
	})
	got := Render(context.Background(), `<!-- wp:test/repeat --><!-- wp:paragraph --><p>x</p><!-- /wp:paragraph --><!-- /wp:test/repeat -->`)
	if strings.Count(got, "<p>x</p>") != 2 || !IsDynamic("test/repeat") {
		t.Errorf("got %q", got)
	}
	found := false
	for _, n := range DynamicNames() {
		found = found || n == "test/repeat"
	}
	if !found {
		t.Error("DynamicNames misses the container block")
	}
}
