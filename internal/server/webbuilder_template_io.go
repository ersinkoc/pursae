package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
	"wpgo/internal/webbuilder"
)

// Template files of the Theme Builder: one template as a JSON file, to move a header, a footer, a
// popup or any other template between sites.
//
//	GET  web-builder/v2/templates/{id}/export → TemplateFile (attachment)
//	POST web-builder/v2/templates/import      TemplateFile → 201 {id, editUrl}
//
// The Theme Builder screen offers both (row action "Export", the "Import Template" button). An
// import never goes live by itself: it makes a disabled template without a snapshot, which the
// editor renders and publishes (the snapshot needs the browser), exactly like a theme library
// import. The doc is validated and normalized for the importing user's capabilities, so a file is
// no way around unfiltered_html.

var wbExportNameRE = regexp.MustCompile(`[^a-z0-9]+`)

// wbTemplateFileOf builds the file of template t; ok is false when it has no stored design yet.
func (s *Server) wbTemplateFileOf(c *gin.Context, t *store.WBTemplate) (webbuilder.TemplateFile, bool) {
	d, err := s.store.WebBuilder().Doc(c, t.ID)
	if err != nil || len(d.Doc) == 0 {
		return webbuilder.TemplateFile{}, false
	}
	f := webbuilder.TemplateFile{Format: webbuilder.TemplateFileFormat, Version: webbuilder.TemplateFileVersion, Type: t.Type,
		Title: t.Title, Conditions: t.Conditions, Priority: t.Priority, Doc: d.Doc}
	if f.Conditions == nil {
		f.Conditions = []string{}
	}
	if t.Type == webbuilder.TemplatePopup {
		st := s.wbPopupSettings(c, t.ID)
		f.Popup = &st
	}
	return f, true
}

// wbTemplateFileName is the download name: wb-template-<type>-<title>.json.
func wbTemplateFileName(t *store.WBTemplate) string {
	name := strings.Trim(wbExportNameRE.ReplaceAllString(strings.ToLower(t.Title), "-"), "-")
	if len(name) > 60 {
		name = strings.Trim(name[:60], "-")
	}
	if name == "" {
		name = strconv.FormatInt(t.ID, 10)
	}
	return "wb-template-" + string(t.Type) + "-" + name + ".json"
}

func wbSendTemplateFile(c *gin.Context, t *store.WBTemplate, f webbuilder.TemplateFile) {
	b, _ := json.MarshalIndent(f, "", " ")
	c.Header("Cache-Control", "no-store")
	c.Header("Content-Disposition", `attachment; filename="`+wbTemplateFileName(t)+`"`)
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, "application/json; charset=utf-8", append(b, '\n'))
}

// wbRESTTemplateExport is GET templates/{id}/export.
func (s *Server) wbRESTTemplateExport(c *gin.Context, r *restRequest) {
	_, t := s.wbTemplateFor(c, r)
	if t == nil {
		return
	}
	f, ok := s.wbTemplateFileOf(c, t)
	if !ok {
		wbBadTemplate(c, "This template has no design yet. Open it in Pursae Web Builder and publish it first.")
		return
	}
	wbSendTemplateFile(c, t, f)
}

// wbTemplateExportAdmin is the row action "Export" of the Theme Builder screen (a nonce'd GET that
// downloads the file; the REST route needs headers a link cannot send).
func (s *Server) wbTemplateExportAdmin(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Query("post"), 10, 64)
	if !s.checkNonce(c, "export-wb-template_"+itoa64(id)) {
		s.denyNonce(c)
		return
	}
	t, err := s.store.WebBuilder().Template(c, id)
	if err != nil || t.Status == "trash" {
		c.String(http.StatusNotFound, "You attempted to edit an item that does not exist. Perhaps it was deleted?")
		return
	}
	f, ok := s.wbTemplateFileOf(c, t)
	if !ok {
		s.wbTBRedirect(c, false, map[string][]string{"wb_error": {"export"}})
		return
	}
	wbSendTemplateFile(c, t, f)
}

