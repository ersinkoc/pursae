package server

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

func TestGapCSPMerge(t *testing.T) {
	policy := "default-src 'self'; img-src 'self' data: https:; script-src 'self' 'unsafe-inline'; frame-src 'self'; object-src 'none'"
	got := cspMerge(policy, map[string][]string{
		"script-src": {"https://www.youtube.com", "'self'"},
		"frame-src":  {"https://www.youtube-nocookie.com"},
		"img-src":    {"https://i.ytimg.com"}, // covered by https:
		"child-src":  {"https://player.vimeo.com"},
	})
	want := "default-src 'self'; img-src 'self' data: https:; script-src 'self' 'unsafe-inline' https://www.youtube.com; " +
		"frame-src 'self' https://www.youtube-nocookie.com; object-src 'none'; child-src 'self' https://player.vimeo.com"
	if got != want {
		t.Errorf("cspMerge:\n got %s\nwant %s", got, want)
	}
	if cspMerge(policy, nil) != policy {
		t.Error("empty allow-list changed the policy")
	}
}

func TestGapEmbedScan(t *testing.T) {
	enc := func(v any) string {
		b, _ := json.Marshal(v)
		return strings.NewReplacer(`"`, "&quot;", "/", `\/`).Replace(string(b))
	}
	lb := base64.StdEncoding.EncodeToString([]byte(`{"type":"video","videoType":"vimeo","url":"https:\/\/player.vimeo.com\/video\/1"}`))
	for _, tc := range []struct {
		name, markup        string
		wantScript, wantFra []string
	}{
		{"youtube privacy", `<div data-settings="` + enc(map[string]any{"video_type": "youtube", "youtube_url": "https://www.youtube.com/watch?v=x", "yt_privacy": "yes"}) + `">`,
			[]string{cspYouTube}, []string{cspYouTubeNoCook}},
		{"youtube", `<div data-settings="` + enc(map[string]any{"video_type": "youtube", "youtube_url": "https://youtu.be/x"}) + `">`,
			[]string{cspYouTube}, []string{cspYouTube}},
		{"atomic youtube", `<div data-e-type="widget" data-settings="&#x7B;&quot;source&quot;&#x3A;&quot;https&#x3A;&#x5C;&#x2F;&#x5C;&#x2F;www.youtube.com&#x5C;&#x2F;watch&#x3F;v&#x3D;x&quot;,&quot;privacy&quot;&#x3A;true&#x7D;">`,
			[]string{cspYouTube}, []string{cspYouTubeNoCook}},
		{"background video", `<div data-settings="` + enc(map[string]any{"background_background": "video", "background_video_link": "https://vimeo.com/123"}) + `">`,
			[]string{cspVimeoPlayer}, []string{cspVimeoPlayer}},
		{"vimeo iframe", `<iframe class="elementor-video-iframe" allowfullscreen src="https://player.vimeo.com/video/1?x=1"></iframe>`, nil, []string{cspVimeoPlayer}},
		{"dailymotion", `<iframe src="https://dailymotion.com/embed/video/x1"></iframe>`, nil, []string{cspDailymotion, cspDailymotionWWW, cspDailymotionGeo}},
		{"playlist", `<div class="e-tab-content" data-video-url="https://www.youtube.com/watch?v=y" data-video-type="youtube">`, []string{cspYouTube}, []string{cspYouTube}},
		{"lightbox overlay", `<div data-elementor-lightbox="` + enc(map[string]any{"type": "video", "videoType": "youtube", "url": "https://www.youtube-nocookie.com/embed/x"}) + `">`,
			nil, []string{cspYouTubeNoCook}},
		{"lightbox action", `<a href="#elementor-action%3Aaction%3Dlightbox%26settings%3D` + url.QueryEscape(lb) + `">`, nil, []string{cspVimeoPlayer}},
		{"plain link", `<a href="https://www.youtube.com/watch?v=x">watch</a><p>https://vimeo.com/1</p>`, nil, nil},
	} {
		n := &gweEmbedNeeds{}
		gweEmbedScan(tc.markup, n)
		if strings.Join(n.script, ",") != strings.Join(tc.wantScript, ",") || strings.Join(n.frame, ",") != strings.Join(tc.wantFra, ",") {
			t.Errorf("%s: script=%v frame=%v, want %v %v", tc.name, n.script, n.frame, tc.wantScript, tc.wantFra)
		}
	}
}

