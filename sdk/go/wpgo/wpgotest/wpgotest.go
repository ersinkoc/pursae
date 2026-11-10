//go:build !(wasip1 && wasm)

// Package wpgotest is an in-memory fake of the Pursae plugin host for unit tests. It lets code that
// calls wpgo.GetOption, wpgo.Storage, wpgo.Transients, wpgo.GetPost, wpgo.QueryPosts, wpgo.HTTPGet,
// wpgo.CurrentUser and friends run under a plain `go test` (no Wasm, no site):
//
//	func TestGreeting(t *testing.T) {
//		h := wpgotest.New(t)
//		h.SetOption("blogname", "My Site")
//		h.StubHTTP("https://api.example.com/x", 200, `{"ok":true}`)
//		// ... call your plugin code, then inspect h.Storage(), h.Mails(), h.Notices() ...
//	}
//
// The fake host does not enforce permissions; declare them in plugin.json as usual.
package wpgotest

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"testing"

	"wpgo/sdk/go/wpgo"
)

// Mail is a message captured from wpgo.SendMail.
type Mail = wpgo.Mail

// Host is the fake host. Create it with New.
type Host struct {
	mu         sync.Mutex
	options    map[string]json.RawMessage
	storage    map[string]string
	transients map[string]json.RawMessage
	posts      map[int64]wpgo.Post
	nextPostID int64
	user       *wpgo.User
	http       map[string]wpgo.HTTPResponse
	requests   []wpgo.HTTPRequestArgs
	mails      []Mail
	notices    []map[string]string
	logs       []string
}

// New installs a fresh fake host for the duration of the test.
func New(t testing.TB) *Host {
	h := &Host{
		options: map[string]json.RawMessage{}, storage: map[string]string{},
		transients: map[string]json.RawMessage{}, posts: map[int64]wpgo.Post{}, nextPostID: 1,
		http: map[string]wpgo.HTTPResponse{},
	}
	restore := wpgo.SetHostForTest(h.handle)
	t.Cleanup(restore)
	return h
}

// SetOption sets a site option (any JSON-encodable value).
func (h *Host) SetOption(name string, value any) {
	b, _ := json.Marshal(value)
	h.mu.Lock()
	h.options[name] = b
	h.mu.Unlock()
}

// Option returns the JSON stored for an option.
func (h *Host) Option(name string) (json.RawMessage, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	v, ok := h.options[name]
	return v, ok
}

// SetStorage presets a plugin storage key.
func (h *Host) SetStorage(key, value string) {
	h.mu.Lock()
	h.storage[key] = value
	h.mu.Unlock()
}

// Storage returns a copy of the plugin storage.
func (h *Host) Storage() map[string]string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make(map[string]string, len(h.storage))
	for k, v := range h.storage {
		out[k] = v
	}
	return out
}

// AddPost stores a post (ID 0 gets the next free id) and returns its id.
func (h *Host) AddPost(p wpgo.Post) int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	if p.ID == 0 {
		p.ID = h.nextPostID
	}
	if p.ID >= h.nextPostID {
		h.nextPostID = p.ID + 1
	}
	if p.Type == "" {
		p.Type = "post"
	}
	if p.Status == "" {
		p.Status = "publish"
	}
	h.posts[p.ID] = p
	return p.ID
}

// SetUser sets the current user (nil = anonymous).
func (h *Host) SetUser(u *wpgo.User) {
	h.mu.Lock()
	h.user = u
	h.mu.Unlock()
}

// StubHTTP answers GET (and any other method) requests to url with the given status and body.
func (h *Host) StubHTTP(url string, status int, body string) {
	h.mu.Lock()
	h.http[url] = wpgo.HTTPResponse{Status: status, Body: body}
	h.mu.Unlock()
}

// Requests returns the outbound HTTP requests the code made, in order.
func (h *Host) Requests() []wpgo.HTTPRequestArgs {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]wpgo.HTTPRequestArgs(nil), h.requests...)
}

// Mails returns the messages queued with wpgo.SendMail.
func (h *Host) Mails() []Mail {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]Mail(nil), h.mails...)
}

// Notices returns the admin notices as {"type","message"} maps.
func (h *Host) Notices() []map[string]string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]map[string]string(nil), h.notices...)
}

// Logs returns "level: message" lines written with wpgo.Log.
func (h *Host) Logs() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.logs...)
}

func ok(result any) ([]byte, error) {
	r, _ := json.Marshal(result)
	return json.Marshal(map[string]any{"ok": true, "result": json.RawMessage(r)})
}

func fail(code, msg string) ([]byte, error) {
	return json.Marshal(map[string]any{"ok": false, "code": code, "error": msg})
}

