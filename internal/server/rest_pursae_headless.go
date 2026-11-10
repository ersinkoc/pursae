package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// Pursae Headless over the site REST API (pursae/v1/headless/...): content types and components
// (schemas) and their entries, for the signed-in administrator, so Application Passwords, OAuth
// tokens and the MCP tools can manage Pursae Headless like the admin screens do. The delivery API
// (/api, /graphql) keeps its own tokens. Every route needs manage_options and answers 404 while
// the plugin is off.

func (s *Server) registerPursaeHeadlessREST() {
	api := s.rest
	guard := func(h restHandler) restHandler {
		return func(c *gin.Context, r *restRequest) {
			u := restCurrent(c)
			if u == nil {
				restErr(c, http.StatusUnauthorized, "rest_not_logged_in", "You are not currently logged in.")
				return
			}
			if !u.Can("manage_options", nil) {
				restErr(c, http.StatusForbidden, "rest_forbidden", "Sorry, you are not allowed to do that.")
				return
			}
			if !s.hgOn() {
				restErr(c, http.StatusNotFound, "headless_disabled", "Pursae Headless is not active.")
				return
			}
			h(c, r)
		}
	}
	uid := `(?P<uid>[A-Za-z0-9_.:\-]+)`
	doc := `(?P<doc>[a-z0-9]+)`
	base := "/pursae/v1/headless"
	api.register(pursaeNS, base+"/types", map[string]restHandler{
		http.MethodGet:  guard(s.pursaeHGTypes),
		http.MethodPost: guard(s.pursaeHGTypeSave),
	}, nil)
	api.register(pursaeNS, base+"/types/"+uid, map[string]restHandler{
		http.MethodGet:    guard(s.pursaeHGTypeGet),
		http.MethodPost:   guard(s.pursaeHGTypeSave),
		http.MethodDelete: guard(s.pursaeHGTypeDelete),
	}, nil)
	api.register(pursaeNS, base+"/entries/"+uid, map[string]restHandler{
		http.MethodGet:  guard(s.pursaeHGEntries),
		http.MethodPost: guard(s.pursaeHGEntrySave),
	}, nil)
	api.register(pursaeNS, base+"/entries/"+uid+"/"+doc, map[string]restHandler{
		http.MethodGet:    guard(s.pursaeHGEntryGet),
		http.MethodPost:   guard(s.pursaeHGEntrySave),
		http.MethodDelete: guard(s.pursaeHGEntryDelete),
	}, nil)
	api.register(pursaeNS, base+"/entries/"+uid+"/"+doc+"/publish", map[string]restHandler{http.MethodPost: guard(s.pursaeHGEntryPublish(true))}, nil)
	api.register(pursaeNS, base+"/entries/"+uid+"/"+doc+"/unpublish", map[string]restHandler{http.MethodPost: guard(s.pursaeHGEntryPublish(false))}, nil)
}

func pursaeHGEntryOut(e *hgEntry) map[string]any {
	return map[string]any{"documentId": e.DocumentID, "type": e.TypeUID, "locale": e.Locale, "status": e.Status(),
		"data": e.Data, "published": e.Published, "createdAt": e.CreatedAt, "updatedAt": e.UpdatedAt, "publishedAt": e.PublishedAt}
}

func (s *Server) pursaeHGTypes(c *gin.Context, r *restRequest) {
	reg := s.hgReg(c.Request.Context())
	out := []*hgSchema{}
	out = append(out, reg.Types()...)
	out = append(out, reg.Components()...)
	restJSON(c, http.StatusOK, out)
}

func (s *Server) pursaeHGTypeGet(c *gin.Context, r *restRequest) {
	sc := s.hgReg(c.Request.Context()).Get(r.Params["uid"])
	if sc == nil {
		restErr(c, http.StatusNotFound, "headless_type_not_found", "This content type does not exist.")
		return
	}
	restJSON(c, http.StatusOK, sc)
}

