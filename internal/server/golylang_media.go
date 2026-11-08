package server

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
	"wpgo/internal/wp"
)

// Pursae Multi Language: per-language texts of Media Library items (alternative text, title,
// caption, description). One media row and one file per item; the media row holds the default
// language and pll_media_i18n holds overrides (a NULL column falls back to the media row).
//
//   - Reads: store.AttachmentFilter overlays the request language on every attachment read on the
//     front end (and REST view requests with ?lang=), so featured images, logos, Web Builder
//     dynamic images, Commerce, custom fields, SEO and attachment pages switch language with no
//     per-renderer code. Base keeps the media row's texts and UpdateAttachment writes Base.
//   - Static content: pllContentMedia (called from filterContentTags) swaps the alt text of
//     <img class="wp-image-N"> (or data-wb-media-id, or an uploads URL) and the figcaption of its
//     figure when they still hold the media row's text.
//   - Admin: media modal compat fields, a Translations box on the edit screen, language columns in
//     the list view, pll_i18n on wp/v2/media (context=edit) and GET|POST pll/v1/media/{id}/i18n.
//
// Everything is gated by the plugin being on and the media module (MediaSupport).

// pllMediaField is one translatable text: its form key, REST key and pll_media_i18n column.
type pllMediaField struct {
	Key, REST, Col, Label string
}

var pllMediaFields = []pllMediaField{
	{"alt", "alt_text", "alt_text", "Alternative Text"},
	{"title", "title", "title", "Title"},
	{"caption", "caption", "caption", "Caption"},
	{"description", "description", "description", "Description"},
}

// pllMediaMaxLen caps one text (runes).
const pllMediaMaxLen = 20000

// pllMediaText is one language's overrides, keyed by pllMediaField.Key; nil means "use the media row".
type pllMediaText map[string]*string

func (t pllMediaText) empty() bool {
	for _, v := range t {
		if v != nil {
			return false
		}
	}
	return true
}

func (t pllMediaText) json() map[string]any {
	out := map[string]any{}
	for _, f := range pllMediaFields {
		if v := t[f.Key]; v != nil {
			out[f.REST] = *v
		} else {
			out[f.REST] = nil
		}
	}
	return out
}

func init() {
	store.AttachmentFilter = pllAttachmentFilter
}

// pllMediaOn reports whether the media module is on (plugin on, MediaSupport).
func (s *Server) pllMediaOn() bool {
	return s.golylangOn() && s.pllOpts().MediaSupport
}

// pllMediaLang is the language whose overrides apply to attachments read with ctx: a front request
// or a REST view request (GET with ?lang=, context other than edit) in a non-default language, with
// the media module on. nil everywhere else (admin, writes, default language, plugin off).
func pllMediaLang(ctx context.Context) *pllLanguage {
	r := pllFrom(ctx)
	if r == nil || r.srv == nil || r.Lang == nil || r.Lang.IsDefault {
		return nil
	}
	if !r.Front && !pllMediaRESTView(ctx) {
		return nil
	}
	if v, ok := r.cache.Load("pll:media_on"); ok {
		if on, _ := v.(bool); on {
			return r.Lang
		}
		return nil
	}
	on := r.srv.pllMediaOn()
	r.cache.Store("pll:media_on", on)
	if !on {
		return nil
	}
	return r.Lang
}

// pllMediaRESTView reports whether ctx is a REST read in view context: only a *gin.Context tells,
// so other contexts never get overlays outside the front end.
func pllMediaRESTView(ctx context.Context) bool {
	c, ok := ctx.(*gin.Context)
	if !ok || c.Request == nil || c.Request.Method != http.MethodGet {
		return false
	}
	p := c.Request.URL.Path
	if !strings.HasPrefix(p, "/wp-json") && !(strings.Trim(p, "/") == "" && c.Query("rest_route") != "") {
		return false
	}
	if c.Query("_method") != "" || c.GetHeader("X-HTTP-Method-Override") != "" {
		return false
	}
	return c.Query("context") != "edit"
}

// pllAttachmentFilter is store.AttachmentFilter: one query per call (ids not yet seen in this request).
func pllAttachmentFilter(ctx context.Context, list []*store.Attachment) {
	l := pllMediaLang(ctx)
	if l == nil {
		return
	}
	r := pllFrom(ctx)
	if !r.srv.pllMediaHasLang(ctx, l.Slug) {
		return
	}
	ids := make([]int64, 0, len(list))
	for _, a := range list {
		if a != nil && a.Base == nil {
			ids = append(ids, a.ID)
		}
	}
	rows := r.srv.pllMediaRowsCached(ctx, r, l.Slug, ids)
	for _, a := range list {
		if a == nil || a.Base != nil {
			continue
		}
		t := rows[a.ID]
		if t == nil || t.empty() {
			continue
		}
		a.Base = &store.AttachmentText{Alt: a.Alt, Title: a.Title, Caption: a.Caption, Description: a.Description}
		for key, dst := range map[string]*string{"alt": &a.Alt, "title": &a.Title, "caption": &a.Caption, "description": &a.Description} {
			if v := t[key]; v != nil {
				*dst = *v
			}
		}
	}
}

