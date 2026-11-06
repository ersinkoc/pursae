package server

import (
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// A built page with a carousel links Swiper's core stylesheet before e-swiper, as upstream's
// style dependencies do; without it the slides stack and render blank.
func TestGolementorCarouselLoadsSwiperCSS(t *testing.T) {
	t.Skip("old builder detached from routing, admin and front (R1c/R1d); this file goes in R2")
	ts, c := testServer(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	_, body, _ := get(t, c, base+"/wp-admin/post?post=2&action=web-builder")
	m := regexp.MustCompile(`"ajax":\{"nonce":"([^"]+)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no elementor_ajax nonce")
	}
	tree := `[{"id":"a1b2c3d","elType":"container","isInner":false,"settings":{},"elements":[` +
		`{"id":"b1b2c3d","elType":"widget","widgetType":"image-carousel","isInner":false,"settings":{"carousel":[{"id":0,"url":"` + base + `/wp-includes/images/w-logo-blue.png"}]},"elements":[]}]}]`
	code, out, _ := post(t, c, base+"/wp-admin/admin-ajax", url.Values{"action": {"elementor_ajax"}, "_nonce": {m[1]},
		"editor_post_id": {"2"}, "initial_document_id": {"2"},
		"actions": {`{"s":{"action":"save_builder","data":{"status":"publish","elements":` + tree + `,"settings":{"post_status":"publish"}}}}`}})
	if code != 200 || !strings.Contains(out, `"success":true`) {
		t.Fatalf("save_builder: %d %.300s", code, out)
	}
	_, body, _ = get(t, c, base+"/sample-page/")
	sw := strings.Index(body, "lib/swiper/v8/css/swiper.min.css")
	es := strings.Index(body, "id='e-swiper-css'")
	if es < 0 {
		es = strings.Index(body, `id="e-swiper-css"`)
	}
	if sw < 0 {
		t.Fatal("no Swiper stylesheet on a page with a carousel")
	}
	if es >= 0 && sw > es {
		t.Error("Swiper's stylesheet comes after e-swiper")
	}
}
