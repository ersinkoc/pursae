package server

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"wpgo/internal/webbuilder"
)

// A popup template prints on the pages its conditions match, hidden, with its trigger settings; the
// pages it does not match stay clean, and a site without any popup is unchanged.
func TestWBPopupFront(t *testing.T) {
	srv, base, admin := wbtfStart(t)
	about := wbtfPost(t, srv, "page", "About Us", "About body.", 0)
	post := wbtfPost(t, srv, "post", "Hello World", "Post body.", 0)

	_, body := wbtfGet(t, base+"/"+about.Slug+"/")
	wbtfBad(t, "no popup yet", body, "data-wb-popup", "wb-popup-js", "wb-popups")

	pop := wbtfTemplate(t, srv, webbuilder.TemplatePopup, "Newsletter", []string{"include/singular/page"}, `<h2>POPUP-MARK</h2>`, "", ".wbp{color:red}", nil)
	if err := srv.store.WebBuilder().SetTemplatePopup(t.Context(), pop.ID,
		`{"trigger":"scroll","delay":0,"scroll":40,"selector":"","frequency":"days","days":9,"version":3,"position":"bottom","width":480,"overlay":false,"afterConsent":false}`); err != nil {
		t.Fatal(err)
	}
	srv.wbTemplatesChanged(t.Context())

	code, body := wbtfGet(t, base+"/"+about.Slug+"/")
	if code != http.StatusOK {
		t.Fatalf("page: %d", code)
	}
	id := itoa64(pop.ID)
	wbtfWant(t, "page", body, `id="wb-popup-`+id+`"`, `data-wb-popup="`+id+`"`, `data-trigger="scroll"`, `data-scroll="40"`, `data-frequency="days"`,
		`data-days="9"`, `data-version="3"`, `data-overlay="0"`, `wb-popup-bottom`, `--wb-popup-w:480px`, `role="dialog"`, ` hidden`,
		`POPUP-MARK`, `id="wb-popups"`, `id="wb-popup-js"`, `<style id="wb-css-`+id+`">`, ".wbp{color:red}", "About body.")
	wbtfBad(t, "page", body, "data-consent=")
	if strings.Count(body, `data-wb-popup="`) != 1 || strings.Count(body, `id="wb-popup-js"`) != 1 {
		t.Errorf("popup markup repeated")
	}
	// Not matched: a post. A different page of the site never shows the popup of another type.
	_, body = wbtfGet(t, base+"/"+post.Slug+"/")
	wbtfBad(t, "post", body, "data-wb-popup", "wb-popup-js")

	// A second popup on every view, and the draft/disabled one never shows.
	all := wbtfTemplate(t, srv, webbuilder.TemplatePopup, "Everywhere", []string{"include/general"}, `<p>EVERY-MARK</p>`, "", "", nil)
	off := wbtfTemplate(t, srv, webbuilder.TemplatePopup, "Disabled", []string{"include/general"}, `<p>OFF-MARK</p>`, "", "", nil)
	if _, err := srv.store.Pool.Exec(t.Context(), `UPDATE posts SET status = 'draft' WHERE id = $1`, off.ID); err != nil {
		t.Fatal(err)
	}
	srv.wbTemplatesChanged(t.Context())
	_, body = wbtfGet(t, base+"/"+about.Slug+"/")
	wbtfWant(t, "two popups", body, "POPUP-MARK", "EVERY-MARK", `data-wb-popup="`+itoa64(all.ID)+`"`)
	wbtfBad(t, "two popups", body, "OFF-MARK")
	_, body = wbtfGet(t, base+"/"+post.Slug+"/")
	wbtfWant(t, "post", body, "EVERY-MARK")
	wbtfBad(t, "post", body, "POPUP-MARK")
	_, body = wbtfGet(t, base+"/no-such-page-at-all/")
	wbtfWant(t, "404", body, "EVERY-MARK")

	// Popup settings screen and its save (GET + nonce, like the list's row actions).
	scr := wbThemeBuilderURL() + "&post=" + id + "&action=popup-settings"
	code, page, _ := get(t, admin, base+scr)
	if code != http.StatusOK || !strings.Contains(page, `name="trigger"`) || !strings.Contains(page, `value="40"`) {
		t.Fatalf("settings screen: %d", code)
	}
	nonce := formNonce(t, page)
	q := url.Values{"page": {webbuilder.ThemeBuilderPage}, "action": {"popup-settings-save"}, "post": {id}, "_wpnonce": {nonce},
		"trigger": {"click"}, "selector": {"#open-me"}, "delay": {"1"}, "scroll": {"50"}, "frequency": {"once"}, "days": {"7"}, "version": {"4"},
		"position": {"center"}, "width": {"700"}, "overlay": {"1"}}
	if code, _, loc := get(t, admin, base+"/wp-admin/themes?"+q.Encode()); code != http.StatusFound || !strings.Contains(loc, "saved=1") {
		t.Fatalf("save: %d %s", code, loc)
	}
	raw, _ := srv.store.WebBuilder().TemplatePopup(t.Context(), pop.ID)
	if st := webbuilder.ParsePopup(raw); st.Trigger != "click" || st.Selector != "#open-me" || st.Version != 4 || st.Width != 700 {
		t.Errorf("stored: %s", raw)
	}
	_, body = wbtfGet(t, base+"/"+about.Slug+"/")
	wbtfWant(t, "after save", body, `data-trigger="click"`, `data-selector="#open-me"`, `data-overlay="1"`)
	// An invalid selector is refused and nothing changes.
	q.Set("selector", "a > b")
	code, page, _ = get(t, admin, base+"/wp-admin/themes?"+q.Encode())
	if code != http.StatusOK || !strings.Contains(page, "needs a selector") {
		i := strings.Index(page, `name="selector"`)
		t.Errorf("bad selector: %d notice=%v selector-field=%.200s", code, strings.Contains(page, "notice"), page[max(0, i-20):])
	}
	if raw2, _ := srv.store.WebBuilder().TemplatePopup(t.Context(), pop.ID); raw2 != raw {
		t.Errorf("a refused save changed the settings: %s", raw2)
	}
	// A header template has no popup settings.
	hdr := wbtfTemplate(t, srv, webbuilder.TemplateHeader, "Hdr", []string{"include/general"}, "x", "", "", nil)
	if code, _, _ := get(t, admin, base+wbThemeBuilderURL()+"&post="+itoa64(hdr.ID)+"&action=popup-settings"); code != http.StatusBadRequest {
		t.Errorf("header popup settings: %d", code)
	}
}

