// Command ui-demo is a wpgo example plugin that shows the admin and editor UI API:
//
//	Tools › Gopher Board     an admin page with a form that stores a message (admin:pages)
//	Dashboard › Gopher Board a Dashboard widget showing the message (admin:dashboard)
//	Gopher Notes             a meta box on posts and pages with a subtitle, a mood and a flag (admin:metaboxes)
//	ui-demo/badge            a dynamic block rendered on the server (blocks:register)
//	ui-demo/badge            the same badge as a Pursae Web Builder widget (builder:widgets)
//
// Build: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o plugin.wasm .
package main

import (
	"html"
	"strings"

	"wpgo/sdk/go/wpgo"
)

const messageKey = "message"

// badge renders the badge shared by the block and the Web Builder widget.
func badge(_ wpgo.Context, attrs map[string]any, content string) string {
	text, _ := attrs["text"].(string)
	if strings.TrimSpace(text) == "" {
		text = "Gopher"
	}
	style, _ := attrs["style"].(string)
	if style != "outline" {
		style = "solid"
	}
	out := `<span class="ui-demo-badge ui-demo-badge--` + style + `">` + html.EscapeString(text) + `</span>`
	if loud, _ := attrs["loud"].(bool); loud {
		out = "<strong>" + out + "</strong>"
	}
	return out + content
}

// boardPage renders Tools › Gopher Board and stores the posted message.
func boardPage(_ wpgo.Context, req wpgo.AdminRequest) wpgo.AdminResponse {
	var resp wpgo.AdminResponse
	if req.Method == "POST" {
		msg := strings.TrimSpace(req.Form["message"])
		if len(msg) > 200 {
			msg = msg[:200]
		}
		if err := wpgo.Storage.Set(messageKey, msg); err != nil {
			resp.Notices = append(resp.Notices, wpgo.PageNotice{Type: "error", Message: "Could not save: " + err.Error()})
		} else {
			resp.Notices = append(resp.Notices, wpgo.PageNotice{Type: "success", Message: "Message saved."})
		}
	}
	msg, _, _ := wpgo.Storage.Get(messageKey)
	resp.HTML = `<p>The message shown on the Dashboard.</p>
<table class="form-table" role="presentation"><tr><th scope="row"><label for="message">Message</label></th>
<td><input name="message" type="text" id="message" class="regular-text" value="` + html.EscapeString(msg) + `" /></td></tr></table>
<p class="submit"><button type="submit" class="button button-primary">Save Message</button></p>`
	return resp
}

var badgeFields = []wpgo.SettingsField{
	{ID: "text", Label: "Text", Type: "text", Default: "Gopher"},
	{ID: "style", Label: "Style", Type: "radio", Default: "solid", Options: []wpgo.SettingsOption{{Value: "solid", Label: "Solid"}, {Value: "outline", Label: "Outline"}}},
	{ID: "loud", Label: "Bold", Type: "checkbox"},
}

func init() {
	wpgo.RegisterAdminPage(wpgo.AdminPage{PageTitle: "Gopher Board", MenuTitle: "Gopher Board", Parent: "tools", Render: boardPage})
	wpgo.RegisterDashboardWidget("board", "Gopher Board", "", func(wpgo.Context, wpgo.AdminRequest) string {
		msg, ok, _ := wpgo.Storage.Get(messageKey)
		if !ok || msg == "" {
			return `<p>No message yet. Write one under <a href="/wp-admin/admin?page=ui-demo">Tools › Gopher Board</a>.</p>`
		}
		return "<p>" + html.EscapeString(msg) + "</p>"
	})
	wpgo.RegisterMetaBox(wpgo.MetaBox{
		ID: "notes", Title: "Gopher Notes", Context: "side",
		Fields: []wpgo.SettingsField{
			{ID: "subtitle", Label: "Subtitle", Type: "text"},
			{ID: "mood", Label: "Mood", Type: "select", Default: "happy", Options: []wpgo.SettingsOption{{Value: "happy", Label: "Happy"}, {Value: "sleepy", Label: "Sleepy"}}},
			{ID: "featured", Label: "Featured gopher", Type: "checkbox"},
		},
		Render: func(_ wpgo.Context, req wpgo.AdminRequest) string {
			if req.PostID == 0 {
				return "<p>Save the post once to add notes.</p>"
			}
			return "<p>Notes are stored as post meta.</p>"
		},
	})
	wpgo.RegisterBlock(wpgo.Block{Name: "badge", Title: "Gopher Badge", Description: "A small badge rendered by the ui-demo plugin.",
		Icon: "awards", Keywords: []string{"badge", "gopher"}, Attributes: badgeFields, Render: badge})
	wpgo.RegisterBuilderWidget(wpgo.BuilderWidget{ID: "badge", Title: "Gopher Badge", Icon: "awards", Controls: badgeFields, Render: badge})
}

func main() {}