func gapZip(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, b := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(b)
	}
	zw.Close()
	return buf.Bytes()
}

func TestGapIconZip(t *testing.T) {
	// zip-slip and junk entries are skipped, the single top folder is removed.
	a, err := gweReadIconZip(gapZip(t, map[string][]byte{
		"set/config.json": []byte(`{}`), "set/css/x.css": []byte("a"), "set/../../evil.css": []byte("x"), "/abs.css": []byte("x"),
		"__MACOSX/set/._config.json": []byte("x"), "set/run.php": []byte("<?php"), "set/font/x.woff": []byte("w"),
	}))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for p := range a {
		names = append(names, p)
	}
	if len(a) != 3 || !a.has("config.json") || !a.has("css/x.css") || !a.has("font/x.woff") {
		t.Errorf("entries = %v", names)
	}
	// An entry that inflates past the limit is refused.
	if _, err := gweReadIconZip(gapZip(t, map[string][]byte{"big.svg": make([]byte, gweIconsMaxEntry+10)})); err == nil {
		t.Error("oversized entry accepted")
	}
	if _, err := gweReadIconZip([]byte("not a zip")); err == nil {
		t.Error("garbage accepted")
	}

	fontello, err := gweParseIconSet(gweIconArchive{
		"config.json":    []byte(`{"name":"myfont","css_prefix_text":"icon-","glyphs":[{"css":"heart"},{"css":"star"}]}`),
		"css/myfont.css": []byte(`.icon-heart:before{margin-left: .2em;content:'a'}`), "font/myfont.woff2": []byte("w"), "demo.html": []byte("<script>"),
	})
	if err != nil || fontello.set.Type != "Fontello" || fontello.set.Prefix != "icon-" || len(fontello.set.Icons) != 2 ||
		strings.Contains(string(fontello.files["css/myfont.css"]), "margin-left") || fontello.files["demo.html"] != nil {
		t.Errorf("fontello: %+v %v", fontello, err)
	}
	icomoon, err := gweParseIconSet(gweIconArchive{
		"selection.json": []byte(`{"icons":[{"properties":{"name":"home, house"}},{"properties":{"name":"pen"}}],"preferences":{"fontPref":{"prefix":"im-","classSelector":".imc"}},"metadata":{"name":"MySet"}}`),
		"style.css":      []byte(`.im-home:before{}`), "fonts/MySet.woff": []byte("w"),
	})
	if err != nil || icomoon.set.Type != "Icomoon" || icomoon.set.DisplayPrefix != "imc" || strings.Join(icomoon.set.Icons, ",") != "home,pen" {
		t.Errorf("icomoon: %+v %v", icomoon, err)
	}
	fontastic, err := gweParseIconSet(gweIconArchive{
		"icons-reference.html": []byte("<html>"), "fonts/f.woff": []byte("w"),
		"styles.css": []byte("@font-face { font-family: \"fa-st\"; }\n[class^=\"fs-\"]:before {}\n.fs-cloud:before {\n}\n.fs-sun:before {\n}\n"),
	})
	if err != nil || fontastic.set.Type != "Fontastic" || fontastic.set.Name != "fa-st" || fontastic.set.Prefix != "fs-" || strings.Join(fontastic.set.Icons, ",") != "cloud,sun" {
		t.Errorf("fontastic: %+v %v", fontastic, err)
	}
	if _, err := gweParseIconSet(gweIconArchive{"readme.txt": []byte("x")}); err == nil {
		t.Error("unknown archive accepted")
	}
	used := gweCustomIconsUsed([]gweIconSet{{Name: "a", Prefix: "icon-", Icons: []string{"heart"}}, {Name: "b", Prefix: "icon-", Icons: []string{"star"}}},
		`<i aria-hidden="true" class="icon-heart"></i><div class="icon-box">`)
	if len(used) != 1 || used[0].Name != "a" {
		t.Errorf("used = %+v", used)
	}
}

