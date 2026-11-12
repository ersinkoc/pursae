package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// forumRESTGet reads a forum/v1 route anonymously: status, body and X-WP-Total.
func forumRESTGet(t *testing.T, base, route string) (int, string, string) {
	t.Helper()
	resp, err := http.Get(base + "/wp-json/forum/v1" + route)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header.Get("X-WP-Total")
}

// forum/v1 is read-only, public, lists published non-spam items only and is gone while the plugin
// is off.
func TestForumREST(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	base := ts.URL
	ctx := context.Background()

	if code, body, _ := forumRESTGet(t, base, "/forums"); code != http.StatusNotFound || !strings.Contains(body, "rest_no_route") {
		t.Fatalf("while off: %d %s", code, body)
	}
	index := func() string {
		resp, err := http.Get(base + "/wp-json/")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	if idx := index(); strings.Contains(idx, `forum\/v1`) || strings.Contains(idx, `"forum/v1"`) {
		t.Error("/wp-json lists forum/v1 while the plugin is off")
	}
	if err := srv.forumActivate(ctx); err != nil {
		t.Fatal(err)
	}
	if idx := index(); !strings.Contains(idx, `forum\/v1`) && !strings.Contains(idx, `"forum/v1"`) {
		t.Error("/wp-json does not list forum/v1 while the plugin is on")
	}
	cat, _ := srv.forumSaveForum(ctx, forumForumInput{Title: "General", Kind: "category"}, 1)
	fid, err := srv.forumSaveForum(ctx, forumForumInput{Title: "Lobby", ParentID: cat, Description: "Chat here."}, 1)
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := srv.forumCreateTopic(ctx, forumNewTopic{ForumID: fid, AuthorID: 1, Title: "Public topic", Content: "<p>Hello world</p>", IP: "198.51.100.4"})
	pending, _ := srv.forumCreateTopic(ctx, forumNewTopic{ForumID: fid, AuthorID: 1, Title: "Pending topic", Content: "<p>Wait</p>", Pending: true})
	spam, _ := srv.forumCreateTopic(ctx, forumNewTopic{ForumID: fid, AuthorID: 1, Title: "Spam topic", Content: "<p>Buy</p>"})
	if err := srv.forumModerate(ctx, spam.ID, "spam", 1); err != nil {
		t.Fatal(err)
	}
	_, _ = srv.forumCreateReply(ctx, forumNewReply{TopicID: pub.ID, AuthorID: 1, Content: "<p>First reply</p>"})
	_, _ = srv.forumCreateReply(ctx, forumNewReply{TopicID: pub.ID, AuthorID: 1, Content: "<p>Hidden reply</p>", Pending: true})
	_, _ = srv.forumCreateReply(ctx, forumNewReply{TopicID: pub.ID, AuthorID: 1, Content: "<p>Second reply</p>"})

	code, body, total := forumRESTGet(t, base, "/forums")
	var forums []map[string]any
	if code != http.StatusOK || json.Unmarshal([]byte(body), &forums) != nil || len(forums) != 2 || total != "2" {
		t.Fatalf("forums: %d %s", code, body)
	}
	if forums[0]["kind"] != "category" || forums[1]["title"] != "Lobby" || forums[1]["topic_count"] != float64(1) || forums[1]["reply_count"] != float64(2) {
		t.Errorf("forums: %+v", forums)
	}

	code, body, total = forumRESTGet(t, base, "/forums/"+strconv.FormatInt(fid, 10)+"/topics")
	var topics []map[string]any
	if code != http.StatusOK || json.Unmarshal([]byte(body), &topics) != nil || len(topics) != 1 || total != "1" {
		t.Fatalf("topics: %d %s", code, body)
	}
	if topics[0]["title"] != "Public topic" || strings.Contains(body, "Pending topic") || strings.Contains(body, "Spam topic") {
		t.Errorf("topics: %s", body)
	}
	if strings.Contains(body, "198.51.100.4") || strings.Contains(body, "admin@example.test") || strings.Contains(body, `"login"`) {
		t.Error("REST leaks the IP, email or login")
	}
	if code, body, _ := forumRESTGet(t, base, "/forums/"+strconv.FormatInt(cat, 10)+"/topics"); code != http.StatusOK || strings.TrimSpace(body) != "[]" {
		t.Errorf("category topics: %d %s", code, body)
	}
	if code, _, _ := forumRESTGet(t, base, "/forums/999999/topics"); code != http.StatusNotFound {
		t.Errorf("unknown forum: %d", code)
	}

	code, body, _ = forumRESTGet(t, base, "/topics/"+strconv.FormatInt(pub.ID, 10))
	if code != http.StatusOK || !strings.Contains(body, "Hello world") || !strings.Contains(body, `"reply_count":2`) {
		t.Errorf("topic: %d %s", code, body)
	}
	for _, id := range []int64{pending.ID, spam.ID, 999999} {
		if code, _, _ := forumRESTGet(t, base, "/topics/"+strconv.FormatInt(id, 10)); code != http.StatusNotFound {
			t.Errorf("topic %d: %d", id, code)
		}
		if code, _, _ := forumRESTGet(t, base, "/topics/"+strconv.FormatInt(id, 10)+"/replies"); code != http.StatusNotFound {
			t.Errorf("replies of %d: %d", id, code)
		}
	}

	code, body, total = forumRESTGet(t, base, "/topics/"+strconv.FormatInt(pub.ID, 10)+"/replies?per_page=1")
	var replies []map[string]any
	if code != http.StatusOK || json.Unmarshal([]byte(body), &replies) != nil || len(replies) != 1 || total != "2" {
		t.Fatalf("replies: %d %s", code, body)
	}
	if !strings.Contains(body, "First reply") || replies[0]["position"] != float64(1) {
		t.Errorf("replies page 1: %s", body)
	}
	_, body, _ = forumRESTGet(t, base, "/topics/"+strconv.FormatInt(pub.ID, 10)+"/replies?per_page=1&page=2")
	if !strings.Contains(body, "Second reply") || strings.Contains(body, "Hidden reply") {
		t.Errorf("replies page 2: %s", body)
	}
	if code, _, _ := forumRESTGet(t, base, "/topics/"+strconv.FormatInt(pub.ID, 10)+"/replies?per_page=1&page=9"); code != http.StatusBadRequest {
		t.Errorf("page out of range: %d", code)
	}
	// Read-only: no write method is routed.
	resp, err := http.Post(base+"/wp-json/forum/v1/forums", "application/json", strings.NewReader(`{"title":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode < 400 {
		t.Errorf("POST /forums: %d", resp.StatusCode)
	}

	if err := srv.forumDeactivate(ctx); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := forumRESTGet(t, base, "/topics/"+strconv.FormatInt(pub.ID, 10)); code != http.StatusNotFound {
		t.Errorf("topic while off: %d", code)
	}
}
