package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// Unit: what counts as trust.
func TestWBKitTrustedPrune(t *testing.T) {
	kits := map[string]*wbKit{"a": {Slug: "a", Install: "i1", Code: "h1"}, "b": {Slug: "b", Install: "i2", Code: "h2"}, "c": {Slug: "c", Install: "i3", Code: "h3new"},
		"legacy": {Slug: "legacy"}}
	h := strings.Repeat("ab", 32)
	m := map[string]*wbKitTrustEntry{"a": {Install: "i1", Code: "h1", Inline: []string{h}}, "b": {Install: "old", Code: "h2"}, "c": {Install: "i3", Code: "h3"},
		"legacy": {Install: "", Code: ""}, "gone": {Install: "i9", Code: "h9"}}
	if got := wbKitTrusted(kits, m); !reflect.DeepEqual(got, []string{"a"}) {
		t.Errorf("trusted = %v", got)
	}
	if got := wbKitTrustCode(kits, m); !reflect.DeepEqual(got, map[string][]string{"a": {h}}) {
		t.Errorf("code = %v", got)
	}
	if !wbKitTrustPrune(kits, m) || len(m) != 1 || m["a"] == nil {
		t.Errorf("pruned = %v", m)
	}
	if wbKitTrustPrune(kits, m) {
		t.Error("second prune changed something")
	}
	// Old format (a bare install id), bad slugs, entries without a code hash: not trust.
	got := wbKitTrustDecode([]byte(`{"a":"i1","B":{"i":"x","h":"y"},"c":3,"d":{"i":"x"},"e":{"i":"ok","h":"hh","c":["` + h + `","zz",3]}}`))
	if len(got) != 1 || got["e"] == nil || got["e"].Install != "ok" || !reflect.DeepEqual(got["e"].Inline, []string{h}) {
		t.Errorf("decode = %v", got)
	}
	if got := wbKitTrustDecode([]byte(`[1]`)); len(got) != 0 {
		t.Errorf("decode array = %v", got)
	}
	if a, b := wbNewKitInstall(), wbNewKitInstall(); a == b || len(a) != 32 {
		t.Errorf("install ids %q %q", a, b)
	}
	// Inline hashes: newest kept, once each, sorted.
	many := make([]string, 0, wbKitTrustCodeMax+5)
	for i := range wbKitTrustCodeMax + 5 {
		many = append(many, fmt.Sprintf("%064x", i))
	}
	if l := wbTrustHashes(many[:10], many[5:]); len(l) != wbKitTrustCodeMax || !slices.Contains(l, many[len(many)-1]) || slices.Contains(l, many[0]) {
		t.Errorf("hashes kept: %d", len(l))
	}
}

