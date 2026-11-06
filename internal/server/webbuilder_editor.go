package server

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"log"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/media"
	"wpgo/internal/store"
	"wpgo/internal/webbuilder"
)

// The full-screen editor of Pursae Web Builder: /wp-admin/post?post={id}&action=web-builder-next
// (postEdit dispatches here). The page is a bare HTML document — no admin chrome, no admin-page
// translation — that loads the editor bundle named by the manifest and hands it the boot config
// window.__WEB_BUILDER__ (contract §2). The admin CSP already fits: the bundle, its fonts and the
// REST calls are same-origin, the boot script is inline (script-src 'unsafe-inline'), media are
// same-origin or https images.

// wbEditor writes the editor page of p.
func (s *Server) wbEditor(c *gin.Context, p *store.Post) {
	c.Header("Cache-Control", "no-cache, no-store, must-revalidate, max-age=0")
	u := currentUser(c)
	if u == nil || !u.Can("edit_post", p) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to edit this item.")
		return
	}
	if p.Status == "trash" {
		c.String(http.StatusForbidden, "You cannot edit this item because it is in the Trash. Please restore it and try again.")
		return
	}
	if !s.wbEnabledType(p.Type) {
		c.String(http.StatusForbidden, "This item cannot be edited with Pursae Web Builder.")
		return
	}
	m, err := s.wbManifest()
	if err != nil {
		c.String(http.StatusInternalServerError, "Pursae Web Builder is not built: "+err.Error())
		return
	}
	prefix, entry, err := wbEditorBundle(m)
	if err != nil {
		c.String(http.StatusInternalServerError, "Pursae Web Builder is not built: "+err.Error())
		return
	}
	// The core post lock (_edit_lock), as the block editor's post-lock dialog knows it: taken when
	// free, expired or already ours; another user's live lock is reported to the editor instead.
	if _, err := s.wbTakeLock(c, p.ID, u.ID, false); err != nil {
		s.wbEditorFailed(c, p, err)
		return
	}
	if fresh, err := s.store.GetPost(c, p.ID); err == nil {
		p = fresh
	}
	doc, err := s.wbDocumentFor(c, p, u)
	if err != nil {
		s.wbEditorFailed(c, p, err)
		return
	}
	boot := s.wbBootFor(c, p, u, doc)
	title := "Pursae Web Builder"
	if p.Title != "" {
		title = "Edit “" + p.Title + "” with Pursae Web Builder"
	}
	// The editor UI is left-to-right in every language (its catalogs translate text only).
	lang := strings.ReplaceAll(s.userLocale(c), "_", "-")
	page := wbEditorHTML(title, lang, "ltr", s.links(c).HomeURL(prefix), entry, wbScriptJSON(boot))
	c.Data(http.StatusOK, "text/html; charset=UTF-8", []byte(page))
}

// wbEditorFailed answers a storage error of the editor page: logged, not shown.
func (s *Server) wbEditorFailed(c *gin.Context, p *store.Post, err error) {
	log.Printf("web builder: editor of post %d: %v", p.ID, err)
	c.String(http.StatusInternalServerError, "Pursae Web Builder could not load this item. Please try again.")
}

// wbEditorHTML is the editor document. base is the absolute bundle URL (ending in "/"), boot the
// encoded window.__WEB_BUILDER__ value (wbScriptJSON).
func wbEditorHTML(title, lang, dir, base string, entry webbuilder.ManifestEntry, boot string) string {
	var b strings.Builder
	b.WriteString("<!DOCTYPE html>\n<html lang=\"" + html.EscapeString(lang) + "\" dir=\"" + dir + "\">\n<head>\n")
	b.WriteString("<meta charset=\"utf-8\">\n<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n")
	b.WriteString("<meta name=\"robots\" content=\"noindex, nofollow\">\n")
	b.WriteString("<title>" + html.EscapeString(title) + "</title>\n")
	for _, css := range entry.CSS {
		b.WriteString("<link rel=\"stylesheet\" href=\"" + html.EscapeString(base+css) + "\">\n")
	}
	b.WriteString("</head>\n<body>\n<div id=\"root\"></div>\n")
	b.WriteString("<noscript>Pursae Web Builder needs JavaScript.</noscript>\n")
	b.WriteString("<script>window." + webbuilder.BootGlobal + " = " + boot + ";</script>\n")
	b.WriteString("<script type=\"module\" src=\"" + html.EscapeString(base+entry.JS) + "\"></script>\n")
	b.WriteString("</body>\n</html>\n")
	return b.String()
}

