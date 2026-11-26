package server

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"unicode/utf8"
)

// D2: text a visitor sends that PostgreSQL cannot hold (invalid UTF-8, NUL) reaches the database as
// U+FFFD (store.ValidText): searches answer with no results instead of 500, comments are saved, and
// no answer carries the database's error text.
func TestInvalidUTF8SearchAndComments(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	ctx := context.Background()
	installFrontSite(t, base, c, srv)
	nonce := restNonce(t, srv, c, base)
	anon := asNewClient()

	// The failure this guards against: the raw bytes fail the statement.
	var echo string
	if err := srv.store.Pool.QueryRow(ctx, `SELECT $1::text`, "caf\xe9").Scan(&echo); err == nil {
		t.Fatal("PostgreSQL accepted invalid UTF-8; the test no longer shows anything")
	}

	leaks := func(body string) bool {
		return strings.Contains(body, "SQLSTATE") || strings.Contains(body, "invalid byte sequence")
	}
	// %00 in a query string never gets this far: the firewall refuses it.
	for _, path := range []string{"/?s=%FF", "/?s=caf%E9", "/search/%FF/"} {
		code, body, _ := get(t, anon, base+path)
		if code != http.StatusOK || leaks(body) {
			t.Errorf("front-end %s: %d %.200s", path, code, body)
		}
	}
	for _, path := range []string{"/wp/v2/posts", "/wp/v2/pages", "/wp/v2/categories", "/wp/v2/tags", "/wp/v2/media", "/wp/v2/comments", "/wp/v2/search"} {
		for _, q := range []string{"%FF", "caf%E9"} {
			code, body, _ := get(t, anon, base+"/wp-json"+path+"?search="+q)
			if code != http.StatusOK || leaks(body) {
				t.Errorf("REST %s?search=%s: %d %.200s", path, q, code, body)
			}
		}
	}
	// Users and the admin's comment search go through the same store queries.
	for _, path := range []string{"/wp-json/wp/v2/users?search=%FF", "/wp-json/wp/v2/comments?search=%FF&status=all", "/wp-admin/edit-comments?s=%FF", "/wp-admin/users?s=%FF", "/wp-admin/upload?mode=list&s=%FF", "/wp-admin/edit-tags?taxonomy=category&s=%FF"} {
		req, _ := http.NewRequest(http.MethodGet, base+path, nil)
		req.Header.Set("X-WP-Nonce", nonce)
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || leaks(string(body)) {
			t.Errorf("admin %s: %d %.200s", path, resp.StatusCode, body)
		}
	}

	// A visitor's comment in Latin-1 with a NUL and a broken User-Agent is saved, as valid text.
	hello, err := srv.store.PostBySlug(ctx, "post", "hello-world")
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{"comment_post_ID": {itoa64(hello.ID)}, "comment": {"Caf\xe9 au lait\x00 please"}, "author": {"J\xf6rg"}, "email": {"jorg@example.test"}}
	req, _ := http.NewRequest(http.MethodPost, base+"/wp-comments-post", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "Bot/1.0 \xff")
	resp, err := anon.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("comment with invalid UTF-8: %d %.300s", resp.StatusCode, body)
	}
	var content, author, ua string
	if err := srv.store.Pool.QueryRow(ctx, `SELECT content, author_name, user_agent FROM comments WHERE post_id = $1 AND author_email = 'jorg@example.test'`, hello.ID).Scan(&content, &author, &ua); err != nil {
		t.Fatal(err)
	}
	if content != "Caf\uFFFD au lait\uFFFD please" || author != "J\uFFFDrg" || !utf8.ValidString(ua) {
		t.Errorf("stored comment: %q by %q, UA %q", content, author, ua)
	}

	// The same through REST with a form body (a JSON body is already valid: encoding/json replaces).
	req, _ = http.NewRequest(http.MethodPost, base+"/wp-json/wp/v2/comments", strings.NewReader(url.Values{"post": {itoa64(hello.ID)}, "content": {"Na\xefve REST comment"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-WP-Nonce", nonce)
	resp, err = c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated || leaks(string(body)) {
		t.Errorf("REST comment with invalid UTF-8: %d %.300s", resp.StatusCode, body)
	}
}
