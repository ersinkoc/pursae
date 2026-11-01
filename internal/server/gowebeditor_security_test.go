package server

import (
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"wpgo/internal/store"
)

// Regression tests for the Pursae Web Builder security review.

// gweSecSite is a builder site with the administrator logged in on admin and a helper to log in others.
type gweSecSite struct {
	t     *testing.T
	ts    *httptest.Server
	srv   *Server
	admin *http.Client
}

func gweSecStart(t *testing.T) *gweSecSite {
	t.Helper()
	ts, c, srv := newTestServer(t, true)
	installAndLogin(t, ts, c)
	return &gweSecSite{t: t, ts: ts, srv: srv, admin: c}
}

func (s *gweSecSite) login(login, role string) (*http.Client, int64) {
	s.t.Helper()
	id := asCreateUser(s.t, s.srv, login, role, asStrongPass)
	c := asNewClient()
	if code, _, _ := asLogin(s.t, c, s.ts.URL, "/wp-login", login, asStrongPass); code != http.StatusFound {
		s.t.Fatalf("login %s: %d", login, code)
	}
	return c, id
}

// editorAjax opens the builder for the post and returns an elementor_ajax caller with its nonce.
func (s *gweSecSite) editorAjax(c *http.Client, postID int64) func(actions string) map[string]any {
	s.t.Helper()
	code, body, _ := get(s.t, c, s.ts.URL+"/wp-admin/post?post="+itoa64(postID)+"&action=web-builder")
	if code != http.StatusOK {
		s.t.Fatalf("editor: %d", code)
	}
	m := regexp.MustCompile(`"ajax":\{"nonce":"([^"]+)"`).FindStringSubmatch(body)
	if m == nil {
		s.t.Fatal("no elementor_ajax nonce")
	}
	return func(actions string) map[string]any {
		s.t.Helper()
		resp, err := c.PostForm(s.ts.URL+"/wp-admin/admin-ajax", url.Values{"action": {"elementor_ajax"}, "_nonce": {m[1]},
			"editor_post_id": {itoa64(postID)}, "initial_document_id": {itoa64(postID)}, "actions": {actions}})
		if err != nil {
			s.t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		var out map[string]any
		_ = json.Unmarshal(b, &out)
		return out
	}
}

const gweScriptTree = `[{"id":"a1b2c3d","elType":"container","isInner":false,"settings":{"z_last":"1","a_first":"2"},"elements":[` +
	`{"id":"b1b2c3d","elType":"widget","widgetType":"html","isInner":false,"settings":{"html":"<p>Hi</p><script>steal()</script><img src=x onerror=steal()>"},"elements":[]},` +
	`{"id":"c1b2c3d","elType":"widget","widgetType":"heading","isInner":false,"settings":{"title":"A & B","link":{"url":"https://example.com/?a=1&b=2"}},"elements":[]}]}]`

// A contributor or author (no unfiltered_html) must not store script in a builder document that runs
// for the administrator previewing or publishing it (Elementor's Document::save() runs wp_kses_post).
func TestGWEBuilderSaveFiltersScriptWithoutUnfilteredHTML(t *testing.T) {
	s := gweSecStart(t)
	ctx := context.Background()
	contrib, cid := s.login("contrib", "contributor")
	draft := &store.Post{Type: "post", Title: "Draft", Status: "draft", AuthorID: cid, Meta: map[string]any{}}
	if err := s.srv.store.InsertPost(ctx, draft); err != nil {
		t.Fatal(err)
	}
	ajax := s.editorAjax(contrib, draft.ID)
	ajax(`{"s":{"action":"save_builder","data":{"status":"pending","elements":` + gweScriptTree + `,"settings":{"post_title":"Draft","custom_css":"<script>x()</script>"}}}}`)
	ajax(`{"p":{"action":"save_page_settings","data":{"id":` + itoa64(draft.ID) + `,"data":{"evil":"<script>y()</script>"}}}}`)
	ajax(`{"t":{"action":"save_template","data":{"type":"section","title":"Mine","source":"local","content":` + strconvQuote(gweScriptTree) + `}}}`)

	p, err := s.srv.store.GetPost(ctx, draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := p.Meta["_elementor_data"].(string)
	settings, _ := json.Marshal(p.Meta["_elementor_page_settings"])
	if data == "" || strings.Contains(data, "<script") || strings.Contains(data, "onerror") || strings.Contains(string(settings), "<script") {
		t.Fatalf("script stored by a contributor:\n%s\n%s", data, settings)
	}
	if !strings.Contains(data, "<p>Hi</p>") || !strings.Contains(data, `"title":"A & B"`) || !strings.Contains(data, `https://example.com/?a=1&b=2`) ||
		strings.Index(data, "z_last") > strings.Index(data, "a_first") {
		t.Errorf("filtering changed more than the markup:\n%s", data)
	}
	var tplData, tplStatus string
	if err := s.srv.store.Pool.QueryRow(ctx, `SELECT meta->>'_elementor_data', status FROM posts WHERE type = $1 AND author_id = $2`, gweLibrary, cid).Scan(&tplData, &tplStatus); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(tplData, "<script") || tplStatus != "pending" {
		t.Errorf("contributor's template: status %q\n%s", tplStatus, tplData)
	}

	// An administrator (unfiltered_html) keeps the HTML widget's script.
	page := &store.Post{Type: "page", Title: "Admin page", Status: "draft", AuthorID: 1, Meta: map[string]any{}}
	if err := s.srv.store.InsertPost(ctx, page); err != nil {
		t.Fatal(err)
	}
	s.editorAjax(s.admin, page.ID)(`{"s":{"action":"save_builder","data":{"status":"draft","elements":` + gweScriptTree + `,"settings":{}}}}`)
	if p, _ := s.srv.store.GetPost(ctx, page.ID); !strings.Contains(p.Meta["_elementor_data"].(string), "<script>steal()</script>") {
		t.Errorf("administrator's HTML widget filtered: %v", p.Meta["_elementor_data"])
	}
}

func TestGWEKsesJSONUnit(t *testing.T) {
	in := `{"b":[1,2.50,true,null,"<b>x</b><script>y</script>"],"a":{"k":"plain & <i>ok</i>","z":[]},"e":{}}`
	out, err := gweKsesJSON([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"b":[1,2.50,true,null,"<b>x</b>"],"a":{"k":"plain &amp; <i>ok</i>","z":[]},"e":{}}`
	if string(out) != want {
		t.Errorf("got  %s\nwant %s", out, want)
	}
	if !json.Valid(out) {
		t.Error("invalid JSON")
	}
}

// Role Manager keeps excluded roles out of the editor; its AJAX (save_builder) must refuse them too.
func TestGWERoleManagerExcludedRoleCannotSave(t *testing.T) {
	s := gweSecStart(t)
	ctx := context.Background()
	author, aid := s.login("writer", "author")
	p := &store.Post{Type: "post", Title: "Mine", Status: "draft", AuthorID: aid, Meta: map[string]any{}}
	if err := s.srv.store.InsertPost(ctx, p); err != nil {
		t.Fatal(err)
	}
	ajax := s.editorAjax(author, p.ID) // the nonce, fetched before the role was excluded
	if err := s.srv.settings.Set(ctx, map[string]any{"gwe_exclude_user_roles": []any{"author"}}); err != nil {
		t.Fatal(err)
	}
	out := ajax(`{"s":{"action":"save_builder","data":{"status":"draft","elements":[],"settings":{"post_title":"Changed"}}}}`)
	if out["success"] != false {
		t.Errorf("excluded role saved through elementor_ajax: %v", out)
	}
	if got, _ := s.srv.store.GetPost(ctx, p.ID); got.Title != "Mine" {
		t.Errorf("title changed to %q", got.Title)
	}
}

// Visitors' form submissions: the referrer is stored only as a site URL (a javascript: link in the
// admin's submission view ran script), files are written only for submissions that pass the spam
// checks, and only administrators may read or trash submissions over REST.
func TestGWEFormSubmissionsHardening(t *testing.T) {
	s := gweSecStart(t)
	ctx := context.Background()
	base := s.ts.URL
	page := &store.Post{Type: "page", Title: "Contact", Status: "publish", AuthorID: 1, Meta: map[string]any{}}
	if err := s.srv.store.InsertPost(ctx, page); err != nil {
		t.Fatal(err)
	}
	form := `[{"id":"c0c0c01","elType":"container","isInner":false,"settings":{},"elements":[` +
		`{"id":"f0f0f01","elType":"widget","widgetType":"form","settings":{"form_name":"Contact","submit_actions":["save-to-database"],"form_fields":[` +
		`{"_id":"a1","custom_id":"name","field_type":"text","field_label":"Name"},` +
		`{"_id":"a2","custom_id":"trap","field_type":"honeypot","field_label":"Trap"},` +
		`{"_id":"a3","custom_id":"cv","field_type":"upload","field_label":"CV","file_types":"txt"}]},"elements":[]}]}]`
	s.editorAjax(s.admin, page.ID)(`{"s":{"action":"save_builder","data":{"status":"publish","elements":` + form + `,"settings":{"post_title":"Contact"}}}}`)

	send := func(fields map[string]string, file bool) string {
		t.Helper()
		var body strings.Builder
		mw := multipart.NewWriter(&body)
		for k, v := range map[string]string{"action": "elementor_pro_forms_send_form", "post_id": itoa64(page.ID), "form_id": "f0f0f01"} {
			_ = mw.WriteField(k, v)
		}
		for k, v := range fields {
			_ = mw.WriteField(k, v)
		}
		if file {
			w, _ := mw.CreateFormFile("form_fields[cv]", "cv.txt")
			_, _ = w.Write([]byte("hello"))
		}
		_ = mw.Close()
		resp, err := http.Post(base+"/wp-admin/admin-ajax", mw.FormDataContentType(), strings.NewReader(body.String()))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	if out := send(map[string]string{"form_fields[name]": "Eve", "referrer": "javascript:fetch('/wp-admin/user-new')", "referer_title": "Contact"}, false); !strings.Contains(out, `"success":true`) {
		t.Fatalf("submission: %s", out)
	}
	var referer string
	if err := s.srv.store.Pool.QueryRow(ctx, `SELECT referer FROM gwe_form_submissions ORDER BY id DESC LIMIT 1`).Scan(&referer); err != nil || referer != "" {
		t.Errorf("stored referrer %q (%v)", referer, err)
	}
	send(map[string]string{"form_fields[name]": "Ann", "referrer": base + "/contact/"}, false)
	if err := s.srv.store.Pool.QueryRow(ctx, `SELECT referer FROM gwe_form_submissions ORDER BY id DESC LIMIT 1`).Scan(&referer); err != nil || referer != base+"/contact/" {
		t.Errorf("site referrer dropped: %q (%v)", referer, err)
	}

	formsDir := filepath.Join(s.srv.cfg.UploadsDir, "elementor", "forms")
	if out := send(map[string]string{"form_fields[name]": "Bot", "form_fields[trap]": "filled"}, true); strings.Contains(out, `"success":true`) {
		t.Fatalf("honeypot submission accepted: %s", out)
	}
	if files, _ := filepath.Glob(filepath.Join(formsDir, "*")); len(files) != 0 {
		t.Errorf("a rejected (honeypot) submission stored files: %v", files)
	}
	if out := send(map[string]string{"form_fields[name]": "Cat"}, true); !strings.Contains(out, `"success":true`) {
		t.Errorf("submission with a file: %s", out)
	}
	if files, _ := filepath.Glob(filepath.Join(formsDir, "*")); len(files) == 0 {
		t.Error("an accepted submission's file was not stored")
	}

	_, contribREST := secREST(t, s, "contrib", "contributor")
	if code, _, _, _ := contribREST.do("GET", "/elementor-pro/v1/forms/submissions", nil); code != http.StatusForbidden {
		t.Errorf("contributor reads submissions: %d", code)
	}
	if code, _, _, _ := contribREST.do("DELETE", "/elementor-pro/v1/forms/submissions/1", nil); code != http.StatusForbidden {
		t.Errorf("contributor trashes a submission: %d", code)
	}
	adminREST := newRESTClient(t, s.admin, base)
	if code, _, _, _ := adminREST.do("GET", "/elementor-pro/v1/forms/submissions", nil); code != http.StatusOK {
		t.Errorf("administrator reads submissions: %d", code)
	}
}

func secREST(t *testing.T, s *gweSecSite, login, role string) (*http.Client, *restClient) {
	t.Helper()
	c, _ := s.login(login, role)
	return c, newRESTClient(t, c, s.ts.URL)
}

// A Template widget can name any post id; a private page must not render for visitors who cannot read it.
func TestGWETemplateWidgetHidesPrivatePosts(t *testing.T) {
	s := gweSecStart(t)
	ctx := context.Background()
	secret := &store.Post{Type: "page", Title: "Board notes", Slug: "board-notes", Status: "private", AuthorID: 1, Meta: map[string]any{}}
	public := &store.Post{Type: "page", Title: "Embed", Slug: "embedding-page", Status: "publish", AuthorID: 1, Meta: map[string]any{}}
	for _, p := range []*store.Post{secret, public} {
		if err := s.srv.store.InsertPost(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	s.editorAjax(s.admin, secret.ID)(`{"s":{"action":"save_builder","data":{"status":"private","elements":[{"id":"a1a1a1a","elType":"container","isInner":false,"settings":{},"elements":[` +
		`{"id":"b1b1b1b","elType":"widget","widgetType":"heading","isInner":false,"settings":{"title":"TOPSECRET-PLAN"},"elements":[]}]}],"settings":{}}}}`)
	s.editorAjax(s.admin, public.ID)(`{"s":{"action":"save_builder","data":{"status":"publish","elements":[{"id":"c1c1c1c","elType":"container","isInner":false,"settings":{},"elements":[` +
		`{"id":"d1d1d1d","elType":"widget","widgetType":"template","isInner":false,"settings":{"template_id":"` + itoa64(secret.ID) + `"},"elements":[]}]}],"settings":{}}}}`)
	if code, body, _ := getPage(t, asNewClient(), s.ts.URL+"/?page_id="+itoa64(public.ID)); code != http.StatusOK || strings.Contains(body, "TOPSECRET-PLAN") {
		t.Errorf("private page rendered for a visitor through a Template widget (%d)", code)
	}
	if _, body, _ := getPage(t, s.admin, s.ts.URL+"/?page_id="+itoa64(public.ID)); !strings.Contains(body, "TOPSECRET-PLAN") {
		t.Error("private page not rendered for the administrator who can read it")
	}
}

// Form webhooks connect to public addresses only, checked on the address actually dialled (DNS
// rebinding: a host that resolves publicly for the early check and privately a moment later).
func TestGWEWebhookClientRefusesPrivateAddresses(t *testing.T) {
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit = true }))
	defer srv.Close()
	resp, err := gweWebhookClient.Post(srv.URL, "text/plain", strings.NewReader("x"))
	if err == nil {
		resp.Body.Close()
	}
	if err == nil || hit {
		t.Fatal("webhook client connected to a loopback address")
	}
	if err := gweSafeURL("http://100.64.0.1/"); err == nil {
		t.Error("carrier-grade NAT address accepted")
	}
}
