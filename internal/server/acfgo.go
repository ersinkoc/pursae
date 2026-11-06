package server

import (
	"context"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
)

// ACF-Go is the native clone of Advanced Custom Fields PRO (docs/acf-go-features.md). Like Classic
// Editor, Pursae Cache and Pursae Web Builder it is compiled in, off on a fresh install and switched on the
// Plugins screen (option native_acfgo). While it is off its routes answer 404 and nothing of it
// reaches the admin menu, the editors, REST responses or theme output; field groups and saved
// values stay in the database.
//
// It reaches the rest of the server through a handful of one-line hooks: acfgoMiddleware (renders
// the field boxes into the edit screens and saves the submitted values), acfgoMenu (adminPage),
// acfgoRESTPost/Term/User (REST serializers), acfgoRESTSave (REST writes), acfgoTemplateFuncs
// (template FuncMap) and acfgoAdminPage (admin?page= options pages).

const (
	acfgoSlug   = "acf-go"
	acfgoOption = "native_acfgo"

	acfgoGroupType   = "acf-field-group"
	acfgoPostType    = "acf-post-type"
	acfgoTaxType     = "acf-taxonomy"
	acfgoOptionsType = "acf-ui-options-page"

	acfgoAssets = "/wp-admin/assets/acfgo/"
)

// acfgoRegisterAssets adds ACF-Go's script and style handles to the registry (after assets.go's
// init has loaded it, hence not in an init of this file).
func acfgoRegisterAssets() {
	registry.Scripts["acfgo-blocks"] = &regScript{Src: acfgoAssets + "acfgo-blocks.js", Deps: []string{"jquery", "wp-blocks", "wp-element", "wp-block-editor", "wp-components", "wp-data", "acfgo-input"}, Ver: Version, Footer: true}
	registry.Scripts["acfgo-input"] = &regScript{Src: acfgoAssets + "acfgo-input.js", Deps: []string{"jquery", "jquery-ui-sortable"}, Ver: Version, Footer: true}
	registry.Scripts["acfgo-field-group"] = &regScript{Src: acfgoAssets + "acfgo-field-group.js", Deps: []string{"jquery", "jquery-ui-sortable"}, Ver: Version, Footer: true}
	registry.Styles["acfgo"] = &regStyle{Src: acfgoAssets + "acfgo.css", Ver: Version}
}

var acfgoAssetsOnce sync.Once

// acfgoOn reports whether ACF-Go is active: one cached option read.
func (s *Server) acfgoOn() bool {
	return s.settings.Bool(acfgoOption)
}

// acfgoOnly is the middleware of ACF-Go's own routes: 404 while the plugin is off.
func (s *Server) acfgoOnly() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !s.acfgoOn() {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		c.Next()
	}
}

// acfgoActivate switches the plugin on and registers its content types and the post types and
// taxonomies created in its UI.
func (s *Server) acfgoActivate(ctx context.Context) error {
	if err := s.settings.Set(ctx, map[string]any{acfgoOption: true}); err != nil {
		return err
	}
	s.acfgoStart(ctx)
	s.purgeAll(ctx)
	return nil
}

// acfgoDeactivate switches the plugin off. Field groups, post types and values are kept.
func (s *Server) acfgoDeactivate(ctx context.Context) error {
	if err := s.settings.Set(ctx, map[string]any{acfgoOption: false}); err != nil {
		return err
	}
	s.purgeAll(ctx)
	return nil
}

// acfgoStart is the startup work of an active ACF-Go: its content types exist and the post types
// and taxonomies defined in ACF › Post Types / Taxonomies are registered.
func (s *Server) acfgoStart(ctx context.Context) {
	for _, t := range [][2]string{{acfgoGroupType, "Field Groups"}, {acfgoPostType, "Post Types"}, {acfgoTaxType, "Taxonomies"}, {acfgoOptionsType, "Options Pages"}} {
		if _, err := s.store.Pool.Exec(ctx, `INSERT INTO content_types (slug, name, hierarchical, public, rest_base, is_builtin, supports, labels)
			VALUES ($1, $2, false, false, '', false, '{"title":true}', jsonb_build_object('name', $2::text)) ON CONFLICT (slug) DO NOTHING`, t[0], t[1]); err != nil {
			log.Printf("acf-go: content type %s: %v", t[0], err)
		}
	}
	s.acfgoRegisterTypes(ctx)
}

// acfgoRegisterRoutes adds ACF-Go's admin screens (all 404 while the plugin is off).
func (s *Server) acfgoRegisterRoutes(admin *gin.RouterGroup) {
	acfgoAssetsOnce.Do(acfgoRegisterAssets)
	on := s.acfgoOnly()
	for _, p := range []struct {
		path string
		h    gin.HandlerFunc
	}{
		{"/acf-field-groups", s.acfgoGroupsScreen},
		{"/acf-field-group", s.acfgoGroupEdit},
		{"/acf-post-types", s.acfgoTypesScreen(acfgoPostType)},
		{"/acf-post-type", s.acfgoTypeEdit(acfgoPostType)},
		{"/acf-taxonomies", s.acfgoTypesScreen(acfgoTaxType)},
		{"/acf-taxonomy", s.acfgoTypeEdit(acfgoTaxType)},
		{"/acf-options-pages", s.acfgoTypesScreen(acfgoOptionsType)},
		{"/acf-options-page", s.acfgoTypeEdit(acfgoOptionsType)},
		{"/acf-tools", s.acfgoTools},
	} {
		admin.GET(p.path, on, p.h)
		admin.POST(p.path, on, p.h)
	}
}

