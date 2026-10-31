package server

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"

	"wpgo/internal/blocks"
)

// Block patterns for the block editor: /wp/v2/block-patterns/patterns and /categories
// (WP_REST_Block_Patterns_Controller, WP_REST_Block_Pattern_Categories_Controller). The registry is
// core's patterns (defaults/block-patterns.json) followed by the active theme's (Theme.Patterns: the
// theme's patterns/*.php run by WordPress at build time, so the theme needs no PHP here).

// patternCategoryMigration is WP_REST_Block_Patterns_Controller::$categories_migration: old core
// pattern categories mapped to the current ones.
var patternCategoryMigration = map[string]string{"buttons": "call-to-action", "columns": "text", "query": "posts"}

// themePatternSet is a theme's patterns file.
type themePatternSet struct {
	Categories []map[string]any `json:"categories"`
	Patterns   []map[string]any `json:"patterns"`
}

var themePatternCache sync.Map // web path -> *themePatternSet

// themePatterns reads a theme's patterns file once; nil when the theme has none.
func (s *Server) themePatterns(t *Theme) *themePatternSet {
	if t == nil || t.Patterns == "" {
		return nil
	}
	name := "themes/" + t.Slug + "/" + t.Patterns
	if v, ok := themePatternCache.Load(name); ok {
		return v.(*themePatternSet)
	}
	b, err := fs.ReadFile(s.web, name)
	if err != nil {
		return nil
	}
	set := &themePatternSet{}
	if err := json.Unmarshal(b, set); err != nil {
		return nil
	}
	for _, p := range set.Patterns {
		if content, _ := p["content"].(string); content != "" && !patternRendersInContent(content) {
			p["inserter"] = false
		}
	}
	themePatternCache.Store(name, set)
	return set
}

// patternRendersInContent reports whether every block of a pattern renders in post content here: a
// block that saves no markup of its own (site title, navigation, query loop, post template, comments,
// template parts) needs a server-side renderer. Theme patterns made of such blocks are templates and
// header or footer parts for the Site Editor; they stay registered (so wp:pattern references resolve)
// but are hidden from the inserter, like WordPress' "Inserter: no" patterns.
func patternRendersInContent(content string) bool {
	var ok func(list []*blocks.Block) bool
	ok = func(list []*blocks.Block) bool {
		for _, b := range list {
			name := b.Name()
			if name == "" {
				continue
			}
			if strings.TrimSpace(b.InnerHTML) == "" && !blocks.IsDynamic(name) {
				return false
			}
			if !ok(b.InnerBlocks) {
				return false
			}
		}
		return true
	}
	return ok(blocks.Parse(content))
}

// migratePattern is migrate_pattern_categories() on a copy of a registered pattern.
func migratePattern(p map[string]any) map[string]any {
	out := make(map[string]any, len(p))
	for k, v := range p {
		out[k] = v
	}
	if cats, ok := p["categories"].([]any); ok {
		mc := make([]any, len(cats))
		for i, c := range cats {
			if to, ok := patternCategoryMigration[c.(string)]; ok {
				c = to
			}
			mc[i] = c
		}
		out["categories"] = mc
	}
	return out
}

// canViewPatterns is get_items_permissions_check(): any user who can edit posts.
func canViewPatterns(c *gin.Context) bool {
	u := restCurrent(c)
	if u.Can("edit_posts", nil) || u.Can("edit_pages", nil) {
		return true
	}
	code := http.StatusUnauthorized // rest_authorization_required_code()
	if u != nil {
		code = http.StatusForbidden
	}
	restErr(c, code, "rest_cannot_view", "Sorry, you are not allowed to view the registered block patterns.")
	return false
}

// blockPatterns is GET /wp/v2/block-patterns/patterns.
func (api *restAPI) blockPatterns(c *gin.Context, r *restRequest) {
	if !canViewPatterns(c) {
		return
	}
	restJSON(c, http.StatusOK, api.s.registeredPatterns(c))
}

// registeredPatterns is WP_Block_Patterns_Registry::get_all_registered() as the REST API returns it.
func (s *Server) registeredPatterns(c *gin.Context) []map[string]any {
	core := corePatterns()
	out := make([]map[string]any, 0, len(core)+100)
	for _, p := range core {
		out = append(out, migratePattern(p))
	}
	t := s.activeTheme()
	if set := s.themePatterns(t); set != nil {
		uri := s.links(c).HomeURL(t.URI())
		for _, p := range set.Patterns {
			q := migratePattern(p)
			if content, ok := q["content"].(string); ok {
				q["content"] = strings.ReplaceAll(content, "{{theme_uri}}", uri)
			}
			out = append(out, q)
		}
	}
	return out
}

// blockPatternCategories is GET /wp/v2/block-patterns/categories: core's categories, then the theme's.
func (api *restAPI) blockPatternCategories(c *gin.Context, r *restRequest) {
	if !canViewPatterns(c) {
		return
	}
	out := append([]map[string]any{}, corePatternCategories()...)
	if set := api.s.themePatterns(api.s.activeTheme()); set != nil {
		out = append(out, set.Categories...)
	}
	restJSON(c, http.StatusOK, out)
}