// pllMediaRowsCached loads one language's overrides through the request cache: ids already looked
// up in this request cost nothing.
func (s *Server) pllMediaRowsCached(ctx context.Context, r *pllReq, lang string, ids []int64) map[int64]pllMediaText {
	out := map[int64]pllMediaText{}
	var need []int64
	key := func(id int64) string { return "pll:mi18n:" + lang + ":" + itoa64(id) }
	for _, id := range ids {
		if v, ok := r.cache.Load(key(id)); ok {
			if t, _ := v.(pllMediaText); t != nil {
				out[id] = t
			}
			continue
		}
		need = append(need, id)
	}
	if len(need) == 0 {
		return out
	}
	got := s.pllMediaRows(ctx, lang, need)
	for _, id := range need {
		t := got[id][lang]
		r.cache.Store(key(id), t)
		if t != nil {
			out[id] = t
		}
	}
	return out
}

// pllMediaRows reads overrides: media id → language → texts. lang "" reads every language.
func (s *Server) pllMediaRows(ctx context.Context, lang string, ids []int64) map[int64]map[string]pllMediaText {
	out := map[int64]map[string]pllMediaText{}
	if len(ids) == 0 {
		return out
	}
	q := `SELECT media_id, lang, alt_text, title, caption, description FROM pll_media_i18n WHERE media_id = ANY($1)`
	args := []any{ids}
	if lang != "" {
		q += ` AND lang = $2`
		args = append(args, lang)
	}
	rows, err := s.store.Pool.Query(ctx, q, args...)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var l string
		var alt, title, caption, desc *string
		if rows.Scan(&id, &l, &alt, &title, &caption, &desc) != nil {
			continue
		}
		if out[id] == nil {
			out[id] = map[string]pllMediaText{}
		}
		out[id][l] = pllMediaText{"alt": alt, "title": title, "caption": caption, "description": desc}
	}
	return out
}

// pllMediaPatch changes some texts of one language (keys absent from patch keep their value; a nil
// or blank value falls back to the media row). The default language is the media row itself and is
// refused here. A row left with no text is deleted. changed is false when the stored texts already
// were the patched ones (nothing is written then).
func (s *Server) pllMediaPatch(ctx context.Context, id int64, lang string, patch pllMediaText) (changed bool, err error) {
	l := s.pllLang(ctx, lang)
	if l == nil {
		return false, fmt.Errorf("invalid language %q", lang)
	}
	if l.IsDefault {
		return false, fmt.Errorf("%s is the default language: edit the media item itself", l.Name)
	}
	stored := s.pllMediaRows(ctx, l.Slug, []int64{id})[id][l.Slug]
	cur := pllMediaText{}
	for k, v := range stored {
		cur[k] = v
	}
	for _, f := range pllMediaFields {
		v, ok := patch[f.Key]
		if !ok {
			continue
		}
		cur[f.Key] = pllMediaClean(f.Key, v)
	}
	if pllMediaTextEqual(stored, cur) {
		return false, nil
	}
	defer s.pllMediaLangsForget()
	if cur.empty() {
		_, err := s.store.Pool.Exec(ctx, `DELETE FROM pll_media_i18n WHERE media_id = $1 AND lang = $2`, id, l.Slug)
		return err == nil, err
	}
	_, err = s.store.Pool.Exec(ctx, `INSERT INTO pll_media_i18n (media_id, lang, alt_text, title, caption, description, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, now())
		ON CONFLICT (media_id, lang) DO UPDATE SET alt_text = EXCLUDED.alt_text, title = EXCLUDED.title,
			caption = EXCLUDED.caption, description = EXCLUDED.description, updated_at = now()`,
		id, l.Slug, cur["alt"], cur["title"], cur["caption"], cur["description"])
	return err == nil, err
}

// pllMediaTextEqual compares two override sets field by field (nil and a missing key are the same).
func pllMediaTextEqual(a, b pllMediaText) bool {
	for _, f := range pllMediaFields {
		x, y := a[f.Key], b[f.Key]
		if (x == nil) != (y == nil) || (x != nil && *x != *y) {
			return false
		}
	}
	return true
}

// pllMediaLangSet caches, per server, the languages pll_media_i18n has rows for (refreshed every
// minute and after every change): front requests of a language without overrides make no query.
var pllMediaLangSet sync.Map // *Server => *pllMediaLangCache

