// Command publish-notifier is an example plugin: when a post is saved with the status
// "publish", it POSTs {"id", "title", "link"} as JSON to the webhook URL stored in the option
// publish_notifier_webhook, using wpgo.HTTPRequest.
//
// Build: tools/build-plugins.sh examples/plugins/publish-notifier
package main

import (
	"encoding/json"

	"wpgo/sdk/go/wpgo"
)

func init() {
	// save_post arguments: the post id and the post as GetPost returns it.
	wpgo.AddAction("save_post", wpgo.DefaultPriority, func(_ wpgo.Context, args []any) {
		hook, ok, err := wpgo.GetOption("publish_notifier_webhook")
		if err != nil || !ok || hook == "" || len(args) < 2 {
			return
		}
		body, send := Payload(args[1])
		if !send {
			return
		}
		res, err := wpgo.HTTPRequest(wpgo.HTTPRequestArgs{
			Method:  "POST",
			URL:     hook,
			Headers: map[string]string{"Content-Type": "application/json"},
			Body:    body,
		})
		if err != nil {
			wpgo.Log("error", "webhook: "+err.Error())
		} else if res.Status >= 300 {
			wpgo.Logf("webhook answered HTTP %d", res.Status)
		}
	})
}

// Payload builds the webhook body for a published post; send is false for other statuses.
func Payload(post any) (body string, send bool) {
	m, _ := post.(map[string]any)
	if m == nil || m["status"] != "publish" {
		return "", false
	}
	b, _ := json.Marshal(map[string]any{"id": m["id"], "title": m["title"], "link": m["link"]})
	return string(b), true
}

func main() {}
