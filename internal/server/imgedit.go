package server

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/media"
	"wpgo/internal/store"
)

// The media modal's "Edit Image" (wp-admin/includes/image-edit.php): admin-ajax image-editor and
// imgedit-preview, driven by WordPress's own image-edit.js. The editor markup is wp_image_editor()'s
// output, exported once by tools/wp-registry/imgedit.php.

//go:embed defaults/imgedit.html
var imgeditHTML string

func (s *Server) imageEditorAjax(c *gin.Context, action string) bool {
	if action != "image-editor" && action != "imgedit-preview" {
		return false
	}
	id, _ := strconv.ParseInt(defaultStr(c.PostForm("postid"), c.Query("postid")), 10, 64)
	a, err := s.store.GetAttachment(c, id)
	u := currentUser(c)
	if err != nil || (a.UploaderID != u.ID && !u.Can("edit_others_posts", nil)) || !strings.HasPrefix(a.Mime, "image/") {
		c.String(http.StatusOK, "-1")
		return true
	}
	if !s.checkNonceValue(c, defaultStr(c.PostForm("_ajax_nonce"), c.Query("_ajax_nonce")), "image_editor-"+itoa64(id)) {
		c.String(http.StatusForbidden, "-1")
		return true
	}
	if action == "imgedit-preview" {
		s.imgeditPreview(c, a)
		return true
	}
	var msg map[string]any
	switch c.PostForm("do") {
	case "save":
		msg = s.imgeditSave(c, a, false)
		if msg["error"] != nil {
			c.JSON(http.StatusOK, gin.H{"success": false, "data": msg})
		} else {
			c.JSON(http.StatusOK, gin.H{"success": true, "data": msg})
		}
		return true
	case "scale":
		msg = s.imgeditSave(c, a, true)
	case "restore":
		msg = s.imgeditRestore(c, a)
	}
	a, _ = s.store.GetAttachment(c, id)
	html := s.imgeditMarkup(c, a)
	if msg != nil && msg["error"] != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "data": gin.H{"message": msg, "html": html}})
		return true
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"message": msg, "html": html}})
	return true
}

// imgeditMarkup is wp_image_editor().
func (s *Server) imgeditMarkup(c *gin.Context, a *store.Attachment) string {
	h := imgeditHTML
	if _, ok := a.Meta["backup_file"].(string); !ok {
		i, j := strings.Index(h, "<!--RESTORE-->"), strings.Index(h, "<!--/RESTORE-->")
		if i >= 0 && j > i {
			h = h[:i] + h[j+len("<!--/RESTORE-->"):]
		}
	}
	r := strings.NewReplacer("424242", itoa64(a.ID), "NONCEVALUE", s.nonce(c, "image_editor-"+itoa64(a.ID)),
		"4447", strconv.Itoa(a.Width), "4448", strconv.Itoa(a.Height),
		"RATIOVALUE", strconv.FormatFloat(media.PreviewRatio(a.Width, a.Height), 'f', -1, 64),
		"RANDVALUE", strconv.Itoa(rand.IntN(99999)))
	return r.Replace(h)
}

// parseHistory decodes the editor's history JSON ([{"r":90},{"f":1},{"c":{x,y,w,h,r}}]).
func parseHistory(raw string) []media.Op {
	var steps []map[string]json.RawMessage
	_ = json.Unmarshal([]byte(raw), &steps)
	var ops []media.Op
	for _, st := range steps {
		switch {
		case st["r"] != nil:
			var n looseNumber
			_ = json.Unmarshal(st["r"], &n)
			ops = append(ops, media.Op{Rotate: int(n)})
		case st["f"] != nil:
			var n looseNumber
			_ = json.Unmarshal(st["f"], &n)
			ops = append(ops, media.Op{Flip: int(n)})
		case st["c"] != nil:
			// image-edit.js sends w and h as strings (straight from the input boxes) and x, y, r as numbers.
			var cr struct{ X, Y, W, H, R looseNumber }
			_ = json.Unmarshal(st["c"], &cr)
			ops = append(ops, media.Op{Crop: true, X: float64(cr.X), Y: float64(cr.Y), W: float64(cr.W), H: float64(cr.H), Ratio: float64(cr.R)})
		}
	}
	return ops
}

// imgeditPreview is stream_preview_image(): the current image with the history applied, preview-sized.
func (s *Server) imgeditPreview(c *gin.Context, a *store.Attachment) {
	img, format, err := media.Decode(s.cfg.UploadsDir, a.FilePath)
	if err != nil {
		c.String(http.StatusOK, "-1")
		return
	}
	img = media.ApplyOps(img, parseHistory(c.Query("history")))
	b := img.Bounds()
	if r := media.PreviewRatio(b.Dx(), b.Dy()); r < 1 {
		img = media.Resize(img, max(1, int(float64(b.Dx())*r)), max(1, int(float64(b.Dy())*r)))
	}
	if format != "jpeg" && format != "gif" {
		format = "png"
	}
	var buf bytes.Buffer
	if err := media.EncodeTo(&buf, img, format); err != nil {
		c.String(http.StatusOK, "-1")
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, "image/"+format, buf.Bytes())
}