type pllMediaLangCache struct {
	at    time.Time
	langs map[string]bool
}

const pllMediaLangTTL = time.Minute

// pllMediaHasLang reports whether some media item has overrides in lang (true when unsure).
func (s *Server) pllMediaHasLang(ctx context.Context, lang string) bool {
	if v, ok := pllMediaLangSet.Load(s); ok {
		if lc := v.(*pllMediaLangCache); time.Since(lc.at) < pllMediaLangTTL {
			return lc.langs[lang]
		}
	}
	lctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	rows, err := s.store.Pool.Query(lctx, `SELECT DISTINCT lang FROM pll_media_i18n`)
	if err != nil {
		return true
	}
	langs := map[string]bool{}
	for rows.Next() {
		var l string
		if rows.Scan(&l) == nil {
			langs[l] = true
		}
	}
	rows.Close()
	if rows.Err() != nil {
		return true
	}
	pllMediaLangSet.Store(s, &pllMediaLangCache{at: time.Now(), langs: langs})
	return langs[lang]
}

// pllMediaLangsForget drops the language set after a change.
func (s *Server) pllMediaLangsForget() { pllMediaLangSet.Delete(s) }

// pllMediaClean normalises one submitted text: valid UTF-8, no NUL, capped; blank means nil.
func pllMediaClean(key string, v *string) *string {
	if v == nil {
		return nil
	}
	t := strings.ToValidUTF8(*v, "")
	t = strings.ReplaceAll(t, "\x00", "")
	t = strings.ReplaceAll(t, "\r\n", "\n")
	if key == "alt" || key == "title" {
		t = strings.TrimSpace(t)
	}
	if strings.TrimSpace(t) == "" {
		return nil
	}
	if utf8.RuneCountInString(t) > pllMediaMaxLen {
		t = string([]rune(t)[:pllMediaMaxLen])
	}
	return &t
}

// pllMediaCanEdit is the edit check of the media screens: upload_files, and the item is the user's
// own or the user may edit others' posts.
func pllMediaCanEdit(u *store.User, a *store.Attachment) bool {
	return u != nil && a != nil && u.Can("upload_files", nil) && (a.UploaderID == u.ID || u.Can("edit_others_posts", nil))
}

// pllMediaLangs are the languages that get override fields: all but the default one.
func (s *Server) pllMediaLangs(ctx context.Context) []*pllLanguage {
	var out []*pllLanguage
	for _, l := range s.pllLanguages(ctx) {
		if !l.IsDefault {
			out = append(out, l)
		}
	}
	return out
}

// Static content ---------------------------------------------------------------------------

var (
	pllImgWBIDRe     = regexp.MustCompile(`(?i)\sdata-wb-media-id=["']?([0-9]+)`)
	pllImgAltRe      = regexp.MustCompile(`(?is)\salt\s*=\s*("([^"]*)"|'([^']*)')`)
	pllFigureRe      = regexp.MustCompile(`(?is)<figure\b[^>]*>.*?</figure>`)
	pllFigcaptionRe  = regexp.MustCompile(`(?is)(<figcaption\b[^>]*>)(.*?)(</figcaption>)`)
	pllUploadsPathRe = regexp.MustCompile(`/wp-content/uploads/([^"'?#\s]+)`)
	pllSpaceRe       = regexp.MustCompile(`\s+`)
)

