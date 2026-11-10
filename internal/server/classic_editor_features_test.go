package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"wpgo/internal/store"
)

func TestPostTrackbackSendsForm(t *testing.T) {
	var got url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		got = r.PostForm
		if r.Method != http.MethodPost || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
			t.Errorf("bad request %s %s", r.Method, r.Header.Get("Content-Type"))
		}
	}))
	defer srv.Close()
	allowPrivateFeedHosts = true
	defer func() { allowPrivateFeedHosts = false }()
	if err := postTrackback(context.Background(), srv.URL, "Hello", "http://me.test/hello", "Me", "An excerpt"); err != nil {
		t.Fatal(err)
	}
	if got.Get("title") != "Hello" || got.Get("url") != "http://me.test/hello" || got.Get("blog_name") != "Me" || got.Get("excerpt") != "An excerpt" {
		t.Errorf("form = %v", got)
	}
	allowPrivateFeedHosts = false
	if err := postTrackback(context.Background(), srv.URL, "x", "y", "z", ""); err == nil {
		t.Error("a trackback to a private address was sent")
	}
	if err := postTrackback(context.Background(), "ftp://example.com/", "x", "y", "z", ""); err == nil {
		t.Error("ftp trackback accepted")
	}
}

func ceHeartbeat(t *testing.T, c *http.Client, base string, form url.Values) map[string]any {
	t.Helper()
	resp, err := c.PostForm(base+"/wp-admin/admin-ajax", form)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("heartbeat reply %q: %v", b, err)
	}
	return out
}

