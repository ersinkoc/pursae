package server

import (
	"errors"
	"net/http"
	"slices"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
	"wpgo/internal/webbuilder"
)

// Theme Builder REST routes of web-builder/v2 (contract §11.8): the template list, Add New, display
// conditions, the preview object, overlapping templates and the editor's sample data. Every route
// needs the X-WB-Api handshake and edit_theme_options, except preview-context (edit_posts, plus
// read_post for a post:<id> object).

// wbRegisterTemplateREST adds the template routes through reg (registerWebBuilderREST's helper).
func (s *Server) wbRegisterTemplateREST(reg func(pattern string, methods map[string]restHandler, handshake bool)) {
	const tpl = `/web-builder/v2/templates/(?P<id>[\d]+)`
	reg(`/web-builder/v2/templates`, map[string]restHandler{http.MethodGet: s.wbRESTTemplates, http.MethodPost: s.wbRESTTemplateCreate}, true)
	reg(tpl+`/conditions`, map[string]restHandler{http.MethodPut: s.wbRESTTemplateConditions}, true)
	reg(tpl+`/preview`, map[string]restHandler{http.MethodPut: s.wbRESTTemplatePreview}, true)
	reg(tpl+`/conflicts`, map[string]restHandler{http.MethodGet: s.wbRESTTemplateConflicts}, true)
	reg(`/web-builder/v2/preview-context`, map[string]restHandler{http.MethodGet: s.wbRESTPreviewContext}, true)
}

func wbBadTemplate(c *gin.Context, msg string) {
	restErr(c, http.StatusUnprocessableEntity, webbuilder.ErrBadTemplate, msg)
}

func wbBadCondition(c *gin.Context, errs []webbuilder.ValidationError) {
	msg := "The display conditions are not valid."
	if len(errs) > 0 && errs[0].Message != "" {
		msg = errs[0].Message
	}
	restErr(c, http.StatusUnprocessableEntity, webbuilder.ErrBadCondition, msg, map[string]any{"errors": errs})
}

func wbBadPreview(c *gin.Context) {
	restErr(c, http.StatusUnprocessableEntity, webbuilder.ErrBadPreview, "The preview object is not valid.")
}

// wbTemplateFor loads the {id} template for a template route (edit_theme_options, not trashed).
func (s *Server) wbTemplateFor(c *gin.Context, r *restRequest) (*store.User, *store.WBTemplate) {
	u := wbUser(c, webbuilder.TemplateCap)
	if u == nil {
		return nil, nil
	}
	t, err := s.store.WebBuilder().Template(c, r.ID())
	switch {
	case errors.Is(err, store.ErrNotFound) || (err == nil && t.Status == "trash"):
		restErr(c, http.StatusNotFound, webbuilder.ErrNotFound, "Invalid template ID.")
		return nil, nil
	case err != nil:
		restErr(c, http.StatusInternalServerError, "db_error", "Could not read the template.")
		return nil, nil
	}
	return u, t
}

// wbTemplateSummary is the list row of t.
func (s *Server) wbTemplateSummary(c *gin.Context, t *store.WBTemplate) webbuilder.TemplateSummary {
	lb := s.links(c)
	info, _ := s.wbTemplatePreviewInfo(c, t)
	return webbuilder.TemplateSummary{ID: t.ID, Title: t.Title, Type: t.Type, Status: t.Status, Conditions: t.Conditions,
		Priority: t.Priority, Author: webbuilder.UserRef{ID: t.AuthorID, Name: t.AuthorName}, Modified: wbTime(t.ModifiedAt),
		EditURL: lb.HomeURL(wbEditURL(t.ID)), PreviewURL: info.URL}
}

// wbRESTTemplates is GET templates?type=: newest first, trash excluded.
func (s *Server) wbRESTTemplates(c *gin.Context, r *restRequest) {
	if wbUser(c, webbuilder.TemplateCap) == nil {
		return
	}
	f := store.WBTemplateFilter{}
	if typ := c.Query("type"); typ != "" {
		if _, ok := webbuilder.TemplateTypeLabels[webbuilder.TemplateType(typ)]; !ok {
			wbBadTemplate(c, "Unknown template type.")
			return
		}
		f.Type = webbuilder.TemplateType(typ)
	}
	list, err := s.store.WebBuilder().Templates(c, f)
	if err != nil {
		restErr(c, http.StatusInternalServerError, "db_error", "Could not read the templates.")
		return
	}
	out := make([]webbuilder.TemplateSummary, 0, len(list))
	for _, t := range list {
		out = append(out, s.wbTemplateSummary(c, t))
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, out)
}

// wbRESTTemplateCreate is POST templates {type, title, starter?, postType?} → 201 {id, editUrl}.
func (s *Server) wbRESTTemplateCreate(c *gin.Context, r *restRequest) {
	u := wbUser(c, webbuilder.TemplateCap)
	if u == nil {
		return
	}
	raw, ok := wbBody(c, r, 8<<10)
	if !ok {
		return
	}
	var body webbuilder.TemplateCreateRequest
	if !wbDecode(c, raw, &body) {
		return
	}
	if _, ok := webbuilder.TemplateTypeLabels[body.Type]; !ok {
		wbBadTemplate(c, "Unknown template type.")
		return
	}
	if body.Starter != "" && !slices.Contains(webbuilder.StarterIDs, body.Starter) {
		wbBadTemplate(c, "Unknown starter design.")
		return
	}
	n := store.NewWBTemplate{Type: body.Type, Title: wbClip(body.Title, wbTitleMax), Starter: body.Starter, AuthorID: u.ID}
	if body.Type == webbuilder.TemplateSingle {
		if body.PostType == "" || siteCPT(body.PostType) == nil {
			wbBadTemplate(c, "A Single template needs a public custom post type.")
			return
		}
		n.Conditions = []string{webbuilder.CondInclude + "/singular/" + body.PostType}
	} else if body.PostType != "" {
		wbBadTemplate(c, "Only a Single template takes a post type.")
		return
	}
	t, err := s.store.WebBuilder().CreateTemplate(c, n)
	if err != nil {
		if errors.Is(err, store.ErrTemplateType) {
			wbBadTemplate(c, "Unknown template type.")
			return
		}
		restErr(c, http.StatusInternalServerError, "db_error", "Could not create the template.")
		return
	}
	c.JSON(http.StatusCreated, webbuilder.TemplateCreateResponse{ID: t.ID, EditURL: s.links(c).HomeURL(wbEditURL(t.ID))})
}

