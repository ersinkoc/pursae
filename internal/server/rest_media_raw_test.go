package server

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"testing"
)

// POST /wp/v2/media also takes the file as the raw body named by Content-Disposition, with the other
// fields in the query (WP_REST_Attachments_Controller::upload_from_data), as API clients send it.
func TestRESTMediaRawBodyUpload(t *testing.T) {
	ts, c := testServer(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	_, nonce, _ := get(t, c, base+"/wp-admin/admin-ajax?action=rest-nonce")
	img := image.NewRGBA(image.Rect(0, 0, 400, 300))
	for x := 0; x < 400; x++ {
		img.Set(x, 10, color.RGBA{200, 50, 50, 255})
	}
	var pngData bytes.Buffer
	_ = png.Encode(&pngData, img)

	send := func(disposition, query string, data []byte) (int, map[string]any) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, base+"/wp-json/wp/v2/media"+query, bytes.NewReader(data))
		req.Header.Set("Content-Type", "image/png")
		if disposition != "" {
			req.Header.Set("Content-Disposition", disposition)
		}
		req.Header.Set("X-WP-Nonce", nonce)
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		return resp.StatusCode, out
	}

	code, m := send(`attachment; filename="raw-photo.png"`, "?alt_text=Red+line&title=Raw+photo", pngData.Bytes())
	if code != http.StatusCreated {
		t.Fatalf("raw upload: %d %v", code, m)
	}
	title, _ := m["title"].(map[string]any)
	if m["mime_type"] != "image/png" || m["alt_text"] != "Red line" || title["raw"] != "Raw photo" {
		t.Errorf("raw upload fields: mime %v alt %v title %v", m["mime_type"], m["alt_text"], title)
	}
	d, _ := m["media_details"].(map[string]any)
	if sizes, _ := d["sizes"].(map[string]any); sizes["thumbnail"] == nil {
		t.Errorf("no sub-sizes for a raw upload: %v", d)
	}

	// No file name: nothing was uploaded; an empty body: no data; a path in the name is dropped.
	if code, m = send("", "", pngData.Bytes()); code == http.StatusCreated {
		t.Errorf("an upload without Content-Disposition was accepted: %v", m)
	}
	if code, m = send(`attachment; filename="empty.png"`, "", nil); code != http.StatusBadRequest || m["code"] != "rest_upload_no_data" {
		t.Errorf("empty raw upload: %d %v", code, m)
	}
	if code, m = send(`attachment; filename="../../evil.png"`, "", pngData.Bytes()); code != http.StatusCreated {
		t.Fatalf("raw upload with a path: %d %v", code, m)
	} else if src, _ := m["source_url"].(string); bytes.Contains([]byte(src), []byte("..")) {
		t.Errorf("the path survived: %s", src)
	}
}
