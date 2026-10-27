package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"wpgo/internal/blocks"
	"wpgo/internal/store"
	"wpgo/internal/wp"
)

// Widgets REST API: WP_REST_Sidebars_Controller, WP_REST_Widgets_Controller and
// WP_REST_Widget_Types_Controller, plus the batch/v1 endpoint the widgets editor saves through
// and the block renderer its server-side rendered blocks (archives, categories...) preview with.
//
// Widgets are block widgets (WP_Widget_Block): id "block-<n>" where n is the widgets row id,
// instance {"content": "<!-- wp:... -->"}. Widget areas are the active theme's register_sidebar()
// areas plus wp_inactive_widgets (widgets rows with a NULL area_id).

const inactiveSidebar = "wp_inactive_widgets"

// batchMaxItems is the batch/v1 requests.maxItems WordPress registers.
const batchMaxItems = 25

func (api *restAPI) registerWidgets() {
	api.register("wp/v2", "/wp/v2/sidebars", map[string]restHandler{http.MethodGet: api.sidebarsList}, nil)
	update := map[string]restHandler{http.MethodGet: api.sidebarGet, http.MethodPost: api.sidebarUpdate,
		http.MethodPut: api.sidebarUpdate, http.MethodPatch: api.sidebarUpdate}
	api.register("wp/v2", `/wp/v2/sidebars/(?P<id>[\w-]+)`, update, nil)
	api.register("wp/v2", "/wp/v2/widgets", map[string]restHandler{http.MethodGet: api.widgetsList, http.MethodPost: api.widgetCreate}, nil)
	api.register("wp/v2", `/wp/v2/widgets/(?P<id>[\w-]+)`, map[string]restHandler{http.MethodGet: api.widgetGet,
		http.MethodPost: api.widgetUpdate, http.MethodPut: api.widgetUpdate, http.MethodPatch: api.widgetUpdate, http.MethodDelete: api.widgetDelete}, nil)
	api.register("wp/v2", "/wp/v2/widget-types", map[string]restHandler{http.MethodGet: api.widgetTypesList}, nil)
	api.register("wp/v2", `/wp/v2/widget-types/(?P<id>[a-zA-Z0-9_-]+)`, map[string]restHandler{http.MethodGet: api.widgetTypeGet}, nil)
	api.register("wp/v2", `/wp/v2/widget-types/(?P<id>[a-zA-Z0-9_-]+)/encode`, map[string]restHandler{http.MethodPost: api.widgetTypeEncode}, nil)
	api.register("wp/v2", `/wp/v2/widget-types/(?P<id>[a-zA-Z0-9_-]+)/render`, map[string]restHandler{http.MethodPost: api.widgetTypeRender}, nil)
	api.register("batch/v1", "/batch/v1", map[string]restHandler{http.MethodPost: api.batch}, nil)
	api.register("wp/v2", `/wp/v2/block-renderer/(?P<name>[a-z0-9-]+/[a-z0-9-]+)`, map[string]restHandler{
		http.MethodGet: api.blockRenderer, http.MethodPost: api.blockRenderer}, nil)
}

// canManageWidgets is the permissions_check of the sidebars and widgets controllers.
func canManageWidgets(c *gin.Context) bool {
	u := restCurrent(c)
	if u == nil || !u.Can("edit_theme_options", nil) {
		status := http.StatusForbidden
		if u == nil {
			status = http.StatusUnauthorized
		}
		restErr(c, status, "rest_cannot_manage_widgets", "Sorry, you are not allowed to manage widgets on this site.")
		return false
	}
	return true
}

func (api *restAPI) restURL(c *gin.Context, route string) string {
	return api.s.links(c).HomeURL("/wp-json/" + strings.TrimPrefix(route, "/"))
}

// widgetAreas returns the active theme's areas, creating missing rows, as slug -> row id.
func (s *Server) widgetAreas(ctx context.Context) (*Theme, map[string]int64, error) {
	t := s.activeTheme()
	ids, err := s.store.WidgetAreaIDs(ctx, t.Slug)
	if err != nil {
		return t, nil, err
	}
	for _, a := range t.WidgetAreas {
		if _, ok := ids[a.ID]; !ok {
			id, err := s.store.EnsureWidgetArea(ctx, t.Slug, a.ID, a.Name)
			if err != nil {
				return t, nil, err
			}
			ids[a.ID] = id
		}
	}
	return t, ids, nil
}