// The code hash covers the kit's executable files only: path and bytes.
func TestWBKitCodeHash(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	hash := func() string {
		files, err := wbKitFiles(dir)
		if err != nil {
			t.Fatal(err)
		}
		return wbKitCodeHash(dir, files)
	}
	write("css/site.css", ".a{}")
	write("js/app.js", "a()")
	h0 := hash()
	write("css/site.css", ".a{color:red}")
	write("img/x.png", "png")
	if hash() != h0 {
		t.Error("a stylesheet or image changed the code hash")
	}
	for _, step := range []func(){
		func() { write("js/app.js", "b()") },
		func() { write("js/mod.mjs", "export {}") },
		func() { write("w/x.wasm", "\x00asm") },
		func() { write("icons/a.svg", "<svg/>") },
		func() { _ = os.Rename(filepath.Join(dir, "js", "app.js"), filepath.Join(dir, "js", "app2.js")) },
	} {
		before := hash()
		step()
		if hash() == before {
			t.Error("an executable change kept the code hash")
		}
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
	boot := func() []string { l, _ := srv.wbKitTrustBoot(ctx, adminU, true); return l }
	bootCode := func() map[string][]string { _, m := srv.wbKitTrustBoot(ctx, adminU, true); return m }

	// Inline code approved with a kit: kept with the entry, merged, valid hashes only.
	h1, h2 := strings.Repeat("a", 64), strings.Repeat("b", 64)
	if code, v := put(admin, "tk", map[string]any{"trust": true, "code": []string{h1}}, nil); code != http.StatusOK {
		t.Fatalf("trust with code: %d %v", code, v)
	}
	if code, v := put(admin, "tk", map[string]any{"trust": true, "code": []string{h2, h1}}, nil); code != http.StatusOK ||
		!reflect.DeepEqual(wbrObj(v)["scriptCode"], map[string]any{"tk": []any{h1, h2}}) {
		t.Fatalf("trust with more code: %d %v", code, v)
	}
	for _, bad := range []any{[]string{"x"}, []string{strings.Repeat("A", 64)}, []any{1}} {
		if code, v := put(admin, "tk", map[string]any{"trust": true, "code": bad}, nil); code != http.StatusBadRequest {
			t.Errorf("code %v: %d %v", bad, code, v)
		}
	}
	if got := bootCode(); !reflect.DeepEqual(got, map[string][]string{"tk": {h1, h2}}) {
		t.Errorf("boot code: %v", got)
	}

	// A commit that changes only stylesheets keeps the trust; one that changes a script is asked
	// about again, by everyone (the committer too), with its inline code.
	install := srv.wbKitList()["tk"].Install
	codeHash := srv.wbKitList()["tk"].Code
	wbsKit(t, admin, "tk", map[string]string{"site.css": ".a{color:blue}", "app.js": "window.a=1"})
	if k := srv.wbKitList()["tk"]; k.Install != install || k.Code != codeHash || k.Version < 2 {
		t.Errorf("css commit: install %q → %q, code %q → %q (v%d)", install, k.Install, codeHash, k.Code, k.Version)
	}
	if got := boot(); !reflect.DeepEqual(got, []string{"tk", "tk2"}) {
		t.Errorf("after a css commit: %v", got)
	}
	wbsKit(t, admin, "tk", map[string]string{"site.css": ".a{color:blue}", "app.js": "window.a=2"})
	if k := srv.wbKitList()["tk"]; k.Install != install || k.Code == codeHash {
		t.Errorf("script commit: install %q → %q, code %q → %q", install, k.Install, codeHash, k.Code)
	}
	if got := boot(); !reflect.DeepEqual(got, []string{"tk2"}) {
		t.Errorf("after a script commit: %v", got)
	}
	if got := bootCode(); len(got) != 0 {
		t.Errorf("after a script commit, code: %v", got)
	}
	if m := wbtMeta(t, srv, adminID); strings.Contains(m, `"tk"`) {
		t.Errorf("changed kit not pruned: %s", m)
	}
	if code, v := put(admin, "tk", map[string]any{"trust": true}, nil); code != http.StatusOK || !reflect.DeepEqual(wbtList(v), []string{"tk", "tk2"}) {
		t.Fatalf("trust tk again: %d %v", code, v)
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
	// A stale entry (an install id that is not the kit's, the old bare-id format) is not trust and
	// is pruned at boot.
	if _, err := srv.store.Pool.Exec(ctx, `UPDATE users SET meta = meta || jsonb_build_object($2::text, jsonb_build_object('tk', jsonb_build_object('i', 'feedfeedfeedfeedfeedfeedfeedfeed', 'h', $3::text), 'tk2', meta->$2::text->'tk2', 'gone', jsonb_build_object('i', 'x', 'h', 'y')))
		WHERE id = $1`, adminID, wbKitTrustMeta, srv.wbKitList()["tk"].Code); err != nil {
		t.Fatal(err)
	}
	if got := boot(); !reflect.DeepEqual(got, []string{"tk2"}) {
		t.Errorf("stale entries: %v", got)
	}
	if m := wbtMeta(t, srv, adminID); strings.Contains(m, `"tk"`) || strings.Contains(m, "gone") || !strings.Contains(m, "tk2") {
		t.Errorf("not pruned: %s", m)
	}
	if _, err := srv.store.Pool.Exec(ctx, `UPDATE users SET meta = meta || jsonb_build_object($2::text, jsonb_build_object('tk', $3::text, 'tk2', meta->$2::text->'tk2'))
		WHERE id = $1`, adminID, wbKitTrustMeta, srv.wbKitList()["tk"].Install); err != nil {
		t.Fatal(err)
	}
	if got := boot(); !reflect.DeepEqual(got, []string{"tk2"}) {
		t.Errorf("old-format entry: %v", got)
	}

	// A kit stored before install ids and code hashes: trusting it gives it both, kept by commits
	// that do not change its scripts.
	kits := srv.wbKitList()
	kits["tk"].Install, kits["tk"].Code = "", ""
	if err := srv.settings.Set(ctx, map[string]any{wbKitsOption: kits}); err != nil {
		t.Fatal(err)
	}
	if code, v := put(admin, "tk", map[string]any{"trust": true}, nil); code != http.StatusOK || !reflect.DeepEqual(wbtList(v), []string{"tk", "tk2"}) {
		t.Fatalf("trust legacy kit: %d %v", code, v)
	}
	if k := srv.wbKitList()["tk"]; k.Install == "" || k.Code == "" {
		t.Errorf("legacy kit got install %q code %q", k.Install, k.Code)
	}
	// The code hash of a kit without one is computed on first use (the boot) too.
	kits = srv.wbKitList()
	want := kits["tk2"].Code
	kits["tk2"].Code = ""
	if err := srv.settings.Set(ctx, map[string]any{wbKitsOption: kits}); err != nil {
		t.Fatal(err)
	}
	if got := boot(); !reflect.DeepEqual(got, []string{"tk", "tk2"}) || srv.wbKitList()["tk2"].Code != want {
		t.Errorf("lazy code hash: %v %q (want %q)", got, srv.wbKitList()["tk2"].Code, want)
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
	if _, err := srv.store.Pool.Exec(context.Background(), `UPDATE users SET meta = meta || jsonb_build_object($2::text, jsonb_build_object('bk', jsonb_build_object('i', $3::text, 'h', $4::text))) WHERE id = $1`,
		authorID, wbKitTrustMeta, srv.wbKitList()["bk"].Install, srv.wbKitList()["bk"].Code); err != nil {
		t.Fatal(err)
	}
	if b := open(author.c, own.ID); b["canRunScripts"] != false || !reflect.DeepEqual(b["scriptTrust"], []any{}) {
		t.Errorf("author boot: %v %v", b["canRunScripts"], b["scriptTrust"])
	}
}

var wbtNonceRe = regexp.MustCompile(`'nonce-([A-Za-z0-9+/=_-]+)'`)

// The editor page's policy (csp_mode enforce): the element editor's frames never run a design's
// scripts under it (Live and Preview are served frames with their own policy), so a code page gets
// only what its static frames draw — the design's stylesheets and fonts — never its script or
// connect origins, even in Safe mode. A mixed page (code and builder sections: its Preview runs the
// scripts in srcdoc frames that inherit the editor's policy) gets what the published page allows.
// A user without unfiltered_html gets none of the design's origins.
func TestWBEditorCodeCSP(t *testing.T) {
	srv, base, admin := wbrStart(t)
	ctx := context.Background()
	if err := srv.settings.Set(ctx, map[string]any{"csp_mode": cspModeEnforce}); err != nil {
		t.Fatal(err)
	}
	wbsKit(t, admin, "ck", map[string]string{"site.css": `@import "https://fonts.kit.example/a.css";.a{color:red}`, "app.js": `fetch("https://api.kit.example/x")`})
	html := `<section><canvas id="c"></canvas><script src="https://cdn.body.example/three.module.js" type="module"></script>` +
		`<iframe src="https://maps.frame.example/embed"></iframe><link rel="stylesheet" href="https://css.body.example/x.css"></section>`
	head := `<link rel="stylesheet" href="/wp-content/uploads/web-builder/kits/ck/site.css"><script type="importmap">{"imports":{"three":"https://cdn.head.example/three.js"}}</script>` +
		`<script src="https://cdn.head.example/three.js"></script>`
	page := wbsCodePage(t, srv, admin, "CSP page", "ck", html, head)

	policy := func(c *http.Client, id int64) map[string]string {
		t.Helper()
		code, body, h := wbOpen(t, c, base, id)
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
		return dirs
	}
	has := func(dirs map[string]string, dir, src string) bool {
		return strings.Contains(" "+dirs[dir]+" ", " "+src+" ")
	}

	dirs := policy(admin.c, page)
	for _, dir := range []string{"script-src", "connect-src"} {
		for _, o := range []string{"https://cdn.body.example", "https://cdn.head.example", "https://api.kit.example"} {
			if has(dirs, dir, o) {
				t.Errorf("code page: editor %s allows %s: %q", dir, o, dirs[dir])
			}
		}
	}
	if has(dirs, "frame-src", "https://maps.frame.example") {
		t.Errorf("code page: frame-src %q", dirs["frame-src"])
	}
	for dir, want := range map[string][]string{
		"style-src": {"https://css.body.example", "https://fonts.kit.example"},
		"img-src":   {"blob:"},
		"media-src": {"blob:"},
	} {
		for _, w := range want {
			if !has(dirs, dir, w) {
				t.Errorf("code page: editor %s lacks %s: %q", dir, w, dirs[dir])
			}
		}
	}

	// A mixed page: the published page's origins for the design.
	mixed := wbrPost(t, srv, "page", "CSP mixed", "draft", 1)
	style := map[string]any{"palette": "light", "align": "left", "padding": 0, "titleSize": 40, "hideOn": []any{}}
	doc := map[string]any{"site": map[string]any{"brand": "#2271b1", "fontPair": "modern", "corners": "soft"}, "sections": []any{
		map[string]any{"id": "b1", "kind": "hero", "name": "Hero", "hidden": false, "items": []any{}, "style": style, "content": map[string]any{"title": "Hi"}},
		map[string]any{"id": "c1", "kind": "code", "name": "Code", "hidden": false, "items": []any{}, "style": style, "content": map[string]any{"html": html, "head": head, "kit": "ck"}},
	}}
	body := wbrPublishBody(doc, map[string]any{}, 0, "publish", map[string]any{"title": "CSP mixed", "slug": "csp-mixed", "indexable": true})
	body["serverRender"] = true
	if code, v, _ := admin.do(http.MethodPost, wbrDocPath(mixed.ID, "/publish"), body, nil); code != http.StatusOK {
		t.Fatalf("publish mixed: %d %v", code, v)
	}
	dirs = policy(admin.c, mixed.ID)
	for dir, want := range map[string][]string{
		"script-src":  {"https://cdn.body.example", "https://cdn.head.example", "https://api.kit.example"},
		"connect-src": {"https://cdn.body.example", "https://api.kit.example"},
		"img-src":     {"blob:"},
	} {
		for _, w := range want {
			if !has(dirs, dir, w) {
				t.Errorf("mixed page: editor %s lacks %s: %q", dir, w, dirs[dir])
			}
		}
	}
	resp, err := http.Get(base + "/csp-mixed/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	pub := cspDirectives(resp.Header.Get("Content-Security-Policy"))
	for _, dir := range []string{"script-src", "connect-src", "style-src", "font-src"} {
		for _, src := range strings.Fields(pub[dir]) {
			if strings.HasPrefix(src, "https://") && !has(dirs, dir, src) && !has(dirs, dir, "https:") {
				t.Errorf("mixed page: published %s allows %s, the editor does not: %q", dir, src, dirs[dir])
			}
		}
	}

	// An editor without unfiltered_html opening the same page: none of the design's origins.
	limited, limitedID := wbsLogin(t, srv, base, "csplimited", "editor")
	if _, err := srv.store.Pool.Exec(ctx, `INSERT INTO user_capabilities (user_id, capability, effect) VALUES ($1, 'unfiltered_html', 'deny')`, limitedID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{page, mixed.ID} {
		code, _, h := wbOpen(t, limited.c, base, id)
		if code != http.StatusOK {
			t.Fatalf("limited editor: %d", code)
		}
		for _, o := range []string{"cdn.body.example", "cdn.head.example", "api.kit.example", "css.body.example"} {
			if strings.Contains(h.Get("Content-Security-Policy"), o) {
				t.Errorf("limited editor's policy names %s", o)
			}
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