// pursaeHGTypeSave creates (POST on the collection) or updates (POST on a uid) a content type or
// component. The schema is the body, or its "schema" member.
func (s *Server) pursaeHGTypeSave(c *gin.Context, r *restRequest) {
	ctx := c.Request.Context()
	raw := r.Body
	if inner, ok := raw["schema"].(map[string]any); ok {
		raw = inner
	}
	b, _ := json.Marshal(raw)
	var sc hgSchema
	if err := json.Unmarshal(b, &sc); err != nil || len(raw) == 0 {
		restErr(c, http.StatusBadRequest, "rest_invalid_param", "The schema could not be read.")
		return
	}
	reg := s.hgReg(ctx)
	action := "create"
	if want := r.Params["uid"]; want != "" {
		sc.UID = want
		if reg.Get(want) == nil {
			restErr(c, http.StatusNotFound, "headless_type_not_found", "This content type does not exist.")
			return
		}
		action = "update"
	} else {
		sc.UID = ""
		hgNormalizeSchema(&sc)
		if sc.UID != "" && reg.Get(sc.UID) != nil {
			restErr(c, http.StatusConflict, "headless_type_exists", "A content type or component with this ID already exists.")
			return
		}
	}
	if err := s.hgSaveSchema(ctx, &sc); err != nil {
		restErr(c, http.StatusBadRequest, "headless_invalid_schema", err.Error())
		return
	}
	s.hgEmitSchema(ctx, action, &sc)
	status := http.StatusOK
	if action == "create" {
		status = http.StatusCreated
	}
	restJSON(c, status, s.hgReg(ctx).Get(sc.UID))
}

func (s *Server) pursaeHGTypeDelete(c *gin.Context, r *restRequest) {
	ctx := c.Request.Context()
	uid := r.Params["uid"]
	sc := s.hgReg(ctx).Get(uid)
	if sc == nil {
		restErr(c, http.StatusNotFound, "headless_type_not_found", "This content type does not exist.")
		return
	}
	if force, _ := r.Bool(c, "force"); !force {
		restErr(c, http.StatusNotImplemented, "rest_trash_not_supported", "Content types cannot be trashed; pass force=true to delete this one and all its entries.")
		return
	}
	if err := s.hgDeleteSchema(ctx, uid); err != nil {
		restErr(c, http.StatusBadRequest, "headless_delete_failed", err.Error())
		return
	}
	s.hgEmitSchema(ctx, "delete", sc)
	restJSON(c, http.StatusOK, map[string]any{"deleted": true, "uid": uid})
}

// pursaeHGType resolves the {uid} of an entry route, answering 404 itself.
func (s *Server) pursaeHGType(c *gin.Context, r *restRequest) *hgSchema {
	sc := s.hgReg(c.Request.Context()).Get(r.Params["uid"])
	if sc == nil || sc.IsComponent() {
		restErr(c, http.StatusNotFound, "headless_type_not_found", "This content type does not exist.")
		return nil
	}
	return sc
}

func (s *Server) pursaeHGLocale(c *gin.Context, r *restRequest, sc *hgSchema) (string, bool) {
	req, _ := r.Str(c, "locale")
	loc, err := s.hgEntryLocale(c.Request.Context(), sc, req)
	if err != nil {
		restErr(c, http.StatusBadRequest, "rest_invalid_param", err.Error())
		return "", false
	}
	return loc, true
}

