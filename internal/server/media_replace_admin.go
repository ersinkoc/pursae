package server

import (
	"errors"
	"fmt"
	"html"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"wpgo/internal/media"
	"wpgo/internal/store"
)

// The admin side of media replacement and focal points (media_replace.go): the Replace media and
// Focal point boxes of the attachment edit screen, the media modal's focal point field and links.

// canEditAttachment is the rule of every attachment edit: upload_files, and one's own upload or
// edit_others_posts.
func canEditAttachment(u *store.User, a *store.Attachment) bool {
	return u != nil && u.Can("upload_files", nil) && (a.UploaderID == u.ID || u.Can("edit_others_posts", nil))
}

// mediaReplaceSubmit is the Replace media box's form (POST /wp-admin/media-replace).
func (s *Server) mediaReplaceSubmit(c *gin.Context) {
	u := currentUser(c)
	id, _ := strconv.ParseInt(c.PostForm("post_ID"), 10, 64)
	a, err := s.store.GetAttachment(c, id)
	if err != nil {
		c.String(http.StatusNotFound, "Invalid attachment ID.")
		return
	}
	if !s.checkNonce(c, "media-replace_"+itoa64(id)) {
		s.denyNonce(c)
		return
	}
	if !canEditAttachment(u, a) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to edit this item.")
		return
	}
	back := "/wp-admin/post?post=" + itoa64(id) + "&action=edit"
	fh, err := c.FormFile("replacement")
	if err != nil {
		c.Redirect(http.StatusFound, back+"&replace_error="+url.QueryEscape("No file was uploaded.")+"#wpgo-replace-media")
		return
	}
	if fh.Size > maxUploadBytes {
		c.Redirect(http.StatusFound, back+"&replace_error="+url.QueryEscape(mediaErrText(errUploadTooLarge))+"#wpgo-replace-media")
		return
	}
	f, err := fh.Open()
	if err != nil {
		c.Redirect(http.StatusFound, back+"&replace_error="+url.QueryEscape(err.Error())+"#wpgo-replace-media")
		return
	}
	defer f.Close()
	warning, err := s.replaceAttachment(c, a, fh.Filename, f)
	if err != nil {
		c.Redirect(http.StatusFound, back+"&replace_error="+url.QueryEscape(mediaErrText(err))+"#wpgo-replace-media")
		return
	}
	q := "&replaced=1"
	if warning != "" {
		q += "&replace_warning=" + url.QueryEscape(warning)
	}
	c.Redirect(http.StatusFound, back+q)
}

// mediaReplaceNotices are the edit screen's notices after a replacement.
func mediaReplaceNotices(c *gin.Context) []notice {
	var out []notice
	if c.Query("replaced") == "1" {
		out = append(out, notice{Type: "success", Message: "Media file replaced. Everything that uses it now shows the new file.", Dismissible: true})
	}
	if w := c.Query("replace_warning"); w != "" {
		out = append(out, notice{Type: "warning", Message: template.HTML(template.HTMLEscapeString(w)), Dismissible: true})
	}
	if e := c.Query("replace_error"); e != "" {
		out = append(out, notice{Type: "error", Message: template.HTML("The file could not be replaced. " + template.HTMLEscapeString(e)), Dismissible: true})
	}
	return out
}

// mediaReplaceAccept is the file input's accept list: the same kind as the current file.
func mediaReplaceAccept(mime string) string {
	switch k := mediaKind(mime); k {
	case "image", "audio", "video":
		return k + "/*"
	}
	return ""
}

// focalPickerData is what the Focal point box shows.
type focalPickerData struct {
	Image, Value, Label string
	X, Y                string // the dot's position, CSS percentages
}

func (s *Server) focalPicker(c *gin.Context, a *store.Attachment) *focalPickerData {
	if !strings.HasPrefix(a.Mime, "image/") || a.Width <= 0 || a.Height <= 0 {
		return nil
	}
	img := s.uploadURL(c, a.FilePath)
	for _, n := range []string{"medium_large", "large", "medium"} {
		if v := a.Variant(n); v != nil {
			img = s.uploadURL(c, v.FilePath)
			break
		}
	}
	f := media.Focal{X: 0.5, Y: 0.5}
	value := ""
	if p := attachmentFocal(a); p != nil {
		f = *p
		value = strconv.FormatFloat(p.X, 'f', -1, 64) + "," + strconv.FormatFloat(p.Y, 'f', -1, 64)
	}
	x, y, _ := strings.Cut(f.CSS(), " ")
	return &focalPickerData{Image: img, Value: value, Label: focalLabel(f), X: x, Y: y}
}