// imgeditSave is wp_save_image(): applies the history (or scales), writes "<name>-e<time>.<ext>",
// regenerates the sub-sizes and keeps the original for "Restore image".
func (s *Server) imgeditSave(c *gin.Context, a *store.Attachment, scale bool) map[string]any {
	img, format, err := media.Decode(s.cfg.UploadsDir, a.FilePath)
	if err != nil {
		return map[string]any{"error": "Unable to edit this image."}
	}
	if format != "jpeg" && format != "png" && format != "gif" {
		return map[string]any{"error": "Images of this type can't be edited yet."}
	}
	target := c.DefaultPostForm("target", "all")
	if scale {
		fw, _ := strconv.Atoi(c.PostForm("fwidth"))
		fh, _ := strconv.Atoi(c.PostForm("fheight"))
		b := img.Bounds()
		if fw < 1 || fh < 1 || fw > b.Dx() || fh > b.Dy() {
			return map[string]any{"error": "Images cannot be scaled to a size larger than the original."}
		}
		img = media.Resize(img, fw, fh)
		target = "all"
	} else {
		ops := parseHistory(c.PostForm("history"))
		if len(ops) == 0 {
			return map[string]any{"error": "Nothing to save, the image has not changed."}
		}
		img = media.ApplyOps(img, ops)
	}
	dir, file := path.Split(a.FilePath)
	ext := path.Ext(file)
	base := strings.TrimSuffix(file, ext)
	if i := strings.LastIndex(base, "-e"); i > 0 && len(base)-i == 15 {
		base = base[:i] // drop a previous edit suffix
	}
	newRel := fmt.Sprintf("%s%s-e%d%s", dir, base, time.Now().UnixMilli(), ext)
	if err := media.WriteFile(s.cfg.UploadsDir, newRel, img, format); err != nil {
		return map[string]any{"error": "Unable to save the image."}
	}
	info, err := media.ProcessImage(s.cfg.UploadsDir, newRel, s.imageSizes())
	if err != nil {
		return map[string]any{"error": "Unable to save the image."}
	}
	if a.Meta == nil {
		a.Meta = map[string]any{}
	}
	if _, ok := a.Meta["backup_file"]; !ok {
		backup, _ := json.Marshal(a.Variants)
		a.Meta["backup_file"] = a.FilePath
		a.Meta["backup_size"] = []int{a.Width, a.Height}
		a.Meta["backup_variants"] = string(backup)
	}
	ndir := path.Dir(info.RelPath)
	var variants []store.AttachmentVariant
	for _, v := range info.Variants {
		variants = append(variants, store.AttachmentVariant{Name: v.Name, FilePath: path.Join(ndir, v.File), Width: v.Width, Height: v.Height, Mime: v.Mime, FileSize: v.Size})
	}
	switch target {
	case "thumbnail":
		// Only the thumbnail changes; the main file and other sizes stay.
		var keep []store.AttachmentVariant
		for _, v := range a.Variants {
			if v.Name != "thumbnail" {
				keep = append(keep, v)
			}
		}
		for _, v := range variants {
			if v.Name == "thumbnail" {
				keep = append(keep, v)
			}
		}
		a.Variants = keep
	case "nothumb":
		var thumb []store.AttachmentVariant
		for _, v := range a.Variants {
			if v.Name == "thumbnail" {
				thumb = append(thumb, v)
			}
		}
		for _, v := range variants {
			if v.Name != "thumbnail" {
				thumb = append(thumb, v)
			}
		}
		a.FilePath, a.Width, a.Height, a.FileSize = info.RelPath, info.Width, info.Height, info.FileSize
		a.Variants = thumb
	default:
		a.FilePath, a.Width, a.Height, a.FileSize = info.RelPath, info.Width, info.Height, info.FileSize
		a.Variants = variants
	}
	if err := s.store.ReplaceAttachmentFile(c, a); err != nil {
		return map[string]any{"error": "Unable to save the image."}
	}
	s.purgeAll(c)
	out := map[string]any{"msg": "Image saved", "fw": a.Width, "fh": a.Height}
	if t := a.Variant("thumbnail"); t != nil {
		out["thumbnail"] = s.uploadURL(c, t.FilePath)
	}
	if scale {
		out["msg"] = "Image scaled."
	}
	return out
}

// imgeditRestore is wp_restore_image().
func (s *Server) imgeditRestore(c *gin.Context, a *store.Attachment) map[string]any {
	backup, ok := a.Meta["backup_file"].(string)
	if !ok {
		return map[string]any{"error": "Image metadata is inconsistent."}
	}
	a.FilePath = backup
	if sz, ok := a.Meta["backup_size"].([]any); ok && len(sz) == 2 {
		w, _ := sz[0].(float64)
		h, _ := sz[1].(float64)
		a.Width, a.Height = int(w), int(h)
	}
	var vs []store.AttachmentVariant
	if raw, ok := a.Meta["backup_variants"].(string); ok {
		_ = json.Unmarshal([]byte(raw), &vs)
	}
	a.Variants = vs
	delete(a.Meta, "backup_file")
	delete(a.Meta, "backup_size")
	delete(a.Meta, "backup_variants")
	if err := s.store.ReplaceAttachmentFile(c, a); err != nil {
		return map[string]any{"error": "Image metadata is inconsistent."}
	}
	s.purgeAll(c)
	return map[string]any{"msg": "Image restored successfully."}
}

// looseNumber decodes a JSON number or a numeric string, the way PHP reads the editor's history.
type looseNumber float64

func (n *looseNumber) UnmarshalJSON(b []byte) error {
	v := strings.Trim(string(b), `"`)
	if v == "" || v == "null" {
		*n = 0
		return nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return nil // PHP casts junk to 0
	}
	*n = looseNumber(f)
	return nil
}
