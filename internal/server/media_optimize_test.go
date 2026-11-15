package server

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wpgo/internal/media"
)

// Media › Optimization: off by default; on, uploads are written as WebP (the upload kept as the
// original, or deleted when only the optimized file is kept); "Optimize existing images" converts
// the library and redirects the old file URLs.
func TestMediaOptimization(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	ctx := context.Background()
	var uid int64
	_ = srv.store.Pool.QueryRow(ctx, `SELECT id FROM users ORDER BY id LIMIT 1`).Scan(&uid)

	r := rand.New(rand.NewSource(1))
	img := image.NewRGBA(image.Rect(0, 0, 900, 600))
	for y := 0; y < 600; y++ {
		for x := 0; x < 900; x++ {
			n := uint8(r.Intn(16))
			img.Set(x, y, color.RGBA{uint8(x/4) + n, uint8(y/3) + n, 120, 255})
		}
	}
	var jpg bytes.Buffer
	if err := media.EncodeToQuality(&jpg, img, "jpeg", 98); err != nil {
		t.Fatal(err)
	}
	upload := func(name string) int64 {
		t.Helper()
		a, err := srv.insertAttachmentFile(ctx, name, bytes.NewReader(jpg.Bytes()), uid, 0)
		if err != nil {
			t.Fatal(err)
		}
		return a.ID
	}
	exists := func(rel string) bool {
		_, err := os.Stat(filepath.Join(srv.cfg.UploadsDir, filepath.FromSlash(rel)))
		return err == nil
	}

	plain := upload("plain.jpg")
	if a, _ := srv.store.GetAttachment(ctx, plain); a.Mime != "image/jpeg" || a.Meta[mediaOptimizedMeta] != nil {
		t.Fatalf("optimized while off: %s %v", a.Mime, a.Meta[mediaOptimizedMeta])
	}

	_, body, _ := get(t, c, base+"/wp-admin/upload")
	if !strings.Contains(body, `href="/wp-admin/media-optimization"`) {
		t.Error("no Media › Optimization menu item")
	}
	_, body, _ = get(t, c, base+"/wp-admin/media-optimization")
	if !strings.Contains(body, `id="pursae_media_optimize" value="1" />`) || strings.Contains(body, `id="optimize-all"`) {
		t.Fatal("optimization is not off by default, or offers the bulk run while off")
	}
	nonce := formNonce(t, body)
	save := func(enabled, original string) {
		t.Helper()
		code, _, loc := post(t, c, base+"/wp-admin/media-optimization", url.Values{"_wpnonce": {nonce}, "enabled": {enabled}, "original": {original}, "quality": {"75"}})
		if code != http.StatusFound || !strings.Contains(loc, "settings-updated") {
			t.Fatalf("save: %d %s", code, loc)
		}
	}
	save("1", "keep")
	if !srv.mediaOptimizeOn() || srv.mediaOptimizeDiscard() || srv.mediaOptimizeQuality() != 75 {
		t.Fatal("settings not stored")
	}

	kept := upload("kept.jpg")
	a, _ := srv.store.GetAttachment(ctx, kept)
	mark, _ := a.Meta[mediaOptimizedMeta].(map[string]any)
	if a.Mime != "image/webp" || !strings.HasSuffix(a.FilePath, "kept.webp") || a.Meta["original_image"] != "kept.jpg" || mark["after"] == nil {
		t.Fatalf("keep original: %s %s %v %v", a.FilePath, a.Mime, a.Meta["original_image"], mark)
	}
	if mark["after"].(float64) >= mark["before"].(float64) {
		t.Errorf("not smaller: %v", mark)
	}
	if !exists(strings.Replace(a.FilePath, "kept.webp", "kept.jpg", 1)) {
		t.Error("the original was not kept")
	}
	for _, v := range a.Variants {
		if v.Mime != "image/webp" {
			t.Errorf("size %s is %s", v.Name, v.Mime)
		}
	}

	save("1", "discard")
	only := upload("only.jpg")
	a, _ = srv.store.GetAttachment(ctx, only)
	if !strings.HasSuffix(a.FilePath, "only.webp") || a.Meta["original_image"] != nil || exists(strings.Replace(a.FilePath, "only.webp", "only.jpg", 1)) {
		t.Fatalf("discard original: %s %v", a.FilePath, a.Meta["original_image"])
	}

	// The image uploaded while optimization was off.
	old, _ := srv.store.GetAttachment(ctx, plain)
	oldMedium := old.Variant("medium").FilePath
	if st := srv.mediaOptimizeStats(ctx); st.Pending != 1 || st.Optimized != 2 {
		t.Fatalf("stats: %+v", st)
	}
	_, body, _ = get(t, c, base+"/wp-admin/media-optimization")
	if !strings.Contains(body, `value="Optimize 1 existing images"`) {
		t.Fatal("no Optimize existing images button")
	}
	if _, _, loc := post(t, c, base+"/wp-admin/media-optimization", url.Values{"_wpnonce": {nonce}, "action": {"optimize-all"}}); !strings.Contains(loc, "optimize=started") {
		t.Fatalf("optimize-all: %s", loc)
	}
	for i := 0; i < 200 && optimizeRunning.Load(); i++ {
		time.Sleep(50 * time.Millisecond)
	}
	if j := srv.optimizeJobState(); !j.Finished || j.Done != 1 {
		t.Fatalf("job: %+v", j)
	}
	a, _ = srv.store.GetAttachment(ctx, plain)
	if a.Mime != "image/webp" || a.Meta["original_image"] != nil || a.Meta[mediaOptimizedMeta] == nil {
		t.Fatalf("existing image: %s %s %v", a.FilePath, a.Mime, a.Meta)
	}
	if exists(oldMedium) {
		t.Error("the old size is still on disk")
	}
	resp, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Get(base + "/wp-content/uploads/" + oldMedium)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if loc := resp.Header.Get("Location"); resp.StatusCode/100 != 3 || !strings.Contains(loc, a.Variant("medium").FilePath) {
		t.Errorf("old size URL: %d %s", resp.StatusCode, loc)
	}
	if st := srv.mediaOptimizeStats(ctx); st.Pending != 0 || st.Optimized != 3 || st.Before <= st.After {
		t.Errorf("stats after the run: %+v", st)
	}
	if srv.optimizeAllAsync(false) {
		t.Error("a second run started with nothing to do")
	}
}