// focalLabel describes a focal point for people: "Center" or "30% from the left, 20% from the top".
func focalLabel(f media.Focal) string {
	if f.Centered() {
		return "Center (default)"
	}
	x, y, _ := strings.Cut(f.CSS(), " ")
	return x + " from the left, " + y + " from the top"
}

// focalCompatHTML is the media modal's Focal point field (an attachment_fields_to_edit row: the
// modal saves it with save-attachment-compat).
func (s *Server) focalCompatHTML(c *gin.Context, a *store.Attachment) string {
	fp := s.focalPicker(c, a)
	if fp == nil || !canEditAttachment(currentUser(c), a) {
		return ""
	}
	id := itoa64(a.ID)
	return fmt.Sprintf(`<table class="compat-attachment-fields wpgo-focal-compat"><tbody><tr class="compat-field-focal_point"><th scope="row" class="label"><label for="attachments-%[1]s-focal_point"><span class="alignleft">Focal point</span><br class="clear" /></label></th><td class="field"><div class="wpgo-focal">`+
		`<div class="wpgo-focal-picker" tabindex="0" role="button" aria-label="Focal point: click the part of the image to keep in view"><img src="%[2]s" alt="" draggable="false" /><span class="wpgo-focal-dot" style="left:%[3]s;top:%[4]s"></span></div>`+
		`<input type="hidden" class="wpgo-focal-value" id="attachments-%[1]s-focal_point" name="attachments[%[1]s][focal_point]" value="%[5]s" />`+
		`<p class="help"><span class="wpgo-focal-label">%[6]s</span> <button type="button" class="button-link wpgo-focal-reset">Reset</button><br />Cropped sizes and cover images keep this point in view.</p></div></td></tr></tbody></table>`,
		id, html.EscapeString(fp.Image), fp.X, fp.Y, html.EscapeString(fp.Value), html.EscapeString(fp.Label))
}

// focalCompatSave is save-attachment-compat's part for the focal point field.
func (s *Server) focalCompatSave(c *gin.Context, a *store.Attachment) error {
	raw, ok := c.GetPostForm("attachments[" + itoa64(a.ID) + "][focal_point]")
	if !ok {
		return nil
	}
	f, valid := parseFocal(raw)
	if !valid {
		return errors.New("invalid focal point")
	}
	return s.setAttachmentFocal(c, a, f)
}

// focalAssets are the focal point picker's styles and script, for the edit screen and the media
// modal: a click (or the arrow keys) moves the point, the hidden input gets "x,y" ("" for the
// centre), the previews follow, and in the modal the field is saved after a short pause.
const focalAssets = `<style id="wpgo-focal-css">
.wpgo-focal-picker{position:relative;display:inline-block;max-width:100%;cursor:crosshair;line-height:0;outline-offset:2px}
.wpgo-focal-picker img{display:block;max-width:100%;height:auto;user-select:none}
.wpgo-focal-dot{position:absolute;width:22px;height:22px;margin:-11px 0 0 -11px;box-sizing:border-box;border:3px solid #fff;border-radius:50%;background:rgba(34,113,177,.55);box-shadow:0 0 0 1px rgba(0,0,0,.5),0 1px 4px rgba(0,0,0,.4);pointer-events:none}
.wpgo-focal-previews{display:flex;flex-wrap:wrap;gap:12px;margin-top:12px}
.wpgo-focal-previews figure{margin:0;text-align:center;font-size:12px;color:#646970}
.wpgo-focal-previews img{display:block;object-fit:cover;background:#f0f0f1;margin-bottom:4px}
.attachment-info .wpgo-replace-attachment{display:block;margin-top:4px}
.compat-field-focal_point .field{width:100%}
</style>
<script id="wpgo-focal-js">
(function () {
	if (window.wpgoFocal) { return; }
	window.wpgoFocal = true;
	function round(v) { return Math.round(v * 1000) / 10; }
	function set(box, x, y, save) {
		x = Math.min(1, Math.max(0, x)); y = Math.min(1, Math.max(0, y));
		var dot = box.querySelector('.wpgo-focal-dot'), input = box.querySelector('.wpgo-focal-value'), label = box.querySelector('.wpgo-focal-label');
		var pos = round(x) + '% ' + round(y) + '%', centre = Math.abs(x - 0.5) < 0.005 && Math.abs(y - 0.5) < 0.005;
		if (dot) { dot.style.left = round(x) + '%'; dot.style.top = round(y) + '%'; }
		if (label) { label.textContent = centre ? 'Center (default)' : round(x) + '% from the left, ' + round(y) + '% from the top'; }
		box.querySelectorAll('.wpgo-focal-previews img').forEach(function (img) { img.style.objectPosition = pos; });
		box.dataset.x = x; box.dataset.y = y;
		if (!input) { return; }
		input.value = centre ? '' : x.toFixed(4) + ',' + y.toFixed(4);
		if (save && input.form && input.form.classList.contains('compat-item') && window.jQuery) {
			clearTimeout(box._wpgoSave);
			box._wpgoSave = setTimeout(function () { window.jQuery(input).trigger('change'); }, 700);
		}
	}
	function current(box) {
		var dot = box.querySelector('.wpgo-focal-dot');
		var x = parseFloat(box.dataset.x), y = parseFloat(box.dataset.y);
		if (isNaN(x)) { x = parseFloat(dot && dot.style.left) / 100; }
		if (isNaN(y)) { y = parseFloat(dot && dot.style.top) / 100; }
		return [isNaN(x) ? 0.5 : x, isNaN(y) ? 0.5 : y];
	}
	document.addEventListener('click', function (e) {
		var picker = e.target.closest && e.target.closest('.wpgo-focal-picker');
		var box = e.target.closest && e.target.closest('.wpgo-focal');
		if (!box) { return; }
		if (picker) {
			var r = picker.querySelector('img').getBoundingClientRect();
			if (r.width > 0 && r.height > 0) { set(box, (e.clientX - r.left) / r.width, (e.clientY - r.top) / r.height, true); }
		} else if (e.target.closest('.wpgo-focal-reset')) {
			e.preventDefault();
			set(box, 0.5, 0.5, true);
		}
	});
	document.addEventListener('keydown', function (e) {
		var picker = e.target.classList && e.target.classList.contains('wpgo-focal-picker') ? e.target : null;
		if (!picker) { return; }
		var d = { ArrowLeft: [-1, 0], ArrowRight: [1, 0], ArrowUp: [0, -1], ArrowDown: [0, 1] }[e.key];
		if (!d) { return; }
		e.preventDefault();
		var box = picker.closest('.wpgo-focal'), p = current(box), step = e.shiftKey ? 0.1 : 0.01;
		set(box, p[0] + d[0] * step, p[1] + d[1] * step, true);
	});
})();
</script>`

