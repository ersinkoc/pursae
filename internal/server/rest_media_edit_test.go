package server

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wpgo/internal/store"
)

// halvesPNG is a w×h PNG, its left half red and its right half blue.
func halvesPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.RGBA{255, 0, 0, 255}
			if x >= w/2 {
				c = color.RGBA{0, 0, 255, 255}
			}
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// rawUpload sends a file as the raw body of POST /wp/v2/media.
func rawUpload(t *testing.T, rc *restClient, name, query string, data []byte) map[string]any {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, rc.base+"/wp-json/wp/v2/media"+query, bytes.NewReader(data))
	req.Header.Set("Content-Type", "image/png")
	req.Header.Set("Content-Disposition", `attachment; filename="`+name+`"`)
	req.Header.Set("X-WP-Nonce", rc.nonce)
	resp, err := rc.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload %s: %d %s", name, resp.StatusCode, raw)
	}
	return out
}

func TestRESTMediaEdit(t *testing.T) {
	s := secStart(t)
	orig := rawUpload(t, s.admin, "halves.png", "?alt_text=Two+halves&caption=A+caption", halvesPNG(t, 200, 100))
	id := itoa64(int64(orig["id"].(float64)))
	src, _ := orig["source_url"].(string)
	editPath := "/wp/v2/media/" + id + "/edit"

	// decoded opens the edited file and checks its size.
	decoded := func(m map[string]any, w, h int) image.Image {
		t.Helper()
		d, _ := m["media_details"].(map[string]any)
		file, _ := d["file"].(string)
		f, err := os.Open(filepath.Join(s.srv.cfg.UploadsDir, filepath.FromSlash(file)))
		if err != nil {
			t.Fatalf("edited file %q: %v", file, err)
		}
		defer f.Close()
		img, _, err := image.Decode(f)
		if err != nil {
			t.Fatal(err)
		}
		if b := img.Bounds(); b.Dx() != w || b.Dy() != h {
			t.Fatalf("edited %s is %dx%d, want %dx%d", file, b.Dx(), b.Dy(), w, h)
		}
		return img
	}
	red := func(c color.Color) bool { r, g, b, _ := c.RGBA(); return r > 0xc000 && g < 0x4000 && b < 0x4000 }
	blue := func(c color.Color) bool { r, g, b, _ := c.RGBA(); return b > 0xc000 && g < 0x4000 && r < 0x4000 }

	// Rotate 90 degrees clockwise: the red (left) half ends at the top.
	code, m, _, hdr := s.admin.do("POST", editPath, map[string]any{"src": src, "modifiers": []any{
		map[string]any{"type": "rotate", "args": map[string]any{"angle": 90}}}})
	if code != http.StatusCreated {
		t.Fatalf("rotate: %d %v", code, m)
	}
	if m["id"] == orig["id"] || hdr.Get("X-WP-Upload-Attachment-ID") != itoa64(int64(m["id"].(float64))) || !strings.HasSuffix(hdr.Get("Location"), "/wp-json/wp/v2/media/"+itoa64(int64(m["id"].(float64)))) {
		t.Errorf("rotate: not a new attachment / headers: %v %v", m["id"], hdr)
	}
	img := decoded(m, 100, 200)
	if !red(img.At(50, 20)) || !blue(img.At(50, 180)) {
		t.Errorf("rotate: top %v bottom %v", img.At(50, 20), img.At(50, 180))
	}
	if !strings.Contains(m["source_url"].(string), "halves-edited") || m["alt_text"] != "Two halves" {
		t.Errorf("rotate: name or alt not carried: %v %v", m["source_url"], m["alt_text"])
	}
	if c, _ := m["caption"].(map[string]any); c["raw"] != "A caption" {
		t.Errorf("rotate: caption %v", m["caption"])
	}
	d, _ := m["media_details"].(map[string]any)
	if sizes, _ := d["sizes"].(map[string]any); sizes["thumbnail"] == nil {
		t.Errorf("rotate: no sub-sizes: %v", d["sizes"])
	}
	// The original attachment is untouched.
	if _, o, _, _ := s.admin.do("GET", "/wp/v2/media/"+id+"?context=edit", nil); o["source_url"] != src {
		t.Errorf("the original changed: %v", o["source_url"])
	}

	// Crop the right half (percentages).
	code, m, _, _ = s.admin.do("POST", editPath, map[string]any{"src": src, "modifiers": []any{
		map[string]any{"type": "crop", "args": map[string]any{"left": 50, "top": 0, "width": 50, "height": 100}}}})
	if code != http.StatusCreated {
		t.Fatalf("crop: %d %v", code, m)
	}
	img = decoded(m, 100, 100)
	if !blue(img.At(1, 1)) || !blue(img.At(98, 98)) {
		t.Errorf("crop: %v %v", img.At(1, 1), img.At(98, 98))
	}

	// Rotate, then crop the top half of the rotated image: all red. Editing an edited image keeps one "-edited".
	editedSrc, _ := m["source_url"].(string)
	code, m, _, _ = s.admin.do("POST", editPath, map[string]any{"src": src, "modifiers": []any{
		map[string]any{"type": "rotate", "args": map[string]any{"angle": 90}},
		map[string]any{"type": "crop", "args": map[string]any{"left": 0, "top": 0, "width": 100, "height": 50}}}})
	if code != http.StatusCreated {
		t.Fatalf("rotate+crop: %d %v", code, m)
	}
	img = decoded(m, 100, 100)
	if !red(img.At(1, 1)) || !red(img.At(98, 98)) {
		t.Errorf("rotate+crop: %v %v", img.At(1, 1), img.At(98, 98))
	}

	// The legacy flat parameters: rotation 180 then x/y/width/height; flip.
	code, m, _, _ = s.admin.do("POST", editPath, map[string]any{"src": src, "rotation": 180, "x": 0, "y": 0, "width": 50, "height": 100})
	if code != http.StatusCreated {
		t.Fatalf("legacy: %d %v", code, m)
	}
	if img = decoded(m, 100, 100); !blue(img.At(50, 50)) {
		t.Errorf("legacy rotate 180 + crop left half: %v", img.At(50, 50))
	}
	code, m, _, _ = s.admin.do("POST", editPath, map[string]any{"src": src, "title": "Flipped", "modifiers": []any{
		map[string]any{"type": "flip", "args": map[string]any{"flip": map[string]any{"horizontal": true, "vertical": false}}}}})
	if code != http.StatusCreated {
		t.Fatalf("flip: %d %v", code, m)
	}
	if img = decoded(m, 200, 100); !blue(img.At(5, 50)) || !red(img.At(195, 50)) {
		t.Errorf("flip: %v %v", img.At(5, 50), img.At(195, 50))
	}
	if tt, _ := m["title"].(map[string]any); tt["raw"] != "Flipped" {
		t.Errorf("the title sent with the edit was not used: %v", m["title"])
	}

	// A previously edited file as the source of its own edit.
	var editedID int64
	_ = s.srv.store.Pool.QueryRow(context.Background(), `SELECT id FROM media WHERE file_path LIKE '%halves-edited-1.png'`).Scan(&editedID)
	if editedID == 0 {
		t.Fatalf("no halves-edited-1.png (the second edit should be uniquely named); src %s", editedSrc)
	}
	eid := itoa64(editedID)
	code, m, _, _ = s.admin.do("POST", "/wp/v2/media/"+eid+"/edit", map[string]any{"src": editedSrc, "rotation": 90})
	if code != http.StatusCreated {
		t.Fatalf("edit of an edit: %d %v", code, m)
	}
	if su, _ := m["source_url"].(string); strings.Contains(su, "edited-1-edited") || strings.Contains(su, "edited-edited") {
		t.Errorf("edit of an edit: %s", su)
	}

	// Bad input.
	for name, tc := range map[string]struct {
		body map[string]any
		code string
		st   int
	}{
		"no src":          {map[string]any{"rotation": 90}, "rest_missing_callback_param", 400},
		"src of another":  {map[string]any{"src": "https://example.test/wp-content/uploads/other.png", "rotation": 90}, "rest_unknown_attachment", 404},
		"nothing to do":   {map[string]any{"src": src}, "rest_image_not_edited", 400},
		"empty modifiers": {map[string]any{"src": src, "modifiers": []any{}}, "rest_invalid_param", 400},
		"bad type":        {map[string]any{"src": src, "modifiers": []any{map[string]any{"type": "blur", "args": map[string]any{}}}}, "rest_invalid_param", 400},
		"no angle":        {map[string]any{"src": src, "modifiers": []any{map[string]any{"type": "rotate", "args": map[string]any{}}}}, "rest_invalid_param", 400},
		"crop > 100":      {map[string]any{"src": src, "modifiers": []any{map[string]any{"type": "crop", "args": map[string]any{"left": 0, "top": 0, "width": 150, "height": 10}}}}, "rest_invalid_param", 400},
		"crop negative":   {map[string]any{"src": src, "modifiers": []any{map[string]any{"type": "crop", "args": map[string]any{"left": -5, "top": 0, "width": 10, "height": 10}}}}, "rest_invalid_param", 400},
		"zero crop":       {map[string]any{"src": src, "x": 10, "y": 10, "width": 0, "height": 10}, "rest_invalid_param", 400},
		"rotation 360":    {map[string]any{"src": src, "rotation": 360}, "rest_invalid_param", 400},
		"rotation string": {map[string]any{"src": src, "rotation": "abc"}, "rest_invalid_param", 400},
		"tiny crop":       {map[string]any{"src": src, "modifiers": []any{map[string]any{"type": "crop", "args": map[string]any{"left": 0, "top": 0, "width": 0.1, "height": 0.1}}}}, "rest_image_crop_failed", 400},
	} {
		code, e, _, _ := s.admin.do("POST", editPath, tc.body)
		if code != tc.st || e["code"] != tc.code {
			t.Errorf("%s: %d %v, want %d %s", name, code, e, tc.st, tc.code)
		}
	}

	// Not an image.
	var adminID int64
	ctx := context.Background()
	_ = s.srv.store.Pool.QueryRow(ctx, `SELECT id FROM users WHERE login = 'admin'`).Scan(&adminID)
	pdf := &store.Attachment{UploaderID: adminID, Slug: "doc", FilePath: "2026/11/doc.pdf", Mime: "application/pdf", Title: "doc"}
	if err := s.srv.store.InsertAttachment(ctx, pdf); err != nil {
		t.Fatal(err)
	}
	if code, e, _, _ := s.admin.do("POST", "/wp/v2/media/"+itoa64(pdf.ID)+"/edit", map[string]any{"src": "https://x.test/wp-content/uploads/2026/11/doc.pdf", "rotation": 90}); code != 404 || e["code"] != "rest_unknown_attachment" {
		t.Errorf("pdf: %d %v", code, e)
	}
	if code, e, _, _ := s.admin.do("POST", "/wp/v2/media/999999/edit", map[string]any{"src": src, "rotation": 90}); code != 404 || e["code"] != "rest_post_invalid_id" {
		t.Errorf("missing attachment: %d %v", code, e)
	}

	// Permissions: upload_files, then edit_post on the attachment.
	body := map[string]any{"src": src, "rotation": 90}
	if code, e, _, _ := s.anon().do("POST", editPath, body); code != 401 || e["code"] != "rest_cannot_edit_image" {
		t.Errorf("anonymous: %d %v", code, e)
	}
	_, sub := s.user("subby", "subscriber")
	if code, e, _, _ := sub.do("POST", editPath, body); code != 403 || e["code"] != "rest_cannot_edit_image" {
		t.Errorf("subscriber: %d %v", code, e)
	}
	_, author := s.user("authy", "author")
	if code, e, _, _ := author.do("POST", editPath, body); code != 403 || e["code"] != "rest_cannot_edit" {
		t.Errorf("author on someone else's image: %d %v", code, e)
	}
	mine := rawUpload(t, author, "mine.png", "", halvesPNG(t, 40, 20))
	if code, e, _, _ := author.do("POST", "/wp/v2/media/"+itoa64(int64(mine["id"].(float64)))+"/edit", map[string]any{"src": mine["source_url"], "rotation": 90}); code != http.StatusCreated {
		t.Errorf("author on own image: %d %v", code, e)
	} else if e["author"] != mine["author"] {
		t.Errorf("the edited copy belongs to %v, not the author %v", e["author"], mine["author"])
	}
}

