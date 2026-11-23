package server

import (
	"context"
	"net/http"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// Unit: what counts as trust.
func TestWBKitTrustedPrune(t *testing.T) {
	kits := map[string]*wbKit{"a": {Slug: "a", Install: "i1"}, "b": {Slug: "b", Install: "i2"}, "legacy": {Slug: "legacy"}}
	m := map[string]string{"a": "i1", "b": "old", "legacy": "", "gone": "i9"}
	if got := wbKitTrusted(kits, m); !reflect.DeepEqual(got, []string{"a"}) {
		t.Errorf("trusted = %v", got)
	}
	if !wbKitTrustPrune(kits, m) || !reflect.DeepEqual(m, map[string]string{"a": "i1"}) {
		t.Errorf("pruned = %v", m)
	}
	if wbKitTrustPrune(kits, m) {
		t.Error("second prune changed something")
	}
	if got := wbKitTrustDecode([]byte(`{"a":"i1","B":"x","c":3,"d":"","e":"ok"}`)); !reflect.DeepEqual(got, map[string]string{"a": "i1", "e": "ok"}) {
		t.Errorf("decode = %v", got)
	}
	if got := wbKitTrustDecode([]byte(`[1]`)); len(got) != 0 {
		t.Errorf("decode array = %v", got)
	}
	if a, b := wbNewKitInstall(), wbNewKitInstall(); a == b || len(a) != 32 {
		t.Errorf("install ids %q %q", a, b)
	}
}

// wbtMeta is the raw trust meta of a user ("" without one).
func wbtMeta(t *testing.T, srv *Server, uid int64) string {
	t.Helper()
	var raw *string
	if err := srv.store.Pool.QueryRow(context.Background(), `SELECT (meta->'`+wbKitTrustMeta+`')::text FROM users WHERE id = $1`, uid).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw == nil {
		return ""
	}
	return *raw
}

func wbtList(v any) []string {
	out := []string{}
	l, _ := wbrObj(v)["scriptTrust"].([]any)
	for _, x := range l {
		s, _ := x.(string)
		out = append(out, s)
	}
	return out
}

func TestWBKitTrustREST(t *testing.T) {
	srv, base, admin := wbrStart(t)
	ctx := context.Background()
	wbsKit(t, admin, "tk", map[string]string{"site.css": ".a{color:red}", "app.js": "window.a=1"})
	wbsKit(t, admin, "tk2", map[string]string{"site.css": ".b{color:red}"})
	const route = "/web-builder/v2/kits/tk/trust"
	put := func(c *wbrClient, slug string, body any, hdr map[string]string) (int, any) {
		code, v, _ := c.do(http.MethodPut, "/web-builder/v2/kits/"+slug+"/trust", body, hdr)
		return code, v
	}
	adminID := int64(1)

	// Grant, idempotent; both kits.
	for range 2 {
		if code, v := put(admin, "tk", map[string]any{"trust": true}, nil); code != http.StatusOK || wbrObj(v)["trust"] != true || !reflect.DeepEqual(wbtList(v), []string{"tk"}) {
			t.Fatalf("trust tk: %d %v", code, v)
		}
	}
	if code, v := put(admin, "tk2", map[string]any{"trust": true}, nil); code != http.StatusOK || !reflect.DeepEqual(wbtList(v), []string{"tk", "tk2"}) {
		t.Fatalf("trust tk2: %d %v", code, v)
	}
	before := wbtMeta(t, srv, adminID)
	if !strings.Contains(before, `"tk"`) || !strings.Contains(before, `"tk2"`) {
		t.Fatalf("meta = %s", before)
	}

	// CSRF and handshake: no nonce (cookie only) is a logged-out request; no X-WB-Api is refused.
	if code, v := put(admin, "tk", map[string]any{"trust": false}, map[string]string{"X-WP-Nonce": ""}); code != http.StatusUnauthorized {
		t.Errorf("without nonce: %d %v", code, v)
	}
	if code, v := put(admin, "tk", map[string]any{"trust": false}, map[string]string{"X-WP-Nonce": "0123456789"}); code != http.StatusForbidden && code != http.StatusUnauthorized {
		t.Errorf("bad nonce: %d %v", code, v)
	}
	if code, v := put(admin, "tk", map[string]any{"trust": false}, map[string]string{"X-WB-Api": ""}); code != http.StatusConflict {
		t.Errorf("without X-WB-Api: %d %v", code, v)
	}
	// Bad bodies, unknown and invalid slugs, other methods.
	for _, body := range []any{map[string]any{}, map[string]any{"trust": "yes"}, map[string]any{"trust": nil}, map[string]any{"trust": 1}, []byte(`[true]`)} {
		if code, v := put(admin, "tk", body, nil); code != http.StatusBadRequest {
			t.Errorf("body %v: %d %v", body, code, v)
		}
	}
	if code, v := put(admin, "nope", map[string]any{"trust": true}, nil); code != http.StatusNotFound || wbrCode(v) != "wb_kit_not_found" {
		t.Errorf("unknown kit: %d %v", code, v)
	}
	for _, slug := range []string{"Bad_Slug", "-x", "..%2Ftk", "tk%2F..%2Ftk2"} {
		if code, _ := put(admin, slug, map[string]any{"trust": true}, nil); code == http.StatusOK {
			t.Errorf("slug %q accepted", slug)
		}
	}
	if code, _, _ := admin.do(http.MethodGet, route, nil, nil); code == http.StatusOK {
		t.Errorf("GET trust: %d", code)
	}
	if got := wbtMeta(t, srv, adminID); got != before {
		t.Errorf("refused requests changed the meta: %s → %s", before, got)
	}

	// Who may: an editor (unfiltered_html on a single site) for themselves only; the same editor
	// with unfiltered_html denied, an author: no.
	editor, editorID := wbsLogin(t, srv, base, "trusteditor", "editor")
	if code, v := put(editor, "tk", map[string]any{"trust": true, "user": adminID, "userId": adminID}, nil); code != http.StatusOK || !reflect.DeepEqual(wbtList(v), []string{"tk"}) {
		t.Fatalf("editor trust: %d %v", code, v)
	}
	if code, v := put(editor, "tk2", map[string]any{"trust": false, "user": adminID}, nil); code != http.StatusOK || !reflect.DeepEqual(wbtList(v), []string{"tk"}) {
		t.Fatalf("editor untrust tk2: %d %v", code, v)
	}
	if got := wbtMeta(t, srv, adminID); got != before {
		t.Errorf("the editor changed the admin's trust: %s → %s", before, got)
	}
	if !strings.Contains(wbtMeta(t, srv, editorID), `"tk"`) {
		t.Errorf("editor meta = %s", wbtMeta(t, srv, editorID))
	}
	limited, limitedID := wbsLogin(t, srv, base, "trustlimited", "editor")
	if _, err := srv.store.Pool.Exec(ctx, `INSERT INTO user_capabilities (user_id, capability, effect) VALUES ($1, 'unfiltered_html', 'deny')`, limitedID); err != nil {
		t.Fatal(err)
	}
	author, authorID := wbsLogin(t, srv, base, "trustauthor", "author")
	for name, c := range map[string]*wbrClient{"editor without unfiltered_html": limited, "author": author} {
		if code, v := put(c, "tk", map[string]any{"trust": true}, nil); code != http.StatusForbidden {
			t.Errorf("%s: %d %v", name, code, v)
		}
	}
	for _, id := range []int64{limitedID, authorID} {
		if m := wbtMeta(t, srv, id); m != "" {
			t.Errorf("user %d meta = %s", id, m)
		}
	}
	if code, _ := put(anonWBR(t, base), "tk", map[string]any{"trust": true}, nil); code != http.StatusUnauthorized {
		t.Errorf("logged out: %d", code)
	}

	// Application Passwords: the content scope alone cannot, content + Appearance can (the
	// password's own user).
	contentPass, _ := mcpTestAgent(t, base, admin.c, "Trust content", []string{"content"})
	themesPass, _ := mcpTestAgent(t, base, admin.c, "Trust themes", []string{"content", "themes"})
	if code, out := wbsBasic(t, base, contentPass, http.MethodPut, route, map[string]any{"trust": false}); code != http.StatusForbidden {
		t.Errorf("content scope: %d %v", code, out)
	}
	if got := wbtMeta(t, srv, adminID); got != before {
		t.Errorf("content scope changed the trust: %s", got)
	}
	if code, out := wbsBasic(t, base, themesPass, http.MethodPut, "/web-builder/v2/kits/tk2/trust", map[string]any{"trust": false}); code != http.StatusOK {
		t.Errorf("themes scope: %d %v", code, out)
	}
	if code, v := put(admin, "tk2", map[string]any{"trust": true}, nil); code != http.StatusOK || len(wbtList(v)) != 2 {
		t.Fatalf("trust tk2 again: %d %v", code, v)
	}

	adminU, err := srv.store.UserByID(ctx, adminID)
	if err != nil {
		t.Fatal(err)
	}
	boot := func() []string { return srv.wbKitTrustBoot(ctx, adminU) }

	// A commit (a new version of the same installation) keeps the trust.
	install := srv.wbKitList()["tk"].Install
	wbsKit(t, admin, "tk", map[string]string{"site.css": ".a{color:blue}", "app.js": "window.a=2"})
	if k := srv.wbKitList()["tk"]; k.Install != install || k.Version < 2 {
		t.Errorf("commit changed the install: %q → %q (v%d)", install, k.Install, k.Version)
	}
	if got := boot(); !reflect.DeepEqual(got, []string{"tk", "tk2"}) {
		t.Errorf("after commit: %v", got)
	}
	// Deleting a kit removes it from every user; uploading it again is a new installation.
	if code, v, _ := admin.do(http.MethodDelete, "/web-builder/v2/kits/tk", nil, nil); code != http.StatusOK {
		t.Fatalf("delete: %d %v", code, v)
	}
	for _, id := range []int64{adminID, editorID} {
		if m := wbtMeta(t, srv, id); strings.Contains(m, `"tk"`) {
			t.Errorf("user %d still has tk after the delete: %s", id, m)
		}
	}
	if m := wbtMeta(t, srv, editorID); m != "" {
		t.Errorf("editor's emptied trust kept: %s", m)
	}
	wbsKit(t, admin, "tk", map[string]string{"site.css": ".a{color:green}"})
	if k := srv.wbKitList()["tk"]; k.Install == "" || k.Install == install {
		t.Errorf("re-installed kit install %q (was %q)", k.Install, install)
	}
	if got := boot(); !reflect.DeepEqual(got, []string{"tk2"}) {
		t.Errorf("after re-install: %v", got)
	}
	// A stale entry (an install id that is not the kit's) is not trust and is pruned at boot.
	if _, err := srv.store.Pool.Exec(ctx, `UPDATE users SET meta = meta || jsonb_build_object($2::text, jsonb_build_object('tk', 'feedfeedfeedfeedfeedfeedfeedfeed', 'tk2', meta->$2::text->>'tk2', 'gone', 'x'))
		WHERE id = $1`, adminID, wbKitTrustMeta); err != nil {
		t.Fatal(err)
	}
	if got := boot(); !reflect.DeepEqual(got, []string{"tk2"}) {
		t.Errorf("stale entries: %v", got)
	}
	if m := wbtMeta(t, srv, adminID); strings.Contains(m, `"tk"`) || strings.Contains(m, "gone") || !strings.Contains(m, "tk2") {
		t.Errorf("not pruned: %s", m)
	}

	// A kit stored before install ids: trusting it gives it one, kept by commits.
	kits := srv.wbKitList()
	kits["tk"].Install = ""
	if err := srv.settings.Set(ctx, map[string]any{wbKitsOption: kits}); err != nil {
		t.Fatal(err)
	}
	if got := boot(); !reflect.DeepEqual(got, []string{"tk2"}) {
		t.Errorf("legacy kit: %v", got)
	}
	if code, v := put(admin, "tk", map[string]any{"trust": true}, nil); code != http.StatusOK || !reflect.DeepEqual(wbtList(v), []string{"tk", "tk2"}) {
		t.Fatalf("trust legacy kit: %d %v", code, v)
	}
	if srv.wbKitList()["tk"].Install == "" {
		t.Error("legacy kit got no install id")
	}
	// Revoking.
	if code, v := put(admin, "tk", map[string]any{"trust": false}, nil); code != http.StatusOK || wbrObj(v)["trust"] != false || !reflect.DeepEqual(wbtList(v), []string{"tk2"}) {
		t.Errorf("revoke: %d %v", code, v)
	}
	if code, v := put(admin, "tk2", map[string]any{"trust": false}, nil); code != http.StatusOK || len(wbtList(v)) != 0 {
		t.Errorf("revoke tk2: %d %v", code, v)
	}
	if m := wbtMeta(t, srv, adminID); m != "" {
		t.Errorf("empty trust kept: %s", m)
	}
}

// anonWBR is a logged-out REST client.
func anonWBR(t *testing.T, base string) *wbrClient {
	return &wbrClient{t: t, c: anonClient(), base: base}
}

// The boot config: scriptTrust and canRunScripts.
func TestWBKitTrustBoot(t *testing.T) {
	srv, base, admin := wbrStart(t)
	wbsKit(t, admin, "bk", map[string]string{"site.css": ".a{color:red}"})
	page := wbsCodePage(t, srv, admin, "Boot page", "bk", `<p>hi</p>`, "")
	open := func(c *http.Client, id int64) map[string]any {
		code, body, _ := wbOpen(t, c, base, id)
		if code != http.StatusOK {
			t.Fatalf("editor: %d %.300s", code, body)
		}
		boot, _ := wbBootOf(t, body)
		return boot
	}
	b := open(admin.c, page)
	if b["canRunScripts"] != true || !reflect.DeepEqual(b["scriptTrust"], []any{}) {
		t.Errorf("admin boot before: %v %v", b["canRunScripts"], b["scriptTrust"])
	}
	if code, v, _ := admin.do(http.MethodPut, "/web-builder/v2/kits/bk/trust", map[string]any{"trust": true}, nil); code != http.StatusOK {
		t.Fatalf("trust: %d %v", code, v)
	}
	if b := open(admin.c, page); !reflect.DeepEqual(b["scriptTrust"], []any{"bk"}) {
		t.Errorf("admin boot after: %v", b["scriptTrust"])
	}
	// An author: no scripts, no trust (even with a stored value).
	author, authorID := wbsLogin(t, srv, base, "bootauthor", "author")
	own := wbrPost(t, srv, "post", "Author post", "draft", authorID)
	if _, err := srv.store.Pool.Exec(context.Background(), `UPDATE users SET meta = meta || jsonb_build_object($2::text, jsonb_build_object('bk', $3::text)) WHERE id = $1`,
		authorID, wbKitTrustMeta, srv.wbKitList()["bk"].Install); err != nil {
		t.Fatal(err)
	}
	if b := open(author.c, own.ID); b["canRunScripts"] != false || !reflect.DeepEqual(b["scriptTrust"], []any{}) {
		t.Errorf("author boot: %v %v", b["canRunScripts"], b["scriptTrust"])
	}
}

var wbtNonceRe = regexp.MustCompile(`'nonce-([A-Za-z0-9+/=_-]+)'`)

// The editor page's policy lets a design's scripts run in the frame (a srcdoc document inherits
// it) for a user who may run them, under csp_mode enforce: the response's nonce (which the editor
// stamps on the frame's scripts), the origins the published page allows for the same markup, blob:
// workers, images and media. A user without unfiltered_html gets none of the design's origins.
func TestWBEditorCodeCSP(t *testing.T) {
	srv, base, admin := wbrStart(t)
	ctx := context.Background()
	if err := srv.settings.Set(ctx, map[string]any{"csp_mode": cspModeEnforce}); err != nil {
		t.Fatal(err)
	}
	wbsKit(t, admin, "ck", map[string]string{"site.css": ".a{color:red}", "app.js": `fetch("https://api.kit.example/x")`})
	html := `<section><canvas id="c"></canvas><script src="https://cdn.body.example/three.module.js" type="module"></script>` +
		`<iframe src="https://maps.frame.example/embed"></iframe></section>`
	head := `<link rel="stylesheet" href="/wp-content/uploads/web-builder/kits/ck/site.css"><script type="importmap">{"imports":{"three":"https://cdn.head.example/three.js"}}</script>` +
		`<script src="https://cdn.head.example/three.js"></script>`
	page := wbsCodePage(t, srv, admin, "CSP page", "ck", html, head)

	// The published page's policy for the same markup.
	resp, err := http.Get(base + "/csp-page/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	published := resp.Header.Get("Content-Security-Policy")

	code, body, h := wbOpen(t, admin.c, base, page)
	if code != http.StatusOK {
		t.Fatalf("editor: %d", code)
	}
	csp := h.Get("Content-Security-Policy")
	dirs := cspDirectives(csp)
	m := wbtNonceRe.FindStringSubmatch(dirs["script-src"])
	if m == nil || !strings.Contains(dirs["script-src"], "'strict-dynamic'") {
		t.Fatalf("enforced nonce policy missing: %q", csp)
	}
	if !strings.Contains(body, `<script nonce="`+m[1]+`">window.__WEB_BUILDER__`) {
		t.Errorf("the boot script does not carry the policy's nonce %s", m[1])
	}
	for dir, want := range map[string][]string{
		"script-src":  {"https://cdn.body.example", "https://cdn.head.example", "https://api.kit.example"},
		"connect-src": {"'self'", "https://cdn.body.example", "https://api.kit.example"},
		"worker-src":  {"'self'", "blob:"},
		"img-src":     {"blob:"},
		"media-src":   {"blob:"},
		"frame-src":   {"https:"},
	} {
		for _, w := range want {
			if !strings.Contains(" "+dirs[dir]+" ", " "+w+" ") {
				t.Errorf("editor %s lacks %s: %q", dir, w, dirs[dir])
			}
		}
	}
	// Every origin the published page allows for the design is allowed in the editor.
	pub := cspDirectives(published)
	for _, dir := range []string{"script-src", "connect-src", "style-src", "font-src", "img-src", "media-src"} {
		for _, src := range strings.Fields(pub[dir]) {
			if strings.HasPrefix(src, "https://") && !strings.Contains(" "+dirs[dir]+" ", " "+src+" ") && !strings.Contains(" "+dirs[dir]+" ", " https: ") {
				t.Errorf("published %s allows %s, the editor does not: %q", dir, src, dirs[dir])
			}
		}
	}

	// An editor without unfiltered_html opening the same page: none of the design's origins.
	limited, limitedID := wbsLogin(t, srv, base, "csplimited", "editor")
	if _, err := srv.store.Pool.Exec(ctx, `INSERT INTO user_capabilities (user_id, capability, effect) VALUES ($1, 'unfiltered_html', 'deny')`, limitedID); err != nil {
		t.Fatal(err)
	}
	code, _, h = wbOpen(t, limited.c, base, page)
	if code != http.StatusOK {
		t.Fatalf("limited editor: %d", code)
	}
	for _, o := range []string{"cdn.body.example", "cdn.head.example", "api.kit.example"} {
		if strings.Contains(h.Get("Content-Security-Policy"), o) {
			t.Errorf("limited editor's policy names %s", o)
		}
	}
}

// cspDirectives splits a policy into its directives.
func cspDirectives(policy string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(policy, ";") {
		f := strings.Fields(part)
		if len(f) > 0 {
			out[strings.ToLower(f[0])] = strings.Join(f[1:], " ")
		}
	}
	return out
}
