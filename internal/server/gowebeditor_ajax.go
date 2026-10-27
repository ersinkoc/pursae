package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"wpgo/internal/builder"
	"wpgo/internal/store"
	"wpgo/internal/wp"
)

// The elementor_ajax protocol: one POST to admin-ajax with action=elementor_ajax, _nonce (for the
// elementor_ajax action), editor_post_id, initial_document_id and actions = a JSON object of
// {"<request id>": {"action": "<name>", "data": {...}}}, run in order. The reply wraps each result as
// {"success", "code", "data"}; a bad nonce is HTTP 401 "Token Expired.".

// gweError is an action failure with its status code.
type gweError struct {
	code int
	msg  string
}

func (e *gweError) Error() string { return e.msg }

func gweFail(code int, msg string) error { return &gweError{code, msg} }

var errGweNotFound = gweFail(http.StatusBadRequest, "Action not found.")

// gweAjax handles GoWebEditor's admin-ajax actions; it reports whether the action was one of them.
func (s *Server) gweAjax(c *gin.Context, action string) bool {
	switch action {
	case "elementor_ajax":
		s.gweAjaxBatch(c)
	case "heartbeat":
		s.gweHeartbeat(c)
	case "elementor_js_log":
		jsonSuccess(c, true)
	case "elementor_library_direct_actions":
		s.gweLibraryDirect(c)
	default:
		return s.gweProAjax(c, action) // GoWebEditor Pro: form submissions
	}
	return true
}

type gweRequest struct {
	ID     string
	Action string          `json:"action"`
	Data   json.RawMessage `json:"data"`
}

// gweDecodeActions decodes the actions object keeping its key order.
func gweDecodeActions(raw string) ([]gweRequest, error) {
	dec := json.NewDecoder(strings.NewReader(raw))
	t, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := t.(json.Delim); !ok || d != '{' {
		return nil, errors.New("actions must be an object")
	}
	var out []gweRequest
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := kt.(string)
		var r gweRequest
		if err := dec.Decode(&r); err != nil {
			return nil, err
		}
		r.ID = key
		out = append(out, r)
	}
	return out, nil
}

func (s *Server) gweAjaxBatch(c *gin.Context) {
	u := currentUser(c)
	if !s.checkNonceValue(c, c.PostForm("_nonce"), "elementor_ajax") || u == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "data": gin.H{"responses": gin.H{"": gin.H{"success": false, "code": 200, "data": "Token Expired."}}}})
		return
	}
	if !u.Can("edit_posts", nil) {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "data": "Access denied."})
		return
	}
	reqs, err := gweDecodeActions(c.PostForm("actions"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "data": "Invalid actions."})
		return
	}
	editorID, _ := strconv.ParseInt(c.PostForm("editor_post_id"), 10, 64)
	responses := map[string]any{}
	for _, r := range reqs {
		data, err := s.gweAction(c, r.Action, r.Data, editorID)
		if err != nil {
			code := http.StatusInternalServerError
			var ge *gweError
			if errors.As(err, &ge) {
				code = ge.code
			}
			responses[r.ID] = gin.H{"success": false, "code": code, "data": err.Error()}
			continue
		}
		responses[r.ID] = gin.H{"success": true, "code": 200, "data": data}
	}
	c.Header("Cache-Control", "no-cache, no-store, must-revalidate, max-age=0")
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"responses": responses}})
}

// gweUIActions only record editor UI state upstream; they succeed without doing anything.
var gweUIActions = map[string]bool{
	"notice_bar_dismiss": true, "elementor_image_optimization_campaign": true, "enable_unfiltered_files_upload": true,
	"set_admin_notice_viewed": true, "save_general_settings": true, "editor_set_global_settings": true,
	"mark_template_as_favorite": true, "update_templates": true, "process_global_styles": true,
}