func TestClassicEditorAutosaveLockAndSwitch(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	ctx := context.Background()
	if err := srv.settings.Set(ctx, map[string]any{"native_classic_editor": true, "classic-editor-replace": "classic", "classic-editor-allow-users": "allow"}); err != nil {
		t.Fatal(err)
	}
	p := &store.Post{Type: "post", Title: "Stored", Slug: "stored", Content: "stored body", Status: "draft", AuthorID: 1}
	if err := srv.store.InsertPost(ctx, p); err != nil {
		t.Fatal(err)
	}
	editURL := base + "/wp-admin/post?post=" + strconv.FormatInt(p.ID, 10) + "&action=edit"

	// Opening the classic editor takes the lock and prints the dialog and the active lock.
	_, body, _ := get(t, c, editURL)
	if !strings.Contains(body, `id="active_post_lock"`) || !strings.Contains(body, `id="post-lock-dialog" class="notification-dialog-wrap hidden"`) {
		t.Fatalf("no lock markup in the classic editor")
	}
	nonce := formNonce(t, body)
	if got, _ := srv.store.GetPost(ctx, p.ID); !strings.HasSuffix(got.Meta["_edit_lock"].(string), ":1") {
		t.Errorf("_edit_lock = %v", got.Meta["_edit_lock"])
	}

	// Heartbeat autosave of an own draft writes the post and answers like wp_autosave().
	out := ceHeartbeat(t, c, base, url.Values{"action": {"heartbeat"},
		"data[wp_autosave][post_id]": {strconv.FormatInt(p.ID, 10)}, "data[wp_autosave][post_title]": {"Autosaved title"},
		"data[wp_autosave][content]": {"autosaved body"}, "data[wp_autosave][_wpnonce]": {nonce}})
	as, _ := out["wp_autosave"].(map[string]any)
	if as["success"] != true || !strings.HasPrefix(as["message"].(string), "Draft saved at ") {
		t.Fatalf("wp_autosave = %v", out["wp_autosave"])
	}
	if got, _ := srv.store.GetPost(ctx, p.ID); got.Title != "Autosaved title" || got.Content != "autosaved body" {
		t.Errorf("draft not autosaved: %q %q", got.Title, got.Content)
	}
	// A bad nonce is refused and changes nothing.
	out = ceHeartbeat(t, c, base, url.Values{"action": {"heartbeat"},
		"data[wp_autosave][post_id]": {strconv.FormatInt(p.ID, 10)}, "data[wp_autosave][post_title]": {"Evil"}, "data[wp_autosave][_wpnonce]": {"bogus"}})
	if as, _ := out["wp_autosave"].(map[string]any); as["success"] != false {
		t.Errorf("bad nonce accepted: %v", out["wp_autosave"])
	}
	if got, _ := srv.store.GetPost(ctx, p.ID); got.Title != "Autosaved title" {
		t.Errorf("title changed by a bad nonce: %q", got.Title)
	}
	// A published post only gets an autosave revision.
	pub := &store.Post{Type: "post", Title: "Live", Slug: "live", Content: "live body", Status: "publish", AuthorID: 1}
	if err := srv.store.InsertPost(ctx, pub); err != nil {
		t.Fatal(err)
	}
	_, pbody, _ := get(t, c, base+"/wp-admin/post?post="+strconv.FormatInt(pub.ID, 10)+"&action=edit")
	out = ceHeartbeat(t, c, base, url.Values{"action": {"heartbeat"},
		"data[wp_autosave][post_id]": {strconv.FormatInt(pub.ID, 10)}, "data[wp_autosave][post_title]": {"Live"},
		"data[wp_autosave][content]": {"edited live body"}, "data[wp_autosave][_wpnonce]": {formNonce(t, pbody)}})
	if as, _ := out["wp_autosave"].(map[string]any); as["success"] != true {
		t.Fatalf("published autosave: %v", out)
	}
	if got, _ := srv.store.GetPost(ctx, pub.ID); got.Content != "live body" {
		t.Errorf("published content overwritten by an autosave: %q", got.Content)
	}

	// Another user's live lock: heartbeat reports it, the editor shows the dialog, nothing is stolen.
	other := asCreateUser(t, srv, "ed2", "editor", asStrongPass)
	if _, err := srv.store.Pool.Exec(ctx, `UPDATE users SET display_name = 'Edith' WHERE id = $1`, other); err != nil {
		t.Fatal(err)
	}
	live := strconv.FormatInt(time.Now().Unix(), 10) + ":" + strconv.FormatInt(other, 10)
	wbSetMeta(t, srv, p.ID, "_edit_lock", live)
	out = ceHeartbeat(t, c, base, url.Values{"action": {"heartbeat"}, "data[wp-refresh-post-lock][post_id]": {strconv.FormatInt(p.ID, 10)}})
	rl, _ := out["wp-refresh-post-lock"].(map[string]any)
	le, _ := rl["lock_error"].(map[string]any)
	if le == nil || le["text"] != "Edith is currently editing" {
		t.Fatalf("wp-refresh-post-lock = %v", out["wp-refresh-post-lock"])
	}
	_, body, _ = get(t, c, editURL)
	if strings.Contains(body, `notification-dialog-wrap hidden`) || !strings.Contains(body, "Edith is currently editing this post") {
		t.Errorf("no take-over dialog while another user holds the lock")
	}
	if got, _ := srv.store.GetPost(ctx, p.ID); got.Meta["_edit_lock"] != live {
		t.Errorf("a live lock was stolen: %v", got.Meta["_edit_lock"])
	}
	// Take over needs the lock nonce.
	_, _, _ = get(t, c, editURL+"&get-post-lock=1&_wpnonce=bogus")
	if got, _ := srv.store.GetPost(ctx, p.ID); got.Meta["_edit_lock"] != live {
		t.Errorf("take over without a nonce worked")
	}
	lockNonce := regexpFirst(t, body, `get-post-lock=1&amp;_wpnonce=([0-9a-f]+)`)
	_, _, _ = get(t, c, editURL+"&get-post-lock=1&_wpnonce="+lockNonce)
	if got, _ := srv.store.GetPost(ctx, p.ID); !strings.HasSuffix(got.Meta["_edit_lock"].(string), ":1") {
		t.Errorf("take over did not move the lock: %v", got.Meta["_edit_lock"])
	}
	// Leaving the editor drops the holder's lock.
	_, _ = c.PostForm(base+"/wp-admin/admin-ajax", url.Values{"action": {"wp-remove-post-lock"}, "post_ID": {strconv.FormatInt(p.ID, 10)}, "_wpnonce": {nonce}})
	if got, _ := srv.store.GetPost(ctx, p.ID); got.Meta["_edit_lock"] != nil {
		t.Errorf("lock not removed: %v", got.Meta["_edit_lock"])
	}

	// Switch links and settings follow the plugin.
	_, list, _ := get(t, c, base+"/wp-admin/edit")
	if !strings.Contains(list, "Edit (classic editor)") {
		t.Error("no classic switch link while the plugin is on")
	}
	_, opts, _ := get(t, c, base+"/wp-admin/options-writing")
	if !strings.Contains(opts, "classic-editor-allow-users") {
		t.Error("Writing settings lack the editor rows while the plugin is on")
	}
	if err := srv.settings.Set(ctx, map[string]any{"native_classic_editor": false}); err != nil {
		t.Fatal(err)
	}
	_, list, _ = get(t, c, base+"/wp-admin/edit")
	if strings.Contains(list, "Edit (classic editor)") {
		t.Error("classic switch link shown while the plugin is off")
	}
	_, opts, _ = get(t, c, base+"/wp-admin/options-writing")
	if strings.Contains(opts, "classic-editor-allow-users") {
		t.Error("editor rows shown on Settings > Writing while the plugin is off")
	}
	if srv.classicSwitchAllowed() || srv.classicSwitchURL(p) != "" {
		t.Error("switch allowed with the plugin off")
	}
}