// wbRESTTemplateConditions is PUT templates/{id}/conditions {conditions, priority}.
func (s *Server) wbRESTTemplateConditions(c *gin.Context, r *restRequest) {
	_, t := s.wbTemplateFor(c, r)
	if t == nil {
		return
	}
	raw, ok := wbBody(c, r, 64<<10)
	if !ok {
		return
	}
	var body webbuilder.TemplateConditionsRequest
	if !wbDecode(c, raw, &body) {
		return
	}
	norm, errs := webbuilder.ValidateConditionsRequest(body)
	if len(errs) > 0 {
		wbBadCondition(c, errs)
		return
	}
	if err := s.store.WebBuilder().SetTemplateConditions(c, t.ID, norm.Conditions, norm.Priority); err != nil {
		restErr(c, http.StatusInternalServerError, "db_error", "Could not save the display conditions.")
		return
	}
	s.wbTemplatesChanged(c)
	c.JSON(http.StatusOK, webbuilder.TemplateConditionsResponse(norm))
}

// wbRESTTemplatePreview is PUT templates/{id}/preview {object} → {preview, dynamic}.
func (s *Server) wbRESTTemplatePreview(c *gin.Context, r *restRequest) {
	_, t := s.wbTemplateFor(c, r)
	if t == nil {
		return
	}
	raw, ok := wbBody(c, r, 4<<10)
	if !ok {
		return
	}
	var body webbuilder.TemplatePreviewRequest
	if !wbDecode(c, raw, &body) {
		return
	}
	pv, err := wbParsePreview(body.Object)
	if err != nil {
		wbBadPreview(c)
		return
	}
	q, err := s.wbResolvePreview(c, pv, t)
	if err != nil {
		restErr(c, http.StatusNotFound, webbuilder.ErrNotFound, "The preview object was not found.")
		return
	}
	dyn, err := s.wbDynContext(c, body.Object, q)
	if err != nil {
		restErr(c, http.StatusInternalServerError, "db_error", "Could not build the preview.")
		return
	}
	if err := s.store.WebBuilder().SetTemplatePreview(c, t.ID, body.Object); err != nil {
		restErr(c, http.StatusInternalServerError, "db_error", "Could not save the preview.")
		return
	}
	resp := webbuilder.TemplatePreviewResponse{Dynamic: dyn,
		Preview: webbuilder.TemplatePreview{Object: body.Object, Label: s.wbPreviewLabel(q), URL: s.wbPreviewURL(s.links(c), q, t.ID)}}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, resp)
}

// wbRESTTemplateConflicts is GET templates/{id}/conflicts: other published templates of the same
// location whose include rules can match a view this template's include rules match.
func (s *Server) wbRESTTemplateConflicts(c *gin.Context, r *restRequest) {
	_, t := s.wbTemplateFor(c, r)
	if t == nil {
		return
	}
	out := []webbuilder.TemplateConflict{}
	if t.Type == "" {
		c.JSON(http.StatusOK, out)
		return
	}
	others, err := s.store.WebBuilder().Templates(c, store.WBTemplateFilter{Statuses: []string{webbuilder.StatusPublish}})
	if err != nil {
		restErr(c, http.StatusInternalServerError, "db_error", "Could not read the templates.")
		return
	}
	me := webbuilder.TemplateCandidate{ID: t.ID, Type: t.Type, Conditions: webbuilder.ParseConditions(t.Conditions), Priority: t.Priority, Modified: t.ModifiedAt}
	lb := s.links(c)
	for _, o := range others {
		if o.ID == t.ID || o.Type == "" {
			continue
		}
		oc := webbuilder.TemplateCandidate{ID: o.ID, Type: o.Type, Conditions: webbuilder.ParseConditions(o.Conditions), Priority: o.Priority, Modified: o.ModifiedAt}
		if webbuilder.TemplatesConflict(&me, &oc) {
			out = append(out, webbuilder.TemplateConflict{ID: o.ID, Title: o.Title, Type: o.Type, Conditions: o.Conditions,
				Priority: o.Priority, EditURL: lb.HomeURL(wbEditURL(o.ID))})
		}
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, out)
}

// wbRESTPreviewContext is GET preview-context?object= → DynContext (edit_posts; post:<id> also
// needs read_post, checked by wbResolvePreview). "latest" means the front page here.
func (s *Server) wbRESTPreviewContext(c *gin.Context, r *restRequest) {
	if wbUser(c, "edit_posts") == nil {
		return
	}
	object := c.Query("object")
	pv, err := wbParsePreview(object)
	if err != nil {
		wbBadPreview(c)
		return
	}
	q, err := s.wbResolvePreview(c, pv, nil)
	if err != nil {
		restErr(c, http.StatusNotFound, webbuilder.ErrNotFound, "The preview object was not found.")
		return
	}
	dyn, err := s.wbDynContext(c, object, q)
	if err != nil {
		restErr(c, http.StatusInternalServerError, "db_error", "Could not build the preview.")
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, dyn)
}