func (s *Server) gweAction(c *gin.Context, action string, raw json.RawMessage, editorID int64) (any, error) {
	var data map[string]any
	if len(raw) > 0 && string(raw) != "null" {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		_ = dec.Decode(&data)
	}
	if data == nil {
		data = map[string]any{}
	}
	u := currentUser(c)
	if gweUIActions[action] {
		return true, nil
	}
	switch action {
	case "get_widgets_config", "refresh_widgets_config":
		return json.RawMessage(s.gweWidgetsConfig(c)), nil
	case "get_widgets_default_value_translations":
		return map[string]any{}, nil
	case "enqueue_google_fonts":
		return s.gweFontsEnqueueAjax(c, data) // gap-integrations: local Google Fonts
	case "introduction_viewed":
		key, _ := data["introductionKey"].(string)
		if key == "" {
			return nil, gweFail(http.StatusBadRequest, "Invalid introduction key.")
		}
		return true, s.gweSetUserMeta(c, u, "gwe_introduction", map[string]any{key: true})
	case "dismissed_editor_notices":
		return true, nil
	case "render_atomic_element": // GoWebEditor v4 (gowebeditor_v4.go)
		return s.gweRenderAtomic(c, raw, editorID)
	case "get_document_config":
		p, err := s.gwePostFor(c, gweInt(data["id"]), "edit_post")
		if err != nil {
			return nil, err
		}
		return s.gweDocConfig(c, p), nil
	case "save_builder":
		p, err := s.gwePostFor(c, editorID, "edit_post")
		if err != nil {
			return nil, err
		}
		return s.gweSaveBuilder(c, p, data, raw)
	case "discard_changes":
		p, err := s.gwePostFor(c, editorID, "edit_post")
		if err != nil {
			return nil, err
		}
		_, err = s.store.Pool.Exec(c, `DELETE FROM post_revisions WHERE post_id = $1 AND is_autosave`, p.ID)
		return true, err
	case "render_widget":
		return s.gweRenderWidget(c, raw, editorID)
	case "editor_get_wp_widget_form":
		return "", nil
	case "get_revisions":
		p, err := s.gwePostFor(c, editorID, "edit_post")
		if err != nil {
			return nil, err
		}
		return s.gweRevisions(c, p)
	case "get_revision_data":
		return s.gweRevisionData(c, gweInt(data["id"]))
	case "delete_revision":
		return s.gweDeleteRevision(c, gweInt(data["id"]))
	case "finder_get_category_items":
		return s.gweFinderItems(c, data)
	case "get_library_data":
		return s.gweLibraryData(c, data)
	case "save_template":
		return s.gweSaveTemplate(c, data, editorID)
	case "get_template_data":
		return s.gweTemplateData(c, data)
	case "delete_template":
		return s.gweDeleteTemplates(c, []int64{gweInt(data["template_id"])})
	case "bulk_delete_templates":
		var ids []int64
		if list, ok := data["template_ids"].([]any); ok {
			for _, v := range list {
				ids = append(ids, gweInt(v))
			}
		}
		return s.gweDeleteTemplates(c, ids)
	case "rename_template":
		p, err := s.gwePostFor(c, gweInt(data["id"]), "edit_post")
		if err != nil {
			return nil, err
		}
		title, _ := data["title"].(string)
		_, err = s.store.Pool.Exec(c, `UPDATE posts SET title = $2, modified_at = now() WHERE id = $1`, p.ID, strings.TrimSpace(title))
		return true, err
	case "get_templates_quota", "get_folders", "create_folder", "get_item_children", "search_templates", "load_more_templates",
		"bulk_undo_delete_items", "import_template", "import_from_json":
		return nil, gweFail(http.StatusBadRequest, "The cloud template library is not available in GoWebEditor.")
	}
	if strings.HasPrefix(action, "save_") && strings.HasSuffix(action, "_settings") {
		name := strings.TrimSuffix(strings.TrimPrefix(action, "save_"), "_settings")
		values, _ := data["data"].(map[string]any)
		switch name {
		case "editorPreferences":
			return map[string]any{}, s.gweSetUserMeta(c, u, "gwe_editor_preferences", values)
		case "page":
			id := gweInt(data["id"])
			if id == 0 {
				id = editorID
			}
			p, err := s.gwePostFor(c, id, "edit_post")
			if err != nil {
				return nil, err
			}
			settings := gwePageSettings(p)
			for k, v := range values {
				settings[k] = v
			}
			p.Meta["_elementor_page_settings"] = settings
			return map[string]any{}, s.store.UpdatePost(c, p, u.ID)
		}
		return map[string]any{}, nil
	}
	if out, ok, err := s.gweProAction(c, action, data, raw, editorID); ok { // GoWebEditor Pro actions
		return out, err
	}
	return nil, errGweNotFound
}

