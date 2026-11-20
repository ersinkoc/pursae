package server

import (
	"strings"
	"testing"

	"wpgo/internal/webbuilder"
)

// wbCodePartsCache gives a result only for the inputs it was made from (strings by content,
// infos and kit versions by identity) and keeps to its bounds.
func TestWBCodePartsCache(t *testing.T) {
	m := newWBCodePartsCache(3, 1<<20)
	kits := &map[string]int{"a": 1}
	hi := &webbuilder.CodeDocInfo{Lang: "tr"}
	in := wbCodePartsIn{header: "<header>h</header>", main: strings.Repeat("m", 1000), footer: "<footer>f</footer>", current: "/x/", hi: hi, kits: kits}
	out := wbCodePartsOf(in)
	if out.lang != "tr" || out.main != in.main {
		t.Fatalf("out %+v", out)
	}
	m.put(in, out)
	same := in
	same.main = strings.Repeat("m", 1000) // equal content, another string
	if got := m.get(same); got != out {
		t.Fatal("no hit for equal inputs")
	}
	for name, change := range map[string]func(*wbCodePartsIn){
		"header":  func(x *wbCodePartsIn) { x.header += " " },
		"main":    func(x *wbCodePartsIn) { x.main = strings.Repeat("n", 1000) },
		"footer":  func(x *wbCodePartsIn) { x.footer = "" },
		"current": func(x *wbCodePartsIn) { x.current = "/y/" },
		"info":    func(x *wbCodePartsIn) { x.hi = &webbuilder.CodeDocInfo{Lang: "tr"} },
		"main info": func(x *wbCodePartsIn) {
			x.mi = hi
		},
		"kits": func(x *wbCodePartsIn) { x.kits = &map[string]int{"a": 1} },
	} {
		x := in
		change(&x)
		if m.get(x) != nil {
			t.Errorf("%s changed: hit", name)
		}
	}
	// Bounds: entries, then bytes; a result larger than the byte bound is not kept.
	for i := 0; i < 5; i++ {
		x := in
		x.current = "/p" + strings.Repeat("x", i) + "/"
		m.put(x, wbCodePartsOf(x))
	}
	if m.len() != 3 || m.get(in) != nil {
		t.Errorf("len %d after 6 puts (max 3)", m.len())
	}
	small := newWBCodePartsCache(10, 5000)
	small.put(in, out)
	big := in
	big.main = strings.Repeat("b", 6000)
	small.put(big, wbCodePartsOf(big))
	if small.len() != 1 || small.get(big) != nil || small.get(in) == nil {
		t.Errorf("byte bound: len %d", small.len())
	}
	small.purge()
	if small.len() != 0 || small.bytes != 0 {
		t.Error("purge")
	}
}

// BenchmarkWBCodeParts: the code parts of a large imported page (header 29 KB, main 160 KB,
// footer 29 KB in one shell) worked out on every response, and found in the memo.
func BenchmarkWBCodeParts(b *testing.B) {
	shell := `<div class="shell">`
	pad := func(tag string, n int) string {
		var sb strings.Builder
		for sb.Len() < n {
			sb.WriteString(`<div class="row"><a href="/x/" class="nav">Item</a><p>Lorem ipsum dolor sit amet.</p></div>`)
		}
		return "<" + tag + ">" + sb.String() + "</" + tag + ">"
	}
	kits := &map[string]int{}
	in := wbCodePartsIn{
		header:  shell + `<a href="#c">Skip</a>` + pad("header", 29<<10) + `</div>`,
		main:    shell + `<main id="c">` + pad("section", 160<<10) + `</main></div>`,
		footer:  shell + pad("footer", 29<<10) + `</div>`,
		current: "/galeri/", kits: kits,
		hi: &webbuilder.CodeDocInfo{First: []string{shell}, Last: []string{shell}, Common: []string{shell}},
		mi: &webbuilder.CodeDocInfo{First: []string{shell}, Last: []string{shell}, Common: []string{shell}},
		fi: &webbuilder.CodeDocInfo{First: []string{shell}, Last: []string{shell}, Common: []string{shell}},
	}
	if out := wbCodePartsOf(in); strings.Count(out.header+out.main+out.footer, shell) != 1 {
		b.Fatal("shell not shared")
	}
	b.Run("direct", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			wbCodePartsOf(in)
		}
	})
	m := newWBCodePartsCache(wbCodePartsMemoMax, wbCodePartsMemoBytes)
	m.put(in, wbCodePartsOf(in))
	again := in
	again.main = strings.Clone(in.main) // equal markup in another string, as each response has
	b.Run("memo", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if m.get(again) == nil {
				b.Fatal("miss")
			}
		}
	})
}