// pllContentMedia translates the media texts baked into static HTML (post content, Web Builder
// snapshots): the alt text of a media library image and the caption of its figure, when they still
// read as the media row's text (or the alt is empty). Text an author changed in the content stays.
// Only for overlaid requests; default-language pages are returned unchanged at no cost.
func (s *Server) pllContentMedia(ctx context.Context, content string) string {
	if !strings.Contains(content, "<img") {
		return content
	}
	if l := pllMediaLang(ctx); l == nil || !s.pllMediaHasLang(ctx, l.Slug) {
		return content
	}
	imgs := contentImgRe.FindAllString(content, -1)
	if len(imgs) == 0 {
		return content
	}
	var ids []int64
	var paths []string
	for _, img := range imgs {
		if id := pllImgID(img); id > 0 {
			ids = append(ids, id)
		} else if p := pllImgUploadPath(img); p != "" {
			paths = append(paths, p)
		}
	}
	byPath := s.pllMediaByPath(ctx, paths)
	for _, id := range byPath {
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return content
	}
	atts := s.attachmentsByIDs(ctx, ids)
	find := func(img string) *store.Attachment {
		id := pllImgID(img)
		if id == 0 {
			id = byPath[pllImgUploadPath(img)]
		}
		a := atts[id]
		if a == nil || a.Base == nil {
			return nil
		}
		return a
	}
	// Captions first: a figure with exactly one image whose figcaption reads as the media caption.
	if strings.Contains(content, "<figcaption") {
		content = pllFigureRe.ReplaceAllStringFunc(content, func(fig string) string {
			in := contentImgRe.FindAllString(fig, -1)
			if len(in) != 1 {
				return fig
			}
			a := find(in[0])
			if a == nil || a.Caption == a.Base.Caption || a.Base.Caption == "" {
				return fig
			}
			m := pllFigcaptionRe.FindStringSubmatchIndex(fig)
			if m == nil {
				return fig
			}
			inner := fig[m[4]:m[5]]
			var repl string
			switch pllNormText(inner) {
			case pllNormText(a.Base.Caption):
				repl = wp.KsesPost(a.Caption)
			case pllNormText(wp.Texturize(a.Base.Caption)):
				repl = wp.Texturize(wp.KsesPost(a.Caption))
			default:
				return fig
			}
			return fig[:m[4]] + repl + fig[m[5]:]
		})
	}
	return contentImgRe.ReplaceAllStringFunc(content, func(img string) string {
		a := find(img)
		if a == nil || a.Alt == a.Base.Alt {
			return img
		}
		alt := ` alt="` + html.EscapeString(a.Alt) + `"`
		m := pllImgAltRe.FindStringSubmatchIndex(img)
		if m == nil {
			return strings.Replace(img, "<img", "<img"+alt, 1)
		}
		cur := ""
		if m[4] >= 0 {
			cur = img[m[4]:m[5]]
		} else if m[6] >= 0 {
			cur = img[m[6]:m[7]]
		}
		if strings.TrimSpace(cur) != "" && pllNormText(cur) != pllNormText(a.Base.Alt) {
			return img
		}
		return img[:m[0]] + alt + img[m[1]:]
	})
}

// pllImgID is the media id of an <img>: class wp-image-N or data-wb-media-id.
func pllImgID(img string) int64 {
	for _, re := range []*regexp.Regexp{imgClassIDRe, pllImgWBIDRe} {
		if m := re.FindStringSubmatch(img); m != nil {
			if id, _ := strconv.ParseInt(m[1], 10, 64); id > 0 {
				return id
			}
		}
	}
	return 0
}

// pllImgUploadPath is the uploads-relative file of an <img> src ("" when it is not an upload).
func pllImgUploadPath(img string) string {
	src := imgSrc(img)
	if src == "" {
		return ""
	}
	m := pllUploadsPathRe.FindStringSubmatch(src)
	if m == nil || strings.Contains(m[1], "..") {
		return ""
	}
	return m[1]
}

// pllMediaByPath maps uploads-relative files (originals and sub-sizes) to media ids in one query.
// Each path is looked up at most once per request (the request cache of Multi Language).
func (s *Server) pllMediaByPath(ctx context.Context, paths []string) map[string]int64 {
	out := map[string]int64{}
	if len(paths) == 0 {
		return out
	}
	r := pllFrom(ctx)
	key := func(p string) string { return "pll:mpath:" + p }
	var need []string
	seen := map[string]bool{}
	for _, p := range paths {
		if seen[p] {
			continue
		}
		seen[p] = true
		if r != nil {
			if v, ok := r.cache.Load(key(p)); ok {
				if id, _ := v.(int64); id > 0 {
					out[p] = id
				}
				continue
			}
		}
		need = append(need, p)
	}
	if len(need) == 0 {
		return out
	}
	rows, err := s.store.Pool.Query(ctx, `SELECT id, file_path FROM media WHERE file_path = ANY($1) AND deleted_at IS NULL
		UNION ALL SELECT media_id, file_path FROM media_variants WHERE file_path = ANY($1)`, need)
	if err != nil {
		return out
	}
	found := map[string]int64{}
	for rows.Next() {
		var id int64
		var p string
		if rows.Scan(&id, &p) == nil {
			found[p] = id
		}
	}
	rows.Close()
	if rows.Err() != nil {
		return out
	}
	for _, p := range need {
		if id := found[p]; id > 0 {
			out[p] = id
		}
		if r != nil {
			r.cache.Store(key(p), found[p])
		}
	}
	return out
}

// pllNormText compares texts the way they read: entities decoded, whitespace collapsed, trimmed.
func pllNormText(s string) string {
	return strings.TrimSpace(pllSpaceRe.ReplaceAllString(html.UnescapeString(s), " "))
}

// Admin: media modal --------------------------------------------------------------------------

const pllMediaPrefetchKey = "pll.media.prefetch"