func gweInt(v any) int64 {
	switch t := v.(type) {
	case json.Number:
		n, _ := t.Int64()
		return n
	case float64:
		return int64(t)
	case string:
		n, _ := strconv.ParseInt(t, 10, 64)
		return n
	case int64:
		return t
	case int:
		return int64(t)
	}
	return 0
}

// gwePostFor loads a post the current user may act on with cap.
func (s *Server) gwePostFor(c *gin.Context, id int64, cap string) (*store.Post, error) {
	if id == 0 {
		return nil, gweFail(http.StatusBadRequest, "Post not found.")
	}
	p, err := s.store.GetPost(c, id)
	if err != nil {
		return nil, gweFail(http.StatusNotFound, "Post not found.")
	}
	if !currentUser(c).Can(cap, p) {
		return nil, gweFail(http.StatusForbidden, "Access denied.")
	}
	return p, nil
}

var gweWidgetsCache sync.Map // home URL -> filled widgets config

func (s *Server) gweWidgetsConfig(c *gin.Context) []byte {
	home := s.links(c).Home
	if v, ok := gweWidgetsCache.Load(home); ok {
		return s.gweProWidgetsConfig(c, s.gweThemeWidgetsConfig(c, v.([]byte))) // plus the Site widgets and Pro patches
	}
	out := []byte(s.gweFill(c, string(gweData().widgetsCfg)))
	gweWidgetsCache.Store(home, out)
	return s.gweProWidgetsConfig(c, s.gweThemeWidgetsConfig(c, out))
}

// gweRenderContext is the renderer context for this site.
func (s *Server) gweRenderContext(ctx context.Context, editing bool) *builder.RenderContext {
	lb := s.links(ctx)
	kit, settings := s.gweKitSettings(ctx)
	rc := &builder.RenderContext{HomeURL: lb.Home, UploadsURL: lb.HomeURL("/wp-content/uploads"), Editing: editing}
	if kit != nil {
		rc.KitID = kit.ID
	}
	_ = settings
	if kit != nil {
		rc.Kit = gweDocument(kit)
	}
	rc.Breakpoints = builder.KitBreakpoints(rc.Kit)
	rc.PostTitle = func(id int64) string {
		if p, err := s.store.GetPost(ctx, id); err == nil {
			return p.Title
		}
		return ""
	}
	rc.Attachment = func(id int64) *builder.Attachment {
		a, err := s.store.GetAttachment(ctx, id)
		if err != nil {
			return nil
		}
		out := &builder.Attachment{ID: a.ID, URL: s.uploadURL(ctx, a.FilePath), Width: a.Width, Height: a.Height, Alt: a.Alt,
			Title: a.Title, Caption: a.Caption, Description: a.Description, MimeType: a.Mime}
		for _, v := range a.Variants {
			out.Sizes = append(out.Sizes, builder.AttachmentSize{Name: v.Name, URL: s.uploadURL(ctx, v.FilePath), Width: v.Width, Height: v.Height, MimeType: v.Mime})
		}
		return out
	}
	rc.Theme = s.gweThemeData(ctx)           // Site widgets (gowebeditor_theme.go)
	rc.Pro = s.gweProData(ctx)               // GoWebEditor Pro: dynamic tags, queries (gowebeditor_pro_data.go)
	rc.Atomic = s.gweAtomicEnv(ctx, editing) // GoWebEditor v4: atomic elements (gowebeditor_v4.go)
	rc.MediaURL = func(id int64, size string) string {
		a, err := s.store.GetAttachment(ctx, id)
		if err != nil {
			return ""
		}
		if size != "" && size != "full" {
			if v := a.Variant(size); v != nil {
				return s.uploadURL(ctx, v.FilePath)
			}
		}
		return s.uploadURL(ctx, a.FilePath)
	}
	return rc
}

