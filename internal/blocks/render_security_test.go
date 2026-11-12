package blocks

import (
	"context"
	"os"
	"os/exec"
	"runtime/debug"
	"strings"
	"testing"
)

// Deeply nested block delimiters in stored content must not drive the recursive tree walks without
// bound: a goroutine stack overflow is a fatal error that takes the whole server process down. The
// child process lowers the stack limit to 32 MB (the default 1 GB is reached at about 2-3 million
// levels, ~60-80 MB of post content, under the 512 MB body limit) and renders, serializes and
// re-parses 200,000 nested levels (5.4 MB).
func TestBlocksDeepNestingDoesNotOverflowStack(t *testing.T) {
	const n = 200000
	if os.Getenv("WPGO_BLOCKS_DEEP_CHILD") == "1" {
		debug.SetMaxStack(32 << 20)
		in := strings.Repeat("<!-- wp:group -->", n) + "<p>x</p>" + strings.Repeat("<!-- /wp:group -->", n)
		_ = Render(context.Background(), in)
		_ = Serialize(Parse(in))
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestBlocksDeepNestingDoesNotOverflowStack$", "-test.count=1")
	cmd.Env = []string{"WPGO_BLOCKS_DEEP_CHILD=1"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := string(out)
		if i := strings.Index(msg, "fatal error"); i >= 0 {
			msg = msg[max(0, i-120):min(len(msg), i+40)]
		} else if len(msg) > 300 {
			msg = msg[:300]
		}
		t.Fatalf("rendering %d nested blocks crashed the process: %v\n%s", n, err, msg)
	}
}

func treeDepth(bs []*Block) int {
	d := 0
	for _, b := range bs {
		d = max(d, 1+treeDepth(b.InnerBlocks))
	}
	return d
}

// Parse builds at most MaxDepth levels; deeper delimiters stay as HTML of the deepest block, the
// text is kept, and the blocks after the over-deep part are parsed normally again.
func TestParseCapsNestingDepth(t *testing.T) {
	at := func(n int) string {
		return strings.Repeat("<!-- wp:group -->", n) + "<p>x</p>" + strings.Repeat("<!-- /wp:group -->", n)
	}
	if got := treeDepth(Parse(at(MaxDepth))); got != MaxDepth {
		t.Fatalf("depth %d content parsed to depth %d", MaxDepth, got)
	}
	in := at(MaxDepth+50) + "<!-- wp:paragraph --><p>after</p><!-- /wp:paragraph -->"
	bs := Parse(in)
	if got := treeDepth(bs); got != MaxDepth {
		t.Fatalf("over-deep content parsed to depth %d, want %d", got, MaxDepth)
	}
	if len(bs) != 2 || bs[1].Name() != "core/paragraph" {
		t.Fatalf("blocks after the over-deep part were not parsed back to the top level: %d blocks", len(bs))
	}
	out := Render(context.Background(), in)
	if !strings.Contains(out, "<p>x</p>") || !strings.Contains(out, "<p>after</p>") {
		t.Fatalf("over-deep content lost its HTML: %.200q", out)
	}
	if got := Serialize(Parse(in)); got != in {
		t.Fatalf("over-deep content does not round-trip through Serialize")
	}
	// A void block past the limit is kept as text as well.
	void := strings.Repeat("<!-- wp:group -->", MaxDepth) + `<!-- wp:spacer /-->` + strings.Repeat("<!-- /wp:group -->", MaxDepth)
	if got := treeDepth(Parse(void)); got != MaxDepth {
		t.Fatalf("void block past the limit parsed to depth %d", got)
	}
}

// A tree that does not come from Parse is still rendered only down to MaxDepth, also through
// container blocks that render their own inner blocks.
func TestRenderBlockCapsNestingDepth(t *testing.T) {
	RegisterContainer("test/deep-container", func(ctx context.Context, b *Block, _ string) string {
		return "[" + RenderInner(ctx, b) + "]"
	})
	leaf := freeform("deep")
	cur := leaf
	for i := 0; i < 3*MaxDepth; i++ {
		name := "core/group"
		if i%2 == 0 {
			name = "test/deep-container"
		}
		b := newBlock(name, nil)
		open, closeTag := "<div>", "</div>"
		b.InnerBlocks = []*Block{cur}
		b.InnerHTML = open + closeTag
		b.InnerContent = []*string{&open, nil, &closeTag}
		cur = b
	}
	out := RenderBlock(context.Background(), cur)
	if strings.Contains(out, "deep") {
		t.Fatal("blocks past MaxDepth were rendered")
	}
	// MaxDepth rendered levels plus the own HTML of the first block past the limit.
	if got := strings.Count(out, "<div>"); got != MaxDepth+1 {
		t.Fatalf("rendered %d levels, want %d", got, MaxDepth+1)
	}
}