// TestGapWidgetsSite runs the gap features through the server: wp_body_open in every theme, the
// per-page video CSP, local Prism, the native breadcrumb trail, Post Comments, Custom Icons and the
// popup editor's CSS wrapper.
func TestGapWidgetsSite(t *testing.T) {
	ts, c := testServer(t)
	base := ts.URL
	if code, body, _ := post(t, c, base+"/wp-admin/install?step=2", url.Values{
		"weblog_title": {"Gap Site"}, "user_name": {"admin"}, "admin_password": {"correct horse"}, "admin_email": {"admin@example.test"},
	}); code != http.StatusOK || !strings.Contains(body, "Success!") {
		t.Fatalf("install: %d", code)
	}
	if code, _, _ := post(t, c, base+"/wp-login", url.Values{"log": {"admin"}, "pwd": {"correct horse"}}); code != http.StatusFound {
		t.Fatalf("login: %d", code)
	}
	ctx := context.Background()
	pool := getPool(t)
	insert := func(typ, slug, title string, parent int64, meta map[string]any) int64 {
		t.Helper()
		b, _ := json.Marshal(meta)
		var id int64
		var par any
		if parent > 0 {
			par = parent
		}
		if err := pool.QueryRow(ctx, `INSERT INTO posts (type, author_id, parent_id, slug, title, status, published_at, meta)
			VALUES ($1, 1, $2, $3, $4, 'publish', now(), $5) RETURNING id`, typ, par, slug, title, string(b)).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	builtPage := func(slug, title string, parent int64, els string) int64 {
		return insert("page", slug, title, parent, map[string]any{"_elementor_edit_mode": "builder", "_elementor_template_type": "wp-page", "_elementor_data": els})
	}
	var sampleID int64
	if err := pool.QueryRow(ctx, `SELECT id FROM posts WHERE type='page' AND title='Sample Page'`).Scan(&sampleID); err != nil {
		t.Fatal(err)
	}
	videoPage := builtPage("gap-video", "Gap Video", sampleID, `[{"id":"c1","elType":"container","settings":{},"elements":[
		{"id":"v1","elType":"widget","widgetType":"video","settings":{"video_type":"youtube","youtube_url":"https://www.youtube.com/watch?v=XHOmBV4js_E","yt_privacy":"yes"},"elements":[]},
		{"id":"ch","elType":"widget","widgetType":"code-highlight","settings":{"language":"go","code":"package main","copy_to_clipboard":"copy-to-clipboard"},"elements":[]},
		{"id":"bc","elType":"widget","widgetType":"breadcrumbs","settings":{},"elements":[]},
		{"id":"pc","elType":"widget","widgetType":"post-comments","settings":{},"elements":[]},
		{"id":"ic","elType":"widget","widgetType":"icon","settings":{"selected_icon":{"value":"icon-heart","library":"gaptest"}},"elements":[]}]}]`)
	_ = videoPage
	builtPage("gap-plain", "Gap Plain", 0, `[{"id":"c2","elType":"container","settings":{},"elements":[
		{"id":"t2","elType":"widget","widgetType":"text-editor","settings":{"editor":"<p>Plain</p>"},"elements":[]}]}]`)
	insert(gweLibrary, "gap-snippet", "Gap snippet", 0, map[string]any{"_elementor_template_type": "code_snippet",
		"_gwe_code": `<div id="gap-body-start"></div>`, "_gwe_code_location": "body_start", "_gwe_conditions": []any{"include/general"}})
	popupID := insert(gweLibrary, "gap-popup", "Gap Popup", 0, map[string]any{"_elementor_edit_mode": "builder", "_elementor_template_type": "popup",
		"_elementor_data": `[]`})

	fetch := func(path string) (string, http.Header) {
		t.Helper()
		resp, err := c.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: %d", path, resp.StatusCode)
		}
		return string(b), resp.Header
	}

	// Video CSP only where a video is used; Prism is local; the trail and comments render.
	body, h := fetch("/sample-page/gap-video/")
	csp := h.Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'self' 'unsafe-inline' https://www.youtube.com;") {
		t.Errorf("video page CSP = %s", csp)
	}
	if strings.Contains(body, "cdnjs.cloudflare.com") || !regexp.MustCompile(`(?s)id="prismjs_core-js".*id="prismjs_loader-js".*id="prismjs_normalize-js".*id="prismjs_toolbar-js".*id="prismjs_copy_to_clipboard-js"`).MatchString(body) ||
		!strings.Contains(body, `/wp-content/plugins/gowebeditor/assets/lib/prismjs/components/prism-core.min.js`) {
		t.Error("Prism scripts are not the local copies in upstream order")
	}
	if !strings.Contains(body, `<a itemprop="item" href="`+base+`/sample-page/"><span itemprop="name">Sample Page</span></a>`) ||
		!strings.Contains(body, `<span class="breadcrumb_last" aria-current="page" itemprop="itemListElement" itemscope itemtype="https://schema.org/ListItem"><span itemprop="name">Gap Video</span>`) {
		t.Error("breadcrumb trail missing")
	}
	if !regexp.MustCompile(`(?s)elementor-widget-post-comments.*<div id="comments" class="comments-area">.*id="respond"`).MatchString(body) {
		t.Error("post comments widget does not print the theme's comments area")
	}
	if _, h := fetch("/gap-plain/"); strings.Contains(h.Get("Content-Security-Policy"), "youtube") {
		t.Errorf("plain page CSP relaxed: %s", h.Get("Content-Security-Policy"))
	}

	// wp_body_open: the body-start snippet follows <body> in every theme.
	bodyStart := regexp.MustCompile(`<body class="[^"]*">\s*<!-- Golementor custom code: Gap snippet -->\s*<div id="gap-body-start"></div>`)
	for _, theme := range []string{"twentysixteen", "twentyseventeen", "twentytwenty", "twentytwentyone", helloThemeSlug} {
		_, themes, _ := get(t, c, base+"/wp-admin/themes")
		if theme != "twentysixteen" {
			m := regexp.MustCompile(`action=activate&(?:amp;)?stylesheet=` + theme + `&(?:amp;)?_wpnonce=([0-9a-zA-Z]+)`).FindStringSubmatch(themes)
			if m == nil {
				t.Fatalf("no activate link for %s", theme)
			}
			if code, _, _ := get(t, c, base+"/wp-admin/themes?action=activate&stylesheet="+theme+"&_wpnonce="+m[1]); code != http.StatusFound {
				t.Fatalf("activate %s: %d", theme, code)
			}
		}
		for _, path := range []string{"/gap-plain/", "/"} {
			page, _ := fetch(path)
			if !bodyStart.MatchString(page) {
				t.Errorf("%s %s: body-start snippet not right after <body>", theme, path)
			}
			if n := strings.Count(page, `id="gap-body-start"`); n != 1 {
				t.Errorf("%s %s: snippet printed %d times", theme, path, n)
			}
		}
	}

	// Custom Icons: upload a Fontello export, see it in the editor's library and on the page.
	_, adminPage, _ := get(t, c, base+"/wp-admin/gowebeditor-icons")
	m := regexp.MustCompile(`name="_wpnonce" value="([^"]+)"`).FindStringSubmatch(adminPage)
	if m == nil {
		t.Fatal("no icons nonce")
	}
	zipData := gapZip(t, map[string][]byte{
		"fontello-1/config.json":     []byte(`{"name":"gaptest","css_prefix_text":"icon-","glyphs":[{"css":"heart"},{"css":"star"}]}`),
		"fontello-1/css/gaptest.css": []byte(`.icon-heart:before{content:'\e800'}`), "fontello-1/font/gaptest.woff2": []byte("wOF2"),
		"fontello-1/demo.html": []byte("<script>alert(1)</script>"),
	})
	var form bytes.Buffer
	mw := multipart.NewWriter(&form)
	mw.WriteField("_wpnonce", m[1])
	mw.WriteField("action", "upload")
	mw.WriteField("label", "Gap Icons")
	fw, _ := mw.CreateFormFile("zip_upload", "gap.zip")
	fw.Write(zipData)
	mw.Close()
	resp, err := c.Post(base+"/wp-admin/gowebeditor-icons", mw.FormDataContentType(), &form)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if loc := resp.Header.Get("Location"); resp.StatusCode != http.StatusFound || !strings.Contains(loc, "message=") {
		t.Fatalf("upload: %d %s", resp.StatusCode, loc)
	}
	if code, _, _ := get(t, c, base+"/wp-content/uploads/gowebeditor/custom-icons/gaptest/css/gaptest.css"); code != http.StatusOK {
		t.Errorf("icon stylesheet: %d", code)
	}
	if code, _, _ := get(t, c, base+"/wp-content/uploads/gowebeditor/custom-icons/gaptest/demo.html"); code == http.StatusOK {
		t.Error("demo.html from the archive was published")
	}
	body, _ = fetch("/sample-page/gap-video/")
	if !strings.Contains(body, `id='elementor-icons-gaptest-css' href='`+base+`/wp-content/uploads/gowebeditor/custom-icons/gaptest/css/gaptest.css`) {
		t.Error("custom icon stylesheet not printed on the page using it")
	}
	if plain, _ := fetch("/gap-plain/"); strings.Contains(plain, "elementor-icons-gaptest") {
		t.Error("custom icon stylesheet printed on a page without its icons")
	}
	_, editor, _ := get(t, c, base+"/wp-admin/post?post="+itoa64(videoPage)+"&action=golementor")
	if !strings.Contains(editor, `"custom_icon_type":"Fontello"`) || !strings.Contains(editor, `"label":"Gap Icons"`) ||
		!strings.Contains(editor, `"customIconsURL":"`+base+`/wp-admin/gowebeditor-icons"`) {
		t.Error("editor icon library misses the custom set")
	}

	// Page Title tag with context in the editor (render_tags of the edited page).
	nm := regexp.MustCompile(`"ajax":\{"url":"[^"]*","nonce":"([^"]+)"`).FindStringSubmatch(editor)
	if nm == nil {
		nm = regexp.MustCompile(`"ajax":\{"nonce":"([^"]+)"`).FindStringSubmatch(editor)
	}
	if nm == nil {
		t.Fatal("no elementor_ajax nonce")
	}
	key := base64.StdEncoding.EncodeToString([]byte("page-title")) + "-" + base64.StdEncoding.EncodeToString([]byte(url.QueryEscape(`{"include_context":"yes"}`)))
	_, tags, _ := post(t, c, base+"/wp-admin/admin-ajax", url.Values{"action": {"elementor_ajax"}, "_nonce": {nm[1]}, "editor_post_id": {itoa64(videoPage)},
		"actions": {`{"r":{"action":"render_tags","data":{"post_id":` + itoa64(videoPage) + `,"tags":["` + key + `"]}}}`}})
	if !strings.Contains(tags, `"Page: Gap Video"`) {
		t.Errorf("render_tags page-title = %s", tags)
	}

	// Popup editor: page settings CSS targets this popup's dialog.
	_, popupEditor, _ := get(t, c, base+"/wp-admin/post?post="+itoa64(popupID)+"&action=golementor")
	if !strings.Contains(popupEditor, `"cssWrapperSelector":"#elementor-popup-modal-`+itoa64(popupID)+`"`) {
		t.Error("popup document cssWrapperSelector is not the popup's modal")
	}
}