func (s *Server) gweRenderWidget(c *gin.Context, raw json.RawMessage, editorID int64) (any, error) {
	p, err := s.gwePostFor(c, editorID, "edit_post")
	if err != nil {
		return nil, err
	}
	// Decoded with builder.ParseElements so the element keeps its saved settings order.
	var req struct {
		Data json.RawMessage `json:"data"`
	}
	_ = json.Unmarshal(raw, &req)
	els, err := builder.ParseElements(append(append([]byte("["), req.Data...), ']'))
	if err != nil || len(els) != 1 || els[0] == nil || els[0].ElType == "" {
		return nil, gweFail(http.StatusBadRequest, "Invalid element.")
	}
	s.gweThemeSample(c, p) // theme parts preview their Site widgets with sample content
	out, err := builder.RenderWidget(els[0], gweDocument(p), s.gweRenderContext(c, true))
	if err != nil {
		return nil, gweFail(http.StatusInternalServerError, err.Error())
	}
	return map[string]any{"render": s.gweFixURLs(c, out)}, nil
}

// gweLabAssets is where the renderer's embedded control defaults point (placeholder images).
var gweLabAssets = []string{"http://localhost:8095/wp-content/plugins/elementor/assets/", `http:\/\/localhost:8095\/wp-content\/plugins\/elementor\/assets\/`}

// gweFixURLs points upstream asset URLs in render output at the served GoWebEditor assets.
func (s *Server) gweFixURLs(ctx context.Context, out string) string {
	if !strings.Contains(out, "localhost:8095") {
		return out
	}
	assets := s.links(ctx).HomeURL(gweAssetsPath)
	return strings.NewReplacer(gweLabAssets[0], assets, gweLabAssets[1], strings.ReplaceAll(assets, "/", `\/`)).Replace(out)
}

// --- save_builder ------------------------------------------------------------------------------

// gwePostFields are the document settings upstream maps onto post fields instead of page settings.
var gwePostFields = map[string]bool{"post_title": true, "post_status": true, "post_excerpt": true, "post_featured_image": true,
	"menu_order": true, "comment_status": true, "template": true, "site_name": true, "site_description": true, "id": true}

