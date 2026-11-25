package server

import (
	"context"
	"html"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"wpgo/internal/wp"
)

// releaseNotes040 is the 0.4.0 section of CHANGELOG.md, as tools/changelog-notes.sh gives it to the
// release (and the Hub gives it to the sites).
func releaseNotes040(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("testdata/release-notes-0.4.0.md")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRenderReleaseNotesBlocks(t *testing.T) {
	md := "Summary line one\nline two.\n\n### Added\n\n- First item\n  wraps here.\n- Second `code` item\n\n#### Sub\n\n- Lazy\ncontinuation\n\nAfter the list.\n\n```\na < b\n```\n"
	got := string(renderReleaseNotes(md))
	for _, want := range []string{
		"<p>Summary line one line two.</p>",
		`<h3 class="pursae-notes-section is-added"><span class="pursae-news-label">Added</span></h3>`,
		"<ul class=\"pursae-notes-cards is-added\">\n<li>First item wraps here.</li>\n<li>Second <code>code</code> item</li>\n</ul>",
		"<h4>Sub</h4>",
		"<li>Lazy continuation</li>",
		"<p>After the list.</p>",
		"<pre><code>a &lt; b</code></pre>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "###") || strings.Contains(got, "- First") || strings.Contains(got, "`") {
		t.Errorf("raw markdown left:\n%s", got)
	}
}

// An item that starts with a bold title is a closed card: the title is the summary, the rest opens on
// a click; the cards take the colour of their section.
func TestRenderReleaseNotesCards(t *testing.T) {
	md := "### Security\n\n- **Members get no wp-admin.** Their `ck_` keys stop.\n- **Title only.**\n- No title here.\n\n### Something else\n\n- **A.** b\n"
	got := string(renderReleaseNotes(md))
	for _, want := range []string{
		`<h3 class="pursae-notes-section is-security"><span class="pursae-news-label">Security</span></h3>`,
		`<ul class="pursae-notes-cards is-security">`,
		`<li><details class="pursae-note"><summary><strong>Members get no wp-admin.</strong></summary><div class="pursae-note-body">Their <code>ck_</code> keys stop.</div></details></li>`,
		"<li><strong>Title only.</strong></li>",
		"<li>No title here.</li>",
		`<ul class="pursae-notes-cards is-other">`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, " open") {
		t.Errorf("a card is open by default:\n%s", got)
	}
}

func TestRenderReleaseNotesInline(t *testing.T) {
	for in, want := range map[string]string{
		"**bold** text": "<strong>bold</strong> text",
		"see [the docs](https://pursae.com/docs)": `see <a href="https://pursae.com/docs" target="_blank" rel="noopener noreferrer">the docs</a>`,
		"[relative](docs/x.md) stays text":        "relative stays text",
		"a `<b>` tag":                             "a <code>&lt;b&gt;</code> tag",
		`escaped \*star\*`:                        "escaped *star*",
		"**`code` in bold**":                      "<strong><code>code</code> in bold</strong>",
	} {
		if got := strings.TrimSpace(string(renderReleaseNotes(in))); got != "<p>"+want+"</p>" {
			t.Errorf("%q:\n got %s\nwant <p>%s</p>", in, got, want)
		}
	}
}

// Every character is escaped exactly once: the text reads as written in the notes, also when the
// notes come with entities already in them.
func TestRenderReleaseNotesEscapesOnce(t *testing.T) {
	for _, in := range []string{
		`the block editor's image crop saves a new "-edited" attachment & more`,
		`the block editor&#39;s image crop saves a new &#34;-edited&#34; attachment &amp; more`,
	} {
		got := string(renderReleaseNotes(in))
		if strings.Contains(got, "&amp;#") || strings.Contains(got, "&amp;amp;") {
			t.Errorf("double escaped: %s", got)
		}
		text := html.UnescapeString(wp.StripTags(got))
		if want := `the block editor's image crop saves a new "-edited" attachment & more`; strings.TrimSpace(text) != want {
			t.Errorf("%q reads %q", in, text)
		}
	}
}

func TestRenderReleaseNotesNeutralisesMarkup(t *testing.T) {
	md := "Hi <script>alert(1)</script> <img src=x onerror=alert(2)>\n\n" +
		"- [click](javascript:alert(3)) and [data](data:text/html,x) and [enc](javascript&#58;alert(4))\n" +
		"- [ok](https://example.com/a?b=1&c=\"2\") <a href=\"javascript:alert(5)\">raw</a>\n"
	got := string(renderReleaseNotes(md))
	low := strings.ToLower(got)
	for _, bad := range []string{"<script", "<img", `href="javascript`, `href="data`, "<a href=\"javascript"} {
		if strings.Contains(low, bad) {
			t.Errorf("%q survived:\n%s", bad, got)
		}
	}
	if !strings.Contains(got, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Errorf("the script tag is not shown as text:\n%s", got)
	}
	if !strings.Contains(got, `href="https://example.com/a?b=1&amp;c=%222%22"`) && !strings.Contains(got, `href="https://example.com/a?b=1&amp;c=&#34;2&#34;"`) {
		t.Errorf("the https link is not kept:\n%s", got)
	}
	if !strings.Contains(got, "<li>click and data and enc and ") && !strings.Contains(got, "<li>click and data and enc</li>") {
		t.Errorf("a javascript: link keeps its text without the link:\n%s", got)
	}
}

func TestTrimNotesInline(t *testing.T) {
	// The first sentence when it fits, without an ellipsis.
	got := trimNotesInline(parseNotesInline("The block editor's image crop saves a new attachment. Patterns open in the editor and much more text follows here."), 70)
	if got != "The block editor&#39;s image crop saves a new attachment." && got != "The block editor's image crop saves a new attachment." {
		t.Errorf("sentence: %q", got)
	}
	// Else whole words and an ellipsis, never a cut word or a cut code span.
	long := "Personal data export and erasure now cover `Pursae Contact Form` entries, Web Builder form submissions and more"
	got = trimNotesInline(parseNotesInline(long), 60)
	if !strings.HasSuffix(got, "…") || strings.Contains(got, ",…") {
		t.Errorf("ellipsis: %q", got)
	}
	plain := html.UnescapeString(wp.StripTags(got))
	if !strings.HasPrefix(long, strings.ReplaceAll(strings.TrimSuffix(plain, "…"), "Pursae Contact Form", "`Pursae Contact Form`")) {
		t.Errorf("not a prefix at a word: %q", plain)
	}
	if n := utf8.RuneCountInString(plain); n > 61 {
		t.Errorf("too long (%d): %q", n, plain)
	}
	if strings.Count(got, "<code>") != strings.Count(got, "</code>") {
		t.Errorf("code span cut: %q", got)
	}
	got = trimNotesInline(parseNotesInline("Short `x` and a long code span `"+strings.Repeat("y", 80)+"` at the end"), 40)
	if strings.Contains(got, "yyy") || !strings.HasSuffix(got, "…") {
		t.Errorf("code span that does not fit: %q", got)
	}
	// "e.g. the" is no sentence end.
	got = trimNotesInline(parseNotesInline("Some setting names, e.g. the ones under Settings, are kept as they are in WordPress."), 50)
	if strings.Contains(got, "e.g.…") || !strings.HasSuffix(got, "…") {
		t.Errorf("abbreviation: %q", got)
	}
	// No little word before the ellipsis.
	if got := trimNotesInline(parseNotesInline("with a 10 s timeout and a 150 KB cap on every single response body"), 26); got != "with a 10 s timeout…" {
		t.Errorf("stop words: %q", got)
	}
	// Short text is left whole.
	if got := trimNotesInline(parseNotesInline("Short."), 50); got != "Short." {
		t.Errorf("short: %q", got)
	}
}

func TestReleaseNotesDigest040(t *testing.T) {
	d := releaseNotesDigest(releaseNotes040(t), notesDigestItems)
	if d == nil {
		t.Fatal("no digest")
	}
	if s := string(d.Summary); !strings.HasPrefix(s, "Editor gaps, complete personal-data tools") || !strings.HasSuffix(s, "WooCommerce products.") {
		t.Errorf("summary: %q", s)
	}
	var labels []string
	items := 0
	for _, g := range d.Groups {
		labels = append(labels, g.Label+"/"+g.Class)
		for _, it := range g.Items {
			items++
			s := string(it)
			plain := html.UnescapeString(wp.StripTags(s))
			if utf8.RuneCountInString(plain) > notesDigestItemLen+1 {
				t.Errorf("item too long: %q", plain)
			}
			if strings.ContainsAny(plain, "`#") || strings.Contains(s, "&amp;#") {
				t.Errorf("raw markdown or double escaping: %q", s)
			}
		}
	}
	if got := strings.Join(labels, " "); got != "Added/added Changed/changed Fixed/fixed Security/security" {
		t.Errorf("groups: %s", got)
	}
	if items != 4 || d.More != 17-4 {
		t.Errorf("items %d, more %d", items, d.More)
	}
	first := string(d.Groups[0].Items[0])
	if !strings.Contains(first, "<code>POST /wp/v2/media/{id}/edit</code>") || !strings.Contains(html.UnescapeString(first), "the block editor's image crop") {
		t.Errorf("first item: %s", first)
	}
	if releaseNotesDigest("", 4) != nil || releaseNotesDigest("\n\n", 4) != nil {
		t.Error("digest of empty notes")
	}
	// Notes without headings: the items go under one unnamed group.
	if d := releaseNotesDigest("- one\n- two\n- three", 2); d == nil || len(d.Groups) != 1 || len(d.Groups[0].Items) != 2 || d.More != 1 || d.Groups[0].Class != "other" {
		t.Errorf("headingless digest: %+v", d)
	}
}

// The Dashboard box and Dashboard › Updates with the real 0.4.0 notes from the cached Hub index.
func TestDashboardNewsRendersReleaseNotes(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	released := time.Now().Add(-2 * time.Hour).UTC()
	srv.hubCacheSet(context.Background(), srv.hubCacheKey("wpgo_hub_index"), hubIndex{API: 1, Core: &hubCore{
		Latest: "99.0.0", Released: released.Format(time.RFC3339), Notes: releaseNotes040(t),
		NotesURL: "https://github.com/pursae/pursae/releases/tag/v99.0.0",
	}}, time.Hour)
	_, body, _ := get(t, c, ts.URL+"/wp-admin/")
	i := strings.Index(body, `id="dashboard_primary"`)
	if i < 0 {
		t.Fatal("no Pursae News box")
	}
	box := body[i:]
	if j := strings.Index(box[1:], `class="postbox `); j > 0 {
		box = box[:j]
	}
	for _, want := range []string{
		`>Pursae 99.0.0</a>`,
		`class="pursae-news-badge is-update">Update available<`,
		`<time datetime="` + released.Format(time.RFC3339) + `" title="` + released.Format(time.RFC3339) + `">` + wp.FormatDate(released.In(srv.loc()), "j F Y") + `</time>`,
		`2 hours ago`,
		`Editor gaps, complete personal-data tools`,
		`<span class="pursae-news-label">Added</span>`,
		`is-security"><span class="pursae-news-label">Security</span>`,
		`<code>POST /wp/v2/media/{id}/edit</code>`,
		`13 more changes`,
		`<a href="https://github.com/pursae/pursae/releases/tag/v99.0.0" target="_blank" rel="noopener noreferrer">Read the full release notes`,
		`href="/wp-admin/update-core">Update now</a>`,
	} {
		if !strings.Contains(box, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, bad := range []string{"###", "&amp;#39;", "&amp;#34;", "- REST", "`", "T20:", "style="} {
		if strings.Contains(stripAttrs(box), bad) {
			t.Errorf("%q in the box", bad)
		}
	}
	if !strings.Contains(html.UnescapeString(box), "the block editor's image crop") {
		t.Error("the apostrophe does not read as written")
	}

	// Dashboard › Updates: the full notes, rendered the same way.
	_, body, _ = get(t, c, ts.URL+"/wp-admin/update-core")
	for _, want := range []string{`<h2 id="release-notes">Pursae 99.0.0 release notes</h2>`, `<h3 class="pursae-notes-section is-security"><span class="pursae-news-label">Security</span></h3>`,
		"Patterns created as &#34;Not synced&#34; keep their sync status (<code>meta.wp_pattern_sync_status</code>).", "Read the release notes online"} {
		if !strings.Contains(body, want) {
			t.Errorf("update-core: missing %q", want)
		}
	}
	if strings.Contains(body, "### Added") || strings.Contains(body, "&amp;#39;") {
		t.Error("update-core: raw or double-escaped notes")
	}

	// In Turkish the date reads "21 Kasım 2026", the labels and links are translated, the notes stay English.
	if err := srv.settings.Set(context.Background(), map[string]any{"WPLANG": "tr_TR"}); err != nil {
		t.Fatal(err)
	}
	srv.hubCacheSet(context.Background(), srv.hubCacheKey("wpgo_hub_index"), hubIndex{API: 1, Core: &hubCore{
		Latest: "99.0.0", Released: "2026-11-21T20:25:56Z", Notes: releaseNotes040(t)}}, time.Hour)
	_, body, _ = get(t, c, ts.URL+"/wp-admin/")
	for _, want := range []string{"21 Kasım 2026", "Güncelleme var", "Sürüm notlarının tamamını okuyun", ">Güvenlik<", "13 değişiklik daha",
		"Editor gaps, complete personal-data tools", `href="/wp-admin/update-core#release-notes"`} {
		if !strings.Contains(body, want) {
			t.Errorf("tr_TR: missing %q", want)
		}
	}
}

// stripAttrs drops attribute values, so a URL in an href does not count as text.
func stripAttrs(s string) string {
	var b strings.Builder
	in, q := false, byte(0)
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case q != 0:
			if ch == q {
				q = 0
			}
			continue
		case in && (ch == '"' || ch == '\''):
			q = ch
			continue
		case ch == '<':
			in = true
		case ch == '>':
			in = false
		}
		b.WriteByte(ch)
	}
	return b.String()
}
