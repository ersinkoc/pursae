package wp

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"net/url"
	"strings"
	"time"

	"golang.org/x/text/encoding/ianaindex"
)

// FeedItem is one entry of a fetched feed (SimplePie_Item for the RSS block).
type FeedItem struct {
	Title       string
	Link        string
	Description string
	Author      string
	Date        time.Time // zero when the feed carries no usable date
}

// Feed is a parsed RSS 2.0, RSS 1.0 (RDF) or Atom document.
type Feed struct {
	Title string
	Items []FeedItem
}

const atomNamespace = "http://www.w3.org/2005/Atom"

type feedLink struct {
	Base string `xml:"http://www.w3.org/XML/1998/namespace base,attr"`
	Rel  string `xml:"rel,attr"`
	Href string `xml:"href,attr"`
	Text string `xml:",chardata"`
}

func (l feedLink) isAlternate(atom bool) bool {
	return l.Rel == "" || l.Rel == "alternate" ||
		(atom && l.Rel == "http://www.iana.org/assignments/relation/alternate")
}

type feedItem struct {
	Base  string     `xml:"http://www.w3.org/XML/1998/namespace base,attr"`
	Title feedText   `xml:"title"`
	Links []feedLink `xml:"link"`
	GUID  struct {
		IsPermalink string `xml:"isPermaLink,attr"`
		Text        string `xml:",chardata"`
	} `xml:"guid"`
	Description string   `xml:"description"`
	Encoded     string   `xml:"http://purl.org/rss/1.0/modules/content/ encoded"`
	Summary     feedText `xml:"summary"`
	Content     feedText `xml:"content"`
	PubDate     string   `xml:"pubDate"`
	DCDate      string   `xml:"http://purl.org/dc/elements/1.1/ date"`
	Published   string   `xml:"published"`
	Updated     string   `xml:"updated"`
	Author      struct {
		Name string `xml:"name"`      // Atom <author><name>
		Text string `xml:",chardata"` // RSS <author>
	} `xml:"author"`
	Creator string `xml:"http://purl.org/dc/elements/1.1/ creator"`
	Source  struct {
		Author struct {
			Name string `xml:"name"`
		} `xml:"author"`
	} `xml:"source"`
}

// feedText preserves nested XHTML while retaining the decoded text behavior of ordinary Atom content.
type feedText string

func (t *feedText) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	var v struct {
		Type  string `xml:"type,attr"`
		Text  string `xml:",chardata"`
		Inner string `xml:",innerxml"`
	}
	if err := d.DecodeElement(&v, &start); err != nil {
		return err
	}
	if strings.EqualFold(v.Type, "xhtml") {
		*t = feedText(v.Inner)
	} else {
		*t = feedText(v.Text)
	}
	return nil
}

type feedDoc struct {
	XMLName xml.Name
	feedItem
	Channel *struct {
		Title string     `xml:"title"`
		Items []feedItem `xml:"item"`
	} `xml:"channel"`
	Items   []feedItem `xml:"item"`  // RSS 1.0 lists items beside the channel
	Entries []feedItem `xml:"entry"` // Atom
}

var ErrNotFeed = errors.New("the data could not be converted to UTF-8 or is not a feed")

