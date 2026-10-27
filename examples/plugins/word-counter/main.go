// Command word-counter is a demo wpgo plugin that adds a word count to the
// post statistics computed by the core through the "wpgo_post_stats" filter.
//
// Build: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o plugin.wasm .
package main

import (
	"html"
	"regexp"
	"strings"

	"wpgo/sdk/go/wpgo"
)

var tags = regexp.MustCompile(`<[^>]*>`)

func init() {
	// The core calls ApplyFilters(ctx, "wpgo_post_stats", stats, post) where
	// stats is a JSON object and post carries at least a "content" field.
	wpgo.AddFilter("wpgo_post_stats", wpgo.DefaultPriority,
		func(_ wpgo.Context, stats map[string]any, args []any) map[string]any {
			if stats == nil {
				stats = map[string]any{}
			}
			stats["word_count"] = CountWords(postContent(args))
			return stats
		})
}

// CountWords counts whitespace-separated words in HTML content, ignoring tags.
func CountWords(content string) int {
	text := html.UnescapeString(tags.ReplaceAllString(content, " "))
	return len(strings.Fields(text))
}

func postContent(args []any) string {
	if len(args) == 0 {
		return ""
	}
	switch p := args[0].(type) {
	case string:
		return p
	case map[string]any:
		s, _ := p["content"].(string)
		return s
	}
	return ""
}

func main() {}