// acfgoMiddleware is where ACF-Go meets the core's edit screens without touching them: before the
// handler of a screen that edits an object (post, user, term, comment, attachment) it saves the
// submitted acf[...] values and queues the field boxes, which acfgo-input.js moves into the form.
func (s *Server) acfgoMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		if !strings.HasPrefix(path, "/wp-admin/") || !s.settings.Installed() || !s.acfgoOn() {
			c.Next()
			return
		}
		u := currentUser(c)
		if u == nil {
			c.Next()
			return
		}
		screen := strings.TrimPrefix(path, "/wp-admin/")
		if c.Request.Method == http.MethodPost {
			s.acfgoSaveScreen(c, screen)
		} else if c.Request.Method == http.MethodGet {
			s.acfgoRenderScreen(c, screen)
		}
		c.Next()
	}
}

// acfgoAjax answers ACF-Go's admin-ajax actions.
func (s *Server) acfgoAjax(c *gin.Context, action string) bool {
	if !strings.HasPrefix(action, "acfgo/") || !s.acfgoOn() {
		return false
	}
	switch action {
	case "acfgo/validate_save_post":
		s.acfgoValidateAjax(c)
	case "acfgo/meta_box_save":
		s.acfgoMetaBoxSave(c)
	case "acfgo/query_posts":
		s.acfgoQueryPostsAjax(c)
	case "acfgo/oembed":
		s.acfgoOembedAjax(c)
	case "acfgo/block_form":
		s.acfgoBlockFormAjax(c)
	case "acfgo/block_preview":
		s.acfgoBlockPreviewAjax(c)
	default:
		c.String(http.StatusBadRequest, "0")
	}
	return true
}

// Admin menu ----------------------------------------------------------------------------------

// acfgoMenuPositions are the menu positions of the core's top-level items (wp-admin/menu.php).
var acfgoMenuPositions = map[string]float64{"menu-dashboard": 2, "menu-posts": 5, "menu-media": 10, "menu-pages": 20, "menu-comments": 25,
	"menu-appearance": 60, "menu-plugins": 65, "menu-users": 70, "menu-tools": 75, "toplevel_page_wpgo-cache": 76, "menu-settings": 80}

