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

func TestDynamicNamesOmitsNilRenderers(t *testing.T) {
	const name = "test/optional-renderer"
	t.Cleanup(func() {
		mu.Lock()
		delete(dynamic, name)
		delete(raw, name)
		mu.Unlock()
	})
	RegisterDynamic(name, nil)
	RegisterContainer(name, func(context.Context, *Block, string) string { return "" })
	count := 0
	for _, active := range DynamicNames() {
		if active == name {
			count++
		}
	}
	if count != 1 {
		t.Errorf("active renderer listed %d times", count)
	}
	RegisterContainer(name, nil)
	for _, active := range DynamicNames() {
		if active == name {
			t.Errorf("nil renderer listed as active")
		}
	}
}
