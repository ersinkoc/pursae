package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"wpgo/internal/caps"
	"wpgo/internal/store"
)

// O2: the REST dispatcher decodes a JSON body before the handler's permission check. The body a
// caller may send therefore depends on who is asking (restJSONLimit), is refused before it is read
// when Content-Length announces too much, and holds a bounded number of values.

func TestJSONValuesWithin(t *testing.T) {
	for _, tc := range []struct {
		body string
		max  int
		ok   bool
	}{
		{`{}`, 1, true},
		{`{"a":1}`, 1, true},
		{`{"a":[0,0,0]}`, 3, false}, // object, array and two commas
		{`{"a":[0,0,0]}`, 4, true},
		{`{"a":"[,,,,{{{{"}`, 1, true},     // commas and brackets in a string do not count
		{`{"a":"\"[,,,"}`, 1, true},        // an escaped quote does not end the string
		{`{"a":"\\","b":[1,2]}`, 3, false}, // an escaped backslash does: b's array counts
		{`[` + strings.Repeat("0,", 999) + `0]`, 999, false},
		{`[` + strings.Repeat("0,", 999) + `0]`, 1000, true}, // a thousand elements
	} {
		if got := jsonValuesWithin([]byte(tc.body), tc.max); got != tc.ok {
			t.Errorf("jsonValuesWithin(%.40s, %d) = %v, want %v", tc.body, tc.max, got, tc.ok)
		}
	}
}

func TestRESTJSONLimitByUser(t *testing.T) {
	user := func(granted ...string) *store.User {
		u := &store.User{Caps: caps.Grants{}}
		for _, c := range granted {
			u.Caps[c] = true
		}
		return u
	}
	if restJSONLimit(nil) != restJSONSmallMax {
		t.Errorf("anonymous: %d", restJSONLimit(nil))
	}
	if n := restJSONLimit(user("read", "level_0", "forum_participate")); n != restJSONSmallMax {
		t.Errorf("front-end member: %d", n)
	}
	if n := restJSONLimit(user("read", "edit_posts", "upload_files")); n != restJSONMax {
		t.Errorf("back-office user: %d", n)
	}
}