// pllMediaPrefetch loads the overrides of a list of attachments once (query-attachments), so the
// modal's compat fields cost one query per page instead of one per item.
func (s *Server) pllMediaPrefetch(c *gin.Context, list []*store.Attachment) {
	if len(list) == 0 || !s.pllMediaOn() {
		return
	}
	ids := make([]int64, len(list))
	for i, a := range list {
		ids[i] = a.ID
	}
	rows := s.pllMediaRows(c, "", ids)
	for _, id := range ids {
		if rows[id] == nil {
			rows[id] = map[string]pllMediaText{}
		}
	}
	c.Set(pllMediaPrefetchKey, rows)
}

// pllMediaTexts are the overrides of one attachment, every language (prefetched when available).
func (s *Server) pllMediaTexts(c *gin.Context, id int64) map[string]pllMediaText {
	if v, ok := c.Get(pllMediaPrefetchKey); ok {
		if m, _ := v.(map[int64]map[string]pllMediaText); m != nil {
			if t, ok := m[id]; ok {
				return t
			}
		}
	}
	return s.pllMediaRows(c, "", []int64{id})[id]
}

// golylangMediaCompat is the compat item of wp.media's attachment details: the per-language texts.
// WordPress' AttachmentCompat posts the fields to save-attachment-compat on change.
func (s *Server) golylangMediaCompat(c *gin.Context, a *store.Attachment) string {
	if !s.pllMediaOn() || !pllMediaCanEdit(currentUser(c), a) {
		return ""
	}
	langs := s.pllMediaLangs(c)
	if len(langs) == 0 {
		return ""
	}
	texts := s.pllMediaTexts(c, a.ID)
	id := itoa64(a.ID)
	var b strings.Builder
	b.WriteString(`<table class="compat-attachment-fields pll-media-i18n"><tbody>`)
	for _, l := range langs {
		t := texts[l.Slug]
		slug := html.EscapeString(l.Slug)
		fmt.Fprintf(&b, `<tr class="compat-field-pll_i18n_%s"><th scope="row" class="label" colspan="2"><strong>%s %s</strong></th></tr>`,
			slug, l.FlagHTML(), html.EscapeString(l.Name))
		for _, f := range pllMediaFields {
			if f.Key == "alt" && !strings.HasPrefix(a.Mime, "image/") {
				continue
			}
			val := ""
			if t != nil && t[f.Key] != nil {
				val = *t[f.Key]
			}
			fid := "attachments-" + id + "-pll-" + slug + "-" + f.Key
			name := "attachments[" + id + "][pll_i18n][" + slug + "][" + f.Key + "]"
			fmt.Fprintf(&b, `<tr class="compat-field-pll_i18n_%s_%s"><th scope="row" class="label"><label for="%s"><span class="alignleft">%s</span><br class="clear" /></label></th><td class="field">`,
				slug, f.Key, fid, html.EscapeString(f.Label))
			attrs := fmt.Sprintf(`id="%s" name="%s" lang="%s"%s`, fid, name, html.EscapeString(l.W3C()), pllDirAttr(l))
			if f.Key == "title" {
				fmt.Fprintf(&b, `<input type="text" class="text" %s value="%s" />`, attrs, html.EscapeString(val))
			} else {
				fmt.Fprintf(&b, `<textarea %s>%s</textarea>`, attrs, html.EscapeString(val))
			}
			b.WriteString(`</td></tr>`)
		}
	}
	fmt.Fprintf(&b, `<tr class="compat-field-pll_i18n_help"><td colspan="2"><p class="help">%s</p></td></tr>`,
		html.EscapeString(s.pllMediaHelp(c)))
	b.WriteString(`</tbody></table>`)
	return b.String()
}

func pllDirAttr(l *pllLanguage) string {
	if l.RTL {
		return ` dir="rtl"`
	}
	return ""
}

func (s *Server) pllMediaHelp(ctx context.Context) string {
	def := "the default language"
	if d := s.pllDefault(ctx); d != nil {
		def = d.Name
	}
	return "Translations of this media item. An empty field shows the text in " + def + "."
}

// golylangMediaSaveForm saves the per-language fields of a form: prefix is "pll_i18n" on the edit
// screen and "attachments[ID][pll_i18n]" in the media modal. Only languages present in the form
// change. The caller checked the nonce and the user's right to edit a.
func (s *Server) golylangMediaSaveForm(c *gin.Context, a *store.Attachment, prefix string) {
	if !s.pllMediaOn() || !pllMediaCanEdit(currentUser(c), a) {
		return
	}
	changed := false
	for _, l := range s.pllMediaLangs(c) {
		patch := pllMediaText{}
		for _, f := range pllMediaFields {
			if v, ok := c.GetPostForm(prefix + "[" + l.Slug + "][" + f.Key + "]"); ok {
				patch[f.Key] = &v
			}
		}
		if len(patch) == 0 {
			continue
		}
		if ch, err := s.pllMediaPatch(c, a.ID, l.Slug, patch); err == nil && ch {
			changed = true
		}
	}
	if changed {
		s.purgeAll(c)
	}
}