// acfgoMenu adds the ACF menu, the menus of options pages and of the post types created in ACF.
func (s *Server) acfgoMenu(c *gin.Context, menu []*menuTop, screen string) []*menuTop {
	if !s.acfgoOn() {
		return menu
	}
	u := currentUser(c)
	type entry struct {
		pos float64
		top *menuTop
	}
	var tops []entry
	sub := func(slug, title string, match ...string) *menuSub {
		ms := &menuSub{Slug: slug, Title: template.HTML(template.HTMLEscapeString(title)), Current: slug == screen}
		for _, m := range match {
			if m == screen || strings.HasPrefix(screen, m+"?") {
				ms.Current = true
			}
		}
		return ms
	}
	if u.Can("manage_options", nil) && s.settings.String("acfgo_show_admin") != "0" {
		top := &menuTop{Slug: "acf-field-groups", Title: "ACF", ID: "toplevel_page_edit-post_type-acf-field-group", Icon: "dashicons-welcome-widgets-menus"}
		top.Sub = []*menuSub{
			sub("acf-field-groups", "Field Groups", "acf-field-group"),
			sub("acf-post-types", "Post Types", "acf-post-type"),
			sub("acf-taxonomies", "Taxonomies", "acf-taxonomy"),
			sub("acf-options-pages", "Options Pages", "acf-options-page"),
			sub("acf-tools", "Tools"),
		}
		top.Sub[0].First = true
		tops = append(tops, entry{80.5, top})
	}
	ctx := c.Request.Context()
	// Post types created in ACF › Post Types.
	for _, pt := range s.acfgoPostTypes(ctx) {
		if !pt.ShowUI || !u.Can("edit_posts", nil) {
			continue
		}
		list := "edit?post_type=" + pt.Name
		top := &menuTop{Slug: list, Title: template.HTML(template.HTMLEscapeString(pt.label("menu_name"))), ID: "menu-posts-" + pt.Name, Icon: pt.MenuIcon}
		top.Sub = []*menuSub{sub(list, pt.label("all_items"), "post?post_type="+pt.Name), sub("post-new?post_type="+pt.Name, pt.label("add_new_item"))}
		top.Sub[0].First = true
		for _, tx := range s.acfgoTaxonomies(ctx) {
			if tx.ShowUI && tx.forType(pt.Name) {
				top.Sub = append(top.Sub, sub("edit-tags?taxonomy="+tx.Name+"&post_type="+pt.Name, tx.label("menu_name"), "term?taxonomy="+tx.Name))
			}
		}
		pos := pt.MenuPosition
		if pos == 0 {
			pos = 25.5
		}
		tops = append(tops, entry{pos, top})
	}
	// Taxonomies created in ACF for posts and pages go under their menus.
	for _, tx := range s.acfgoTaxonomies(ctx) {
		if !tx.ShowUI {
			continue
		}
		for _, m := range menu {
			if (m.ID == "menu-posts" && tx.forType("post")) || (m.ID == "menu-pages" && tx.forType("page")) {
				slug := "edit-tags?taxonomy=" + tx.Name
				if m.ID == "menu-pages" {
					slug += "&post_type=page"
				}
				ms := sub(slug, tx.label("menu_name"), "term?taxonomy="+tx.Name)
				if ms.Current {
					m.Current = true
				}
				m.Sub = append(m.Sub, ms)
			}
		}
	}
	// Options pages: top-level pages get their own menu, child pages join their parent.
	pages := s.acfgoOptionsPages(ctx)
	for _, op := range pages {
		if op.ParentSlug != "" || !u.Can(op.Capability, nil) {
			continue
		}
		slug := "admin?page=" + op.MenuSlug
		top := &menuTop{Slug: slug, Title: template.HTML(template.HTMLEscapeString(op.MenuTitle)), ID: "toplevel_page_" + op.MenuSlug, Icon: op.IconURL}
		top.Sub = []*menuSub{sub(slug, op.MenuTitle)}
		top.Sub[0].First = true
		pos := op.Position
		if pos == 0 {
			pos = 99.5
		}
		tops = append(tops, entry{pos, top})
	}
	for _, op := range pages {
		if op.ParentSlug == "" || !u.Can(op.Capability, nil) {
			continue
		}
		ms := sub("admin?page="+op.MenuSlug, op.MenuTitle)
		attach := func(m *menuTop) bool {
			if m.Slug == op.ParentSlug || m.Slug == "admin?page="+op.ParentSlug || strings.TrimSuffix(m.Slug, ".php") == strings.TrimSuffix(op.ParentSlug, ".php") {
				if ms.Current {
					m.Current = true
				}
				m.Sub = append(m.Sub, ms)
				return true
			}
			return false
		}
		done := false
		for _, m := range menu {
			if done = attach(m); done {
				break
			}
		}
		if !done {
			for _, e := range tops {
				if done = attach(e.top); done {
					break
				}
			}
		}
	}
	for _, e := range tops {
		for _, ms := range e.top.Sub {
			if ms.Current {
				e.top.Current = true
			}
		}
		if e.top.Slug == screen {
			e.top.Current = true
		}
		// Insert before the first core item placed after this position.
		at := len(menu)
		for i, m := range menu {
			if p, ok := acfgoMenuPositions[m.ID]; ok && p > e.pos {
				at = i
				// keep a separator that precedes the item with it
				if i > 0 && menu[i-1].Separator {
					at = i - 1
				}
				break
			}
		}
		menu = append(menu[:at], append([]*menuTop{e.top}, menu[at:]...)...)
	}
	for i, m := range menu {
		if m.Separator {
			continue
		}
		m.First = i == 0 || menu[i-1].Separator
		m.Last = i == len(menu)-1 || menu[i+1].Separator
	}
	return menu
}

// Small helpers --------------------------------------------------------------------------------

func acfgoInt(v any) int64 {
	switch t := v.(type) {
	case int:
		return int64(t)
	case int64:
		return t
	case float64:
		return int64(t)
	case string:
		n, _ := strconv.ParseInt(strings.TrimSpace(t), 10, 64)
		return n
	case bool:
		if t {
			return 1
		}
	}
	return 0
}

func acfgoStr(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case bool:
		if t {
			return "1"
		}
		return "0"
	}
	return ""
}

func acfgoBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return t != "" && t != "0" && t != "false"
	case float64:
		return t != 0
	case int:
		return t != 0
	case int64:
		return t != 0
	}
	return false
}

// acfgoList turns a stored value into a list (arrays stay, a scalar becomes one item, empty → nil).
func acfgoList(v any) []any {
	switch t := v.(type) {
	case nil:
		return nil
	case []any:
		return t
	case []string:
		out := make([]any, len(t))
		for i, s := range t {
			out[i] = s
		}
		return out
	case string:
		if t == "" {
			return nil
		}
		return []any{t}
	}
	return []any{v}
}

// acfgoUserByID is a nil-safe UserByID.
func (s *Server) acfgoUserByID(ctx context.Context, id int64) *store.User {
	if id <= 0 {
		return nil
	}
	u, err := s.store.UserByID(ctx, id)
	if err != nil {
		return nil
	}
	return u
}