// wbRESTTemplateImport is POST templates/import: the body is a TemplateFile.
func (s *Server) wbRESTTemplateImport(c *gin.Context, r *restRequest) {
	u := wbUser(c, webbuilder.TemplateCap)
	if u == nil {
		return
	}
	raw, ok := wbBody(c, r, webbuilder.MaxDocBytes)
	if !ok {
		return
	}
	var f webbuilder.TemplateFile
	if !wbDecode(c, raw, &f) {
		return
	}
	if f.Format != webbuilder.TemplateFileFormat || f.Version != webbuilder.TemplateFileVersion {
		wbBadTemplate(c, "This is not a Pursae Web Builder template file.")
		return
	}
	if _, ok := webbuilder.TemplateTypeLabels[f.Type]; !ok {
		wbBadTemplate(c, "Unknown template type.")
		return
	}
	if !s.wbTemplateTypeOffered(f.Type) {
		wbBadTemplate(c, "Product templates need Pursae Commerce.")
		return
	}
	norm, errs := webbuilder.ValidateConditionsRequest(webbuilder.TemplateConditionsRequest{Conditions: f.Conditions, Priority: f.Priority})
	if len(errs) > 0 {
		wbBadCondition(c, errs)
		return
	}
	if f.Type == webbuilder.TemplateSingle && len(norm.Conditions) == 0 {
		wbBadTemplate(c, "A Single template needs the rule of its post type.")
		return
	}
	if len(f.Doc) == 0 {
		wbBadTemplate(c, "The file has no design.")
		return
	}
	res, ok := wbParseDoc(c, f.Doc, u)
	if !ok {
		return
	}
	if f.Popup != nil {
		f.Popup.Selector = strings.TrimSpace(f.Popup.Selector)
		if msg := webbuilder.ValidatePopup(*f.Popup); msg != "" {
			wbBadTemplate(c, msg)
			return
		}
	}
	wb := s.store.WebBuilder()
	n := store.NewWBTemplate{Type: f.Type, Title: wbClip(strings.TrimSpace(f.Title), wbTitleMax), Priority: norm.Priority, AuthorID: u.ID}
	if len(norm.Conditions) > 0 {
		n.Conditions = norm.Conditions
	}
	t, err := wb.CreateTemplate(c, n)
	if err != nil {
		if errors.Is(err, store.ErrTemplateType) {
			wbBadTemplate(c, "Unknown template type.")
			return
		}
		restErr(c, http.StatusInternalServerError, "db_error", "Could not create the template.")
		return
	}
	fail := func() {
		_ = s.store.TrashPost(c, t.ID)
		restErr(c, http.StatusInternalServerError, "db_error", "Could not import the template.")
	}
	// The post is new and empty: the content hash is that of "" (see the theme import).
	d := &store.WebBuilderDoc{PostID: t.ID, Doc: res.JSON, Page: json.RawMessage(`{}`), Renderer: 0,
		ContentHash: store.WebBuilderContentHash(""), SnapshotUser: u.ID, SnapshotUnfiltered: u.Can("unfiltered_html", nil)}
	if err := wb.PublishDoc(c, d, 0); err != nil {
		fail()
		return
	}
	if _, err := s.store.Pool.Exec(c, `UPDATE posts SET meta = meta || jsonb_build_object($2::text, '1') WHERE id = $1`, t.ID, webbuilder.PostMarkerMeta); err != nil {
		fail()
		return
	}
	if f.Type == webbuilder.TemplatePopup && f.Popup != nil {
		b, _ := json.Marshal(f.Popup)
		if err := wb.SetTemplatePopup(c, t.ID, string(b)); err != nil {
			fail()
			return
		}
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusCreated, webbuilder.TemplateCreateResponse{ID: t.ID, EditURL: s.links(c).HomeURL(wbEditURL(t.ID))})
}

// wbThemePartsExport builds a theme library parts file (web-builder/parts.json, format
// web-builder-theme) from the site's published templates, so a theme designed on a site can be
// shipped as a library theme. Single templates (the pack format has none) and templates without a
// stored design are left out; pages are not exported. Keys are <type>, then <type>-2, ….
func (s *Server) wbThemePartsExport(c *gin.Context) (wbThemePack, error) {
	pack := wbThemePack{Format: "web-builder-theme", Version: 1, Templates: []wbPackTemplate{}, Pages: []wbPackPage{}}
	list, err := s.store.WebBuilder().Templates(c, store.WBTemplateFilter{Statuses: []string{"publish"}})
	if err != nil {
		return pack, err
	}
	seen := map[string]int{}
	for i := len(list) - 1; i >= 0; i-- { // oldest first: stable keys
		t := list[i]
		if t.Type == webbuilder.TemplateSingle {
			continue
		}
		f, ok := s.wbTemplateFileOf(c, t)
		if !ok {
			continue
		}
		seen[string(t.Type)]++
		key := string(t.Type)
		if n := seen[key]; n > 1 {
			key += "-" + strconv.Itoa(n)
		}
		pack.Templates = append(pack.Templates, wbPackTemplate{Key: key, Type: string(t.Type), Title: f.Title, Conditions: f.Conditions, Priority: f.Priority, Doc: f.Doc})
	}
	return pack, nil
}

// wbThemePartsExportAdmin is "Export Theme Parts" of the Theme Builder screen (nonce'd GET download).
func (s *Server) wbThemePartsExportAdmin(c *gin.Context) {
	if !s.checkNonce(c, "export-wb-theme-parts") {
		s.denyNonce(c)
		return
	}
	pack, err := s.wbThemePartsExport(c)
	if err != nil {
		s.wbTBRedirect(c, false, map[string][]string{"wb_error": {"failed"}})
		return
	}
	b, _ := json.MarshalIndent(pack, "", " ")
	c.Header("Cache-Control", "no-store")
	c.Header("Content-Disposition", `attachment; filename="parts.json"`)
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, "application/json; charset=utf-8", append(b, '\n'))
}