// ParseFeed reads an RSS or Atom document the way fetch_feed() does through SimplePie:
// titles, links, descriptions, authors and dates of every item, in document order.
func ParseFeed(data []byte) (*Feed, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	dec.CharsetReader = func(label string, r io.Reader) (io.Reader, error) {
		enc, err := ianaindex.IANA.Encoding(label)
		if err != nil || enc == nil {
			return nil, errors.New("unsupported charset " + label)
		}
		return enc.NewDecoder().Reader(r), nil
	}
	var doc feedDoc
	if err := dec.Decode(&doc); err != nil {
		return nil, ErrNotFeed
	}
	f := &Feed{Title: strings.TrimSpace(string(doc.Title))}
	var raw []feedItem
	kind := strings.ToLower(doc.XMLName.Local)
	atom := kind == "feed" || kind == "entry"
	switch kind {
	case "rss":
		if doc.Channel == nil {
			return nil, ErrNotFeed
		}
		f.Title = strings.TrimSpace(doc.Channel.Title)
		raw = doc.Channel.Items
	case "rdf":
		if doc.Channel != nil {
			f.Title = strings.TrimSpace(doc.Channel.Title)
		}
		raw = doc.Items
	case "feed":
		raw = doc.Entries
	case "entry":
		if doc.XMLName.Space != atomNamespace {
			return nil, ErrNotFeed
		}
		raw = []feedItem{doc.feedItem}
	default:
		return nil, ErrNotFeed
	}
	var rootBase *url.URL
	if atom {
		rootBase = feedBase(nil, doc.Base)
	}
	for _, it := range raw {
		item := FeedItem{Title: strings.TrimSpace(string(it.Title)), Description: it.Description}
		itemBase := rootBase
		if kind == "feed" {
			itemBase = feedBase(rootBase, it.Base)
		}
		if item.Description == "" {
			item.Description = it.Encoded
		}
		if item.Description == "" {
			item.Description = string(it.Summary)
		}
		if item.Description == "" {
			item.Description = string(it.Content)
		}
		var linkBase string
		for _, l := range it.Links {
			if href := strings.TrimSpace(l.Href); href != "" && l.isAlternate(atom) {
				item.Link, linkBase = href, l.Base
				break
			}
		}
		if item.Link == "" {
			for _, l := range it.Links {
				if strings.TrimSpace(l.Href) != "" || !l.isAlternate(atom) || strings.TrimSpace(l.Text) == "" {
					continue
				}
				item.Link, linkBase = strings.TrimSpace(l.Text), l.Base
				break
			}
		}
		if atom && item.Link != "" {
			item.Link = atomItemLink(itemBase, linkBase, item.Link)
		}
		if item.Link == "" && kind == "rss" && (it.GUID.IsPermalink == "" || strings.EqualFold(it.GUID.IsPermalink, "true")) {
			guid := strings.TrimSpace(it.GUID.Text)
			if u, err := url.Parse(guid); err == nil && u.IsAbs() && u.Host != "" {
				item.Link = guid
			}
		}
		item.Author = strings.TrimSpace(it.Creator)
		if item.Author == "" {
			item.Author = strings.TrimSpace(it.Author.Name)
		}
		if item.Author == "" {
			item.Author = strings.TrimSpace(it.Author.Text)
		}
		if item.Author == "" && atom {
			item.Author = strings.TrimSpace(it.Source.Author.Name)
		}
		if item.Author == "" && atom {
			item.Author = strings.TrimSpace(doc.Author.Name)
		}
		for _, d := range []string{it.PubDate, it.DCDate, it.Published, it.Updated} {
			if t, ok := parseFeedDate(d); ok {
				item.Date = t
				break
			}
		}
		f.Items = append(f.Items, item)
	}
	return f, nil
}

func feedBase(parent *url.URL, value string) *url.URL {
	if value == "" {
		return parent
	}
	base, err := url.Parse(value)
	if err != nil {
		return parent
	}
	if base.IsAbs() {
		return base
	}
	if parent != nil {
		return parent.ResolveReference(base)
	}
	return nil
}

func atomItemLink(parent *url.URL, override, value string) string {
	base := feedBase(parent, override)
	if base == nil {
		return value
	}
	ref, err := url.Parse(value)
	if err != nil || ref.IsAbs() {
		return value
	}
	return base.ResolveReference(ref).String()
}

var feedDateLayouts = []string{
	time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC822, time.RFC3339, time.RFC3339Nano,
	"Mon, 2 Jan 2006 15:04:05 -0700", "Mon, 2 Jan 2006 15:04:05 MST", "2 Jan 2006 15:04:05 -0700",
	"Mon, 2 Jan 2006 15:04 -0700", "Mon, 2 Jan 2006 15:04 MST", "2 Jan 2006 15:04 -0700", "2 Jan 2006 15:04 MST",
	"2006-01-02T15:04:05Z07:00", "2006-01-02T15:04Z07:00", "2006-01-02 15:04:05", "2006-01-02",
}

// US time zone abbreviations in the RSS date format (RFC 822).
var rssNamedZoneHours = map[string]int{
	"EST": -5, "EDT": -4, "CST": -6, "CDT": -5,
	"MST": -7, "MDT": -6, "PST": -8, "PDT": -7,
}

func parseFeedDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	loc := time.UTC
	if _, zone, ok := strings.CutLast(s, " "); ok {
		if hours, known := rssNamedZoneHours[zone]; known {
			loc = time.FixedZone(zone, hours*60*60)
		}
	}
	for _, l := range feedDateLayouts {
		if t, err := time.ParseInLocation(l, s, loc); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
