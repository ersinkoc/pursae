// Command hello-gopher is a demo wpgo plugin.
//
// It appends a small footer to single posts (when the "hello_gopher_footer"
// option is enabled) and shows a random gopher one-liner as an admin notice.
//
// Build: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o plugin.wasm .
package main

import (
	"math/rand/v2"

	"wpgo/sdk/go/wpgo"
)

// footerOption toggles the content footer. Any truthy value enables it.
const footerOption = "hello_gopher_footer"

// footer is appended to single posts.
const footer = `<p class="hello-gopher-footer"><small>Served by a Wasm plugin 🐹</small></p>`

// sayings are original one-liners shown in the admin dashboard.
var sayings = []string{
	"Gophers don't panic. They return an error and keep digging.",
	"Today's forecast: zero nil pointers, light goroutines in the afternoon.",
	"A small interface is a happy interface.",
	"Your tunnels are concurrent, but never racy.",
	"gofmt has already settled today's style debate.",
	"This dashboard was garbage collected for your comfort.",
	"Keep calm and close your channels.",
	"Somewhere, a gopher is compiling faster than you can blink.",
}

func init() {
	wpgo.AddFilter("the_content", wpgo.DefaultPriority, addFooter)
	wpgo.AddAction("admin_notices", wpgo.DefaultPriority, func(wpgo.Context, []any) {
		_ = wpgo.AdminNotice(wpgo.NoticeInfo, "Hello, Gopher! "+randomSaying())
	})
}

// addFooter appends the footer to single post views when the option is on.
// The core passes the post context as the first extra argument; see the
// plugins README for its shape.
func addFooter(_ wpgo.Context, content string, args []any) string {
	if !isSingular(args) || !wpgo.OptionBool(footerOption, false) {
		return content
	}
	return content + "\n" + footer
}

func isSingular(args []any) bool {
	if len(args) == 0 {
		return false
	}
	post, ok := args[0].(map[string]any)
	if !ok {
		return false
	}
	single, _ := post["is_single"].(bool)
	return single
}

func randomSaying() string { return sayings[rand.IntN(len(sayings))] }

func main() {}