// sidebarAreaID resolves a sidebar id to a widget_areas row (nil for wp_inactive_widgets).
func (s *Server) sidebarAreaID(ctx context.Context, sidebar string) (*int64, bool, error) {
	if sidebar == "" || sidebar == inactiveSidebar {
		return nil, true, nil
	}
	t, ids, err := s.widgetAreas(ctx)
	if err != nil {
		return nil, false, err
	}
	if t.widgetArea(sidebar) == nil {
		return nil, false, nil
	}
	id := ids[sidebar]
	return &id, true, nil
}

// widgetID is the WordPress widget id ("block-12").
func widgetID(w *store.AdminWidget) string { return w.Type + "-" + itoa64(w.ID) }

var widgetIDRe = regexp.MustCompile(`^(.+)-(\d+)$`)

// parseWidgetID is wp_parse_widget_id(); it returns the row id.
func parseWidgetID(id string) (string, int64, bool) {
	m := widgetIDRe.FindStringSubmatch(id)
	if m == nil {
		return "", 0, false
	}
	n, err := strconv.ParseInt(m[2], 10, 64)
	return m[1], n, err == nil
}

// widgetSidebar is the sidebar id a widget belongs to for the active theme.
func widgetSidebar(w *store.AdminWidget) string {
	if w.AreaSlug == "" {
		return inactiveSidebar
	}
	return w.AreaSlug
}

// loadWidget finds a widget by its WordPress id among the active theme's and the inactive widgets.
func (s *Server) loadWidget(c *gin.Context, id string) (*store.AdminWidget, bool) {
	base, n, ok := parseWidgetID(id)
	if !ok {
		return nil, false
	}
	w, err := s.store.GetAdminWidget(c, n)
	if err != nil || w.Type != base {
		return nil, false
	}
	if w.AreaSlug != "" && (w.AreaTheme != s.activeTheme().Slug || s.activeTheme().widgetArea(w.AreaSlug) == nil) {
		return nil, false
	}
	return w, true
}

// widgetHash is wp_hash() for encoded widget instances.
func (s *Server) widgetHash(data []byte) string {
	key := s.cfg.Secret
	if key == "" {
		key = s.settings.String("auth_secret")
	}
	m := hmac.New(sha256.New, []byte("widget-instance|"+key))
	m.Write(data)
	return hex.EncodeToString(m.Sum(nil))
}

func (s *Server) encodeInstance(instance map[string]any) map[string]any {
	raw, _ := json.Marshal(orEmptyMap(instance))
	return map[string]any{"encoded": base64.StdEncoding.EncodeToString(raw), "hash": s.widgetHash(raw), "raw": orEmptyMap(instance)}
}

func orEmptyMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// renderContext is the context dynamic blocks render with outside a front-end request.
func (s *Server) renderContext(c *gin.Context) context.Context {
	return withRender(c.Request.Context(), &renderCtx{s: s, lb: s.links(c)})
}

// renderWidget is wp_render_widget() for one widget.
func (s *Server) renderWidget(c *gin.Context, w *store.AdminWidget) string {
	return strings.TrimSpace(string(s.renderWidgetArea(s.renderContext(c), []*store.Widget{{ID: w.ID, AreaSlug: w.AreaSlug, Type: w.Type, Settings: w.Settings}})))
}

// blockWidgetType is WP_Widget_Block as the widget-types endpoint describes it.
func (api *restAPI) blockWidgetType(c *gin.Context) map[string]any {
	return map[string]any{
		"id": "block", "name": "Block", "description": "A widget containing a block.", "is_multi": true,
		"classname": "widget_block", "widget_class": "WP_Widget_Block", "option_name": "widget_block",
		"customize_selective_refresh": true,
		"_links": map[string]any{
			"collection": []map[string]any{{"href": api.restURL(c, "wp/v2/widget-types")}},
			"self":       []map[string]any{{"href": api.restURL(c, "wp/v2/widget-types/block")}},
		},
	}
}

func (api *restAPI) widgetTypeByID(c *gin.Context, id string) map[string]any {
	if id == "block" {
		return api.blockWidgetType(c)
	}
	return nil
}