// wbScriptJSON encodes a value for an inline script: scriptJSON ("</" → "<\/") plus "<!--" escaped,
// which would otherwise put the HTML parser in the script's double-escaped state.
func wbScriptJSON(v any) string {
	return strings.ReplaceAll(scriptJSON(v), "<!--", `\u003c!--`)
}

// --- boot config ---------------------------------------------------------------------------------

// wbBoot is webbuilder.Boot with the document carried as raw JSON (wbDocument): the stored doc is
// the client's JSON and goes back unchanged, fields a newer editor added included. The JSON shape
// is the contract's (TestWebBuilderBootShape compares the keys).
type wbBoot struct {
	APIVersion      int                  `json:"apiVersion"`
	RendererVersion int                  `json:"rendererVersion"`
	Document        *wbDocument          `json:"document"`
	User            webbuilder.UserRef   `json:"user"`
	REST            webbuilder.BootREST  `json:"rest"`
	Links           webbuilder.BootLinks `json:"links"`
	Caps            webbuilder.BootCaps  `json:"caps"`
	Locale          string               `json:"locale"`
	Fonts           webbuilder.BootFonts `json:"fonts"`
	Media           webbuilder.BootMedia `json:"media"`
	SiteAssets      string               `json:"siteAssets"`
	AI              webbuilder.BootAI    `json:"ai"`
}

// wbDocument is webbuilder.DocumentResponse with raw doc / page JSON (GET documents/{id} answers it
// too).
type wbDocument struct {
	PostID   int64                `json:"postId"`
	PostType string               `json:"postType"`
	Status   string               `json:"status"`
	Rev      int                  `json:"rev"`
	Doc      json.RawMessage      `json:"doc"` // null: a new page
	Page     json.RawMessage      `json:"page"`
	Draft    *wbDraft             `json:"draft"`
	Detached bool                 `json:"detached"`
	Modified string               `json:"modified"`
	Renderer int                  `json:"renderer"`
	Lock     *webbuilder.LockInfo `json:"lock"`
}

// wbDraft is webbuilder.DraftState with raw JSON.
type wbDraft struct {
	Doc     json.RawMessage `json:"doc"`
	Page    json.RawMessage `json:"page"`
	BaseRev int             `json:"baseRev"`
	SavedAt string          `json:"savedAt"`
}

// wbDocumentFor is the document of p as user u sees it: the stored doc and its rev, u's own draft
// when it is newer, and another user's live edit lock.
func (s *Server) wbDocumentFor(ctx context.Context, p *store.Post, u *store.User) (*wbDocument, error) {
	postPage := wbPostPage(p)
	d := &wbDocument{PostID: p.ID, PostType: p.Type, Status: p.Status, Page: postPage,
		Modified: p.ModifiedAt.UTC().Format(time.RFC3339)}
	wb := s.store.WebBuilder()
	row, err := wb.Doc(ctx, p.ID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		row = nil
	case err != nil:
		return nil, err
	default:
		d.Rev, d.Doc, d.Detached, d.Renderer = row.Rev, row.Doc, row.Detached, row.Renderer
		d.Page = wbPageJSON(row.Page, postPage)
	}
	if u != nil {
		dr, err := wb.Draft(ctx, p.ID, u.ID)
		switch {
		case errors.Is(err, store.ErrNotFound):
		case err != nil:
			return nil, err
		case row == nil || dr.UpdatedAt.After(row.UpdatedAt):
			d.Draft = &wbDraft{Doc: dr.Doc, Page: wbPageJSON(dr.Page, d.Page), BaseRev: dr.BaseRev,
				SavedAt: dr.UpdatedAt.UTC().Format(time.RFC3339)}
		}
		d.Lock = s.wbLockHolder(ctx, p, u.ID)
	}
	return d, nil
}