// An image saved by the wp-admin image editor keeps original_image pointing at the pre-edit upload;
// the block editor's edit applies to the edited main file (the one src names), not to that original.
func TestRESTMediaEditAfterImgedit(t *testing.T) {
	s, a, call := imgeditEnv(t)
	ctx := context.Background()
	// Pretend the 600x400 main file is a "-scaled" copy of a bigger, different original upload.
	if err := os.WriteFile(filepath.Join(s.srv.cfg.UploadsDir, "2026/11/pic-big.png"), halvesPNG(t, 1200, 800), 0o644); err != nil {
		t.Fatal(err)
	}
	a.Meta["original_image"] = "pic-big.png"
	if err := s.srv.store.UpdateAttachment(ctx, a); err != nil {
		t.Fatal(err)
	}
	if out := call(url.Values{"do": {"save"}, "target": {"all"}, "history": {`[{"c":{"x":0,"y":0,"w":"300","h":"200","r":0}}]`}}); out["success"] != true {
		t.Fatalf("imgedit save: %v", out)
	}
	got, _ := s.srv.store.GetAttachment(ctx, a.ID)
	if got.Meta["backup_file"] == nil || got.Meta["original_image"] != "pic-big.png" {
		t.Fatalf("after imgedit save: %v", got.Meta)
	}
	code, m, _, _ := s.admin.do("POST", "/wp/v2/media/"+itoa64(a.ID)+"/edit", map[string]any{
		"src":       "https://example.test/wp-content/uploads/" + got.FilePath,
		"modifiers": []any{map[string]any{"type": "rotate", "args": map[string]any{"angle": 90}}}})
	if code != http.StatusCreated {
		t.Fatalf("edit: %d %v", code, m)
	}
	d, _ := m["media_details"].(map[string]any)
	// The 300x200 crop, turned: 200x300 (the unedited original would give 800x1200).
	if d["width"] != float64(200) || d["height"] != float64(300) {
		t.Errorf("edited from the wrong file: %vx%v (%v)", d["width"], d["height"], d["file"])
	}
}