// restWidget prepares a widget; edit forces the edit context (create, update and delete answer with it).
func (api *restAPI) restWidget(c *gin.Context, w *store.AdminWidget, embed, edit bool) map[string]any {
	s := api.s
	sidebar := widgetSidebar(w)
	out := map[string]any{"id": widgetID(w), "id_base": w.Type, "sidebar": sidebar, "rendered": ""}
	if sidebar != inactiveSidebar {
		out["rendered"] = s.renderWidget(c, w)
	}
	if edit || restContext(c) == "edit" {
		out["rendered_form"] = nil
		out["instance"] = s.encodeInstance(w.Settings)
		if w.Type == "block" {
			content, _ := w.Settings["content"].(string)
			out["rendered_form"] = blockWidgetForm(w.ID, content)
		}
	}
	out["_links"] = map[string]any{
		"self":                      []map[string]any{{"href": api.restURL(c, "wp/v2/widgets/"+widgetID(w))}},
		"collection":                []map[string]any{{"href": api.restURL(c, "wp/v2/widgets")}},
		"about":                     []map[string]any{{"href": api.restURL(c, "wp/v2/widget-types/"+w.Type), "embeddable": true}},
		"https://api.w.org/sidebar": []map[string]any{{"href": api.restURL(c, "wp/v2/sidebars/"+sidebar+"/")}},
	}
	if embed {
		if t := api.widgetTypeByID(c, w.Type); t != nil {
			out["_embedded"] = map[string]any{"about": []any{t}}
		}
	}
	return out
}

// blockWidgetForm is WP_Widget_Block::form().
func blockWidgetForm(number int64, content string) string {
	n := strconv.FormatInt(number, 10)
	return `<p><label for="widget-block-` + n + `-content" class="screen-reader-text">Block HTML:</label><textarea id="widget-block-` + n +
		`-content" name="widget-block[` + n + `][content]" rows="6" cols="50" class="widefat code">` + html.EscapeString(content) + `</textarea></p>`
}

func wantsEmbed(c *gin.Context) bool {
	v, ok := c.GetQuery("_embed")
	if !ok {
		return false
	}
	if v == "" || v == "1" || v == "true" {
		return true
	}
	for _, rel := range strings.Split(v, ",") {
		if strings.TrimSpace(rel) == "about" {
			return true
		}
	}
	return false
}

// Sidebars -----------------------------------------------------------------------

func (api *restAPI) restSidebar(c *gin.Context, t *Theme, id string, widgets []string) map[string]any {
	out := map[string]any{"id": id, "name": "", "description": "", "class": "", "before_widget": "", "after_widget": "",
		"before_title": "", "after_title": "", "status": "inactive", "widgets": widgets}
	if a := t.widgetArea(id); a != nil {
		out["status"], out["name"], out["description"] = "active", a.Name, a.Description
		out["before_widget"], out["after_widget"], out["before_title"], out["after_title"] = a.BeforeWidget, a.AfterWidget, a.BeforeTitle, a.AfterTitle
	} else if id == inactiveSidebar {
		out["name"] = "Inactive widgets"
	}
	if out["widgets"] == nil {
		out["widgets"] = []string{}
	}
	out["_links"] = map[string]any{
		"collection":               []map[string]any{{"href": api.restURL(c, "wp/v2/sidebars")}},
		"self":                     []map[string]any{{"href": api.restURL(c, "wp/v2/sidebars/"+id)}},
		"https://api.w.org/widget": []map[string]any{{"href": api.restURL(c, "wp/v2/widgets?sidebar="+id), "embeddable": true}},
	}
	return out
}

// sidebarsWidgets is wp_get_sidebars_widgets() for the active theme.
func (api *restAPI) sidebarsWidgets(c *gin.Context) (*Theme, map[string][]*store.AdminWidget, error) {
	t, _, err := api.s.widgetAreas(c)
	if err != nil {
		return t, nil, err
	}
	list, err := api.s.store.ThemeWidgets(c, t.Slug)
	if err != nil {
		return t, nil, err
	}
	out := map[string][]*store.AdminWidget{}
	for _, w := range list {
		if w.AreaSlug != "" && t.widgetArea(w.AreaSlug) == nil {
			continue
		}
		sb := widgetSidebar(w)
		out[sb] = append(out[sb], w)
	}
	return t, out, nil
}

func widgetIDs(ws []*store.AdminWidget) []string {
	ids := []string{}
	for _, w := range ws {
		ids = append(ids, widgetID(w))
	}
	return ids
}

