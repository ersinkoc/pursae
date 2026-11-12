package server

import (
	"net/http"
	"strings"
	"testing"
)

// A subscriber's profile URL is copied into the comments it writes (REST create_item and the comment
// form). The profile URL must not carry a javascript: link into the front end: the Latest Comments
// block links the commenter's name with that URL.
func TestRESTUsers_ProfileURLJavascriptInLatestComments(t *testing.T) {
	s := secStart(t)
	pub := s.post("Public thread", "publish", "")
	_, sub := s.user("mallory", "subscriber")

	payload := "javascript:alert(document.domain)"
	code, me, _, _ := sub.do("POST", "/wp/v2/users/me", map[string]any{"url": payload})
	if code != http.StatusOK {
		t.Fatalf("update own profile: %d %v", code, me)
	}
	if got, _ := me["url"].(string); got != "" {
		t.Errorf("profile url stored as %q, want it blanked (esc_url_raw)", got)
	}
	code, out, _, _ := sub.do("POST", "/wp/v2/comments", map[string]any{"post": pub, "content": "Nice post, see my site"})
	if code != http.StatusCreated {
		t.Fatalf("create comment: %d %v", code, out)
	}
	cid := out["id"].(float64)
	if code, out, _, _ := s.admin.do("POST", "/wp/v2/comments/"+itoa64(int64(cid)), map[string]any{"status": "approved"}); code != http.StatusOK {
		t.Fatalf("approve comment: %d %v", code, out)
	}
	code, page, _, _ := s.admin.do("POST", "/wp/v2/pages", map[string]any{"title": "Recent", "content": "<!-- wp:latest-comments /-->", "status": "publish"})
	if code != http.StatusCreated {
		t.Fatalf("create page: %d %v", code, page)
	}
	_, body, _ := get(t, s.anon().c, page["link"].(string))
	if !strings.Contains(body, "wp-block-latest-comments") {
		t.Fatalf("latest comments block not rendered: %s", body)
	}
	if strings.Contains(body, `href="javascript:`) {
		t.Errorf("a subscriber's javascript: profile URL is rendered as the comment author link on the public site")
	}

	// A legitimate profile URL is kept (and gets http:// when the scheme is missing, as in WordPress).
	if code, me, _, _ := sub.do("POST", "/wp/v2/users/me", map[string]any{"url": "example.com/me"}); code != http.StatusOK || me["url"] != "http://example.com/me" {
		t.Errorf("legit profile url: %d %v", code, me["url"])
	}
}

func TestUserURLSanitize(t *testing.T) {
	for in, want := range map[string]string{
		"":                        "",
		"https://example.com/":    "https://example.com/",
		" example.com ":           "http://example.com",
		"javascript:alert(1)":     "",
		"JaVaScRiPt:alert(1)":     "",
		"data:text/html,<script>": "",
		"java\tscript:alert(1)":   "",
	} {
		if got := userURL(in); got != want {
			t.Errorf("userURL(%q) = %q, want %q", in, got, want)
		}
	}
}