// restBodyDo sends a JSON body to /wp-json<path>; chunked hides the length (no Content-Length).
func restBodyDo(t *testing.T, c *http.Client, base, method, path, nonce string, body []byte, chunked bool) (int, map[string]any) {
	t.Helper()
	var rd io.Reader = bytes.NewReader(body)
	if chunked {
		rd = io.MultiReader(bytes.NewReader(body)) // not a *bytes.Reader: the length is unknown
	}
	req, _ := http.NewRequest(method, base+"/wp-json"+path, rd)
	req.Header.Set("Content-Type", "application/json")
	if nonce != "" {
		req.Header.Set("X-WP-Nonce", nonce)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

// countingReader counts what the server read of a request body.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// restBodyInProcess posts a JSON body through the site's handler in process, as client (its cookies
// and nonce; nil: anonymous), and reports how many body bytes the server read. length is the
// announced Content-Length, -1 for none (chunked).
func restBodyInProcess(t *testing.T, h http.Handler, base string, client *wbrClient, path string, body []byte, length int64) (int, map[string]any, int64) {
	t.Helper()
	cr := &countingReader{r: bytes.NewReader(body)}
	req := httptest.NewRequest(http.MethodPost, base+"/wp-json"+path, cr)
	req.ContentLength = length
	req.Header.Set("Content-Type", "application/json")
	if client != nil {
		for _, ck := range client.c.Jar.Cookies(req.URL) {
			req.AddCookie(ck)
		}
		req.Header.Set("X-WP-Nonce", client.nonce)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out, cr.n
}

// jsonPadded is a JSON object of about size bytes with few values.
func jsonPadded(size int) []byte {
	return []byte(`{"title":"x","pad":"` + strings.Repeat("a", size) + `"}`)
}

func TestRESTJSONBodyLimits(t *testing.T) {
	srv, base, admin := wbrStart(t)
	anon := asNewClient()
	h := srv.Handler()

	// Anonymous: 2 MiB is refused before it is decoded: not read at all when Content-Length announces
	// it, read no further than the limit when the length is unknown.
	big := jsonPadded(2 << 20)
	code, v, read := restBodyInProcess(t, h, base, nil, "/wp/v2/posts", big, int64(len(big)))
	if code != http.StatusRequestEntityTooLarge || v["code"] != "rest_request_too_large" || read != 0 {
		t.Errorf("anonymous 2 MiB: %d %v, %d bytes read", code, v, read)
	}
	if d, _ := v["data"].(map[string]any); d["limit"] != float64(restJSONSmallMax) {
		t.Errorf("anonymous 2 MiB: data %v", v["data"])
	}
	code, v, read = restBodyInProcess(t, h, base, nil, "/wp/v2/posts", big, -1)
	if code != http.StatusRequestEntityTooLarge || read > restJSONSmallMax+1 {
		t.Errorf("anonymous chunked 2 MiB: %d %v, %d bytes read", code, v, read)
	}
	if code, v := restBodyDo(t, anon, base, http.MethodPost, "/wp/v2/posts", "", big, true); code != http.StatusRequestEntityTooLarge {
		t.Errorf("anonymous chunked 2 MiB over HTTP: %d %v", code, v)
	}
	// Under the limit the handler answers as before (its own permission check).
	if code, v := restBodyDo(t, anon, base, http.MethodPost, "/wp/v2/posts", "", jsonPadded(900<<10), false); code != http.StatusUnauthorized {
		t.Errorf("anonymous 900 KB: %d %v", code, v)
	}
	if code, v := restBodyDo(t, anon, base, http.MethodPost, "/wp/v2/posts", "", []byte(`{"title":`), false); code != http.StatusBadRequest || v["code"] != "rest_invalid_json" {
		t.Errorf("anonymous broken JSON: %d %v", code, v)
	}

	// A front-end member (self-registered subscriber) gets the visitor's limit.
	sub := wbrUser(t, srv, base, "member", "subscriber")
	if code, v, read := restBodyInProcess(t, h, base, sub, "/wp/v2/users/me", big, int64(len(big))); code != http.StatusRequestEntityTooLarge || read != 0 {
		t.Errorf("subscriber 2 MiB: %d %v, %d bytes read", code, v, read)
	}
	if code, v := restBodyDo(t, sub.c, base, http.MethodPost, "/wp/v2/users/me", sub.nonce, []byte(`{"description":"Hello"}`), false); code != http.StatusOK {
		t.Errorf("subscriber profile update: %d %v", code, v)
	}

	// A back-office user keeps the large limit: a Web Builder publish over 1 MiB still works.
	page := wbrPost(t, srv, "page", "Big Page", "draft", 1)
	doc, render := wbrFixture(t, "initial")
	body := wbrPublishBody(doc, render, 0, "publish", nil)
	body["padding"] = strings.Repeat("b", 1536<<10) // the editor's payload grows with the page
	if code, v, _ := admin.do(http.MethodPost, wbrDocPath(page.ID, "/publish"), body, nil); code != http.StatusOK {
		t.Fatalf("admin publish of 1.5 MiB: %d %v", code, v)
	}
	if code, v := restBodyDo(t, admin.c, base, http.MethodPost, "/wp/v2/posts", admin.nonce, jsonPadded(3<<20), false); code != http.StatusCreated {
		t.Errorf("admin 3 MiB post: %d %v", code, v)
	}
	// … but not one with millions of tiny values (the 1 GB decode).
	many := []byte(`{"title":"x","a":[` + strings.Repeat("0,", restJSONMaxValues) + `0]}`)
	if code, v := restBodyDo(t, admin.c, base, http.MethodPost, "/wp/v2/posts", admin.nonce, many, false); code != http.StatusRequestEntityTooLarge || v["code"] != "rest_request_too_large" {
		t.Errorf("admin, too many values: %d %v", code, v)
	}
	// Over the back-office limit: refused unread.
	huge := jsonPadded(restJSONMax + 1)
	if code, v, read := restBodyInProcess(t, h, base, admin, "/wp/v2/posts", huge, int64(len(huge))); code != http.StatusRequestEntityTooLarge || read != 0 {
		t.Errorf("admin over 32 MiB: %d %v, %d bytes read", code, v, read)
	}
}

// D2: REST errors never carry the database's own message.
func TestRESTErrHidesDatabaseErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	run := func(status int, code, msg string) (int, restError) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/wp-json/wp/v2/posts", nil)
		restErr(c, status, code, msg)
		var out restError
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return w.Code, out
	}
	pgMsg := `ERROR: invalid byte sequence for encoding "UTF8": 0xff (SQLSTATE 22021)`
	for _, code := range []string{"rest_query_error", "rest_cannot_create"} {
		st, out := run(http.StatusInternalServerError, code, "insert post: "+pgMsg)
		if st != http.StatusInternalServerError || out.Code != code || out.Message != restInternalMessage || strings.Contains(out.Message, "SQLSTATE") {
			t.Errorf("%s: %d %+v", code, st, out)
		}
	}
	if _, out := run(http.StatusInternalServerError, "rest_query_error", "context deadline exceeded"); out.Message != restInternalMessage {
		t.Errorf("rest_query_error keeps its detail: %+v", out)
	}
	if _, out := run(http.StatusBadRequest, "rest_invalid_param", "Invalid parameter(s): status"); out.Message != "Invalid parameter(s): status" {
		t.Errorf("an ordinary message was replaced: %+v", out)
	}
}