func (s *Server) gweSaveBuilder(c *gin.Context, p *store.Post, data map[string]any, raw json.RawMessage) (any, error) {
	u := currentUser(c)
	status, _ := data["status"].(string)
	// The tree is stored as sent: re-marshalling a map would sort the settings keys, and their order
	// shows in the rendered markup.
	var req struct {
		Elements json.RawMessage `json:"elements"`
	}
	_ = json.Unmarshal(raw, &req)
	elements := []byte(strings.TrimSpace(string(req.Elements)))
	if _, err := builder.ParseElements(elements); err != nil || !strings.HasPrefix(string(elements), "[") {
		elements = []byte("[]")
	}
	settings, _ := data["settings"].(map[string]any)
	if settings == nil {
		settings = map[string]any{}
	}
	typ := gweDocType(p)
	published := p.Status == "publish" || p.Status == "private" || p.Status == "future"

	if status == "autosave" && published {
		id, err := s.gweAutosave(c, p, elements, settings)
		if err != nil {
			return nil, err
		}
		return s.gweSaveResponse(c, p, "inherit", gweRevBase+id, true)
	}

	if t, ok := settings["post_title"].(string); ok {
		p.Title = t
	}
	if p.Type == "post" {
		if t, ok := settings["post_excerpt"].(string); ok {
			p.Excerpt = t
		}
	}
	if img, ok := settings["post_featured_image"].(map[string]any); ok {
		p.FeaturedMediaID = gweInt(img["id"])
	}
	if _, ok := settings["menu_order"]; ok {
		p.MenuOrder = int(gweInt(settings["menu_order"]))
	}
	if cs, ok := settings["comment_status"].(string); ok && (cs == "open" || cs == "closed") {
		p.CommentStatus = cs
	}
	if tpl, ok := settings["template"].(string); ok {
		if tpl == "default" || tpl == "" {
			delete(p.Meta, "_wp_page_template")
		} else {
			p.Meta["_wp_page_template"] = tpl
		}
	}
	if typ == "kit" {
		opts := map[string]any{}
		if v, ok := settings["site_name"].(string); ok {
			opts["blogname"] = v
		}
		if v, ok := settings["site_description"].(string); ok {
			opts["blogdescription"] = v
		}
		if len(opts) > 0 && u.Can("manage_options", nil) {
			if err := s.settings.Set(c, opts); err != nil {
				return nil, err
			}
		}
	}
	page := map[string]any{}
	for k, v := range settings {
		if !gwePostFields[k] {
			page[k] = v
		}
	}

	switch status {
	case "publish", "private":
		if !u.Can("publish_post", p) && !u.Can("publish_"+gwePlural(p), nil) {
			status = "pending"
		}
	case "draft", "pending":
	case "autosave":
		status = p.Status // a draft's autosave saves the draft itself
		if status == "auto-draft" {
			status = "draft"
		}
	default:
		return nil, gweFail(http.StatusBadRequest, "Invalid status.")
	}
	if typ == "kit" {
		status = "publish"
	}
	if status == "publish" && p.Status == "future" {
		status = "future"
	}
	p.Status = status

	var compact bytes.Buffer
	if json.Compact(&compact, elements) == nil {
		elements = compact.Bytes()
	}
	p.Meta["_elementor_data"] = string(elements)
	p.Meta["_elementor_page_settings"] = page
	p.Meta["_elementor_edit_mode"] = "builder"
	p.Meta["_elementor_template_type"] = typ
	p.Meta["_elementor_version"] = gweVersion
	delete(p.Meta, "_elementor_css")
	if typ != "kit" {
		if res, err := builder.Render(gweDocument(p), s.gweRenderContext(c, false)); err == nil && res != nil {
			p.Content = gwePlainText(res.HTML)
		}
	}
	start := time.Now().Add(-time.Second)
	if err := s.store.UpdatePost(c, p, u.ID); err != nil {
		return nil, err
	}
	revID, err := s.gweRecordRevision(c, p, start, elements, page)
	if err != nil {
		return nil, err
	}
	_, _ = s.store.Pool.Exec(c, `DELETE FROM post_revisions WHERE post_id = $1 AND is_autosave`, p.ID)
	s.gweDropCSS(p.ID)
	fresh, err := s.store.GetPost(c, p.ID)
	if err == nil {
		p = fresh
	}
	s.afterPostSave(c, p)
	if typ == "kit" {
		s.purgeAll(c)
	}
	s.gweAfterPartSave(c, p) // GoWebEditor theme parts and theme kits update their custom theme
	_ = revID
	return s.gweSaveResponse(c, p, p.Status, p.ID, false)
}

