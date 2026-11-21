package server

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"wpgo/internal/media"
	"wpgo/internal/store"
)

// POST /wp/v2/media/{id}/edit is WP_REST_Attachments_Controller::edit_media_item(): the block
// editor's image crop/rotate toolbar (and the media editor of the editor package) sends the edits as
// "modifiers"; the edited image is saved as a new attachment "<name>-edited.<ext>" and returned with
// 201. The original attachment is left untouched.

// mediaEditOp is one modifier, applied in order.
type mediaEditOp struct {
	kind                     string // rotate, crop, flip
	angle                    float64
	left, top, width, height float64
	horizontal, vertical     bool
}

// mediaEditTypes are the MIME types edit_media_item() accepts that can be decoded and written here
// (WordPress also lists image/avif and image/heic, which are not decoded).
var mediaEditTypes = map[string]bool{"image/jpeg": true, "image/png": true, "image/gif": true, "image/webp": true}

var mediaEditedSuffix = regexp.MustCompile(`-edited(-\d+)?$`)

func (api *restAPI) editMedia(c *gin.Context, r *restRequest) {
	s := api.s
	u := restCurrent(c)
	// edit_media_item_permissions_check(): upload_files, then update_item_permissions_check().
	if u == nil || !u.Can("upload_files", nil) {
		forbidden(c, "rest_cannot_edit_image", "Sorry, you are not allowed to upload media on this site.")
		return
	}
	a, err := s.store.GetAttachment(c, r.ID())
	if err != nil {
		restErr(c, http.StatusNotFound, "rest_post_invalid_id", "Invalid post ID.")
		return
	}
	if !mediaEditable(u, a) {
		forbidden(c, "rest_cannot_edit", "Sorry, you are not allowed to edit this post.")
		return
	}

	src, ok := r.Str(c, "src")
	if !ok || strings.TrimSpace(src) == "" {
		restErr(c, http.StatusBadRequest, "rest_missing_callback_param", "Missing parameter(s): src", map[string]any{"params": []string{"src"}})
		return
	}
	ops, param, msg := parseMediaEditOps(c, r)
	if param != "" {
		restErr(c, http.StatusBadRequest, "rest_invalid_param", "Invalid parameter(s): "+param, map[string]any{"params": map[string]string{param: msg}})
		return
	}

	// wp_get_original_image_path() and wp_image_file_matches_image_meta(): an image, and src one of its files.
	if !strings.HasPrefix(a.Mime, "image/") || a.Width == 0 || !mediaEditSrcMatches(src, a) {
		restErr(c, http.StatusNotFound, "rest_unknown_attachment", "Unable to get meta information for file.")
		return
	}
	if !mediaEditTypes[a.Mime] {
		restErr(c, http.StatusBadRequest, "rest_cannot_edit_file_type", "This type of file cannot be edited.")
		return
	}
	if len(ops) == 0 {
		restErr(c, http.StatusBadRequest, "rest_image_not_edited", "The image was not edited. Edit the image before applying the changes.")
		return
	}

	rel := mediaEditSource(s, c, a)
	img, format, err := media.Decode(s.cfg.UploadsDir, rel)
	if err != nil || !mediaEditWritable(format) {
		slog.Warn("media edit: the image could not be opened", "attachment", a.ID, "file", rel, "err", err)
		restErr(c, http.StatusInternalServerError, "rest_unknown_image_file_type", "Unable to edit this image.")
		return
	}
	if format == "jpeg" && rel != a.FilePath {
		// The original upload may still carry its EXIF orientation; the editor shows it upright.
		if f, err := os.Open(filepath.Join(s.cfg.UploadsDir, filepath.FromSlash(rel))); err == nil {
			if o := media.JPEGOrientation(f); o > 1 {
				img = media.Orient(img, o)
			}
			_ = f.Close()
		}
	}

	for _, op := range ops {
		switch op.kind {
		case "rotate":
			// The client's angle is clockwise; WP_Image_Editor::rotate() turns counter-clockwise.
			if op.angle != 0 {
				img = media.RotateDegrees(img, -op.angle)
			}
		case "flip":
			img = media.Flip(img, op.horizontal, op.vertical)
		case "crop":
			b := img.Bounds()
			x := int(math.Round(float64(b.Dx()) * op.left / 100))
			y := int(math.Round(float64(b.Dy()) * op.top / 100))
			w := int(math.Round(float64(b.Dx()) * op.width / 100))
			h := int(math.Round(float64(b.Dy()) * op.height / 100))
			if w == b.Dx() && h == b.Dy() {
				continue
			}
			cropped, ok := media.Crop(img, image.Rect(x, y, x+w, y+h))
			if !ok || w < 1 || h < 1 {
				restErr(c, http.StatusBadRequest, "rest_image_crop_failed", "Unable to crop this image.")
				return
			}
			img = cropped
		}
	}

	// The file name: "<name>-edited.<ext>", never "-edited-edited" (wp_unique_filename adds -1, -2 …).
	ext := path.Ext(rel)
	name := strings.TrimSuffix(path.Base(rel), ext)
	if mediaEditedSuffix.MatchString(name) {
		name = mediaEditedSuffix.ReplaceAllString(name, "-edited")
	} else {
		name += "-edited"
	}
	var buf bytes.Buffer
	if err := media.EncodeTo(&buf, img, format); err != nil {
		slog.Warn("media edit: the image could not be encoded", "attachment", a.ID, "err", err)
		restErr(c, http.StatusInternalServerError, "rest_image_save_failed", "Unable to save the edited image.")
		return
	}
	na, err := s.insertAttachmentFile(c, name+ext, &buf, u.ID, 0)
	if err != nil {
		slog.Warn("media edit: the edited image could not be saved", "attachment", a.ID, "err", err)
		restErr(c, http.StatusInternalServerError, "rest_image_save_failed", "Unable to save the edited image.")
		return
	}
	// Title, description, caption and alt text come from the edited attachment; the editor package
	// also sends its unsaved edits of those fields (and the parent post) with the request.
	na.Title, na.Description, na.Caption, na.Alt = a.Title, a.Description, a.Caption, a.Alt
	for field, dst := range map[string]*string{"title": &na.Title, "caption": &na.Caption, "description": &na.Description, "alt_text": &na.Alt} {
		if v, ok := r.Str(c, field); ok {
			*dst = v
		}
	}
	if v, ok := r.Int(c, "post"); ok && v >= 0 {
		if v == 0 || s.mediaEditParentOK(c, u, v) {
			na.PostID = v
		}
	}
	ksesAttachmentText(u, na)
	if err := s.store.UpdateAttachment(c, na); err != nil {
		slog.Warn("media edit: the new attachment could not be updated", "attachment", na.ID, "err", err)
	}
	s.purgeAttachment(na)
	c.Header("X-WP-Upload-Attachment-ID", itoa64(na.ID))
	c.Header("Location", s.links(c).HomeURL("/wp-json/wp/v2/media/"+itoa64(na.ID)))
	restJSON(c, http.StatusCreated, s.restMedia(c, na, "edit"))
}