// golylangMediaCompatSave is save-attachment-compat's hook.
func (s *Server) golylangMediaCompatSave(c *gin.Context, a *store.Attachment) {
	s.golylangMediaSaveForm(c, a, "attachments["+itoa64(a.ID)+"][pll_i18n]")
}

// golylangMediaSaved is the edit screen's save hook (attachmentSave).
func (s *Server) golylangMediaSaved(c *gin.Context, a *store.Attachment) {
	s.golylangMediaSaveForm(c, a, "pll_i18n")
}

// Admin: edit screen and list --------------------------------------------------------------------

// golylangMediaBox is the Translations box of the attachment edit screen.
func (s *Server) golylangMediaBox(c *gin.Context, a *store.Attachment) template.HTML {
	if !s.pllMediaOn() || !pllMediaCanEdit(currentUser(c), a) {
		return ""
	}
	langs := s.pllMediaLangs(c)
	if len(langs) == 0 {
		return ""
	}
	texts := s.pllMediaTexts(c, a.ID)
	var b strings.Builder
	b.WriteString(`<div id="pll_media_i18n" class="postbox"><div class="postbox-header"><h2 class="hndle">Translations</h2></div><div class="inside">`)
	fmt.Fprintf(&b, `<p class="description">%s</p>`, html.EscapeString(s.pllMediaHelp(c)))
	for _, l := range langs {
		t := texts[l.Slug]
		slug := html.EscapeString(l.Slug)
		fmt.Fprintf(&b, `<fieldset class="pll-media-i18n-lang" id="pll-media-i18n-%s" lang="%s"%s><legend><h3>%s %s</h3></legend>`,
			slug, html.EscapeString(l.W3C()), pllDirAttr(l), l.FlagHTML(), html.EscapeString(l.Name))
		for _, f := range pllMediaFields {
			if f.Key == "alt" && !strings.HasPrefix(a.Mime, "image/") {
				continue
			}
			val := ""
			if t != nil && t[f.Key] != nil {
				val = *t[f.Key]
			}
			fid := "pll_i18n_" + slug + "_" + f.Key
			name := "pll_i18n[" + slug + "][" + f.Key + "]"
			fmt.Fprintf(&b, `<p><label for="%s"><strong>%s</strong></label><br />`, fid, html.EscapeString(f.Label))
			switch f.Key {
			case "title":
				fmt.Fprintf(&b, `<input type="text" class="widefat" name="%s" id="%s" value="%s" />`, name, fid, html.EscapeString(val))
			case "description":
				fmt.Fprintf(&b, `<textarea class="widefat" rows="4" name="%s" id="%s">%s</textarea>`, name, fid, html.EscapeString(val))
			default:
				fmt.Fprintf(&b, `<textarea class="widefat" rows="2" name="%s" id="%s">%s</textarea>`, name, fid, html.EscapeString(val))
			}
			b.WriteString(`</p>`)
		}
		b.WriteString(`</fieldset>`)
	}
	b.WriteString(`</div></div>`)
	return template.HTML(b.String())
}

// golylangMediaColumns adds a column per language to the Media Library list: a tick for the default
// language (the media item itself), a pencil where a translation exists and a plus where it does not.
func (s *Server) golylangMediaColumns(c *gin.Context, d *mediaListData, list []*store.Attachment) {
	if !s.pllMediaOn() || len(list) == 0 {
		return
	}
	langs := s.pllLanguages(c)
	if len(langs) < 2 {
		return
	}
	for _, l := range langs {
		d.PluginHeads = append(d.PluginHeads, pllColumnHead(l))
	}
	ids := make([]int64, len(list))
	for i, a := range list {
		ids[i] = a.ID
	}
	rows := s.pllMediaRows(c, "", ids)
	for i := range d.Rows {
		if i >= len(list) {
			break
		}
		a := list[i]
		for _, l := range langs {
			name := html.EscapeString(l.Name)
			edit := "/wp-admin/post?post=" + itoa64(a.ID) + "&amp;action=edit"
			var cell string
			switch {
			case !d.Rows[i].CanEdit:
			case l.IsDefault:
				cell = fmt.Sprintf(`<a href="%s" class="pll_icon_tick" title="Edit this item"><span class="dashicons dashicons-yes" aria-hidden="true"></span><span class="screen-reader-text">Edit this item</span></a>`, edit)
			case rows[a.ID][l.Slug] != nil:
				cell = fmt.Sprintf(`<a href="%s#pll-media-i18n-%s" class="pll_icon_edit" title="Edit the translation in %s"><span class="dashicons dashicons-edit" aria-hidden="true"></span><span class="screen-reader-text">Edit the translation in %s</span></a>`,
					edit, html.EscapeString(l.Slug), name, name)
			default:
				cell = fmt.Sprintf(`<a href="%s#pll-media-i18n-%s" class="pll_icon_add" title="Add a translation in %s"><span class="dashicons dashicons-plus" aria-hidden="true"></span><span class="screen-reader-text">Add a translation in %s</span></a>`,
					edit, html.EscapeString(l.Slug), name, name)
			}
			d.Rows[i].PluginCols = append(d.Rows[i].PluginCols, template.HTML(fmt.Sprintf(`<td class="language_%s column-language_%s" data-colname="%s">%s</td>`,
				html.EscapeString(l.Slug), html.EscapeString(l.Slug), name, cell)))
		}
	}
}