func (api *restAPI) sidebarsList(c *gin.Context, r *restRequest) {
	if !canManageWidgets(c) {
		return
	}
	t, sw, err := api.sidebarsWidgets(c)
	if err != nil {
		restErr(c, http.StatusInternalServerError, "rest_sidebars_failed", err.Error())
		return
	}
	out := []map[string]any{}
	for _, a := range t.WidgetAreas {
		out = append(out, api.restSidebar(c, t, a.ID, widgetIDs(sw[a.ID])))
	}
	out = append(out, api.restSidebar(c, t, inactiveSidebar, widgetIDs(sw[inactiveSidebar])))
	restJSON(c, http.StatusOK, out)
}

func (api *restAPI) sidebarGet(c *gin.Context, r *restRequest) {
	if !canManageWidgets(c) {
		return
	}
	t, sw, err := api.sidebarsWidgets(c)
	if err != nil {
		restErr(c, http.StatusInternalServerError, "rest_sidebars_failed", err.Error())
		return
	}
	id := r.Params["id"]
	if id != inactiveSidebar && t.widgetArea(id) == nil {
		restErr(c, http.StatusNotFound, "rest_sidebar_not_found", "No sidebar exists with that id.")
		return
	}
	restJSON(c, http.StatusOK, api.restSidebar(c, t, id, widgetIDs(sw[id])))
}

func (api *restAPI) sidebarUpdate(c *gin.Context, r *restRequest) {
	if !canManageWidgets(c) {
		return
	}
	s := api.s
	id := r.Params["id"]
	areaID, ok, err := s.sidebarAreaID(c, id)
	if err != nil {
		restErr(c, http.StatusInternalServerError, "rest_sidebars_failed", err.Error())
		return
	}
	if !ok {
		restErr(c, http.StatusNotFound, "rest_sidebar_not_found", "No sidebar exists with that id.")
		return
	}
	if raw, has := r.Body["widgets"]; has {
		list, isList := raw.([]any)
		if !isList && raw != nil {
			restErr(c, http.StatusBadRequest, "rest_invalid_param", "Invalid parameter(s): widgets", map[string]any{"params": map[string]string{"widgets": "widgets is not of type array."}})
			return
		}
		var ids []int64
		seen := map[int64]bool{}
		for _, v := range list {
			sid, _ := v.(string)
			if w, ok := s.loadWidget(c, sid); ok && !seen[w.ID] {
				seen[w.ID] = true
				ids = append(ids, w.ID)
			}
		}
		if err := s.store.SetAreaWidgets(c, areaID, ids); err != nil {
			restErr(c, http.StatusInternalServerError, "rest_sidebars_failed", err.Error())
			return
		}
		s.purgeTags("widgets")
	} else if vals := c.PostFormArray("widgets[]"); len(vals) > 0 {
		var ids []int64
		for _, sid := range vals {
			if w, ok := s.loadWidget(c, sid); ok {
				ids = append(ids, w.ID)
			}
		}
		if err := s.store.SetAreaWidgets(c, areaID, ids); err != nil {
			restErr(c, http.StatusInternalServerError, "rest_sidebars_failed", err.Error())
			return
		}
		s.purgeTags("widgets")
	}
	api.sidebarGet(c, r)
}

// Widgets ------------------------------------------------------------------------

func (api *restAPI) widgetsList(c *gin.Context, r *restRequest) {
	if !canManageWidgets(c) {
		return
	}
	t, sw, err := api.sidebarsWidgets(c)
	if err != nil {
		restErr(c, http.StatusInternalServerError, "rest_widgets_failed", err.Error())
		return
	}
	filter, hasFilter := c.GetQuery("sidebar")
	embed := wantsEmbed(c)
	order := []string{}
	for _, a := range t.WidgetAreas {
		order = append(order, a.ID)
	}
	order = append(order, inactiveSidebar)
	out := []map[string]any{}
	for _, sb := range order {
		if hasFilter && sb != filter {
			continue
		}
		for _, w := range sw[sb] {
			out = append(out, api.restWidget(c, w, embed, false))
		}
	}
	restJSON(c, http.StatusOK, out)
}

func (api *restAPI) widgetGet(c *gin.Context, r *restRequest) {
	if !canManageWidgets(c) {
		return
	}
	w, ok := api.s.loadWidget(c, r.Params["id"])
	if !ok {
		restErr(c, http.StatusNotFound, "rest_widget_not_found", "No widget was found with that id.")
		return
	}
	restJSON(c, http.StatusOK, api.restWidget(c, w, wantsEmbed(c), false))
}