func TestClassicEditorProfileDefaultEditor(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	ctx := context.Background()
	_ = srv.settings.Set(ctx, map[string]any{"native_classic_editor": true, "classic-editor-allow-users": "disallow"})
	_, body, _ := get(t, c, base+"/wp-admin/profile")
	if strings.Contains(body, "classic-editor-user-settings") {
		t.Fatal("Default Editor row shown while switching is off")
	}
	_ = srv.settings.Set(ctx, map[string]any{"classic-editor-allow-users": "allow", "classic-editor-replace": "block"})
	_, body, _ = get(t, c, base+"/wp-admin/profile")
	if !strings.Contains(body, `name="classic-editor-user-settings" id="classic-editor-user-block" value="block" checked="checked"`) {
		t.Fatal("Default Editor row missing or wrong default")
	}
	form := url.Values{"_wpnonce": {formNonce(t, body)}, "from": {"profile"}, "checkuser_id": {"1"}, "email": {"admin@example.test"},
		"nickname": {"admin"}, "display_name": {"admin"}, "classic-editor-user-settings": {"classic"}}
	if code, _, _ := post(t, c, base+"/wp-admin/profile", form); code != http.StatusFound {
		t.Fatalf("profile save: %d", code)
	}
	u, _ := srv.store.UserByID(ctx, 1)
	if u.Meta["classic-editor-settings"] != "classic" {
		t.Fatalf("meta = %v", u.Meta["classic-editor-settings"])
	}
	// The saved choice decides which editor a fresh post opens in.
	p := &store.Post{Type: "post", Title: "P", Slug: "p", Status: "draft", AuthorID: 1}
	if err := srv.store.InsertPost(ctx, p); err != nil {
		t.Fatal(err)
	}
	_, ed, _ := get(t, c, base+"/wp-admin/post?post="+strconv.FormatInt(p.ID, 10)+"&action=edit")
	if !strings.Contains(ed, `id="post_ID"`) {
		t.Error("the user's classic default did not open the classic editor")
	}
}

func TestDeliverTrackbacksMovesToPinged(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	ctx := context.Background()
	hits := make(chan url.Values, 4)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		hits <- r.PostForm
	}))
	defer remote.Close()
	allowPrivateFeedHosts = true
	defer func() { allowPrivateFeedHosts = false }()
	p := &store.Post{Type: "post", Title: "Pingy", Slug: "pingy", Content: "<p>Body text</p>", Status: "publish", AuthorID: 1}
	if err := srv.store.InsertPost(ctx, p); err != nil {
		t.Fatal(err)
	}
	wbSetMeta(t, srv, p.ID, "_to_ping", remote.URL+"/a\n"+remote.URL+"/b")
	srv.deliverTrackbacks(ctx, p.ID, "http://x.test/pingy")
	if len(hits) != 2 {
		t.Fatalf("%d pings sent, want 2", len(hits))
	}
	if h := <-hits; h.Get("title") != "Pingy" || h.Get("excerpt") != "Body text" {
		t.Errorf("ping = %v", h)
	}
	got, _ := srv.store.GetPost(ctx, p.ID)
	if got.Meta["_to_ping"] != nil || !strings.Contains(got.Meta["_pinged"].(string), "/a") || !strings.Contains(got.Meta["_pinged"].(string), "/b") {
		t.Errorf("meta after sending: to_ping=%v pinged=%v", got.Meta["_to_ping"], got.Meta["_pinged"])
	}
	// Nothing is sent again, and a draft is never pinged.
	wbSetMeta(t, srv, p.ID, "_to_ping", remote.URL+"/a")
	srv.deliverTrackbacks(ctx, p.ID, "http://x.test/pingy")
	if len(hits) != 1 {
		t.Errorf("an already pinged URL was pinged again (%d queued)", len(hits))
	}
}

func regexpFirst(t *testing.T, s, re string) string {
	t.Helper()
	m := regexp.MustCompile(re).FindStringSubmatch(s)
	if m == nil {
		t.Fatalf("no match for %s", re)
	}
	return m[1]
}