// REST ---------------------------------------------------------------------------------------

// golylangMediaREST adds pll_i18n (language → texts, null = the media row's) to wp/v2/media in
// context=edit for users who may edit the item.
func (s *Server) golylangMediaREST(c *gin.Context, a *store.Attachment, ctxName string, out map[string]any) {
	if ctxName != "edit" || !s.pllMediaOn() || !pllMediaCanEdit(restCurrent(c), a) {
		return
	}
	out["pll_i18n"] = s.pllMediaI18nJSON(c, a.ID)
}

func (s *Server) pllMediaI18nJSON(ctx context.Context, id int64) map[string]any {
	rows := s.pllMediaRows(ctx, "", []int64{id})[id]
	out := map[string]any{}
	for _, l := range s.pllMediaLangs(ctx) {
		t := rows[l.Slug]
		if t == nil {
			t = pllMediaText{}
		}
		out[l.Slug] = t.json()
	}
	return out
}

// pllMediaRESTParse reads {"<lang>": {"alt_text": "…", "title": …}} (string sets, null or ""
// clears) and checks every language before anything is written. The default language is refused
// (it is the media row: use alt_text, title, …).
func (s *Server) pllMediaRESTParse(ctx context.Context, body map[string]any) (map[string]pllMediaText, error) {
	out := map[string]pllMediaText{}
	for lang, raw := range body {
		fields, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("pll_i18n[%s] must be an object", lang)
		}
		patch := pllMediaText{}
		for _, f := range pllMediaFields {
			for _, k := range []string{f.REST, f.Key} {
				v, ok := fields[k]
				if !ok {
					continue
				}
				switch t := v.(type) {
				case nil:
					patch[f.Key] = nil
				case string:
					patch[f.Key] = &t
				case map[string]any: // {"raw": "…"} like title/caption on wp/v2
					if r, ok := t["raw"].(string); ok {
						patch[f.Key] = &r
					}
				default:
					return nil, fmt.Errorf("pll_i18n[%s][%s] must be a string", lang, k)
				}
			}
		}
		if len(patch) == 0 {
			continue
		}
		l := s.pllLang(ctx, lang)
		if l == nil {
			return nil, fmt.Errorf("invalid language %q", lang)
		}
		if l.IsDefault {
			return nil, fmt.Errorf("%s is the default language: edit the media item itself", l.Name)
		}
		out[l.Slug] = patch
	}
	return out, nil
}

// pllMediaRESTApply writes parsed patches and purges the page cache when something changed.
func (s *Server) pllMediaRESTApply(ctx context.Context, id int64, patches map[string]pllMediaText) error {
	changed := false
	for lang, patch := range patches {
		ch, err := s.pllMediaPatch(ctx, id, lang, patch)
		if err != nil {
			return err
		}
		changed = changed || ch
	}
	if changed {
		s.purgeAll(ctx)
	}
	return nil
}

// pllMediaRESTPatch is pllMediaRESTParse then pllMediaRESTApply.
func (s *Server) pllMediaRESTPatch(ctx context.Context, id int64, body map[string]any) error {
	patches, err := s.pllMediaRESTParse(ctx, body)
	if err != nil {
		return err
	}
	return s.pllMediaRESTApply(ctx, id, patches)
}

// golylangMediaRESTParse validates pll_i18n of a wp/v2/media update before anything is saved
// (ok false: it answered 400). patches is nil when there is nothing to apply.
func (s *Server) golylangMediaRESTParse(c *gin.Context, r *restRequest) (patches map[string]pllMediaText, ok bool) {
	raw, has := r.Body["pll_i18n"]
	if !has || raw == nil || !s.pllMediaOn() {
		return nil, true
	}
	body, isMap := raw.(map[string]any)
	if !isMap {
		restErr(c, http.StatusBadRequest, "rest_invalid_param", "Invalid parameter(s): pll_i18n")
		return nil, false
	}
	patches, err := s.pllMediaRESTParse(c, body)
	if err != nil {
		restErr(c, http.StatusBadRequest, "rest_invalid_param", "Invalid parameter(s): pll_i18n ("+err.Error()+")")
		return nil, false
	}
	return patches, true
}