// Rotations by angles that are not multiples of 90 grow the canvas; the result of each one is
// bounded like the decoded source, and too many modifiers are refused.
func TestRESTMediaEditRotationBounds(t *testing.T) {
	s := secStart(t)
	orig := rawUpload(t, s.admin, "big.png", "", halvesPNG(t, 200, 100))
	src, _ := orig["source_url"].(string)
	editPath := "/wp/v2/media/" + itoa64(int64(orig["id"].(float64))) + "/edit"
	old := mediaEditMaxPixels
	mediaEditMaxPixels = 60_000
	t.Cleanup(func() { mediaEditMaxPixels = old })

	rot := func(angle float64) any {
		return map[string]any{"type": "rotate", "args": map[string]any{"angle": angle}}
	}
	flip := map[string]any{"type": "flip", "args": map[string]any{"flip": map[string]any{"horizontal": true, "vertical": false}}}

	// 45, flip, 45: the second turn's canvas (about 85K px) exceeds the bound.
	code, e, _, _ := s.admin.do("POST", editPath, map[string]any{"src": src, "modifiers": []any{rot(45), flip, rot(45)}})
	if code != http.StatusBadRequest || e["code"] != "rest_image_rotation_failed" {
		t.Errorf("growing rotations: %d %v", code, e)
	}
	// Consecutive rotations fold into one turn: 45 + 45 is a single 90 (100x200), within the bound.
	code, m, _, _ := s.admin.do("POST", editPath, map[string]any{"src": src, "modifiers": []any{rot(45), rot(45)}})
	if code != http.StatusCreated {
		t.Fatalf("folded rotations: %d %v", code, m)
	}
	if d, _ := m["media_details"].(map[string]any); d["width"] != float64(100) || d["height"] != float64(200) {
		t.Errorf("folded rotations: %vx%v", d["width"], d["height"])
	}
	// More than mediaEditMaxModifiers modifiers.
	many := make([]any, mediaEditMaxModifiers+1)
	for i := range many {
		many[i] = flip
	}
	if code, e, _, _ := s.admin.do("POST", editPath, map[string]any{"src": src, "modifiers": many}); code != http.StatusBadRequest || e["code"] != "rest_invalid_param" {
		t.Errorf("too many modifiers: %d %v", code, e)
	}
}