// mediaEditParentOK reports whether u may attach media to post id (edit_post on it).
func (s *Server) mediaEditParentOK(c *gin.Context, u *store.User, id int64) bool {
	p, err := s.store.GetPost(c, id)
	return err == nil && u.Can("edit_post", p)
}

// mediaEditSource is the file the edits apply to: the original upload when the main file is a
// "-scaled" or "-rotated" copy of it (wp_get_original_image_path()), else the attachment's file.
func mediaEditSource(s *Server, c *gin.Context, a *store.Attachment) string {
	if orig, ok := a.Meta["original_image"].(string); ok && orig != "" && path.Base(orig) == orig {
		rel := path.Join(path.Dir(a.FilePath), orig)
		_ = s.gocdnEnsureLocal(c, rel) // GoCDN: the file may live only in the bucket
		if _, err := os.Stat(filepath.Join(s.cfg.UploadsDir, filepath.FromSlash(rel))); err == nil && mediaEditTypes[media.MimeFor(rel)] {
			return rel
		}
	}
	_ = s.gocdnEnsureLocal(c, a.FilePath)
	return a.FilePath
}

func mediaEditWritable(format string) bool {
	switch format {
	case "jpeg", "png", "gif", "webp":
		return true
	}
	return false
}

// mediaEditSrcMatches is wp_image_file_matches_image_meta(): the file name of src is the
// attachment's file, its original upload or one of its sizes. The host is not compared (a CDN or
// the image service may serve the file).
func mediaEditSrcMatches(src string, a *store.Attachment) bool {
	u, err := url.Parse(strings.TrimSpace(src))
	if err != nil {
		return false
	}
	p := u.Path
	if dp, err := url.PathUnescape(p); err == nil {
		p = dp
	}
	base := path.Base(p)
	if base == "" || base == "." || base == "/" {
		return false
	}
	names := []string{path.Base(a.FilePath)}
	if orig, ok := a.Meta["original_image"].(string); ok && orig != "" {
		names = append(names, path.Base(orig))
	}
	for _, v := range a.Variants {
		names = append(names, path.Base(v.FilePath))
	}
	for _, n := range names {
		if n == base {
			return true
		}
	}
	return false
}

