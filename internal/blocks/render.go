package blocks

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
)

// RenderFunc renders a dynamic block. content is the already rendered inner HTML.
type RenderFunc func(ctx context.Context, b *Block, content string) string

var (
	mu      sync.RWMutex
	dynamic = map[string]RenderFunc{}
)

// RegisterDynamic registers a server-side renderer, like register_block_type() with render_callback.
func RegisterDynamic(name string, fn RenderFunc) {
	mu.Lock()
	dynamic[name] = fn
	mu.Unlock()
}

var raw = map[string]RenderFunc{}

// RegisterContainer registers a renderer that renders its own inner blocks (RenderInner), for blocks
// like core/post-template that render their children once per item with different context. The
// RenderFunc receives empty content.
func RegisterContainer(name string, fn RenderFunc) {
	mu.Lock()
	raw[name] = fn
	mu.Unlock()
}

// RenderInner renders the inner blocks and inner HTML of b (what RenderBlock passes as content).
func RenderInner(ctx context.Context, b *Block) string {
	return renderInner(ctx, b, depthFrom(ctx))
}

func renderInner(ctx context.Context, b *Block, depth int) string {
	var sb strings.Builder
	inner := 0
	for _, chunk := range b.InnerContent {
		if chunk != nil {
			sb.WriteString(*chunk)
			continue
		}
		if inner < len(b.InnerBlocks) {
			sb.WriteString(renderBlock(ctx, b.InnerBlocks[inner], depth))
		}
		inner++
	}
	return sb.String()
}

// depthKey carries the nesting depth into container renderers, so RenderInner continues the count.
type depthKey struct{}

func depthFrom(ctx context.Context) int {
	d, _ := ctx.Value(depthKey{}).(int)
	return d
}

// DynamicNames lists the block names that have a server-side renderer, sorted.
func DynamicNames() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(dynamic)+len(raw))
	for n := range dynamic {
		out = append(out, n)
	}
	for n := range raw {
		if dynamic[n] == nil {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// IsDynamic reports whether a server-side renderer is registered for a block name.
func IsDynamic(name string) bool {
	mu.RLock()
	defer mu.RUnlock()
	return dynamic[name] != nil || raw[name] != nil
}

// Render is do_blocks(): freeform HTML is emitted as is, static blocks emit
// their saved markup and dynamic blocks call their renderer.
func Render(ctx context.Context, content string) string {
	if !HasBlocks(content) && (!strings.Contains(content, "<!--") || !strings.Contains(content, "wp:")) {
		return content
	}
	var sb strings.Builder
	depth := depthFrom(ctx)
	for _, b := range Parse(content) {
		sb.WriteString(renderBlock(ctx, b, depth))
	}
	return sb.String()
}

// RenderBlock is render_block() for a single parsed block.
func RenderBlock(ctx context.Context, b *Block) string {
	return renderBlock(ctx, b, depthFrom(ctx))
}

// renderBlock renders b at nesting depth depth (0 for a top-level block). A tree deeper than
// MaxDepth (Parse never builds one, but trees can come from elsewhere) renders its blocks past the
// limit as their own HTML without inner blocks instead of recursing further.
func renderBlock(ctx context.Context, b *Block, depth int) string {
	if depth >= MaxDepth {
		return b.InnerHTML
	}
	if b.BlockName != nil {
		mu.RLock()
		rf := raw[*b.BlockName]
		mu.RUnlock()
		if rf != nil {
			return rf(context.WithValue(ctx, depthKey{}, depth+1), b, "")
		}
	}
	content := renderInner(ctx, b, depth+1)
	if b.BlockName == nil {
		return content
	}
	mu.RLock()
	fn := dynamic[*b.BlockName]
	mu.RUnlock()
	if fn != nil {
		return fn(ctx, b, content)
	}
	return content
}

// Serialize writes blocks back to the delimited format (serialize_blocks()).
func Serialize(blocks []*Block) string {
	var sb strings.Builder
	for _, b := range blocks {
		serialize(&sb, b)
	}
	return sb.String()
}

func serialize(sb *strings.Builder, b *Block) {
	if b.BlockName == nil {
		sb.WriteString(b.InnerHTML)
		return
	}
	name := strings.TrimPrefix(*b.BlockName, "core/")
	sb.WriteString("<!-- wp:" + name + " ")
	if len(b.Attrs) > 0 {
		sb.WriteString(serializeAttrs(b.Attrs) + " ")
	}
	if len(b.InnerContent) == 0 {
		sb.WriteString("/-->")
		return
	}
	sb.WriteString("-->")
	inner := 0
	for _, chunk := range b.InnerContent {
		if chunk != nil {
			sb.WriteString(*chunk)
			continue
		}
		if inner < len(b.InnerBlocks) {
			serialize(sb, b.InnerBlocks[inner])
		}
		inner++
	}
	sb.WriteString("<!-- /wp:" + name + " -->")
}

// serializeAttrs mirrors serializeAttributes() in @wordpress/blocks: JSON with
// characters that could end the HTML comment or confuse the parser escaped.
func serializeAttrs(attrs map[string]any) string {
	raw, err := json.Marshal(attrs) // already escapes <, > and & as unicode escapes
	if err != nil {
		return "{}"
	}
	s := strings.ReplaceAll(string(raw), "--", "\\u002d\\u002d")
	return strings.ReplaceAll(s, "\\\"", "\\u0022")
}