// golylangMediaRESTSave applies the patches golylangMediaRESTParse returned (after the media row
// was saved); false when it answered an error.
func (s *Server) golylangMediaRESTSave(c *gin.Context, a *store.Attachment, patches map[string]pllMediaText) bool {
	if len(patches) == 0 {
		return true
	}
	if err := s.pllMediaRESTApply(c, a.ID, patches); err != nil {
		restErr(c, http.StatusInternalServerError, "rest_cannot_edit", "The translations could not be saved.")
		return false
	}
	return true
}

// registerGolylangMediaREST adds GET|POST pll/v1/media/{id}/i18n: the base texts and the overrides
// of every non-default language. POST takes {"<lang>": {...}} (or {"i18n": {"<lang>": {...}}}).
func (s *Server) registerGolylangMediaREST(api *restAPI) {
	load := func(c *gin.Context, r *restRequest) *store.Attachment {
		if !s.pllMediaOn() {
			restErr(c, http.StatusNotFound, "pll_media_disabled", "Media translation is not enabled.")
			return nil
		}
		a, err := s.store.GetAttachment(c.Request.Context(), r.ID()) // never overlaid: the base texts
		if err != nil {
			restErr(c, http.StatusNotFound, "rest_post_invalid_id", "Invalid post ID.")
			return nil
		}
		if !pllMediaCanEdit(restCurrent(c), a) {
			restErr(c, http.StatusForbidden, "rest_cannot_edit", "Sorry, you are not allowed to edit this item.")
			return nil
		}
		return a
	}
	answer := func(c *gin.Context, a *store.Attachment) {
		restJSON(c, http.StatusOK, map[string]any{"id": a.ID,
			"base": map[string]any{"alt_text": a.Alt, "title": a.Title, "caption": a.Caption, "description": a.Description},
			"i18n": s.pllMediaI18nJSON(c, a.ID)})
	}
	api.register(pllRESTNamespace, `/pll/v1/media/(?P<id>[\d]+)/i18n`, map[string]restHandler{
		http.MethodGet: func(c *gin.Context, r *restRequest) {
			if a := load(c, r); a != nil {
				answer(c, a)
			}
		},
		http.MethodPost: func(c *gin.Context, r *restRequest) {
			a := load(c, r)
			if a == nil {
				return
			}
			body := r.Body
			if in, ok := body["i18n"].(map[string]any); ok {
				body = in
			}
			if err := s.pllMediaRESTPatch(c, a.ID, body); err != nil {
				restErr(c, http.StatusBadRequest, "rest_invalid_param", err.Error())
				return
			}
			answer(c, a)
		},
	}, nil)
}

// WXR --------------------------------------------------------------------------------------------

// pllMediaWXRKey is the attachment postmeta that carries the overrides in an export.
const pllMediaWXRKey = "_pll_media_i18n"

// pllMediaExport returns the overrides of every media item as postmeta JSON values.
func (s *Server) pllMediaExport(ctx context.Context) map[int64]string {
	out := map[int64]string{}
	rows, err := s.store.Pool.Query(ctx, `SELECT media_id, lang, alt_text, title, caption, description FROM pll_media_i18n ORDER BY media_id, lang`)
	if err != nil {
		return out
	}
	all := map[int64]map[string]any{}
	for rows.Next() {
		var id int64
		var l string
		var alt, title, caption, desc *string
		if rows.Scan(&id, &l, &alt, &title, &caption, &desc) != nil {
			continue
		}
		if all[id] == nil {
			all[id] = map[string]any{}
		}
		all[id][l] = pllMediaText{"alt": alt, "title": title, "caption": caption, "description": desc}.json()
	}
	rows.Close()
	for id, m := range all {
		if raw, err := json.Marshal(m); err == nil {
			out[id] = string(raw)
		}
	}
	return out
}

// pllMediaWXRMeta is the export postmeta of one attachment ("" when it has no overrides).
func pllMediaWXRMeta(all map[int64]string, id int64, postmeta func(key, value string) string) string {
	if v := all[id]; v != "" {
		return postmeta(pllMediaWXRKey, v)
	}
	return ""
}

// pllMediaImport stores the overrides of an imported attachment for the languages this site has.
func (s *Server) pllMediaImport(ctx context.Context, id int64, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	var m map[string]any
	if json.Unmarshal([]byte(value), &m) != nil {
		return
	}
	for lang, raw := range m {
		if l := s.pllLang(ctx, lang); l == nil || l.IsDefault {
			delete(m, lang)
			continue
		}
		if _, ok := raw.(map[string]any); !ok {
			delete(m, lang)
		}
	}
	_ = s.pllMediaRESTPatch(ctx, id, m)
}