// wbPostPage is the PageSettings of a post without a builder doc: title, slug, the excerpt (or the
// Pursae SEO description) and the Pursae SEO noindex flag.
func wbPostPage(p *store.Post) json.RawMessage {
	desc := p.Excerpt
	if desc == "" {
		desc = seogoMeta(p.Meta, "description")
	}
	b, _ := json.Marshal(webbuilder.PageSettings{Title: p.Title, Slug: p.Slug, Description: desc,
		Indexable: seogoMeta(p.Meta, "noindex") != "noindex"})
	return b
}

// wbPageJSON is a stored page object, or def when nothing was stored ('{}').
func wbPageJSON(raw, def json.RawMessage) json.RawMessage {
	var m map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &m) != nil || len(m) == 0 {
		return def
	}
	return raw
}

// wbBootFor is window.__WEB_BUILDER__ for p. It carries no secret: the REST nonce is the user's
// own wp_rest nonce, AI is reported off and no key or sealed value is read.
func (s *Server) wbBootFor(c *gin.Context, p *store.Post, u *store.User, doc *wbDocument) *wbBoot {
	lb := s.links(c)
	nonce := s.nonce(c, "wp_rest")
	permalink := lb.Permalink(p)
	exit := "/wp-admin/edit"
	if p.Type != "post" {
		exit += "?post_type=" + p.Type
	}
	return &wbBoot{
		APIVersion:      webbuilder.APIVersion,
		RendererVersion: webbuilder.RendererVersion,
		Document:        doc,
		User:            webbuilder.UserRef{ID: u.ID, Name: wbUserName(u)},
		REST:            webbuilder.BootREST{Root: lb.HomeURL("/wp-json/"), Namespace: webbuilder.RESTNamespace, Nonce: nonce},
		Links: webbuilder.BootLinks{
			Site:      strings.TrimRight(lb.HomeURL("/"), "/"),
			Permalink: permalink,
			Preview:   permalink + previewSep(permalink) + "preview=true",
			Exit:      lb.HomeURL(exit),
			EditPost:  lb.HomeURL("/wp-admin/post?post=" + itoa64(p.ID) + "&action=edit"),
			Settings:  "", // Settings › Web Builder arrives with the switchover's admin screens
		},
		Caps: webbuilder.BootCaps{
			Publish:        u.Can("publish_post", p),
			UploadFiles:    u.Can("upload_files", nil),
			UnfilteredHTML: u.Can("unfiltered_html", nil),
			EditOthers:     u.Can("edit_others_posts", nil),
		},
		Locale:     s.userLocale(c),
		Fonts:      webbuilder.BootFonts{CSSURL: lb.HomeURL("/wp-json" + webbuilder.RouteFontsCSS + "?_wpnonce=" + nonce), Local: s.wbLocalFonts()},
		Media:      webbuilder.BootMedia{MaxUploadBytes: maxUploadBytes, Types: wbMediaTypes()},
		SiteAssets: lb.HomeURL(webbuilder.SiteAssetsPath),
		AI:         webbuilder.BootAI{Enabled: false, Providers: []webbuilder.AIProviderInfo{}},
	}
}

// wbMediaTypes are the MIME types a regular user may upload (wp_get_mime_types), sorted.
var wbMediaTypes = sync.OnceValue(func() []string {
	seen := map[string]bool{}
	out := []string{}
	for _, mt := range media.MimeTypes {
		if !seen[mt] {
			seen[mt] = true
			out = append(out, mt)
		}
	}
	sort.Strings(out)
	return out
})