// widgetInstance reads instance (raw, or encoded + hash) or form_data from a request, like save_widget().
func (api *restAPI) widgetInstance(c *gin.Context, r *restRequest, idBase string) (map[string]any, bool, error) {
	if v, ok := r.Body["instance"]; ok && v != nil {
		inst, isMap := v.(map[string]any)
		if !isMap {
			return nil, false, errors.New("The provided instance is invalid. Must contain raw OR encoded and hash.")
		}
		if raw, ok := inst["raw"]; ok {
			m, isMap := raw.(map[string]any)
			if !isMap {
				if l, isList := raw.([]any); isList && len(l) == 0 {
					m = map[string]any{}
				} else {
					return nil, false, errors.New("The provided instance is invalid. Must contain raw OR encoded and hash.")
				}
			}
			return m, true, nil
		}
		enc, _ := inst["encoded"].(string)
		hash, _ := inst["hash"].(string)
		if enc != "" && hash != "" {
			data, err := base64.StdEncoding.DecodeString(enc)
			if err != nil || !hmac.Equal([]byte(api.s.widgetHash(data)), []byte(hash)) {
				return nil, false, errors.New("The provided instance is malformed.")
			}
			m := map[string]any{}
			if err := json.Unmarshal(data, &m); err != nil {
				return nil, false, errors.New("The provided instance is malformed.")
			}
			return m, true, nil
		}
		return nil, false, errors.New("The provided instance is invalid. Must contain raw OR encoded and hash.")
	}
	if fd, ok := r.get(c, "form_data"); ok {
		if m := formDataInstance(fd, idBase); m != nil {
			return m, true, nil
		}
	}
	return nil, false, nil
}

var formFieldRe = regexp.MustCompile(`^widget-([\w-]+)\[(-?\d*)\]\[([^\]]+)\]$`)