func TestWebBuilderRESTPopupSettings(t *testing.T) {
	srv, base, admin := wbrStart(t)
	pop := wbtTemplate(t, srv, webbuilder.TemplatePopup, "Offer", []string{"include/general"}, 0, "")
	hdr := wbtTemplate(t, srv, webbuilder.TemplateHeader, "Hdr", []string{"include/general"}, 0, "")
	path := "/web-builder/v2/templates/" + itoa64(pop.ID) + "/popup"

	code, v, _ := admin.do(http.MethodGet, path, nil, nil)
	if o := wbrObj(v); code != http.StatusOK || o["trigger"] != "load" || o["frequency"] != "session" {
		t.Fatalf("defaults: %d %v", code, v)
	}
	put := map[string]any{"trigger": "exit", "delay": 0, "scroll": 50, "frequency": "once", "days": 5, "version": 2, "position": "top", "width": 900, "overlay": true, "afterConsent": true}
	code, v, _ = admin.do(http.MethodPut, path, put, nil)
	if o := wbrObj(v); code != http.StatusOK || o["trigger"] != "exit" || o["width"] != float64(900) {
		t.Fatalf("put: %d %v", code, v)
	}
	if code, v, _ = admin.do(http.MethodGet, path, nil, nil); wbrObj(v)["frequency"] != "once" {
		t.Errorf("not stored: %d %v", code, v)
	}
	put["width"] = 10
	if code, _, _ = admin.do(http.MethodPut, path, put, nil); code != http.StatusUnprocessableEntity {
		t.Errorf("bad width: %d", code)
	}
	if code, _, _ = admin.do(http.MethodGet, "/web-builder/v2/templates/"+itoa64(hdr.ID)+"/popup", nil, nil); code != http.StatusUnprocessableEntity {
		t.Errorf("header: %d", code)
	}
	sub := wbrUser(t, srv, base, "sub3", "subscriber")
	if code, _, _ = sub.do(http.MethodGet, path, nil, nil); code != http.StatusForbidden {
		t.Errorf("subscriber: %d", code)
	}
}