// wbFontFamilies maps a font key (gweFontKey) to its Google Fonts family name.
var wbFontFamilies = sync.OnceValue(func() map[string]string {
	m := map[string]string{}
	for fam := range gweGoogleFontsOnce() {
		m[gweFontKey(fam)] = fam
	}
	return m
})

// wbLocalFonts are the Google Fonts families already localized under uploads/web-builder/fonts
// (one css/<key>.css per family, gfonts.go), sorted.
func (s *Server) wbLocalFonts() []string {
	out := []string{}
	entries, err := os.ReadDir(s.gweFontsDir("css"))
	if err != nil {
		return out
	}
	fams := wbFontFamilies()
	for _, e := range entries {
		key, ok := strings.CutSuffix(e.Name(), ".css")
		if !ok || e.IsDir() {
			continue
		}
		if fam, ok := fams[key]; ok {
			out = append(out, fam)
		}
	}
	sort.Strings(out)
	return out
}

// wbUserName is the name the editor shows for a user.
func wbUserName(u *store.User) string {
	if u.DisplayName != "" {
		return u.DisplayName
	}
	return u.Login
}

// --- edit lock -------------------------------------------------------------------------------------

// wbLockValue matches WordPress' _edit_lock "<unix>:<user id>".
var wbLockValue = regexp.MustCompile(`^([0-9]{1,18}):([0-9]{1,18})$`)

// wbParseLock reads a post's _edit_lock meta.
func wbParseLock(v any) (uid int64, at time.Time, ok bool) {
	str, _ := v.(string)
	m := wbLockValue.FindStringSubmatch(str)
	if m == nil {
		return 0, time.Time{}, false
	}
	ts, _ := strconv.ParseInt(m[1], 10, 64)
	uid, _ = strconv.ParseInt(m[2], 10, 64)
	return uid, time.Unix(ts, 0), true
}

// wbLockHolder is another user's live lock on p (wp_check_post_lock), else nil.
func (s *Server) wbLockHolder(ctx context.Context, p *store.Post, me int64) *webbuilder.LockInfo {
	uid, at, ok := wbParseLock(p.Meta[webbuilder.EditLockMeta])
	if !ok || uid == 0 || uid == me || time.Since(at) > webbuilder.LockTTLSeconds*time.Second {
		return nil
	}
	info := &webbuilder.LockInfo{User: webbuilder.UserRef{ID: uid}, Since: at.UTC().Format(time.RFC3339)}
	if lu, err := s.store.UserByID(ctx, uid); err == nil {
		info.User.Name = wbUserName(lu)
	}
	return info
}

// wbTakeLock is wp_set_post_lock: it writes "<now>:<uid>" when the lock is free, expired, unreadable
// or uid's own — or always with take. It reports whether uid now holds the lock. The check and the
// write are one statement, so two editors opening at once cannot both win.
func (s *Server) wbTakeLock(ctx context.Context, postID, uid int64, take bool) (bool, error) {
	now := time.Now().Unix()
	tag, err := s.store.Pool.Exec(ctx, `UPDATE posts SET meta = coalesce(meta, '{}'::jsonb) || jsonb_build_object($2::text, $3::text)
		WHERE id = $1 AND ($4 OR CASE WHEN coalesce(meta->>$2, '') ~ '^[0-9]{1,18}:[0-9]{1,18}$'
			THEN split_part(meta->>$2, ':', 2)::bigint IN (0, $5) OR split_part(meta->>$2, ':', 1)::bigint < $6
			ELSE true END)`,
		postID, webbuilder.EditLockMeta, strconv.FormatInt(now, 10)+":"+strconv.FormatInt(uid, 10), take, uid, now-webbuilder.LockTTLSeconds)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}
