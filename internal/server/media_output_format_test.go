package server

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"wpgo/internal/media"
)

// TestImageOutputFormat checks Settings › Media › Image output format (image_editor_output_format):
// by default WebP uploads get JPEG sizes; "webp" keeps them WebP; "webp-all" converts JPEG uploads
// to WebP and keeps the JPEG as the original image.
func TestImageOutputFormat(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	ctx := context.Background()
	var uid int64
	_ = srv.store.Pool.QueryRow(ctx, `SELECT id FROM users ORDER BY id LIMIT 1`).Scan(&uid)

	img := image.NewRGBA(image.Rect(0, 0, 800, 500))
	for y := 0; y < 500; y++ {
		for x := 0; x < 800; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 90, 255})
		}
	}
	upload := func(name, format string) (string, string, map[string]string) {
		t.Helper()
		var buf bytes.Buffer
		if err := media.EncodeTo(&buf, img, format); err != nil {
			t.Fatal(err)
		}
		a, err := srv.insertAttachmentFile(ctx, name, &buf, uid, 0)
		if err != nil {
			t.Fatal(err)
		}
		a, _ = srv.store.GetAttachment(ctx, a.ID)
		sizes := map[string]string{}
		for _, v := range a.Variants {
			sizes[v.Name] = v.FilePath + " " + v.Mime
		}
		orig, _ := a.Meta["original_image"].(string)
		return a.FilePath + " " + a.Mime + " " + orig, sizes["medium"], sizes
	}

	main, medium, _ := upload("default.webp", "webp")
	if !strings.HasSuffix(main, "default.webp image/webp ") || !strings.HasSuffix(medium, "default-300x188.jpg image/jpeg") {
		t.Errorf("default: %s / %s", main, medium)
	}

	_, body, _ := get(t, c, base+"/wp-admin/options-media")
	if !strings.Contains(body, `<select name="image_output_format" id="image_output_format"`) || !strings.Contains(body, `<option value="" selected="selected">`) {
		t.Fatal("no Image output format on Settings › Media")
	}
	form := url.Values{"option_page": {"media"}, "_wpnonce": {formNonce(t, body)}, "thumbnail_size_w": {"150"}, "thumbnail_size_h": {"150"},
		"thumbnail_crop": {"1"}, "medium_size_w": {"300"}, "medium_size_h": {"300"}, "large_size_w": {"1024"}, "large_size_h": {"1024"},
		"uploads_use_yearmonth_folders": {"1"}, "image_output_format": {"webp"}}
	if code, _, _ := post(t, c, base+"/wp-admin/options", form); code != http.StatusFound || srv.settings.String("image_output_format") != "webp" {
		t.Fatalf("save: %d %q", code, srv.settings.String("image_output_format"))
	}
	form.Set("image_output_format", "gif")
	post(t, c, base+"/wp-admin/options", form)
	if srv.settings.String("image_output_format") != "webp" {
		t.Errorf("unknown value stored: %q", srv.settings.String("image_output_format"))
	}

	main, medium, _ = upload("native.webp", "webp")
	if !strings.HasSuffix(main, "native.webp image/webp ") || !strings.HasSuffix(medium, "native-300x188.webp image/webp") {
		t.Errorf("webp: %s / %s", main, medium)
	}
	main, medium, _ = upload("photo.jpg", "jpeg")
	if !strings.HasSuffix(main, "photo.jpg image/jpeg ") || !strings.HasSuffix(medium, "photo-300x188.jpg image/jpeg") {
		t.Errorf("webp, JPEG upload: %s / %s", main, medium)
	}

	if err := srv.settings.Set(ctx, map[string]any{"image_output_format": "webp-all"}); err != nil {
		t.Fatal(err)
	}
	main, medium, sizes := upload("meadow.jpg", "jpeg")
	if !strings.HasSuffix(main, "meadow.webp image/webp meadow.jpg") || !strings.HasSuffix(medium, "meadow-300x188.webp image/webp") ||
		!strings.HasSuffix(sizes["thumbnail"], "meadow-150x150.webp image/webp") {
		t.Errorf("webp-all: %s / %v", main, sizes)
	}
	if code, _, _ := get(t, c, base+"/wp-content/uploads/"+strings.Fields(main)[0]); code != http.StatusOK {
		t.Errorf("converted file: %d", code)
	}
}