// parseMediaEditOps reads "modifiers" or, without it, the legacy rotation / x, y, width, height
// parameters (the "modifiers" param takes precedence, as in WordPress). On invalid input it returns
// the parameter name and the reason.
func parseMediaEditOps(c *gin.Context, r *restRequest) (ops []mediaEditOp, param, msg string) {
	if raw, ok := r.Body["modifiers"]; ok {
		list, ok := raw.([]any)
		if !ok {
			return nil, "modifiers", "modifiers is not of type array."
		}
		if len(list) == 0 {
			return nil, "modifiers", "modifiers must contain at least 1 items."
		}
		if len(list) > 32 {
			return nil, "modifiers", "modifiers must contain at most 32 items."
		}
		for i, item := range list {
			op, err := parseMediaEditModifier(item)
			if err != nil {
				return nil, "modifiers", fmt.Sprintf("modifiers[%d] %s.", i, err.Error())
			}
			ops = append(ops, op)
		}
		return ops, "", ""
	}
	if v, ok := r.get(c, "rotation"); ok && v != nil && v != "" {
		n, ok := mediaEditNumber(v)
		if !ok || n != math.Trunc(n) {
			return nil, "rotation", "rotation is not of type integer."
		}
		if n <= 0 || n >= 360 {
			return nil, "rotation", "rotation must be between 0 (exclusive) and 360 (exclusive)"
		}
		ops = append(ops, mediaEditOp{kind: "rotate", angle: n})
	}
	var crop [4]float64
	have := 0
	for i, key := range []string{"x", "y", "width", "height"} {
		v, ok := r.get(c, key)
		if !ok || v == nil {
			continue
		}
		n, ok := mediaEditNumber(v)
		if !ok {
			return nil, key, key + " is not of type number."
		}
		if n < 0 || n > 100 {
			return nil, key, key + " must be between 0 (inclusive) and 100 (inclusive)"
		}
		crop[i] = n
		have++
	}
	if have == 4 {
		op := mediaEditOp{kind: "crop", left: crop[0], top: crop[1], width: crop[2], height: crop[3]}
		if op.width <= 0 || op.height <= 0 {
			return nil, "width", "the crop must be wider and taller than 0"
		}
		ops = append(ops, op)
	}
	return ops, "", ""
}

func parseMediaEditModifier(item any) (mediaEditOp, error) {
	m, ok := item.(map[string]any)
	if !ok {
		return mediaEditOp{}, errors.New("is not of type object")
	}
	typ, _ := m["type"].(string)
	args, ok := m["args"].(map[string]any)
	if !ok {
		return mediaEditOp{}, errors.New("args is a required property")
	}
	num := func(key string, lo, hi float64) (float64, error) {
		v, ok := args[key]
		if !ok {
			return 0, fmt.Errorf("args[%s] is a required property", key)
		}
		n, ok := mediaEditNumber(v)
		if !ok || math.IsNaN(n) || math.IsInf(n, 0) {
			return 0, fmt.Errorf("args[%s] is not of type number", key)
		}
		if n < lo || n > hi {
			return 0, fmt.Errorf("args[%s] must be between %g and %g", key, lo, hi)
		}
		return n, nil
	}
	switch typ {
	case "rotate":
		a, err := num("angle", -3600, 3600)
		return mediaEditOp{kind: "rotate", angle: a}, err
	case "crop":
		op := mediaEditOp{kind: "crop"}
		for _, f := range []struct {
			key string
			dst *float64
		}{{"left", &op.left}, {"top", &op.top}, {"width", &op.width}, {"height", &op.height}} {
			n, err := num(f.key, 0, 100)
			if err != nil {
				return op, err
			}
			*f.dst = n
		}
		if op.width <= 0 || op.height <= 0 {
			return op, errors.New("args[width] and args[height] must be greater than 0")
		}
		return op, nil
	case "flip":
		fl, ok := args["flip"].(map[string]any)
		if !ok {
			return mediaEditOp{}, errors.New("args[flip] is a required property")
		}
		op := mediaEditOp{kind: "flip"}
		for key, dst := range map[string]*bool{"horizontal": &op.horizontal, "vertical": &op.vertical} {
			v, ok := fl[key]
			if !ok {
				return op, fmt.Errorf("args[flip][%s] is a required property", key)
			}
			b, ok := v.(bool)
			if !ok {
				return op, fmt.Errorf("args[flip][%s] is not of type boolean", key)
			}
			*dst = b
		}
		return op, nil
	}
	return mediaEditOp{}, errors.New("type is not one of rotate, crop and flip")
}

// mediaEditNumber reads a JSON number or a numeric string (form-encoded requests).
func mediaEditNumber(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, !math.IsNaN(t) && !math.IsInf(t, 0)
	case string:
		n, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		return n, err == nil && !math.IsNaN(n) && !math.IsInf(n, 0)
	}
	return 0, false
}