func (s *Server) pursaeHGEntries(c *gin.Context, r *restRequest) {
	sc := s.pursaeHGType(c, r)
	if sc == nil {
		return
	}
	loc, ok := s.pursaeHGLocale(c, r, sc)
	if !ok {
		return
	}
	status, _ := r.Str(c, "status")
	if status != "published" {
		status = "draft"
	}
	page, pageSize := 1, 25
	if n, ok := r.Int(c, "page"); ok && n > 0 && n < hgMaxPage {
		page = int(n)
	}
	if n, ok := r.Int(c, "per_page"); ok && n > 0 && n <= 100 {
		pageSize = int(n)
	}
	p := &hgReadParams{Status: status, Locale: loc, Budget: newHGReadBudget(),
		Page: hgPagination{Page: page, PageSize: pageSize, WithCount: true}}
	if f, ok := r.Body["filters"].(map[string]any); ok {
		p.Filters = f
	}
	list, total, err := s.hgFind(c.Request.Context(), sc, p, hgFindOpts{})
	if err != nil {
		restErr(c, http.StatusBadRequest, "rest_invalid_param", err.Error())
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, e := range list {
		out = append(out, pursaeHGEntryOut(e))
	}
	c.Header("X-WP-Total", strconv.Itoa(total))
	restJSON(c, http.StatusOK, out)
}

func (s *Server) pursaeHGEntryGet(c *gin.Context, r *restRequest) {
	sc := s.pursaeHGType(c, r)
	if sc == nil {
		return
	}
	loc, ok := s.pursaeHGLocale(c, r, sc)
	if !ok {
		return
	}
	e, err := s.hgGetEntry(c.Request.Context(), sc.UID, r.Params["doc"], loc)
	if err != nil {
		restErr(c, http.StatusNotFound, "headless_entry_not_found", "This entry does not exist.")
		return
	}
	restJSON(c, http.StatusOK, pursaeHGEntryOut(e))
}

// pursaeHGEntrySave creates (POST on the type) or updates (POST on a document) an entry. The values
// are the "data" member; "publish": true also publishes (a type without draft & publish always does).
func (s *Server) pursaeHGEntrySave(c *gin.Context, r *restRequest) {
	sc := s.pursaeHGType(c, r)
	if sc == nil {
		return
	}
	loc, ok := s.pursaeHGLocale(c, r, sc)
	if !ok {
		return
	}
	data, ok := r.Body["data"].(map[string]any)
	if !ok {
		restErr(c, http.StatusBadRequest, "rest_invalid_param", "Invalid parameter(s): data")
		return
	}
	ctx := c.Request.Context()
	doc := r.Params["doc"]
	if sc.IsSingle() {
		doc = s.hgSingleDoc(ctx, sc.UID)
	}
	publish, _ := r.Bool(c, "publish")
	wantPublish := publish && sc.ReviewWorkflow && sc.DraftAndPublish
	if wantPublish {
		publish = false
	}
	uid := restCurrent(c).ID
	e, events, err := s.hgSaveEntry(ctx, sc, doc, data, hgSaveOpts{Locale: loc, Publish: publish, UserID: uid})
	if err != nil {
		var ve hgValidationError
		switch {
		case errors.As(err, &ve):
			restErr(c, http.StatusBadRequest, "headless_invalid_entry", ve.Error())
		case errors.Is(err, errHGNotFound):
			restErr(c, http.StatusNotFound, "headless_entry_not_found", "This entry does not exist.")
		default:
			restErr(c, http.StatusBadRequest, "headless_save_failed", err.Error())
		}
		return
	}
	for _, ev := range events {
		s.hgEmit(ctx, ev, sc, e)
	}
	if wantPublish {
		pe, perr := s.hgPublishEntry(ctx, sc, e.DocumentID, loc, uid)
		if perr != nil {
			restErr(c, http.StatusBadRequest, "headless_publish_failed", "Saved as a draft, but not published: "+perr.Error())
			return
		}
		s.hgEmit(ctx, "entry.publish", sc, pe)
		e = pe
	}
	status := http.StatusOK
	if r.Params["doc"] == "" {
		status = http.StatusCreated
	}
	restJSON(c, status, pursaeHGEntryOut(e))
}

func (s *Server) pursaeHGEntryPublish(publish bool) restHandler {
	return func(c *gin.Context, r *restRequest) {
		sc := s.pursaeHGType(c, r)
		if sc == nil {
			return
		}
		loc, ok := s.pursaeHGLocale(c, r, sc)
		if !ok {
			return
		}
		ctx := c.Request.Context()
		var e *hgEntry
		var err error
		ev := "entry.unpublish"
		if publish {
			e, err = s.hgPublishEntry(ctx, sc, r.Params["doc"], loc, restCurrent(c).ID)
			ev = "entry.publish"
		} else {
			e, err = s.hgUnpublishEntry(ctx, sc, r.Params["doc"], loc)
		}
		if err != nil {
			if errors.Is(err, errHGNotFound) {
				restErr(c, http.StatusNotFound, "headless_entry_not_found", "This entry does not exist.")
			} else {
				restErr(c, http.StatusBadRequest, "headless_publish_failed", err.Error())
			}
			return
		}
		s.hgEmit(ctx, ev, sc, e)
		restJSON(c, http.StatusOK, pursaeHGEntryOut(e))
	}
}

func (s *Server) pursaeHGEntryDelete(c *gin.Context, r *restRequest) {
	sc := s.pursaeHGType(c, r)
	if sc == nil {
		return
	}
	loc, ok := s.pursaeHGLocale(c, r, sc)
	if !ok {
		return
	}
	if force, _ := r.Bool(c, "force"); !force {
		restErr(c, http.StatusNotImplemented, "rest_trash_not_supported", "Entries cannot be trashed; pass force=true to delete.")
		return
	}
	ctx := c.Request.Context()
	gone, err := s.hgDeleteEntries(ctx, sc, r.Params["doc"], loc, restCurrent(c).ID)
	if err != nil || len(gone) == 0 {
		restErr(c, http.StatusNotFound, "headless_entry_not_found", "This entry does not exist.")
		return
	}
	for _, e := range gone {
		s.hgEmit(ctx, "entry.delete", sc, e)
	}
	restJSON(c, http.StatusOK, map[string]any{"deleted": true, "documentId": r.Params["doc"]})
}
