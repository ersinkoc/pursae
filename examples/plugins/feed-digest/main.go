// Command feed-digest is an example plugin: an hourly scheduled event downloads a JSON feed
// ([{"title": "...", "url": "https://..."}]) with HTTPGet and keeps it in a transient; the
// [feed-digest limit="5"] shortcode renders it. The feed URL is the option feed_digest_url.
//
// Build: tools/build-plugins.sh examples/plugins/feed-digest
package main

import (
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"strconv"
	"strings"
	"time"

	"wpgo/sdk/go/wpgo"
)

const (
	cronHook     = "cron_feed_digest_refresh"
	transientKey = "feed"
	optionURL    = "feed_digest_url"
)

// Item is one feed entry.
type Item struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

func init() {
	wpgo.AddAction(cronHook, wpgo.DefaultPriority, func(wpgo.Context, []any) { refresh() })
	wpgo.AddShortcode("feed-digest", func(_ wpgo.Context, attrs map[string]string, _ string) string {
		var items []Item
		if ok, _ := wpgo.Transients.Get(transientKey, &items); !ok {
			return ""
		}
		limit, _ := strconv.Atoi(attrs["limit"])
		return Render(items, limit)
	})
	wpgo.OnInit(func() {
		if err := wpgo.ScheduleEvent(cronHook, time.Hour, nil); err != nil {
			wpgo.Log("warning", "schedule: "+err.Error())
		}
	})
}

// refresh downloads the feed and caches the parsed items for two hours.
func refresh() {
	feed, ok, err := wpgo.GetOption(optionURL)
	if err != nil || !ok || feed == "" {
		return
	}
	res, err := wpgo.HTTPGet(feed)
	if err != nil {
		wpgo.Log("error", "fetch feed: "+err.Error())
		return
	}
	items, err := ParseFeed(res.Status, res.Body)
	if err != nil {
		wpgo.Log("error", err.Error())
		return
	}
	_ = wpgo.Transients.Set(transientKey, items, 2*time.Hour)
}

// ParseFeed decodes a feed response, dropping entries without a title or an http(s) link.
func ParseFeed(status int, body string) ([]Item, error) {
	if status != 200 {
		return nil, fmt.Errorf("feed returned HTTP %d", status)
	}
	var all []Item
	if err := json.Unmarshal([]byte(body), &all); err != nil {
		return nil, fmt.Errorf("feed is not valid JSON: %w", err)
	}
	var out []Item
	for _, it := range all {
		u, err := url.Parse(it.URL)
		if strings.TrimSpace(it.Title) == "" || err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			continue
		}
		out = append(out, it)
	}
	return out, nil
}

// Render returns an escaped list of at most limit items (5 when limit < 1).
func Render(items []Item, limit int) string {
	if limit < 1 {
		limit = 5
	}
	if len(items) > limit {
		items = items[:limit]
	}
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<ul class="feed-digest">`)
	for _, it := range items {
		b.WriteString(`<li><a href="` + html.EscapeString(it.URL) + `">` + html.EscapeString(it.Title) + `</a></li>`)
	}
	b.WriteString("</ul>")
	return b.String()
}

func main() {}