// gweRecordRevision attaches the element tree to the revision UpdatePost stored, or stores one.
func (s *Server) gweRecordRevision(c *gin.Context, p *store.Post, since time.Time, elements []byte, page map[string]any) (int64, error) {
	if err := s.gweEnsureRevisionTable(c); err != nil {
		return 0, err
	}
	var revID int64
	err := s.store.Pool.QueryRow(c, `SELECT id FROM post_revisions WHERE post_id = $1 AND NOT is_autosave AND created_at >= $2
		AND id NOT IN (SELECT revision_id FROM builder_revisions) ORDER BY id DESC LIMIT 1`, p.ID, since).Scan(&revID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = s.store.Pool.QueryRow(c, `INSERT INTO post_revisions(post_id, author_id, title, excerpt, content)
			VALUES ($1,$2,$3,$4,$5) RETURNING id`, p.ID, nullable64(currentUser(c).ID), p.Title, p.Excerpt, p.Content).Scan(&revID)
	}
	if err != nil {
		return 0, err
	}
	settings, _ := json.Marshal(page)
	_, err = s.store.Pool.Exec(c, `INSERT INTO builder_revisions(revision_id, data, settings) VALUES ($1, $2::jsonb, $3::jsonb)
		ON CONFLICT (revision_id) DO UPDATE SET data = EXCLUDED.data, settings = EXCLUDED.settings`, revID, string(elements), string(settings))
	return revID, err
}

var gweRevTableOnce sync.Map // pool pointer -> done

func (s *Server) gweEnsureRevisionTable(ctx context.Context) error {
	if _, ok := gweRevTableOnce.Load(s.store.Pool); ok {
		return nil
	}
	s.initGoWebEditor(ctx)
	gweRevTableOnce.Store(s.store.Pool, true)
	return nil
}

// gweAutosave stores the autosave of a published document (an autosave revision, like upstream).
func (s *Server) gweAutosave(c *gin.Context, p *store.Post, elements []byte, settings map[string]any) (int64, error) {
	if err := s.gweEnsureRevisionTable(c); err != nil {
		return 0, err
	}
	title := p.Title
	if t, ok := settings["post_title"].(string); ok {
		title = t
	}
	page := map[string]any{}
	for k, v := range settings {
		if !gwePostFields[k] {
			page[k] = v
		}
	}
	var id int64
	err := s.store.Pool.QueryRow(c, `SELECT id FROM post_revisions WHERE post_id = $1 AND is_autosave ORDER BY id DESC LIMIT 1`, p.ID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		err = s.store.Pool.QueryRow(c, `INSERT INTO post_revisions(post_id, author_id, title, excerpt, content, is_autosave)
			VALUES ($1,$2,$3,$4,$5,true) RETURNING id`, p.ID, nullable64(currentUser(c).ID), title, p.Excerpt, p.Content).Scan(&id)
	} else if err == nil {
		_, err = s.store.Pool.Exec(c, `UPDATE post_revisions SET title = $2, updated_at = now(), created_at = now() WHERE id = $1`, id, title)
	}
	if err != nil {
		return 0, err
	}
	sj, _ := json.Marshal(page)
	_, err = s.store.Pool.Exec(c, `INSERT INTO builder_revisions(revision_id, data, settings) VALUES ($1, $2::jsonb, $3::jsonb)
		ON CONFLICT (revision_id) DO UPDATE SET data = EXCLUDED.data, settings = EXCLUDED.settings`, id, string(elements), string(sj))
	return id, err
}

func (s *Server) gweSaveResponse(c *gin.Context, p *store.Post, status string, currentID int64, autosave bool) (any, error) {
	revs, err := s.gweRevisions(c, p)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(revs))
	for _, r := range revs {
		ids = append(ids, r["id"].(int64))
	}
	latest := revs
	if len(latest) > 2 {
		latest = latest[:2]
	}
	st := p.Status
	if st == "auto-draft" {
		st = "draft"
	}
	link := s.links(c).Permalink(p)
	return map[string]any{
		"status": status,
		"config": map[string]any{"document": map[string]any{
			"last_edited": s.gweLastEdited(c, p, autosave),
			"urls":        map[string]string{"wp_preview": link + previewSep(link) + "preview_id=" + itoa64(p.ID) + "&preview=true"},
			"status":      map[string]string{"value": st, "label": gweStatusLabels[st]},
			"revisions":   map[string]any{"current_id": currentID},
		}},
		"latest_revisions": latest,
		"revisions_ids":    ids,
	}, nil
}

