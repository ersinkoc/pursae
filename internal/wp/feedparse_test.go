package wp

import (
	"testing"
	"time"
)

func TestParseFeedRSSGUIDAndNamedZone(t *testing.T) {
	data := `<rss version="2.0"><channel>
		<item><guid>https://example.test/article</guid><pubDate>Mon, 02 Jan 2006 15:04:05 EST</pubDate></item>
		<item><guid isPermaLink="false">https://example.test/id</guid></item>
		<item><link>https://example.test/page</link><guid>https://example.test/other</guid></item>
	</channel></rss>`
	feed, err := ParseFeed([]byte(data))
	if err != nil || len(feed.Items) != 3 {
		t.Fatalf("feed=%+v, err=%v", feed, err)
	}
	for i, want := range []string{"https://example.test/article", "", "https://example.test/page"} {
		if got := feed.Items[i].Link; got != want {
			t.Errorf("item %d link = %q, want %q", i, got, want)
		}
	}
	want := time.Date(2006, 1, 2, 20, 4, 5, 0, time.UTC)
	if got := feed.Items[0].Date; !got.Equal(want) {
		t.Errorf("publication time = %s, want %s", got, want)
	}
}

func TestParseFeedAtomEntryLinksAndXHTMLTitle(t *testing.T) {
	data := `<feed xmlns="http://www.w3.org/2005/Atom" xml:base="https://example.test/news/">
		<entry xml:base="../archive/">
			<title type="xhtml"><div xmlns="http://www.w3.org/1999/xhtml">First <b>post</b></div></title>
			<link rel="alternate" href="   "/>
			<link rel="self" href="entry.xml"/>
			<link rel="http://www.iana.org/assignments/relation/alternate" xml:base="2026/" href="post%2F1?view=full#body"/>
		</entry>
	</feed>`
	feed, err := ParseFeed([]byte(data))
	if err != nil || len(feed.Items) != 1 {
		t.Fatalf("feed=%+v, err=%v", feed, err)
	}
	item := feed.Items[0]
	if got, want := StripTags(item.Title), "First post"; got != want {
		t.Errorf("title = %q, want %q", got, want)
	}
	if got, want := item.Link, "https://example.test/archive/2026/post%2F1?view=full#body"; got != want {
		t.Errorf("link = %q, want %q", got, want)
	}
}

func TestParseFeedStandaloneAtomEntry(t *testing.T) {
	data := `<entry xmlns="http://www.w3.org/2005/Atom" xml:base="https://example.test/news/">
		<title>Standalone</title><link href="post"/><author><name>Ana</name></author>
	</entry>`
	feed, err := ParseFeed([]byte(data))
	if err != nil || len(feed.Items) != 1 {
		t.Fatalf("feed=%+v, err=%v", feed, err)
	}
	if item := feed.Items[0]; item.Title != "Standalone" || item.Link != "https://example.test/news/post" || item.Author != "Ana" {
		t.Errorf("item = %+v", item)
	}
}
