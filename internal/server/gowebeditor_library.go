package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"wpgo/internal/builder"
	"wpgo/internal/store"
	"wpgo/internal/wp"
)

// Template library, local source only: saved pages, sections and containers are posts of type
// elementor_library. The upstream cloud library is proprietary and not proxied.

var gweTemplateTypes = map[string]bool{"page": true, "section": true, "container": true}

func (s *Server) gweTemplateItem(c *gin.Context, p *store.Post) map[string]any {
	lb := s.links(c)
	author := ""
	if a, err := s.store.UserByID(c, p.AuthorID); err == nil {
		author = a.DisplayName
	}
	loc := s.loc()
	return map[string]any{
		"template_id": p.ID, "source": "local", "type": gweDocType(p), "title": p.Title, "thumbnail": false,
		"date": p.Date().Unix(), "human_date": wp.FormatDate(p.Date().In(loc), "F j, Y"),
		"human_modified_date": wp.FormatDate(p.ModifiedAt.In(loc), "F j, Y"), "author": author, "status": p.Status,
		"hasPageSettings": gweDocType(p) == "page", "tags": []any{},
		"export_link": lb.HomeURL("/wp-admin/admin-ajax?action=elementor_library_direct_actions&library_action=export_template&source=local&_nonce=" +
			s.nonce(c, "elementor_ajax") + "&template_id=" + itoa64(p.ID)),
		"url": lb.Permalink(p),
	}
}

func (s *Server) gweLibraryData(c *gin.Context, data map[string]any) (any, error) {
	templates := []map[string]any{}
	if err := s.gweEnsureLibraryType(c); err != nil {
		return nil, err
	}
	posts, _, err := s.store.QueryPosts(c, store.PostQuery{Types: []string{gweLibrary}, Statuses: []string{"publish", "draft", "private"},
		PerPage: -1, OrderBy: "date", Order: "DESC"})
	if err != nil {
		return nil, err
	}
	u := currentUser(c)
	for _, p := range posts {
		if gweTemplateTypes[gweDocType(p)] && u.Can("edit_post", p) {
			templates = append(templates, s.gweTemplateItem(c, p))
		}
	}
	return map[string]any{"templates": templates, "config": map[string]any{}}, nil
}

func (s *Server) gweSaveTemplate(c *gin.Context, data map[string]any, editorID int64) (any, error) {
	u := currentUser(c)
	if src, _ := data["source"].(string); src != "" && src != "local" {
		return nil, gweFail(http.StatusBadRequest, "The cloud template library is not available in GoWebEditor.")
	}
	typ, _ := data["type"].(string)
	if !gweTemplateTypes[typ] {
		return nil, gweFail(http.StatusBadRequest, "Invalid template type.")
	}
	if !u.Can("edit_posts", nil) {
		return nil, gweFail(http.StatusForbidden, "Access denied.")
	}
	var content json.RawMessage
	switch v := data["content"].(type) {
	case string:
		content = json.RawMessage(v)
	case nil:
		content = json.RawMessage("[]")
	default:
		content, _ = json.Marshal(v)
	}
	if _, err := builder.ParseElements(content); err != nil || !strings.HasPrefix(strings.TrimSpace(string(content)), "[") {
		return nil, gweFail(http.StatusBadRequest, "Template content must be a JSON array.")
	}
	title, _ := data["title"].(string)
	if strings.TrimSpace(title) == "" {
		title = "(no title)"
	}
	if err := s.gweEnsureLibraryType(c); err != nil {
		return nil, err
	}
	p := &store.Post{Type: gweLibrary, AuthorID: u.ID, Title: strings.TrimSpace(title), Status: "publish", CommentStatus: "closed", PingStatus: "closed",
		Meta: map[string]any{"_elementor_edit_mode": "builder", "_elementor_template_type": typ, "_elementor_version": gweVersion, "_elementor_data": string(content)}}
	if ps, ok := data["page_settings"].(map[string]any); ok {
		p.Meta["_elementor_page_settings"] = ps
	}
	if err := s.store.InsertPost(c, p); err != nil {
		return nil, err
	}
	return s.gweTemplateItem(c, p), nil
}

func (s *Server) gweTemplateData(c *gin.Context, data map[string]any) (any, error) {
	p, err := s.gwePostFor(c, gweInt(data["template_id"]), "edit_post")
	if err != nil {
		return nil, err
	}
	if p.Type != gweLibrary {
		return nil, gweFail(http.StatusNotFound, "Template not found.")
	}
	var tree []any
	_ = json.Unmarshal(gweElementsRaw(p), &tree)
	gweNewIDs(tree)
	out := map[string]any{"content": tree}
	if ps, _ := data["page_settings"].(bool); ps || data["page_settings"] != nil {
		out["page_settings"] = gwePageSettings(p)
	}
	return out, nil
}

// gweNewIDs gives imported elements fresh ids, like upstream's import.
func gweNewIDs(list []any) {
	for _, it := range list {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		m["id"] = gweNewID()
		if kids, ok := m["elements"].([]any); ok {
			gweNewIDs(kids)
		}
	}
}

func (s *Server) gweDeleteTemplates(c *gin.Context, ids []int64) (any, error) {
	for _, id := range ids {
		p, err := s.gwePostFor(c, id, "delete_post")
		if err != nil {
			return nil, err
		}
		if p.Type != gweLibrary || gweDocType(p) == "kit" {
			return nil, gweFail(http.StatusBadRequest, "Not a template.")
		}
		if err := s.store.DeletePost(c, p.ID); err != nil {
			return nil, err
		}
	}
	return true, nil
}

// gweLibraryDirect handles elementor_library_direct_actions (template export as JSON).
func (s *Server) gweLibraryDirect(c *gin.Context) {
	if !s.checkNonceValue(c, c.Query("_nonce"), "elementor_ajax") {
		c.String(http.StatusUnauthorized, "Token Expired.")
		return
	}
	if c.Query("library_action") != "export_template" {
		c.String(http.StatusBadRequest, "Action not found.")
		return
	}
	p, err := s.gwePostFor(c, gweInt(c.Query("template_id")), "edit_post")
	if err != nil || p.Type != gweLibrary {
		c.String(http.StatusNotFound, "Template not found.")
		return
	}
	var tree []any
	_ = json.Unmarshal(gweElementsRaw(p), &tree)
	out := map[string]any{"content": tree, "page_settings": gwePageSettings(p), "version": "0.4", "title": p.Title, "type": gweDocType(p)}
	b, _ := json.Marshal(out)
	name := "gowebeditor-" + itoa64(p.ID) + "-" + wp.FormatDate(p.ModifiedAt, "Y-m-d") + ".json"
	c.Header("Content-Disposition", `attachment; filename="`+name+`"`)
	c.Data(http.StatusOK, "application/json", b)
}
