package webbuilder

import (
	"fmt"
	"strings"
	"testing"
)

// CompileFor: keyed by the caller, src called only on a miss, static programs cached, the same
// output as Compile; failed compiles not cached; the byte bound evicts.
func TestProgramCacheCompileFor(t *testing.T) {
	c := NewProgramCache(8)
	dyn := `<p data-wb-slot="site.title">x</p><p>static</p>`
	calls := 0
	src := func(s string) func() string { return func() string { calls++; return s } }
	p1, err := c.CompileFor("a", src(dyn))
	if err != nil {
		t.Fatal(err)
	}
	p2, _ := c.CompileFor("a", src("ignored on a hit"))
	if p1 != p2 || calls != 1 {
		t.Fatalf("hit: same=%v calls=%d", p1 == p2, calls)
	}
	ref, _ := Compile(dyn)
	r := &fakeResolver{text: map[SlotName]string{SlotSiteTitle: "Site <b>"}}
	got, _ := p1.ExecString(r)
	want, _ := ref.ExecString(r)
	if got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
	st, _ := c.CompileFor("s", src("<p>only static</p>"))
	if text, ok := st.Static(); !ok || text != "<p>only static</p>" || c.Len() != 2 {
		t.Fatalf("static program: %q %v len %d", text, ok, c.Len())
	}
	if _, err := c.CompileFor("s", src("x")); err != nil || calls != 2 {
		t.Fatalf("static program not cached: calls=%d", calls)
	}

	// The byte bound: programs over it are evicted, least recently used first.
	b := NewProgramCache(100)
	b.maxBytes = 1000
	big := strings.Repeat("y", 400)
	for i := 0; i < 5; i++ {
		b.CompileFor(fmt.Sprint(i), func() string { return big })
	}
	if b.Len() != 2 || b.bytes != 800 {
		t.Fatalf("len %d bytes %d", b.Len(), b.bytes)
	}
	// One program larger than the bound is still kept (alone).
	b.CompileFor("huge", func() string { return strings.Repeat("z", 5000) })
	if b.Len() != 1 {
		t.Fatalf("len %d", b.Len())
	}
	b.Purge()
	if b.Len() != 0 || b.bytes != 0 {
		t.Fatal("purge")
	}
}

// BenchmarkProgramCache: a cache hit for a 160 KB snapshot, keyed by its hash (Compile) and by
// the caller's key (CompileFor).
func BenchmarkProgramCache(b *testing.B) {
	var sb strings.Builder
	sb.WriteString(`<div class="wb-page"><header><span data-wb-slot="site.title">x</span></header>`)
	for sb.Len() < 160<<10 {
		sb.WriteString(`<section class="item"><div class="row"><a href="/x"><img src="/a.jpg" alt="a"></a><p>Lorem ipsum dolor sit amet.</p></div></section>` + "\n")
	}
	sb.WriteString(`</div>`)
	dyn := sb.String()
	static := strings.Replace(dyn, ` data-wb-slot="site.title"`, ``, 1)
	for _, tc := range []struct{ name, src string }{{"dynamic", dyn}, {"static", static}} {
		c := NewProgramCache(0)
		b.Run(tc.name+"/Compile", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := c.Compile(1, 1, tc.src); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(tc.name+"/CompileFor", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := c.CompileFor("1:1", func() string { return tc.src }); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