func (h *Host) handle(name string, body []byte) ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var req map[string]json.RawMessage
	_ = json.Unmarshal(body, &req)
	str := func(k string) string {
		var s string
		_ = json.Unmarshal(req[k], &s)
		return s
	}
	switch name {
	case "add_hook", "register_shortcode", "register_rest_route", "enqueue_style", "enqueue_script",
		"schedule_event", "unschedule_event":
		return ok(map[string]any{})
	case "register_settings_page":
		return ok(map[string]string{"option_prefix": "plugin_test_"})
	case "log":
		h.logs = append(h.logs, str("level")+": "+str("message"))
		return ok(map[string]any{})
	case "translate":
		return ok(map[string]string{"text": str("text")})
	case "get_option":
		v, found := h.options[str("name")]
		return ok(map[string]any{"found": found, "value": v})
	case "update_option":
		h.options[str("name")] = req["value"]
		return ok(map[string]any{})
	case "storage_get":
		v, found := h.storage[str("key")]
		return ok(map[string]any{"found": found, "value": v})
	case "storage_set":
		h.storage[str("key")] = str("value")
		return ok(map[string]any{})
	case "storage_delete":
		delete(h.storage, str("key"))
		return ok(map[string]any{})
	case "storage_list":
		var limit int
		_ = json.Unmarshal(req["limit"], &limit)
		if limit <= 0 || limit > 500 {
			limit = 500
		}
		prefix, after := str("prefix"), str("after")
		var keys []string
		for k := range h.storage {
			if strings.HasPrefix(k, prefix) && k > after {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		next := ""
		if len(keys) > limit {
			keys = keys[:limit]
			next = keys[len(keys)-1]
		}
		return ok(map[string]any{"keys": keys, "next": next})
	case "transient_get":
		v, found := h.transients[str("key")]
		return ok(map[string]any{"found": found, "value": v})
	case "transient_set":
		h.transients[str("key")] = req["value"]
		return ok(map[string]any{})
	case "transient_delete":
		delete(h.transients, str("key"))
		return ok(map[string]any{})
	case "get_current_user":
		if h.user == nil {
			return ok(map[string]any{"found": false})
		}
		return ok(map[string]any{"found": true, "user": h.user})
	case "http_get", "http_request":
		a := wpgo.HTTPRequestArgs{Method: "GET", URL: str("url")}
		if name == "http_request" {
			_ = json.Unmarshal(body, &a)
			if a.Method == "" {
				a.Method = "GET"
			}
		}
		h.requests = append(h.requests, a)
		resp, found := h.http[a.URL]
		if !found {
			return fail(wpgo.CodeHostError, "wpgotest: no stub for "+a.URL)
		}
		return ok(resp)
	case "send_mail":
		var m Mail
		_ = json.Unmarshal(body, &m)
		h.mails = append(h.mails, m)
		return ok(map[string]any{})
	case "add_admin_notice":
		h.notices = append(h.notices, map[string]string{"type": str("type"), "message": str("message")})
		return ok(map[string]any{})
	case "get_post":
		var id int64
		_ = json.Unmarshal(req["id"], &id)
		p, found := h.posts[id]
		if !found {
			return ok(map[string]any{"found": false})
		}
		return ok(map[string]any{"found": true, "post": p})
	case "query_posts":
		var q wpgo.PostQuery
		_ = json.Unmarshal(body, &q)
		var list []wpgo.Post
		for _, p := range h.posts {
			if q.Type != "" && p.Type != q.Type {
				continue
			}
			if q.Status != "" && q.Status != "any" && p.Status != q.Status {
				continue
			}
			if q.Status == "" && p.Status != "publish" {
				continue
			}
			if q.Author != 0 && p.Author != q.Author {
				continue
			}
			if q.Search != "" && !strings.Contains(strings.ToLower(p.Title+" "+p.Content), strings.ToLower(q.Search)) {
				continue
			}
			list = append(list, p)
		}
		sort.Slice(list, func(i, j int) bool {
			if strings.EqualFold(q.Order, "ASC") {
				return list[i].ID < list[j].ID
			}
			return list[i].ID > list[j].ID
		})
		total := len(list)
		per := q.PerPage
		if per <= 0 {
			per = 10
		}
		page := q.Page
		if page <= 0 {
			page = 1
		}
		lo := (page - 1) * per
		if lo > total {
			lo = total
		}
		hi := lo + per
		if hi > total {
			hi = total
		}
		return ok(wpgo.PostList{Posts: append([]wpgo.Post{}, list[lo:hi]...), Total: total, Pages: (total + per - 1) / per})
	}
	return fail(wpgo.CodeHostError, "wpgotest: host function "+name+" is not faked")
}
