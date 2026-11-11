package i18n

import (
	"fmt"
	"strings"
	"testing"
)

func frontTable(m map[string]string) func(string) (string, bool) {
	return func(s string) (string, bool) {
		v, ok := m[s]
		return v, ok
	}
}

func TestTranslateFrontText(t *testing.T) {
	lookup := frontTable(map[string]string{
		"Skip to content":      "Aller au contenu",
		"More posts":           "Plus d’articles",
		"Fish & Chips":         "Poisson & frites",
		"Search":               "Rechercher",
		"Leave a reply":        "Laisser un <commentaire>",
		"Comments are closed.": "Les commentaires sont fermés.",
		"Menu":                 "Menu FR",
		"Choose":               "Choisir",
		"Page title":           "Titre",
	})
	cases := []struct{ name, in, want string }{
		{"text keeps whitespace", "<a href=\"#main\">\n  Skip to content\n</a>", "<a href=\"#main\">\n  Aller au contenu\n</a>"},
		{"entities and spaces", "<p>Fish  &amp;\n Chips</p>", "<p>Poisson &amp; frites</p>"},
		{"translation escaped", "<h3>Leave a reply</h3>", "<h3>Laisser un &lt;commentaire&gt;</h3>"},
		{"exact only", "<p>More posts here</p><p>More posts</p>", "<p>More posts here</p><p>Plus d’articles</p>"},
		{"no partial heuristics", "<p>Search: x</p><p>Search — Menu</p>", "<p>Search: x</p><p>Search — Menu</p>"},
		{"attributes", `<input type="search" placeholder="Search" aria-label='Menu'><img src="a.png" alt=Search>`,
			`<input type="search" placeholder="Rechercher" aria-label='Menu FR'><img src="a.png" alt="Rechercher">`},
		{"button value", `<input type="submit" value="Search"><input type="text" value="Search">`,
			`<input type="submit" value="Rechercher"><input type="text" value="Search">`},
		{"skipped elements", "<script>var s = \"Search\";</script><style>/* Search */</style><textarea>Search</textarea><pre>Search</pre><code>Search</code>",
			"<script>var s = \"Search\";</script><style>/* Search */</style><textarea>Search</textarea><pre>Search</pre><code>Search</code>"},
		{"svg skipped", `<svg aria-label="Search"><title>Search</title><text>Search</text></svg><p>Search</p>`,
			`<svg aria-label="Search"><title>Search</title><text>Search</text></svg><p>Rechercher</p>`},
		{"translate no", `<div translate="no"><p title="Search">Search</p><div>Menu</div></div><p>Menu</p>`,
			`<div translate="no"><p title="Search">Search</p><div>Menu</div></div><p>Menu FR</p>`},
		{"comments untouched", "<!-- Search --><p>Search</p>", "<!-- Search --><p>Rechercher</p>"},
		{"user content", "<p>Search</p><!--pll:uc--><p>Search</p><!--pll:uc post=4--><h2 title=\"Menu\">Menu</h2><!--/pll:uc--><p>Menu</p><!--/pll:uc--><p>Menu</p>",
			"<p>Rechercher</p><!--pll:uc--><p>Search</p><!--pll:uc post=4--><h2 title=\"Menu\">Menu</h2><!--/pll:uc--><p>Menu</p><!--/pll:uc--><p>Menu FR</p>"},
		{"unclosed user content", "<p>Menu</p><!--pll:uc--><p>Menu</p>", "<p>Menu FR</p><!--pll:uc--><p>Menu</p>"},
		{"stray lt", "<p>1 < 2 Menu</p><p>Menu</p>", "<p>1 < 2 Menu</p><p>Menu FR</p>"},
		{"title", "<head><title>Page title</title></head>", "<head><title>Titre</title></head>"},
		{"option", `<select><option value="1">Choose</option></select>`, `<select><option value="1">Choisir</option></select>`},
		{"no letters", "<p> 2026 </p><p>—</p>", "<p> 2026 </p><p>—</p>"},
	}
	for _, c := range cases {
		if got := string(TranslateFront([]byte(c.in), lookup, nil)); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

func TestTranslateFrontPreservesBytes(t *testing.T) {
	doc := "<!DOCTYPE html>\n<html lang=\"en\"><head><meta charset=\"utf-8\"><title>Site &#8211; Home</title>\n" +
		"<script type=\"application/ld+json\">{\"a\":\"<b>\"}</script></head>\n<body class=\"x\">\n\t<p>Hello <b>world</b>!</p>" +
		"<img src=\"x.png\" alt=\"\"><input type=submit value=Go><br/></body></html>\n"
	none := func(string) (string, bool) { return "", false }
	if got := string(TranslateFront([]byte(doc), none, nil)); got != doc {
		t.Fatalf("bytes changed without translations:\n%s", got)
	}
	noop := func(string, string) string { return "" }
	if got := string(TranslateFront([]byte(doc), nil, noop)); got != doc {
		t.Fatalf("bytes changed by a visit without markers:\n%s", got)
	}
	if got := TranslateFront([]byte(doc), nil, nil); string(got) != doc {
		t.Fatal("nil callbacks must return the document")
	}
}

func TestTranslateFrontSkipsRawTextInsideUntranslatedSubtree(t *testing.T) {
	in := `<div translate="no"><script>let x = "</div>";</script>Search</div><p>Search</p>`
	want := `<div translate="no"><script>let x = "</div>";</script>Search</div><p>Suchen</p>`
	if got := string(TranslateFront([]byte(in), frontTable(map[string]string{"Search": "Suchen"}), nil)); got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestTranslateFrontVisit(t *testing.T) {
	type seen struct{ s, attr string }
	var got []seen
	visit := func(s, attr string) string {
		got = append(got, seen{s, attr})
		return "m" + strings.ReplaceAll(strings.ToLower(s), " ", "")
	}
	lookup := frontTable(map[string]string{"Search": "Suchen"})
	in := `<title>Home</title><p>Read  more</p><option>Pick</option><img alt="Search" src="a.png" /><input type="submit" value="Go">` +
		`<!--pll:uc--><p>Post text</p><!--/pll:uc--><p translate="no">Name</p>`
	out := string(TranslateFront([]byte(in), lookup, visit))
	want := `<title>Home</title><p><pll-t data-pll-s="mreadmore">Read  more</pll-t></p><option>Pick</option>` +
		`<img alt="Suchen" src="a.png"  data-pll-a-alt="msearch"/><input type="submit" value="Go" data-pll-a-value="mgo">` +
		`<!--pll:uc--><p>Post text</p><!--/pll:uc--><p translate="no">Name</p>`
	if out != want {
		t.Fatalf("visual markers:\n got %s\nwant %s", out, want)
	}
	wantSeen := []seen{{"Read more", ""}, {"Pick", ""}, {"Search", "alt"}, {"Go", "value"}}
	if len(got) != len(wantSeen) {
		t.Fatalf("visited %v, want %v", got, wantSeen)
	}
	for i := range got {
		if got[i] != wantSeen[i] {
			t.Fatalf("visited %v, want %v", got, wantSeen)
		}
	}
	// Markers are escaped.
	esc := string(TranslateFront([]byte(`<p>Hi</p>`), nil, func(string, string) string { return `"><x` }))
	if esc != `<p><pll-t data-pll-s="&#34;&gt;&lt;x">Hi</pll-t></p>` {
		t.Fatalf("marker not escaped: %s", esc)
	}
}

func TestStripUserContentSentinels(t *testing.T) {
	in := `<p><!--pll:uc post=12-->Body<!--/pll:uc--></p><a aria-label="&lt;!--pll:uc--&gt;T&lt;!--/pll:uc--&gt;">` +
		`<script>var t="\u003c!--pll:uc--\u003eT\u003c!--\/pll:uc--\u003e";</script><!--pll:uc--><!-- other -->`
	out := string(StripUserContentSentinels([]byte(in)))
	want := `<p>Body</p><a aria-label="T"><script>var t="T";</script><!-- other -->`
	if out != want {
		t.Fatalf("strip:\n got %s\nwant %s", out, want)
	}
	if strings.Contains(out, "pll:uc") {
		t.Fatal("a sentinel leaked")
	}
	plain := []byte("<p>nothing here</p>")
	if got := StripUserContentSentinels(plain); &got[0] != &plain[0] {
		t.Fatal("a document without sentinels must be returned as it is")
	}
	var posts []int64
	rep := string(ReplaceUserContentSentinels([]byte(in), func(open bool, post int64) string {
		if !open {
			return "</pll-uc>"
		}
		posts = append(posts, post)
		if post > 0 {
			return `<pll-uc data-pll-post="12">`
		}
		return ""
	}))
	if !strings.HasPrefix(rep, `<p><pll-uc data-pll-post="12">Body</pll-uc></p><a aria-label="T">`) || strings.Contains(rep, "pll:uc") {
		t.Fatalf("replace: %s", rep)
	}
	if len(posts) != 2 || posts[0] != 12 || posts[1] != 0 {
		t.Fatalf("posts %v", posts)
	}
	if UCOpenPost(7) != "<!--pll:uc post=7-->" {
		t.Fatal(UCOpenPost(7))
	}
}

func TestReplaceUserContentSentinelsMixed(t *testing.T) {
	doc := []byte(`a<!--pll:uc post=12-->b<!--pll:uc-->c<!--/pll:uc--> &lt;!--pll:uc--&gt;d<!-- /pll:uc -->e<!--pll:uc post=-->f/pll:uc<!--/pll:uc-->`)
	var got []string
	out := ReplaceUserContentSentinels(doc, func(open bool, post int64) string {
		if open {
			got = append(got, fmt.Sprintf("o%d", post))
			return "["
		}
		got = append(got, "c")
		return "]"
	})
	// The escaped sentinel is removed without a call; "<!-- /pll:uc -->" and "post=" without an id
	// are no sentinels.
	if string(out) != `a[b[c] d<!-- /pll:uc -->e<!--pll:uc post=-->f/pll:uc]` {
		t.Fatalf("out = %s", out)
	}
	if strings.Join(got, ",") != "o12,o0,c,c" {
		t.Fatalf("calls = %v", got)
	}
}

// TestTranslateFrontWithRecord: the document <title> is translated but never offered for recording
// or visited (it joins a post title with the site name).
func TestTranslateFrontWithRecord(t *testing.T) {
	rec := map[string]bool{}
	lookup := func(s string, record bool) (string, bool) {
		rec[s] = record
		if s == "Blog" {
			return "Journal", true
		}
		return "", false
	}
	var visited []string
	visit := func(s, attr string) string { visited = append(visited, s); return "" }
	out := string(TranslateFrontWith([]byte(`<title>Blog</title><title>Hello world! &#8211; Site</title><p>Menu</p>`), lookup, visit))
	if out != `<title>Journal</title><title>Hello world! &#8211; Site</title><p>Menu</p>` {
		t.Fatalf("got %s", out)
	}
	if rec["Hello world! – Site"] || rec["Blog"] || !rec["Menu"] {
		t.Fatalf("record flags %v", rec)
	}
	if len(visited) != 1 || visited[0] != "Menu" {
		t.Fatalf("visited %v", visited)
	}
}