var gweTagStrip = []*regexp.Regexp{
	regexp.MustCompile(`(?is)<script\b.*?</script>`),
	regexp.MustCompile(`(?is)<style\b.*?</style>`),
	regexp.MustCompile(`(?i)</?(div|span|i|svg|path|section)\b[^>]*>`),
	regexp.MustCompile(`\n\s*\n+`),
}

// gwePlainText is the plain-HTML fallback stored in posts.content (upstream save_plain_text()).
func gwePlainText(h string) string {
	for i, re := range gweTagStrip {
		if i == len(gweTagStrip)-1 {
			h = re.ReplaceAllString(h, "\n")
		} else {
			h = re.ReplaceAllString(h, "")
		}
	}
	lines := strings.Split(h, "\n")
	out := lines[:0]
	for _, l := range lines {
		if t := strings.TrimSpace(l); t != "" {
			out = append(out, t)
		}
	}
	return strings.Join(out, "\n")
}

// --- revisions ---------------------------------------------------------------------------------

// gweRevBase offsets revision ids in the editor protocol: revisions live in their own table, so
// their ids would collide with post ids (upstream revisions are posts).
const gweRevBase = 1_000_000_000_000

func (s *Server) gweRevisionItem(c *gin.Context, id int64, authorID int64, t time.Time, typ, label string) map[string]any {
	author, email := "", ""
	if authorID > 0 {
		if a, err := s.store.UserByID(c, authorID); err == nil {
			author, email = a.DisplayName, a.Email
		}
	}
	lt := t.In(s.loc())
	return map[string]any{
		"id": id, "author": author, "timestamp": t.Unix(),
		"date": "<time>" + wp.HumanTimeDiff(t, time.Now()) + "</time> ago (<time>" + wp.FormatDate(lt, "M j @ H:i") + "</time>)",
		"type": typ, "typeLabel": label,
		"gravatar": fmt.Sprintf(`<img alt='' src='%s' class='avatar avatar-22 photo' height='22' width='22' decoding='async'/>`, s.avatarURL(c, email, 22)),
	}
}

// gweRevisions lists the current version, the autosave and the revisions that carry element trees.
func (s *Server) gweRevisions(c *gin.Context, p *store.Post) ([]map[string]any, error) {
	if err := s.gweEnsureRevisionTable(c); err != nil {
		return nil, err
	}
	out := []map[string]any{s.gweRevisionItem(c, p.ID, p.AuthorID, p.ModifiedAt, "current", "Current Version")}
	rows, err := s.store.Pool.Query(c, `SELECT r.id, coalesce(r.author_id,0), r.created_at, r.is_autosave FROM post_revisions r
		JOIN builder_revisions b ON b.revision_id = r.id WHERE r.post_id = $1 ORDER BY r.is_autosave DESC, r.id DESC LIMIT 100`, p.ID)
	if err != nil {
		return nil, err
	}
	type rev struct {
		id, author int64
		t          time.Time
		auto       bool
	}
	var list []rev
	for rows.Next() {
		var r rev
		if err := rows.Scan(&r.id, &r.author, &r.t, &r.auto); err != nil {
			rows.Close()
			return nil, err
		}
		list = append(list, r)
	}
	rows.Close()
	for _, r := range list {
		typ, label := "revision", "Revision"
		if r.auto {
			typ, label = "autosave", "Autosave"
		}
		out = append(out, s.gweRevisionItem(c, gweRevBase+r.id, r.author, r.t, typ, label))
	}
	return out, nil
}