// pursaeMediaReplace is POST /pursae/v1/media/{id}/replace: the new file as multipart "file", or
// as the raw body named by Content-Disposition (like POST /wp/v2/media). It answers with the
// attachment as GET /wp/v2/media/{id} shows it.
func (s *Server) pursaeMediaReplace(c *gin.Context, r *restRequest) {
	u := restCurrent(c)
	a, err := s.store.GetAttachment(c, r.ID())
	if err != nil {
		restErr(c, http.StatusNotFound, "rest_post_invalid_id", "Invalid post ID.")
		return
	}
	if !canEditAttachment(u, a) {
		restErr(c, http.StatusForbidden, "rest_cannot_edit", "Sorry, you are not allowed to edit this post.")
		return
	}
	var warning string
	if name, ok := restRawUploadName(c); ok {
		warning, err = s.replaceAttachment(c, a, name, c.Request.Body)
	} else {
		fh, ferr := c.FormFile("file")
		if ferr != nil {
			restErr(c, http.StatusBadRequest, "rest_upload_no_data", "No data supplied.")
			return
		}
		if fh.Size > maxUploadBytes {
			restErr(c, http.StatusBadRequest, "rest_upload_file_too_big", mediaErrText(errUploadTooLarge))
			return
		}
		f, oerr := fh.Open()
		if oerr != nil {
			restErr(c, http.StatusInternalServerError, "rest_upload_unknown_error", oerr.Error())
			return
		}
		defer f.Close()
		warning, err = s.replaceAttachment(c, a, fh.Filename, f)
	}
	switch {
	case errors.Is(err, errUploadEmpty):
		restErr(c, http.StatusBadRequest, "rest_upload_no_data", "No data supplied.")
		return
	case errors.Is(err, errUploadTooLarge):
		restErr(c, http.StatusBadRequest, "rest_upload_file_too_big", mediaErrText(err))
		return
	case errors.Is(err, errMediaReplaceType), errors.Is(err, media.ErrType), errors.Is(err, media.ErrSVG):
		restErr(c, http.StatusBadRequest, "rest_upload_invalid_type", err.Error())
		return
	case err != nil:
		restErr(c, http.StatusInternalServerError, "rest_upload_unknown_error", err.Error())
		return
	}
	out := s.restMedia(c, a, "edit")
	if warning != "" {
		out["upload_warning"] = warning
	}
	restJSON(c, http.StatusOK, out)
}
