package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The robots limits are free-text post meta; a value that broke out of the robots meta tag ran script
// for every visitor of the post (the other head tags were escaped, this one was not).
func TestSEOGoRobotsLimitsCannotBreakOutOfTheTag(t *testing.T) {
	ts, c, _ := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	seogoToggle(t, c, base, "activate")
	code, body := restSend(t, c, base, http.MethodPost, "/wp-json/wp/v2/posts", `{"title":"Robots","status":"publish","slug":"robots-post","content":"<p>x</p>"}`)
	if code != http.StatusCreated {
		t.Fatalf("create post: %d %s", code, body)
	}
	var created struct {
		ID   int64  `json:"id"`
		Link string `json:"link"`
	}
	_ = json.Unmarshal([]byte(body), &created)
	payload, _ := json.Marshal(map[string]any{"fields": map[string]any{"max_snippet": `"><script>alert(document.domain)</script>`,
		"max_image_preview": "standard", "max_video_preview": "30"}})
	if code, body := restSend(t, c, base, http.MethodPost, "/wp-json/seogo/v1/meta/"+itoa64(created.ID), string(payload)); code != http.StatusOK {
		t.Fatalf("save meta: %d %s", code, body)
	}
	_, page, _ := get(t, c, created.Link)
	if strings.Contains(page, "<script>alert(document.domain)") {
		t.Fatal("robots limit broke out of the meta tag")
	}
	if !strings.Contains(page, `<meta name="robots" content="index, follow, max-snippet:-1, max-image-preview:standard, max-video-preview:30" />`) {
		i := strings.Index(page, `name="robots"`)
		t.Errorf("robots tag: %.200s", page[max(i-6, 0):])
	}
	for k, v := range map[string]string{"max-snippet": "-1", "max-video-preview": "120", "max-image-preview": "large"} {
		if !seogoRobotsLimitOK(k, v) {
			t.Errorf("%s:%s refused", k, v)
		}
	}
	for k, v := range map[string]string{"max-snippet": "1\"", "max-video-preview": "-2", "max-image-preview": "huge"} {
		if seogoRobotsLimitOK(k, v) {
			t.Errorf("%s:%s accepted", k, v)
		}
	}
}