// formDataInstance extracts widget-<id_base>[n][field] values from a serialized widget form.
func formDataInstance(fd any, idBase string) map[string]any {
	var q url.Values
	switch t := fd.(type) {
	case string:
		q, _ = url.ParseQuery(t)
	case map[string]any:
		if inner, ok := t["widget-"+idBase].(map[string]any); ok {
			for _, v := range inner {
				if m, ok := v.(map[string]any); ok {
					return m
				}
			}
		}
		return nil
	default:
		return nil
	}
	out := map[string]any{}
	for k, v := range q {
		if m := formFieldRe.FindStringSubmatch(k); m != nil && m[1] == idBase && len(v) > 0 {
			out[m[3]] = v[len(v)-1]
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// sanitizeBlockInstance is WP_Widget_Block::update().
func sanitizeBlockInstance(c *gin.Context, in map[string]any) map[string]any {
	content, _ := in["content"].(string)
	if u := restCurrent(c); u == nil || !u.Can("unfiltered_html", nil) {
		content = wp.KsesPost(content)
	}
	out := map[string]any{}
	for k, v := range in {
		out[k] = v
	}
	out["content"] = content
	return out
}

func (api *restAPI) widgetCreate(c *gin.Context, r *restRequest) {
	if !canManageWidgets(c) {
		return
	}
	s := api.s
	if id, _ := r.Str(c, "id"); id != "" {
		restErr(c, http.StatusBadRequest, "rest_invalid_param", "Cannot create existing widget.")
		return
	}
	idBase, _ := r.Str(c, "id_base")
	if idBase == "" {
		restErr(c, http.StatusBadRequest, "rest_invalid_widget", "Widget type (id_base) is required.")
		return
	}
	if idBase != "block" {
		restErr(c, http.StatusBadRequest, "rest_invalid_widget", "The provided widget type (id_base) cannot be updated.")
		return
	}
	sidebar, _ := r.Str(c, "sidebar")
	if sidebar == "" {
		sidebar = inactiveSidebar
	}
	areaID, ok, err := s.sidebarAreaID(c, sidebar)
	if err != nil {
		restErr(c, http.StatusInternalServerError, "rest_widgets_failed", err.Error())
		return
	}
	if !ok {
		restErr(c, http.StatusBadRequest, "rest_invalid_widget", "Invalid widget area.")
		return
	}
	inst, _, err := api.widgetInstance(c, r, idBase)
	if err != nil {
		restErr(c, http.StatusBadRequest, "rest_invalid_widget", err.Error())
		return
	}
	n, err := s.store.CreateWidget(c, areaID, idBase, sanitizeBlockInstance(c, orEmptyMap(inst)))
	if err != nil {
		restErr(c, http.StatusInternalServerError, "rest_widgets_failed", err.Error())
		return
	}
	s.purgeTags("widgets")
	w, _ := s.store.GetAdminWidget(c, n)
	restJSON(c, http.StatusCreated, api.restWidget(c, w, wantsEmbed(c), true))
}

func (api *restAPI) widgetUpdate(c *gin.Context, r *restRequest) {
	if !canManageWidgets(c) {
		return
	}
	s := api.s
	w, ok := s.loadWidget(c, r.Params["id"])
	if !ok {
		restErr(c, http.StatusNotFound, "rest_widget_not_found", "No widget was found with that id.")
		return
	}
	inst, has, err := api.widgetInstance(c, r, w.Type)
	if err != nil {
		restErr(c, http.StatusBadRequest, "rest_invalid_widget", err.Error())
		return
	}
	changed := false
	if has {
		if err := s.store.UpdateWidgetSettings(c, w.ID, sanitizeBlockInstance(c, inst)); err != nil {
			restErr(c, http.StatusInternalServerError, "rest_widgets_failed", err.Error())
			return
		}
		changed = true
	}
	if sidebar, ok := r.Str(c, "sidebar"); ok && sidebar != "" && sidebar != widgetSidebar(w) {
		areaID, valid, err := s.sidebarAreaID(c, sidebar)
		if err != nil {
			restErr(c, http.StatusInternalServerError, "rest_widgets_failed", err.Error())
			return
		}
		if !valid {
			restErr(c, http.StatusBadRequest, "rest_invalid_widget", "Invalid widget area.")
			return
		}
		if err := s.store.MoveWidget(c, w.ID, areaID); err != nil {
			restErr(c, http.StatusInternalServerError, "rest_widgets_failed", err.Error())
			return
		}
		changed = true
	}
	if changed {
		s.purgeTags("widgets")
	}
	w, _ = s.loadWidget(c, widgetID(w))
	restJSON(c, http.StatusOK, api.restWidget(c, w, wantsEmbed(c), true))
}

func (api *restAPI) widgetDelete(c *gin.Context, r *restRequest) {
	if !canManageWidgets(c) {
		return
	}
	s := api.s
	w, ok := s.loadWidget(c, r.Params["id"])
	if !ok {
		restErr(c, http.StatusNotFound, "rest_widget_not_found", "No widget was found with that id.")
		return
	}
	if force, _ := r.Bool(c, "force"); force {
		prev := api.restWidget(c, w, false, true)
		if err := s.store.DeleteWidget(c, w.ID); err != nil {
			restErr(c, http.StatusInternalServerError, "rest_widgets_failed", err.Error())
			return
		}
		s.purgeTags("widgets")
		restJSON(c, http.StatusOK, map[string]any{"deleted": true, "previous": prev})
		return
	}
	if err := s.store.MoveWidget(c, w.ID, nil); err != nil {
		restErr(c, http.StatusInternalServerError, "rest_widgets_failed", err.Error())
		return
	}
	s.purgeTags("widgets")
	w, _ = s.loadWidget(c, widgetID(w))
	restJSON(c, http.StatusOK, api.restWidget(c, w, false, true))
}

// Widget types -------------------------------------------------------------------

func (api *restAPI) widgetTypesList(c *gin.Context, r *restRequest) {
	if !canManageWidgets(c) {
		return
	}
	restJSON(c, http.StatusOK, []map[string]any{api.blockWidgetType(c)})
}

func (api *restAPI) widgetTypeGet(c *gin.Context, r *restRequest) {
	if !canManageWidgets(c) {
		return
	}
	t := api.widgetTypeByID(c, r.Params["id"])
	if t == nil {
		restErr(c, http.StatusNotFound, "rest_widget_type_invalid", "Invalid widget type.")
		return
	}
	restJSON(c, http.StatusOK, t)
}

func (api *restAPI) widgetTypeEncode(c *gin.Context, r *restRequest) {
	if !canManageWidgets(c) {
		return
	}
	s := api.s
	if api.widgetTypeByID(c, r.Params["id"]) == nil {
		restErr(c, http.StatusBadRequest, "rest_invalid_widget", "Cannot preview a widget that does not extend WP_Widget.")
		return
	}
	number := int64(-1)
	if n, ok := r.Int(c, "number"); ok {
		number = n
	}
	inst := map[string]any{}
	if v, ok := r.Body["instance"].(map[string]any); ok {
		enc, _ := v["encoded"].(string)
		hash, _ := v["hash"].(string)
		if enc != "" && hash != "" {
			data, err := base64.StdEncoding.DecodeString(enc)
			if err != nil || !hmac.Equal([]byte(s.widgetHash(data)), []byte(hash)) || json.Unmarshal(data, &inst) != nil {
				restErr(c, http.StatusBadRequest, "rest_invalid_widget", "The provided instance is malformed.")
				return
			}
		}
	}
	if fd, ok := r.get(c, "form_data"); ok {
		if m := formDataInstance(fd, "block"); m != nil {
			inst = sanitizeBlockInstance(c, m)
		}
	}
	content, _ := inst["content"].(string)
	preview := s.renderWidget(c, &store.AdminWidget{ID: number, Type: "block", Settings: inst})
	enc := s.encodeInstance(inst)
	restJSON(c, http.StatusOK, map[string]any{"form": blockWidgetForm(number, content), "preview": preview, "instance": enc})
}

func (api *restAPI) widgetTypeRender(c *gin.Context, r *restRequest) {
	if !canManageWidgets(c) {
		return
	}
	s := api.s
	inst := map[string]any{}
	if v, ok := r.Body["instance"].(map[string]any); ok {
		if raw, ok := v["raw"].(map[string]any); ok {
			inst = raw
		} else if enc, _ := v["encoded"].(string); enc != "" {
			data, err := base64.StdEncoding.DecodeString(enc)
			hash, _ := v["hash"].(string)
			if err == nil && hmac.Equal([]byte(s.widgetHash(data)), []byte(hash)) {
				_ = json.Unmarshal(data, &inst)
			}
		}
	}
	body := s.renderWidget(c, &store.AdminWidget{ID: -1, Type: "block", Settings: inst})
	lb := s.links(c)
	doc := `<!doctype html><html><head><meta charset="utf-8"><link rel="stylesheet" href="` + lb.HomeURL("/wp-includes/css/dist/block-library/style.min.css") +
		`"><link rel="stylesheet" href="` + lb.HomeURL(s.activeTheme().URI()+"/style.css") + `"></head><body class="wp-embed-responsive"><div class="widget-area">` + body + `</div></body></html>`
	restJSON(c, http.StatusOK, map[string]any{"preview": doc})
}

// Batch ---------------------------------------------------------------------------

// batch is WP_REST_Server::serve_batch_request_v1(): each request runs through the router in turn
// with the caller's credentials.
func (api *restAPI) batch(c *gin.Context, r *restRequest) {
	s := api.s
	list, _ := r.Body["requests"].([]any)
	if len(list) == 0 {
		restErr(c, http.StatusBadRequest, "rest_missing_callback_param", "Missing parameter(s): requests", map[string]any{"params": []string{"requests"}})
		return
	}
	if len(list) > batchMaxItems {
		restErr(c, http.StatusBadRequest, "rest_invalid_param", "Invalid parameter(s): requests", map[string]any{"params": map[string]string{"requests": "requests must contain at most 25 items."}})
		return
	}
	responses := []any{}
	for _, item := range list {
		m, _ := item.(map[string]any)
		method, _ := m["method"].(string)
		if method == "" {
			method = http.MethodPost
		}
		method = strings.ToUpper(method)
		path, _ := m["path"].(string)
		if path == "" || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "/batch/") ||
			(method != http.MethodPost && method != http.MethodPut && method != http.MethodPatch && method != http.MethodDelete) {
			responses = append(responses, map[string]any{"body": restError{Code: "rest_batch_not_allowed", Message: "The requested route does not support batch requests.",
				Data: map[string]any{"status": http.StatusBadRequest}}, "status": http.StatusBadRequest, "headers": map[string]string{}})
			continue
		}
		var body []byte
		if b, ok := m["body"]; ok && b != nil {
			body, _ = json.Marshal(b)
		}
		req := httptest.NewRequest(method, "/wp-json"+path, bytes.NewReader(body)).WithContext(c.Request.Context())
		if len(body) > 0 {
			req.Header.Set("Content-Type", "application/json")
		}
		for _, ck := range c.Request.Cookies() {
			req.AddCookie(ck)
		}
		for _, h := range []string{"X-WP-Nonce", "Authorization", "User-Agent"} {
			if v := c.GetHeader(h); v != "" {
				req.Header.Set(h, v)
			}
		}
		if hs, ok := m["headers"].(map[string]any); ok {
			for k, v := range hs {
				if sv, ok := v.(string); ok && !strings.EqualFold(k, "Authorization") && !strings.EqualFold(k, "Cookie") {
					req.Header.Set(k, sv)
				}
			}
		}
		req.Host = c.Request.Host
		req.RemoteAddr = c.Request.RemoteAddr
		rec := httptest.NewRecorder()
		s.engine.ServeHTTP(rec, req)
		var out any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		headers := map[string]string{}
		for _, h := range []string{"Allow", "X-WP-Total", "X-WP-TotalPages", "Location"} {
			if v := rec.Header().Get(h); v != "" {
				headers[h] = v
			}
		}
		responses = append(responses, map[string]any{"body": out, "status": rec.Code, "headers": headers})
	}
	restJSON(c, http.StatusMultiStatus, map[string]any{"responses": responses})
}

// batchOptions is what OPTIONS /batch/v1 answers in WordPress; core-data reads requests.maxItems from it.
func batchOptions() map[string]any {
	return map[string]any{
		"namespace": "batch/v1", "methods": []string{"POST"},
		"endpoints": []map[string]any{{"methods": []string{"POST"}, "args": map[string]any{
			"validation": map[string]any{"type": "string", "enum": []string{"require-all-validate", "normal"}, "default": "normal", "required": false},
			"requests":   map[string]any{"type": "array", "maxItems": batchMaxItems, "required": true, "items": map[string]any{"type": "object"}},
		}}},
	}
}

// Block renderer ----------------------------------------------------------------------

// blockRenderer is WP_REST_Block_Renderer_Controller: it renders a dynamic block with the
// given attributes for ServerSideRender previews.
func (api *restAPI) blockRenderer(c *gin.Context, r *restRequest) {
	u := restCurrent(c)
	if u == nil || !u.Can("edit_posts", nil) {
		status := http.StatusForbidden
		if u == nil {
			status = http.StatusUnauthorized
		}
		restErr(c, status, "block_cannot_read", "Sorry, you are not allowed to read blocks as this user.")
		return
	}
	attrs := map[string]any{}
	if a, ok := r.Body["attributes"].(map[string]any); ok {
		attrs = a
	} else {
		attrs = queryAttributes(c.Request.URL.Query(), "attributes")
	}
	name := r.Params["name"]
	b := &blocks.Block{BlockName: &name, Attrs: attrs, InnerBlocks: []*blocks.Block{}, InnerContent: []*string{}}
	restJSON(c, http.StatusOK, map[string]any{"rendered": blocks.RenderBlock(api.s.renderContext(c), b)})
}

// queryAttributes decodes PHP-style nested query args (attributes[a][b]=1) into a map, typing
// booleans and numbers the way rest_sanitize_value_from_schema() would for the common attributes.
func queryAttributes(q url.Values, prefix string) map[string]any {
	root := map[string]any{}
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !strings.HasPrefix(k, prefix+"[") {
			continue
		}
		parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(k, prefix+"["), "]"), "][")
		cur := root
		for i, p := range parts {
			if i == len(parts)-1 {
				cur[p] = typedQueryValue(q[k][len(q[k])-1])
				break
			}
			next, ok := cur[p].(map[string]any)
			if !ok {
				next = map[string]any{}
				cur[p] = next
			}
			cur = next
		}
	}
	for k, v := range root {
		root[k] = listify(v)
	}
	return root
}

func typedQueryValue(v string) any {
	switch v {
	case "true":
		return true
	case "false":
		return false
	}
	// Leading zeros ("007") stay strings; plain numbers become numbers.
	if v == "0" || !strings.HasPrefix(v, "0") || strings.HasPrefix(v, "0.") {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return v
}

// listify turns maps keyed 0..n-1 into slices.
func listify(v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	for k, x := range m {
		m[k] = listify(x)
	}
	if len(m) == 0 {
		return m
	}
	list := make([]any, len(m))
	for k, x := range m {
		i, err := strconv.Atoi(k)
		if err != nil || i < 0 || i >= len(m) {
			return m
		}
		list[i] = x
	}
	return list
}