func (s *Server) gweRevisionData(c *gin.Context, id int64) (any, error) {
	if err := s.gweEnsureRevisionTable(c); err != nil {
		return nil, err
	}
	if id < gweRevBase {
		p, err := s.store.GetPost(c, id)
		if err != nil {
			return nil, gweFail(http.StatusNotFound, "Invalid revision.")
		}
		if !currentUser(c).Can("edit_post", p) {
			return nil, gweFail(http.StatusForbidden, "Access denied.")
		}
		return map[string]any{"settings": gwePageSettings(p), "elements": gweElementsRaw(p)}, nil
	}
	var postID int64
	var data, settings []byte
	err := s.store.Pool.QueryRow(c, `SELECT r.post_id, b.data, b.settings FROM post_revisions r JOIN builder_revisions b ON b.revision_id = r.id
		WHERE r.id = $1`, id-gweRevBase).Scan(&postID, &data, &settings)
	if err != nil {
		return nil, gweFail(http.StatusNotFound, "Invalid revision.")
	}
	if _, err := s.gwePostFor(c, postID, "edit_post"); err != nil {
		return nil, err
	}
	return map[string]any{"settings": json.RawMessage(settings), "elements": json.RawMessage(data)}, nil
}

func (s *Server) gweDeleteRevision(c *gin.Context, id int64) (any, error) {
	id -= gweRevBase
	var postID int64
	if err := s.store.Pool.QueryRow(c, `SELECT post_id FROM post_revisions WHERE id = $1`, id).Scan(&postID); err != nil {
		return nil, gweFail(http.StatusNotFound, "Invalid revision.")
	}
	if _, err := s.gwePostFor(c, postID, "delete_post"); err != nil {
		return nil, err
	}
	_, err := s.store.Pool.Exec(c, `DELETE FROM post_revisions WHERE id = $1`, id)
	return true, err
}

// --- heartbeat ---------------------------------------------------------------------------------

// gweHeartbeat answers wp.heartbeat ticks; editors get a fresh elementor_ajax nonce.
func (s *Server) gweHeartbeat(c *gin.Context) {
	now := time.Now().Unix()
	out := gin.H{"wp-auth-check": currentUser(c) != nil, "server_time": now}
	if n := c.PostForm("_nonce"); n != "" && !s.checkNonceValue(c, n, "heartbeat-nonce") {
		out["nonces_expired"] = true
	}
	if c.PostForm("data[elementor_post_lock][post_ID]") != "" {
		out["elementorNonce"] = s.nonce(c, "elementor_ajax")
		out["elementor_server_time"] = now
	}
	if c.PostForm("data[wp-refresh-post-nonces][post_id]") != "" || c.PostForm("data[wp-refresh-nonces]") != "" {
		out["rest_nonce"] = s.nonce(c, "wp_rest")
		out["heartbeat_nonce"] = s.nonce(c, "heartbeat-nonce")
	}
	c.JSON(http.StatusOK, out)
}

// --- finder ------------------------------------------------------------------------------------

func (s *Server) gweFinderItems(c *gin.Context, data map[string]any) (any, error) {
	cat, _ := data["category"].(string)
	if cat != "edit" {
		return map[string]any{}, nil
	}
	posts, _, err := s.store.QueryPosts(c, store.PostQuery{Types: []string{"page", "post"}, Statuses: []string{"publish", "draft", "private", "pending", "future"},
		PerPage: 50, Page: 1, OrderBy: "modified", Order: "DESC"})
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	lb := s.links(c)
	u := currentUser(c)
	for _, p := range posts {
		if !u.Can("edit_post", p) {
			continue
		}
		icon := "post"
		if p.Type == "page" {
			icon = "document-file"
		}
		out[itoa64(p.ID)] = map[string]any{
			"title": p.Title, "icon": icon, "url": lb.HomeURL(gweEditURL(p.ID)),
			"keywords": []string{p.Type, "edit", strings.ToLower(p.Title)},
			"actions":  []map[string]string{{"name": "view", "url": lb.Permalink(p), "icon": "preview-medium"}},
		}
	}
	return out, nil
}

// --- CSS files ---------------------------------------------------------------------------------

// gweCSSPath is uploads/elementor/css/post-<id>.css.
func (s *Server) gweCSSPath(id int64) string {
	return filepath.Join(s.cfg.UploadsDir, "elementor", "css", "post-"+itoa64(id)+".css")
}

func (s *Server) gweDropCSS(id int64) {
	_ = os.Remove(s.gweCSSPath(id))
}
