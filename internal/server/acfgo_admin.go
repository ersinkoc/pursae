package server

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/wp"
)

// ACF › Field Groups, the field group editor, Post Types / Taxonomies / Options Pages and Tools.

func (s *Server) acfgoCanAdmin(c *gin.Context) bool {
	if !currentUser(c).Can("manage_options", nil) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to access this page.")
		return false
	}
	return true
}

// Field group list ----------------------------------------------------------------------------------

type acfgoGroupRow struct {
	ID                                         int64
	Key, Title, Description, Status, LocalFile string
	Locations                                  []string
	Fields                                     int
	Local                                      string
	SyncAvailable, Active, InDB                bool
	UnsupportedLocation                        bool // a rule type Pursae has no screen for (widget, menu, menu item)
	EditURL, DuplicateURL, TrashURL            string
	RestoreURL, DeleteURL, SyncURL, ToggleURL  string
}

type acfgoListView struct {
	Key, Label, URL string
	Count           int
	Current         bool
}

type acfgoGroupsData struct {
	Rows      []acfgoGroupRow
	Views     []acfgoListView
	Status    string
	Search    string
	BulkNonce string
	IsTrash   bool
	IsSync    bool
}

func (s *Server) acfgoGroupsScreen(c *gin.Context) {
	if !s.acfgoCanAdmin(c) {
		return
	}
	ctx := c.Request.Context()
	if c.Request.Method == http.MethodPost || c.Query("action") != "" {
		s.acfgoGroupsAction(c)
		return
	}
	d := acfgoGroupsData{Status: c.DefaultQuery("post_status", "all"), Search: strings.TrimSpace(c.Query("s")), BulkNonce: s.nonce(c, "acfgo-bulk")}
	d.IsTrash, d.IsSync = d.Status == "trash", d.Status == "sync"
	all := s.acfgoAllGroups(ctx, true)
	counts := map[string]int{}
	for _, g := range all {
		switch {
		case g.Status == "trash":
			counts["trash"]++
		default:
			counts["all"]++
			if g.Active {
				counts["publish"]++
			} else {
				counts["draft"]++
			}
			if g.SyncAvailable {
				counts["sync"]++
			}
		}
	}
	for _, v := range []struct{ key, label string }{{"all", "All"}, {"publish", "Active"}, {"draft", "Inactive"}, {"sync", "Sync available"}, {"trash", "Trash"}} {
		if v.key != "all" && counts[v.key] == 0 {
			continue
		}
		u := "/wp-admin/acf-field-groups"
		if v.key != "all" {
			u += "?post_status=" + v.key
		}
		d.Views = append(d.Views, acfgoListView{v.key, v.label, u, counts[v.key], d.Status == v.key})
	}
	values := s.acfgoLocationValues(ctx)
	n := func(action string, id int64) string {
		return fmt.Sprintf("/wp-admin/acf-field-groups?action=%s&post=%d&_wpnonce=%s", action, id, s.nonce(c, "acfgo-"+action+"_"+itoa64(id)))
	}
	q := strings.ToLower(d.Search)
	for _, g := range all {
		switch d.Status {
		case "trash":
			if g.Status != "trash" {
				continue
			}
		case "publish", "draft":
			if g.Status != d.Status {
				continue
			}
		case "sync":
			if !g.SyncAvailable || g.Status == "trash" {
				continue
			}
		default:
			if g.Status == "trash" {
				continue
			}
		}
		if q != "" && !strings.Contains(strings.ToLower(g.Title+" "+g.Description+" "+g.Key), q) {
			continue
		}
		count := 0
		acfgoWalkFields(g.Fields, func(acfField) { count++ })
		r := acfgoGroupRow{ID: g.ID, Key: g.Key, Title: g.Title, Description: g.Description, Status: g.Status, Fields: count, Local: g.Local,
			SyncAvailable: g.SyncAvailable, Active: g.Active, InDB: g.ID != 0, LocalFile: g.LocalFile, Locations: s.acfgoLocationSummary(g, values), UnsupportedLocation: acfgoUnsupportedLocation(g)}
		if g.ID != 0 {
			r.EditURL = "/wp-admin/acf-field-group?post=" + itoa64(g.ID)
			r.DuplicateURL, r.TrashURL, r.RestoreURL, r.DeleteURL = n("duplicate", g.ID), n("trash", g.ID), n("untrash", g.ID), n("delete", g.ID)
			r.ToggleURL = n(map[bool]string{true: "deactivate", false: "activate"}[g.Active], g.ID)
		}
		if g.SyncAvailable {
			r.SyncURL = "/wp-admin/acf-field-groups?action=sync&key=" + url.QueryEscape(g.Key) + "&_wpnonce=" + s.nonce(c, "acfgo-sync_"+g.Key)
		}
		d.Rows = append(d.Rows, r)
	}
	var notices []notice
	if m := c.Query("acfmsg"); m != "" {
		notices = append(notices, notice{Type: "success", Message: template.HTML(template.HTMLEscapeString(m)), Dismissible: true})
	}
	s.pageAssets(c).Style("acfgo")
	s.adminPage(c, "acf-field-groups", "Field Groups", "acf-admin-page post-type-acf-field-group edit-php", "admin/acfgo-groups", d, notices...)
}

func (s *Server) acfgoGroupsAction(c *gin.Context) {
	ctx := c.Request.Context()
	done := func(msg string) {
		c.Redirect(http.StatusFound, "/wp-admin/acf-field-groups?acfmsg="+url.QueryEscape(msg))
	}
	action := defaultStr(c.Query("action"), c.PostForm("action"))
	if action == "-1" || action == "" {
		action = c.PostForm("action2")
	}
	if c.Request.Method == http.MethodPost { // bulk actions
		if !s.checkNonce(c, "acfgo-bulk") {
			s.denyNonce(c)
			return
		}
		n := 0
		for _, raw := range c.PostFormArray("post[]") {
			id, _ := strconv.ParseInt(raw, 10, 64)
			var err error
			switch action {
			case "activate":
				err = s.acfgoSetGroupStatus(ctx, id, "publish")
			case "deactivate":
				err = s.acfgoSetGroupStatus(ctx, id, "draft")
			case "trash":
				err = s.acfgoSetGroupStatus(ctx, id, "trash")
			case "untrash":
				err = s.acfgoSetGroupStatus(ctx, id, "untrash")
			case "delete":
				err = s.acfgoDeleteGroup(ctx, id)
			default:
				continue
			}
			if err == nil {
				n++
			}
		}
		for _, key := range c.PostFormArray("sync[]") {
			if action == "sync" && s.acfgoSyncGroup(c, key) == nil {
				n++
			}
		}
		done(fmt.Sprintf("%d field group%s updated.", n, map[bool]string{true: "", false: "s"}[n == 1]))
		return
	}
	if action == "sync" {
		key := c.Query("key")
		if !s.checkNonce(c, "acfgo-sync_"+key) {
			s.denyNonce(c)
			return
		}
		if err := s.acfgoSyncGroup(c, key); err != nil {
			done(err.Error())
			return
		}
		done("Field group synchronised.")
		return
	}
	id, _ := strconv.ParseInt(c.Query("post"), 10, 64)
	if !s.checkNonce(c, "acfgo-"+action+"_"+itoa64(id)) {
		s.denyNonce(c)
		return
	}
	g := s.acfgoGroupByID(ctx, id)
	if g == nil {
		done("Field group not found.")
		return
	}
	switch action {
	case "duplicate":
		cp, err := s.acfgoDuplicateGroup(ctx, g)
		if err != nil {
			done(err.Error())
			return
		}
		done(cp.Title + " created.")
	case "trash":
		_ = s.acfgoSetGroupStatus(ctx, id, "trash")
		done("1 field group moved to the Trash.")
	case "untrash":
		_ = s.acfgoSetGroupStatus(ctx, id, "untrash")
		done("1 field group restored from the Trash.")
	case "delete":
		_ = s.acfgoDeleteGroup(ctx, id)
		done("1 field group permanently deleted.")
	case "activate", "deactivate":
		_ = s.acfgoSetGroupStatus(ctx, id, map[string]string{"activate": "publish", "deactivate": "draft"}[action])
		done("Field group " + action + "d.")
	default:
		done("")
	}
}

// acfgoSyncGroup imports a group's local JSON into the database (ACF's Sync).
func (s *Server) acfgoSyncGroup(c *gin.Context, key string) error {
	ctx := c.Request.Context()
	for _, lf := range s.acfgoLocalJSON("group_") {
		if acfgoStr(lf.data["key"]) != key {
			continue
		}
		g := acfgoGroupFromMap(lf.data)
		if old := s.acfgoGroupByKey(ctx, key); old != nil && old.ID != 0 {
			g.ID = old.ID
		}
		return s.acfgoSaveGroup(ctx, g)
	}
	acfgoLocalMu.Lock()
	var lg *acfGroup
	for _, g := range acfgoLocalGroups {
		if g.Key == key {
			cp := *g
			lg = &cp
		}
	}
	acfgoLocalMu.Unlock()
	if lg != nil {
		lg.Local = ""
		return s.acfgoSaveGroup(ctx, lg)
	}
	return fmt.Errorf("no local JSON for %s", key)
}

// Field group editor --------------------------------------------------------------------------------

type acfgoGroupEditData struct {
	Group     *acfGroup
	IsNew     bool
	Nonce     string
	GroupJSON template.JS
	Schema    template.JS
	Positions []acfgoChoice
}

func (s *Server) acfgoGroupEdit(c *gin.Context) {
	if !s.acfgoCanAdmin(c) {
		return
	}
	ctx := c.Request.Context()
	id, _ := strconv.ParseInt(defaultStr(c.Query("post"), c.PostForm("post_ID")), 10, 64)
	g := &acfGroup{Active: true, Position: "normal", Style: "default", LabelPlacement: "top", InstructionPlacement: "label", Raw: map[string]any{},
		Location: [][]acfRule{{{Param: "post_type", Operator: "==", Value: "post"}}}}
	isNew := id == 0
	if !isNew {
		if g = s.acfgoGroupByID(ctx, id); g == nil {
			c.String(http.StatusNotFound, "You attempted to edit an item that does not exist. Perhaps it was deleted?")
			return
		}
	}
	if c.Request.Method == http.MethodPost {
		if !s.checkNonce(c, "acfgo-group_"+itoa64(id)) {
			s.denyNonce(c)
			return
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(c.PostForm("acfgo_group")), &m); err != nil || m == nil {
			c.String(http.StatusBadRequest, "The field group data is missing or invalid.")
			return
		}
		ng := acfgoGroupFromMap(m)
		ng.ID, ng.Key = g.ID, g.Key
		if ng.Key == "" {
			ng.Key = acfgoStr(m["key"])
		}
		ng.Title = strings.TrimSpace(c.PostForm("post_title"))
		ng.Status = ""
		if err := s.acfgoSaveGroup(ctx, ng); err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
		msg := "1"
		if isNew {
			msg = "6"
		}
		c.Redirect(http.StatusFound, "/wp-admin/acf-field-group?post="+itoa64(ng.ID)+"&message="+msg)
		return
	}
	var notices []notice
	switch c.Query("message") {
	case "1":
		notices = append(notices, notice{Type: "success", Message: "Field group updated.", Dismissible: true})
	case "6":
		notices = append(notices, notice{Type: "success", Message: "Field group published.", Dismissible: true})
	}
	if g.SyncAvailable && g.ID != 0 {
		notices = append(notices, notice{Type: "warning", Message: "The local JSON file of this field group is newer than the database copy. Sync it from the field group list to load it."})
	}
	gm := g.toMap()
	gm["key"] = g.Key
	d := acfgoGroupEditData{Group: g, IsNew: isNew, Nonce: s.nonce(c, "acfgo-group_"+itoa64(id)), GroupJSON: acfgoJSON(gm), Schema: acfgoJSON(s.acfgoEditorSchema(c))}
	s.pageAssets(c).Script("acfgo-field-group", "postbox").Style("acfgo")
	title := "Edit Field Group"
	screen := "acf-field-group"
	if isNew {
		title = "Add New Field Group"
	}
	s.adminPage(c, screen, title, "acf-admin-page post-type-acf-field-group post-php", "admin/acfgo-group-edit", d, notices...)
}

// acfgoEditorSchema is everything the field group editor's JavaScript needs: field types and
// their settings, location rule types and values, and the data sources of relational settings.
func (s *Server) acfgoEditorSchema(c *gin.Context) map[string]any {
	ctx := c.Request.Context()
	var types []map[string]any
	var postTypes []acfgoChoice
	for _, t := range acfgoAllowedTypes(acfField{}) {
		postTypes = append(postTypes, acfgoChoice{Value: t, Label: acfgoTypeName(t)})
	}
	postTypes = append(postTypes, acfgoChoice{Value: "attachment", Label: "Media"})
	locVals := s.acfgoLocationValues(ctx)
	var taxes []acfgoChoice
	for _, t := range locVals["taxonomy"] {
		if t.Value != "all" {
			taxes = append(taxes, t)
		}
	}
	sources := map[string]any{"post_types": postTypes, "post_statuses": locVals["post_status"], "taxonomies": taxes, "terms": locVals["post_taxonomy"],
		"roles": locVals["user_role"][1:], "groups": s.acfgoGroupChoices(c)}
	for _, ft := range acfgoFieldTypes {
		types = append(types, map[string]any{"name": ft.Name, "label": ft.Label, "category": ft.Category, "settings": ft.Settings})
	}
	return map[string]any{"types": types, "sources": sources, "locationParams": acfgoLocationParams, "locationValues": locVals,
		"hideOnScreen": acfgoHideItems}
}

// acfgoGroupChoices lists every group and field for the clone field's "Fields" setting.
func (s *Server) acfgoGroupChoices(c *gin.Context) []acfgoChoice {
	var out []acfgoChoice
	for _, g := range s.acfgoAllGroups(c.Request.Context(), false) {
		out = append(out, acfgoChoice{Value: g.Key, Label: "All fields from " + g.Title + " field group", Group: g.Title})
		for _, f := range g.Fields {
			out = append(out, acfgoChoice{Value: acfgoStr(f["key"]), Label: acfgoStr(f["label"]) + " (" + acfgoStr(f["type"]) + ")", Group: g.Title})
		}
	}
	return out
}

var acfgoHideItems = []acfgoChoice{{"permalink", "Permalink", ""}, {"the_content", "Content Editor", ""}, {"excerpt", "Excerpt", ""}, {"discussion", "Discussion", ""},
	{"comments", "Comments", ""}, {"revisions", "Revisions", ""}, {"slug", "Slug", ""}, {"author", "Author", ""}, {"format", "Format", ""},
	{"page_attributes", "Page Attributes", ""}, {"featured_image", "Featured Image", ""}, {"categories", "Categories", ""}, {"tags", "Tags", ""},
	{"send-trackbacks", "Send Trackbacks", ""}}

// Field types ------------------------------------------------------------------------------------

// acfgoSetting is one setting of a field type in the editor. Type is the control: text, textarea,
// number, true_false, select, radio, checkbox, choices (value : label lines) or a source name
// (post_types, post_statuses, taxonomies, terms, roles, groups, sub_fields) for multi-selects.
type acfgoSetting struct {
	Name         string        `json:"name"`
	Label        string        `json:"label"`
	Type         string        `json:"type"`
	Tab          string        `json:"tab"`
	Instructions string        `json:"instructions,omitempty"`
	Choices      []acfgoChoice `json:"choices,omitempty"`
	Default      any           `json:"default,omitempty"`
	Multiple     bool          `json:"multiple,omitempty"`
	ShowIf       []string      `json:"show_if,omitempty"` // "setting=value" conditions
}

type acfgoFieldType struct {
	Name, Label, Category string
	Settings              []acfgoSetting
}

func ch(pairs ...string) []acfgoChoice {
	var out []acfgoChoice
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, acfgoChoice{Value: pairs[i], Label: pairs[i+1]})
	}
	return out
}

var (
	setDefault     = acfgoSetting{Name: "default_value", Label: "Default Value", Type: "text", Tab: "general", Instructions: "Appears when creating a new post"}
	setDefaultArea = acfgoSetting{Name: "default_value", Label: "Default Value", Type: "textarea", Tab: "general", Instructions: "Appears when creating a new post"}
	setPlaceholder = acfgoSetting{Name: "placeholder", Label: "Placeholder Text", Type: "text", Tab: "presentation", Instructions: "Appears within the input"}
	setPrepend     = acfgoSetting{Name: "prepend", Label: "Prepend", Type: "text", Tab: "presentation", Instructions: "Appears before the input"}
	setAppend      = acfgoSetting{Name: "append", Label: "Append", Type: "text", Tab: "presentation", Instructions: "Appears after the input"}
	setMaxlength   = acfgoSetting{Name: "maxlength", Label: "Character Limit", Type: "number", Tab: "validation", Instructions: "Leave blank for no limit"}
	setMin         = acfgoSetting{Name: "min", Label: "Minimum Value", Type: "number", Tab: "validation"}
	setMax         = acfgoSetting{Name: "max", Label: "Maximum Value", Type: "number", Tab: "validation"}
	setStep        = acfgoSetting{Name: "step", Label: "Step Size", Type: "number", Tab: "presentation"}
	setChoices     = acfgoSetting{Name: "choices", Label: "Choices", Type: "choices", Tab: "general", Instructions: "Enter each choice on a new line. For more control, you may specify both a value and label like this:\nred : Red"}
	setChoiceDef   = acfgoSetting{Name: "default_value", Label: "Default Value", Type: "textarea", Tab: "general", Instructions: "Enter each default value on a new line"}
	setChoiceRF    = acfgoSetting{Name: "return_format", Label: "Return Value", Type: "radio", Tab: "general", Choices: ch("value", "Value", "label", "Label", "array", "Both (Array)"), Default: "value", Instructions: "Specify the returned value on front end"}
	setAllowNull   = acfgoSetting{Name: "allow_null", Label: "Allow Null", Type: "true_false", Tab: "general"}
	setMultiple    = acfgoSetting{Name: "multiple", Label: "Select Multiple", Type: "true_false", Tab: "general", Instructions: "Allow content editors to select multiple values"}
	setLayoutVH    = acfgoSetting{Name: "layout", Label: "Layout", Type: "radio", Tab: "presentation", Choices: ch("vertical", "Vertical", "horizontal", "Horizontal"), Default: "vertical"}
	setLibrary     = acfgoSetting{Name: "library", Label: "Library", Type: "radio", Tab: "general", Choices: ch("all", "All", "uploadedTo", "Uploaded to post"), Default: "all", Instructions: "Limit the media library choice"}
	setMediaRF     = acfgoSetting{Name: "return_format", Label: "Return Format", Type: "radio", Tab: "general", Choices: ch("array", "Image Array", "url", "Image URL", "id", "Image ID"), Default: "array"}
	setFileRF      = acfgoSetting{Name: "return_format", Label: "Return Value", Type: "radio", Tab: "general", Choices: ch("array", "File Array", "url", "File URL", "id", "File ID"), Default: "array"}
	setMime        = acfgoSetting{Name: "mime_types", Label: "Allowed File Types", Type: "text", Tab: "validation", Instructions: "Comma separated list. Leave blank for all types"}
	setPreviewSize = acfgoSetting{Name: "preview_size", Label: "Preview Size", Type: "select", Tab: "presentation", Choices: ch("thumbnail", "Thumbnail (150x150)", "medium", "Medium (300x300)", "large", "Large (1024x1024)", "full", "Full Size"), Default: "medium"}
	setPostType    = acfgoSetting{Name: "post_type", Label: "Filter by Post Type", Type: "post_types", Tab: "general", Multiple: true}
	setPostStatus  = acfgoSetting{Name: "post_status", Label: "Filter by Post Status", Type: "post_statuses", Tab: "general", Multiple: true}
	setTaxFilter   = acfgoSetting{Name: "taxonomy", Label: "Filter by Taxonomy", Type: "terms", Tab: "general", Multiple: true}
	setDateFormats = ch("d/m/Y", "d/m/Y", "m/d/Y", "m/d/Y", "F j, Y", "F j, Y", "Ymd", "Ymd", "Y-m-d", "Y-m-d")
	setWeekStart   = acfgoSetting{Name: "first_day", Label: "Week Starts On", Type: "select", Tab: "presentation", Default: "1", Choices: ch("0", "Sunday", "1", "Monday", "2", "Tuesday", "3", "Wednesday", "4", "Thursday", "5", "Friday", "6", "Saturday")}
	setSubFields   = acfgoSetting{Name: "sub_fields", Label: "Sub Fields", Type: "sub_fields", Tab: "general"}
	setLayoutTBR   = acfgoSetting{Name: "layout", Label: "Layout", Type: "radio", Tab: "presentation", Choices: ch("table", "Table", "block", "Block", "row", "Row"), Default: "block", Instructions: "Specify the style used to render the selected fields"}
)

var acfgoFieldTypes = []acfgoFieldType{
	{"text", "Text", "Basic", []acfgoSetting{setDefault, setMaxlength, setPlaceholder, setPrepend, setAppend}},
	{"textarea", "Text Area", "Basic", []acfgoSetting{setDefaultArea, setMaxlength, {Name: "rows", Label: "Rows", Type: "number", Tab: "presentation", Instructions: "Sets the textarea height"}, setPlaceholder,
		{Name: "new_lines", Label: "New Lines", Type: "select", Tab: "presentation", Choices: ch("wpautop", "Automatically add paragraphs", "br", "Automatically add <br>", "", "No Formatting"), Instructions: "Controls how new lines are rendered"}}},
	{"number", "Number", "Basic", []acfgoSetting{setDefault, setMin, setMax, setPlaceholder, setStep, setPrepend, setAppend}},
	{"range", "Range", "Basic", []acfgoSetting{setDefault, setMin, setMax, setStep, setPrepend, setAppend}},
	{"email", "Email", "Basic", []acfgoSetting{setDefault, setPlaceholder, setPrepend, setAppend}},
	{"url", "URL", "Basic", []acfgoSetting{setDefault, setPlaceholder}},
	{"password", "Password", "Basic", []acfgoSetting{setPlaceholder, setPrepend, setAppend}},
	{"image", "Image", "Content", []acfgoSetting{setMediaRF, setLibrary, {Name: "min_width", Label: "Minimum width (px)", Type: "number", Tab: "validation"}, {Name: "min_height", Label: "Minimum height (px)", Type: "number", Tab: "validation"},
		{Name: "min_size", Label: "Minimum file size (MB)", Type: "number", Tab: "validation"}, {Name: "max_width", Label: "Maximum width (px)", Type: "number", Tab: "validation"},
		{Name: "max_height", Label: "Maximum height (px)", Type: "number", Tab: "validation"}, {Name: "max_size", Label: "Maximum file size (MB)", Type: "number", Tab: "validation"}, setMime, setPreviewSize}},
	{"file", "File", "Content", []acfgoSetting{setFileRF, setLibrary, {Name: "min_size", Label: "Minimum file size (MB)", Type: "number", Tab: "validation"}, {Name: "max_size", Label: "Maximum file size (MB)", Type: "number", Tab: "validation"}, setMime}},
	{"wysiwyg", "WYSIWYG Editor", "Content", []acfgoSetting{setDefaultArea, {Name: "tabs", Label: "Tabs", Type: "select", Tab: "presentation", Choices: ch("all", "Visual & Text", "visual", "Visual Only", "text", "Text Only"), Default: "all"},
		{Name: "toolbar", Label: "Toolbar", Type: "select", Tab: "presentation", Choices: ch("full", "Full", "basic", "Basic"), Default: "full"}, {Name: "media_upload", Label: "Show Media Upload Buttons", Type: "true_false", Tab: "presentation", Default: 1},
		{Name: "delay", Label: "Delay Initialization", Type: "true_false", Tab: "presentation", Instructions: "TinyMCE will not be initialized until field is clicked"}}},
	{"oembed", "oEmbed", "Content", []acfgoSetting{{Name: "width", Label: "Embed Size: Width", Type: "number", Tab: "presentation"}, {Name: "height", Label: "Embed Size: Height", Type: "number", Tab: "presentation"}}},
	{"gallery", "Gallery", "Content", []acfgoSetting{setMediaRF, setLibrary, {Name: "min", Label: "Minimum Selection", Type: "number", Tab: "validation"}, {Name: "max", Label: "Maximum Selection", Type: "number", Tab: "validation"},
		{Name: "min_width", Label: "Minimum width (px)", Type: "number", Tab: "validation"}, {Name: "min_height", Label: "Minimum height (px)", Type: "number", Tab: "validation"},
		{Name: "max_width", Label: "Maximum width (px)", Type: "number", Tab: "validation"}, {Name: "max_height", Label: "Maximum height (px)", Type: "number", Tab: "validation"}, setMime,
		{Name: "insert", Label: "Insert", Type: "select", Tab: "presentation", Choices: ch("append", "Append to the end", "prepend", "Prepend to the beginning"), Default: "append"}, setPreviewSize}},
	{"select", "Select", "Choice", []acfgoSetting{setChoices, setChoiceDef, setChoiceRF, setMultiple, setAllowNull, {Name: "ui", Label: "Stylized UI", Type: "true_false", Tab: "presentation", Instructions: "A searchable dropdown (chips for multiple values) instead of the browser's select"},
		{Name: "ajax", Label: "Use AJAX to lazy load choices?", Type: "true_false", Tab: "presentation", ShowIf: []string{"ui=1"}, Instructions: "Not available in Pursae: the choices are the field's own list"}, {Name: "placeholder", Label: "Placeholder Text", Type: "text", Tab: "presentation"}}},
	{"checkbox", "Checkbox", "Choice", []acfgoSetting{setChoices, setChoiceDef, setChoiceRF, {Name: "allow_custom", Label: "Allow Custom Values", Type: "true_false", Tab: "general", Instructions: "Allow 'custom' values to be added"},
		{Name: "save_custom", Label: "Save Custom Values", Type: "true_false", Tab: "general", ShowIf: []string{"allow_custom=1"}, Instructions: "Save 'custom' values to the field's choices"},
		setLayoutVH, {Name: "toggle", Label: "Add Toggle All", Type: "true_false", Tab: "presentation", Instructions: "Prepend an extra checkbox to toggle all choices"}}},
	{"radio", "Radio Button", "Choice", []acfgoSetting{setChoices, setDefault, setChoiceRF, setAllowNull, {Name: "other_choice", Label: "Allow Other Choice", Type: "true_false", Tab: "general", Instructions: "Add 'other' choice to allow for custom values"},
		{Name: "save_other_choice", Label: "Save Other Choice", Type: "true_false", Tab: "general", ShowIf: []string{"other_choice=1"}, Instructions: "Save 'other' values to the field's choices"}, setLayoutVH}},
	{"button_group", "Button Group", "Choice", []acfgoSetting{setChoices, setDefault, setChoiceRF, setAllowNull, {Name: "layout", Label: "Layout", Type: "radio", Tab: "presentation", Choices: ch("horizontal", "Horizontal", "vertical", "Vertical"), Default: "horizontal"}}},
	{"true_false", "True / False", "Choice", []acfgoSetting{{Name: "default_value", Label: "Default Value", Type: "true_false", Tab: "general"}, {Name: "message", Label: "Message", Type: "text", Tab: "presentation", Instructions: "Displays text alongside the checkbox"},
		{Name: "ui", Label: "Stylized UI", Type: "true_false", Tab: "presentation"}, {Name: "ui_on_text", Label: "On Text", Type: "text", Tab: "presentation", ShowIf: []string{"ui=1"}},
		{Name: "ui_off_text", Label: "Off Text", Type: "text", Tab: "presentation", ShowIf: []string{"ui=1"}}}},
	{"link", "Link", "Relational", []acfgoSetting{{Name: "return_format", Label: "Return Value", Type: "radio", Tab: "general", Choices: ch("array", "Link Array", "url", "Link URL"), Default: "array"}}},
	{"post_object", "Post Object", "Relational", []acfgoSetting{setPostType, setPostStatus, setTaxFilter, {Name: "return_format", Label: "Return Format", Type: "radio", Tab: "general", Choices: ch("object", "Post Object", "id", "Post ID"), Default: "object"},
		setMultiple, setAllowNull, {Name: "ui", Label: "Stylized UI", Type: "true_false", Tab: "presentation", Default: 1, Instructions: "A searchable dropdown (chips for multiple values) instead of the browser's select"},
		{Name: "ajax", Label: "Use AJAX to lazy load choices?", Type: "true_false", Tab: "presentation", Instructions: "Search the choices while typing instead of loading all of them (large sites)"},
		{Name: "bidirectional", Label: "Bidirectional", Type: "true_false", Tab: "general", Instructions: "Update the selected fields of the related objects too, so the relationship shows on both sides"},
		{Name: "bidirectional_target", Label: "Target Field", Type: "groups", Tab: "general", ShowIf: []string{"bidirectional=1"}, Instructions: "Select the field(s) to be updated on the related objects (pick fields, not groups)"}}},
	{"page_link", "Page Link", "Relational", []acfgoSetting{setPostType, setPostStatus, setTaxFilter, {Name: "allow_archives", Label: "Allow Archives URLs", Type: "true_false", Tab: "general", Default: 1}, setMultiple, setAllowNull, {Name: "ajax", Label: "Use AJAX to lazy load choices?", Type: "true_false", Tab: "presentation", Instructions: "Search the choices while typing instead of loading all of them (large sites)"}}},
	{"relationship", "Relationship", "Relational", []acfgoSetting{setPostType, setPostStatus, setTaxFilter, {Name: "return_format", Label: "Return Format", Type: "radio", Tab: "general", Choices: ch("object", "Post Object", "id", "Post ID"), Default: "object"},
		{Name: "min", Label: "Minimum Posts", Type: "number", Tab: "validation"}, {Name: "max", Label: "Maximum Posts", Type: "number", Tab: "validation"},
		{Name: "filters", Label: "Filters", Type: "checkbox", Tab: "presentation", Choices: ch("search", "Search", "post_type", "Post Type", "taxonomy", "Taxonomy"), Default: []any{"search", "post_type", "taxonomy"}},
		{Name: "elements", Label: "Elements", Type: "checkbox", Tab: "presentation", Choices: ch("featured_image", "Featured Image"), Instructions: "Selected elements will be displayed in each result"},
		{Name: "bidirectional", Label: "Bidirectional", Type: "true_false", Tab: "general", Instructions: "Update the selected fields of the related objects too, so the relationship shows on both sides"},
		{Name: "bidirectional_target", Label: "Target Field", Type: "groups", Tab: "general", ShowIf: []string{"bidirectional=1"}, Instructions: "Select the field(s) to be updated on the related objects (pick fields, not groups)"}}},
	{"taxonomy", "Taxonomy", "Relational", []acfgoSetting{{Name: "taxonomy", Label: "Taxonomy", Type: "taxonomies", Tab: "general", Default: "category", Instructions: "Select the taxonomy to be displayed"},
		{Name: "field_type", Label: "Appearance", Type: "select", Tab: "general", Choices: ch("checkbox", "Checkbox", "multi_select", "Multi Select", "radio", "Radio Buttons", "select", "Select"), Default: "checkbox", Instructions: "Select the appearance of this field"},
		{Name: "add_term", Label: "Create Terms", Type: "true_false", Tab: "general", Default: 1, Instructions: "Allow new terms to be created whilst editing"},
		{Name: "save_terms", Label: "Save Terms", Type: "true_false", Tab: "general", Instructions: "Connect selected terms to the post"},
		{Name: "load_terms", Label: "Load Terms", Type: "true_false", Tab: "general", Instructions: "Load value from posts terms"},
		{Name: "return_format", Label: "Return Value", Type: "radio", Tab: "general", Choices: ch("object", "Term Object", "id", "Term ID"), Default: "id"}, setAllowNull}},
	{"user", "User", "Relational", []acfgoSetting{{Name: "role", Label: "Filter by Role", Type: "roles", Tab: "general", Multiple: true},
		{Name: "return_format", Label: "Return Format", Type: "radio", Tab: "general", Choices: ch("array", "User Array", "object", "User Object", "id", "User ID"), Default: "array"}, setMultiple, setAllowNull, {Name: "ajax", Label: "Use AJAX to lazy load choices?", Type: "true_false", Tab: "presentation", Instructions: "Search the choices while typing instead of loading all of them (large sites)"},
		{Name: "bidirectional", Label: "Bidirectional", Type: "true_false", Tab: "general", Instructions: "Update the selected fields of the related objects too, so the relationship shows on both sides"},
		{Name: "bidirectional_target", Label: "Target Field", Type: "groups", Tab: "general", ShowIf: []string{"bidirectional=1"}, Instructions: "Select the field(s) to be updated on the related objects (pick fields, not groups)"}}},
	{"google_map", "Google Map", "Advanced", []acfgoSetting{{Name: "center_lat", Label: "Center (lat)", Type: "text", Tab: "presentation", Instructions: "Center the initial map"}, {Name: "center_lng", Label: "Center (lng)", Type: "text", Tab: "presentation"},
		{Name: "zoom", Label: "Zoom", Type: "number", Tab: "presentation", Instructions: "Set the initial zoom level"}, {Name: "height", Label: "Height", Type: "number", Tab: "presentation", Instructions: "Customize the map height"}}},
	{"date_picker", "Date Picker", "Advanced", []acfgoSetting{{Name: "display_format", Label: "Display Format", Type: "radio", Tab: "general", Choices: setDateFormats, Default: "d/m/Y", Instructions: "The format displayed when editing a post"},
		{Name: "return_format", Label: "Return Format", Type: "radio", Tab: "general", Choices: setDateFormats, Default: "d/m/Y", Instructions: "The format returned via template functions"}, setWeekStart}},
	{"date_time_picker", "Date Time Picker", "Advanced", []acfgoSetting{{Name: "display_format", Label: "Display Format", Type: "radio", Tab: "general", Choices: ch("d/m/Y g:i a", "d/m/Y g:i a", "m/d/Y g:i a", "m/d/Y g:i a", "F j, Y g:i a", "F j, Y g:i a", "Y-m-d H:i:s", "Y-m-d H:i:s"), Default: "d/m/Y g:i a"},
		{Name: "return_format", Label: "Return Format", Type: "radio", Tab: "general", Choices: ch("d/m/Y g:i a", "d/m/Y g:i a", "m/d/Y g:i a", "m/d/Y g:i a", "F j, Y g:i a", "F j, Y g:i a", "Y-m-d H:i:s", "Y-m-d H:i:s"), Default: "d/m/Y g:i a"}, setWeekStart}},
	{"time_picker", "Time Picker", "Advanced", []acfgoSetting{{Name: "display_format", Label: "Display Format", Type: "radio", Tab: "general", Choices: ch("g:i a", "g:i a", "H:i:s", "H:i:s"), Default: "g:i a"},
		{Name: "return_format", Label: "Return Format", Type: "radio", Tab: "general", Choices: ch("g:i a", "g:i a", "H:i:s", "H:i:s"), Default: "g:i a"}}},
	{"color_picker", "Color Picker", "Advanced", []acfgoSetting{setDefault, {Name: "enable_opacity", Label: "Enable Transparency", Type: "true_false", Tab: "general"},
		{Name: "return_format", Label: "Return Format", Type: "radio", Tab: "general", Choices: ch("string", "Hex String", "array", "RGBA Array"), Default: "string"}}},
	{"icon_picker", "Icon Picker", "Advanced", []acfgoSetting{{Name: "tabs", Label: "Tabs", Type: "checkbox", Tab: "general", Choices: ch("dashicons", "Dashicons", "media_library", "Media Library", "url", "URL"), Default: []any{"dashicons", "media_library", "url"}},
		{Name: "return_format", Label: "Return Format", Type: "radio", Tab: "general", Choices: ch("string", "String", "array", "Array"), Default: "string"}}},
	{"message", "Message", "Layout", []acfgoSetting{{Name: "message", Label: "Message", Type: "textarea", Tab: "general"},
		{Name: "new_lines", Label: "New Lines", Type: "select", Tab: "general", Choices: ch("wpautop", "Automatically add paragraphs", "br", "Automatically add <br>", "", "No Formatting"), Default: "wpautop"},
		{Name: "esc_html", Label: "Escape HTML", Type: "true_false", Tab: "general", Instructions: "Allow HTML markup to display as visible text instead of rendering"}}},
	{"accordion", "Accordion", "Layout", []acfgoSetting{{Name: "open", Label: "Open", Type: "true_false", Tab: "general", Instructions: "Display this accordion as open on page load."},
		{Name: "multi_expand", Label: "Multi-Expand", Type: "true_false", Tab: "general", Instructions: "Allow this accordion to open without closing others."},
		{Name: "endpoint", Label: "Endpoint", Type: "true_false", Tab: "general", Instructions: "Define an endpoint for the previous accordion to stop. This accordion will not be visible."}}},
	{"tab", "Tab", "Layout", []acfgoSetting{{Name: "placement", Label: "Placement", Type: "select", Tab: "general", Choices: ch("top", "Top aligned", "left", "Left aligned"), Default: "top"},
		{Name: "endpoint", Label: "New Tab Group", Type: "true_false", Tab: "general", Instructions: "Start a new group of tabs at this tab."},
		{Name: "selected", Label: "Open by default", Type: "true_false", Tab: "general"}}},
	{"group", "Group", "Layout", []acfgoSetting{setSubFields, setLayoutTBR}},
	{"repeater", "Repeater", "Layout", []acfgoSetting{setSubFields, {Name: "layout", Label: "Layout", Type: "radio", Tab: "presentation", Choices: ch("table", "Table", "block", "Block", "row", "Row"), Default: "table"},
		{Name: "pagination", Label: "Pagination", Type: "true_false", Tab: "presentation", Instructions: "Show the rows in pages (the rows of other pages stay in the form and are saved)"},
		{Name: "rows_per_page", Label: "Rows Per Page", Type: "number", Tab: "presentation", ShowIf: []string{"pagination=1"}, Default: 20},
		{Name: "min", Label: "Minimum Rows", Type: "number", Tab: "validation"}, {Name: "max", Label: "Maximum Rows", Type: "number", Tab: "validation"},
		{Name: "collapsed", Label: "Collapsed", Type: "sub_field_choice", Tab: "presentation", Instructions: "Select a sub field to show when row is collapsed"},
		{Name: "button_label", Label: "Button Label", Type: "text", Tab: "presentation", Default: "Add Row"}}},
	{"flexible_content", "Flexible Content", "Layout", []acfgoSetting{{Name: "layouts", Label: "Layouts", Type: "layouts", Tab: "general"},
		{Name: "min", Label: "Minimum Layouts", Type: "number", Tab: "validation"}, {Name: "max", Label: "Maximum Layouts", Type: "number", Tab: "validation"},
		{Name: "button_label", Label: "Button Label", Type: "text", Tab: "presentation", Default: "Add Row"}}},
	{"clone", "Clone", "Layout", []acfgoSetting{{Name: "clone", Label: "Fields", Type: "groups", Tab: "general", Multiple: true, Instructions: "Select one or more fields you wish to clone"},
		{Name: "display", Label: "Display", Type: "select", Tab: "general", Choices: ch("seamless", "Seamless (replaces this field with selected fields)", "group", "Group (displays selected fields in a group within this field)"), Default: "seamless"},
		{Name: "layout", Label: "Layout", Type: "radio", Tab: "general", Choices: ch("block", "Block", "table", "Table", "row", "Row"), Default: "block", ShowIf: []string{"display=group"}},
		{Name: "prefix_label", Label: "Prefix Field Labels", Type: "true_false", Tab: "general", Instructions: "Labels will be displayed as %field_label%"},
		{Name: "prefix_name", Label: "Prefix Field Names", Type: "true_false", Tab: "general", Instructions: "Values will be saved as %field_name%"}}},
}

// Post types, taxonomies and options pages -----------------------------------------------------------

type acfgoTypesData struct {
	Kind, Title, Singular, AddURL string
	Rows                          []acfgoTypeRow
	Views                         []acfgoListView
	Status                        string
}

type acfgoTypeRow struct {
	ID                                          int64
	Title, Key, Slug, Extra, Extra2, Status     string
	Active                                      bool
	EditURL, TrashURL, RestoreURL, DeleteURL    string
	ToggleURL, ToggleLabel, DuplicateURL, Count string
}

var acfgoKindInfo = map[string]struct{ title, singular, list, edit string }{
	acfgoPostType:    {"Post Types", "Post Type", "acf-post-types", "acf-post-type"},
	acfgoTaxType:     {"Taxonomies", "Taxonomy", "acf-taxonomies", "acf-taxonomy"},
	acfgoOptionsType: {"Options Pages", "Options Page", "acf-options-pages", "acf-options-page"},
}

func (s *Server) acfgoTypesScreen(kind string) gin.HandlerFunc {
	info := acfgoKindInfo[kind]
	return func(c *gin.Context) {
		if !s.acfgoCanAdmin(c) {
			return
		}
		ctx := c.Request.Context()
		if a := c.Query("action"); a != "" {
			id, _ := strconv.ParseInt(c.Query("post"), 10, 64)
			if !s.checkNonce(c, "acfgo-"+a+"_"+itoa64(id)) {
				s.denyNonce(c)
				return
			}
			if it := s.acfgoItem(ctx, kind, id); it != nil {
				switch a {
				case "trash", "activate", "deactivate", "untrash":
					_ = s.acfgoSetObjectStatus(ctx, it, map[string]string{"trash": "trash", "activate": "publish", "deactivate": "draft", "untrash": "untrash"}[a])
				case "delete":
					_, _ = s.store.Pool.Exec(ctx, `DELETE FROM posts WHERE id = $1 AND type = $2`, id, kind)
				case "duplicate":
					cp := map[string]any{}
					raw, _ := json.Marshal(it.Data)
					_ = json.Unmarshal(raw, &cp)
					cp["key"], cp["title"] = "", acfgoStr(cp["title"])+" (copy)"
					for _, k := range []string{"post_type", "taxonomy", "menu_slug"} {
						if v := acfgoStr(cp[k]); v != "" {
							cp[k] = v + "-copy"
						}
					}
					cp["active"] = false
					_ = s.acfgoSaveObject(ctx, kind, cp)
				}
			}
			s.acfgoRegisterTypes(ctx)
			c.Redirect(http.StatusFound, "/wp-admin/"+info.list)
			return
		}
		d := acfgoTypesData{Kind: kind, Title: info.title, Singular: info.singular, AddURL: "/wp-admin/" + info.edit, Status: c.DefaultQuery("post_status", "all")}
		items := s.acfgoItems(ctx, kind, true)
		counts := map[string]int{}
		for _, it := range items {
			if it.Status == "trash" {
				counts["trash"]++
			} else {
				counts["all"]++
				counts[it.Status]++
			}
		}
		for _, v := range []struct{ key, label string }{{"all", "All"}, {"publish", "Active"}, {"draft", "Inactive"}, {"trash", "Trash"}} {
			if v.key == "all" || counts[v.key] > 0 {
				u := "/wp-admin/" + info.list
				if v.key != "all" {
					u += "?post_status=" + v.key
				}
				d.Views = append(d.Views, acfgoListView{v.key, v.label, u, counts[v.key], d.Status == v.key})
			}
		}
		n := func(action string, id int64) string {
			return fmt.Sprintf("/wp-admin/%s?action=%s&post=%d&_wpnonce=%s", info.list, action, id, s.nonce(c, "acfgo-"+action+"_"+itoa64(id)))
		}
		for _, it := range items {
			if (d.Status == "trash") != (it.Status == "trash") || (d.Status != "all" && d.Status != "trash" && it.Status != d.Status) {
				continue
			}
			r := acfgoTypeRow{ID: it.ID, Title: it.Title(), Key: it.Key(), Status: it.Status, Active: it.Active(),
				EditURL: "/wp-admin/" + info.edit + "?post=" + itoa64(it.ID), TrashURL: n("trash", it.ID), RestoreURL: n("untrash", it.ID),
				DeleteURL: n("delete", it.ID), DuplicateURL: n("duplicate", it.ID)}
			r.ToggleURL, r.ToggleLabel = n("deactivate", it.ID), "Deactivate"
			if !it.Active() {
				r.ToggleURL, r.ToggleLabel = n("activate", it.ID), "Activate"
			}
			switch kind {
			case acfgoPostType:
				r.Slug = acfgoStr(it.Data["post_type"])
				r.Extra = strings.Join(acfgoStrList(it.Data["taxonomies"]), ", ")
				var cnt int
				_ = s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM posts WHERE type = $1 AND status <> 'trash' AND status <> 'auto-draft'`, r.Slug).Scan(&cnt)
				r.Count = strconv.Itoa(cnt)
			case acfgoTaxType:
				r.Slug = acfgoStr(it.Data["taxonomy"])
				r.Extra = strings.Join(acfgoStrList(it.Data["object_type"]), ", ")
				var cnt int
				_ = s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM terms WHERE taxonomy = $1`, r.Slug).Scan(&cnt)
				r.Count = strconv.Itoa(cnt)
			default:
				op := acfgoOptionsPageFrom(it.Data)
				r.Slug = op.MenuSlug
				r.Extra = defaultStr(op.ParentSlug, "—")
				r.Count = "admin?page=" + op.MenuSlug
			}
			d.Rows = append(d.Rows, r)
		}
		s.pageAssets(c).Style("acfgo")
		s.adminPage(c, info.list, info.title, "acf-admin-page edit-php post-type-"+kind, "admin/acfgo-types", d)
	}
}

type acfgoTypeEditData struct {
	Kind, Title, Singular, ListURL, Nonce string
	ID                                    int64
	IsNew                                 bool
	Data                                  map[string]any
	Labels                                []acfgoLabelRow
	PostTypes, Taxonomies, MenuParents    []acfgoChoice
	Supports                              []acfgoChoice
	Selected                              map[string]bool
	Errors                                []string
}

type acfgoLabelRow struct{ Name, Label, Value string }

var acfgoPostTypeLabelNames = []string{"menu_name", "all_items", "edit_item", "view_item", "view_items", "add_new_item", "add_new", "new_item", "parent_item_colon",
	"search_items", "not_found", "not_found_in_trash", "archives", "attributes", "insert_into_item", "uploaded_to_this_item", "filter_items_list",
	"items_list_navigation", "items_list", "item_published", "item_updated", "item_link", "item_link_description"}
var acfgoTaxLabelNames = []string{"menu_name", "all_items", "edit_item", "view_item", "update_item", "add_new_item", "new_item_name", "parent_item", "parent_item_colon",
	"search_items", "popular_items", "separate_items_with_commas", "add_or_remove_items", "choose_from_most_used", "not_found", "no_terms",
	"items_list_navigation", "items_list", "back_to_items", "item_link", "item_link_description"}

// acfgoTypeEdit is the add/edit screen of a post type, taxonomy or options page.
func (s *Server) acfgoTypeEdit(kind string) gin.HandlerFunc {
	info := acfgoKindInfo[kind]
	return func(c *gin.Context) {
		if !s.acfgoCanAdmin(c) {
			return
		}
		ctx := c.Request.Context()
		id, _ := strconv.ParseInt(defaultStr(c.Query("post"), c.PostForm("post_ID")), 10, 64)
		data := map[string]any{"active": true}
		if id != 0 {
			it := s.acfgoItem(ctx, kind, id)
			if it == nil {
				c.String(http.StatusNotFound, "You attempted to edit an item that does not exist. Perhaps it was deleted?")
				return
			}
			data = it.Data
		}
		var errs []string
		if c.Request.Method == http.MethodPost {
			if !s.checkNonce(c, "acfgo-type_"+itoa64(id)) {
				s.denyNonce(c)
				return
			}
			data, errs = s.acfgoTypeFromForm(c, kind, data)
			if len(errs) == 0 {
				if err := s.acfgoSaveObject(ctx, kind, data); err != nil {
					errs = append(errs, err.Error())
				} else {
					var nid int64
					_ = s.store.Pool.QueryRow(ctx, `SELECT id FROM posts WHERE type = $1 AND slug = $2`, kind, acfgoStr(data["key"])).Scan(&nid)
					c.Redirect(http.StatusFound, "/wp-admin/"+info.edit+"?post="+itoa64(nid)+"&message=1")
					return
				}
			}
		}
		d := acfgoTypeEditData{Kind: kind, Title: info.title, Singular: info.singular, ListURL: "/wp-admin/" + info.list, ID: id, IsNew: id == 0,
			Nonce: s.nonce(c, "acfgo-type_"+itoa64(id)), Data: data, Selected: map[string]bool{}, Errors: errs}
		names := acfgoPostTypeLabelNames
		if kind == acfgoTaxType {
			names = acfgoTaxLabelNames
		}
		for _, n := range names {
			d.Labels = append(d.Labels, acfgoLabelRow{n, strings.ReplaceAll(strings.Title(strings.ReplaceAll(n, "_", " ")), "Item", "Item"), acfgoLabel(data, n)}) //nolint:staticcheck
		}
		for _, t := range acfgoAllowedTypes(acfField{}) {
			d.PostTypes = append(d.PostTypes, acfgoChoice{Value: t, Label: acfgoTypeName(t)})
		}
		for _, v := range acfgoStrList(data["object_type"]) {
			d.Selected["obj:"+v] = true
		}
		for _, v := range acfgoStrList(data["taxonomies"]) {
			d.Selected["tax:"+v] = true
		}
		sup := acfgoStrList(data["supports"])
		if id == 0 && kind == acfgoPostType && data["supports"] == nil {
			sup = []string{"title", "editor", "thumbnail"}
		}
		for _, v := range sup {
			d.Selected["sup:"+v] = true
		}
		rows, err := s.store.Pool.Query(ctx, `SELECT slug, coalesce(labels->>'name', name) FROM taxonomies WHERE slug <> 'post_format' ORDER BY id`)
		if err == nil {
			for rows.Next() {
				var slug, name string
				if rows.Scan(&slug, &name) == nil {
					d.Taxonomies = append(d.Taxonomies, acfgoChoice{Value: slug, Label: name})
				}
			}
			rows.Close()
		}
		d.Supports = ch("title", "Title", "author", "Author", "comments", "Comments", "trackbacks", "Trackbacks", "editor", "Editor", "excerpt", "Excerpt",
			"revisions", "Revisions", "page-attributes", "Page Attributes", "thumbnail", "Featured Image", "custom-fields", "Custom Fields", "post-formats", "Post Formats")
		d.MenuParents = ch("", "No Parent", "options-general", "Settings", "themes", "Appearance", "tools", "Tools", "edit", "Posts", "edit?post_type=page", "Pages", "users", "Users")
		for _, op := range s.acfgoOptionsPages(ctx) {
			if op.ParentSlug == "" && op.MenuSlug != acfgoStr(data["menu_slug"]) {
				d.MenuParents = append(d.MenuParents, acfgoChoice{Value: op.MenuSlug, Label: op.PageTitle})
			}
		}
		var notices []notice
		if c.Query("message") == "1" {
			notices = append(notices, notice{Type: "success", Message: template.HTML(info.singular + " saved."), Dismissible: true})
		}
		for _, e := range errs {
			notices = append(notices, notice{Type: "error", Message: template.HTML(template.HTMLEscapeString(e))})
		}
		title := "Edit " + info.singular
		if id == 0 {
			title = "Add New " + info.singular
		}
		s.pageAssets(c).Style("acfgo")
		s.adminPage(c, info.edit, title, "acf-admin-page post-type-"+kind+" post-php", "admin/acfgo-type-edit", d, notices...)
	}
}

var acfgoSlugRe = func(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	var b strings.Builder
	for _, r := range v {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		} else if r == ' ' {
			b.WriteRune('_')
		}
	}
	return b.String()
}

var acfgoReservedTypes = map[string]bool{"post": true, "page": true, "attachment": true, "revision": true, "nav_menu_item": true, "custom_css": true,
	"customize_changeset": true, "oembed_cache": true, "user_request": true, "wp_block": true, "wp_template": true, "wp_template_part": true,
	"wp_global_styles": true, "wp_navigation": true, "action": true, "author": true, "order": true, "theme": true, "category": true, "post_tag": true,
	"post_format": true, "link_category": true, "nav_menu": true, "wp_theme": true, "type": true, "name": true, "term": true, "taxonomy": true,
	// Pursae Web Builder: Theme Builder templates (webbuilder.TemplatePostType)
	"wb_template": true,
	// Pursae Forum (forum.go)
	"forum": true, "topic": true, "reply": true,
}

// acfgoTypeFromForm reads the add/edit form of a post type, taxonomy or options page.
func (s *Server) acfgoTypeFromForm(c *gin.Context, kind string, old map[string]any) (map[string]any, []string) {
	ctx := c.Request.Context()
	d := map[string]any{}
	for k, v := range old {
		d[k] = v
	}
	var errs []string
	f := func(k string) string { return strings.TrimSpace(c.PostForm(k)) }
	b := func(k string) bool { return c.PostForm(k) == "1" }
	d["active"] = b("active")
	d["description"] = f("description")
	switch kind {
	case acfgoPostType, acfgoTaxType:
		plural, singular := f("plural_label"), f("singular_label")
		slug := acfgoSlugRe(f("slug"))
		if plural == "" || singular == "" {
			errs = append(errs, "Plural and singular labels are required.")
		}
		if slug == "" {
			slug = acfgoSlugRe(singular)
		}
		if len(slug) > 20 || (kind == acfgoTaxType && len(slug) > 32) {
			errs = append(errs, "The key must not exceed 20 characters (32 for taxonomies).")
		}
		if acfgoReservedTypes[slug] {
			errs = append(errs, "\""+slug+"\" is a reserved key in WordPress.")
		}
		field := map[string]string{acfgoPostType: "post_type", acfgoTaxType: "taxonomy"}[kind]
		if slug != acfgoStr(old[field]) {
			var exists bool
			table := map[string]string{acfgoPostType: "content_types", acfgoTaxType: "taxonomies"}[kind]
			_ = s.store.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM `+table+` WHERE slug = $1)`, slug).Scan(&exists)
			for _, it := range s.acfgoItems(ctx, kind, true) {
				if acfgoStr(it.Data[field]) == slug {
					exists = true
				}
			}
			if exists {
				errs = append(errs, "The key \""+slug+"\" is already in use.")
			}
		}
		d[field] = slug
		d["title"] = plural
		labels := acfgoGenerateLabels(kind == acfgoTaxType, singular, plural)
		names := acfgoPostTypeLabelNames
		if kind == acfgoTaxType {
			names = acfgoTaxLabelNames
		}
		if b("advanced_configuration") {
			for _, n := range names {
				if v := f("labels[" + n + "]"); v != "" {
					labels[n] = v
				}
			}
		}
		labels["name"], labels["singular_name"] = plural, singular
		d["labels"] = labels
		d["advanced_configuration"] = b("advanced_configuration")
		d["public"] = b("public")
		d["hierarchical"] = b("hierarchical")
		d["show_ui"] = c.PostForm("show_ui") != "0"
		d["show_in_menu"] = c.PostForm("show_in_menu") != "0"
		d["show_in_rest"] = b("show_in_rest")
		d["rest_base"] = acfgoSlugRe(f("rest_base"))
		d["rewrite"] = map[string]any{"slug": acfgoSlugRe(defaultStr(f("rewrite_slug"), "")), "with_front": "1"}
		if kind == acfgoPostType {
			d["taxonomies"] = c.PostFormArray("taxonomies[]")
			d["supports"] = c.PostFormArray("supports[]")
			d["menu_icon"] = defaultStr(f("menu_icon"), "dashicons-admin-post")
			d["menu_position"] = f("menu_position")
			d["has_archive"] = b("has_archive")
			d["has_archive_slug"] = acfgoSlugRe(f("has_archive_slug"))
			d["exclude_from_search"] = b("exclude_from_search")
			d["delete_with_user"] = b("delete_with_user")
			d["enter_title_here"] = f("enter_title_here")
		} else {
			d["object_type"] = c.PostFormArray("object_type[]")
			d["show_admin_column"] = b("show_admin_column")
			d["show_tagcloud"] = b("show_tagcloud")
			d["show_in_quick_edit"] = b("show_in_quick_edit")
		}
	default:
		title := f("page_title")
		if title == "" {
			errs = append(errs, "Page title is required.")
		}
		slug := wp.SanitizeTitle(defaultStr(f("menu_slug"), title))
		if slug == "" {
			errs = append(errs, "Menu slug is required.")
		}
		for _, it := range s.acfgoItems(ctx, acfgoOptionsType, true) {
			if acfgoStr(it.Data["menu_slug"]) == slug && acfgoStr(it.Data["key"]) != acfgoStr(old["key"]) {
				errs = append(errs, "The menu slug \""+slug+"\" is already in use.")
			}
		}
		d["title"], d["page_title"], d["menu_slug"] = title, title, slug
		d["menu_title"] = f("menu_title")
		d["parent_slug"] = defaultStr(f("parent_slug"), "none")
		d["position"] = f("position")
		d["menu_icon"] = map[string]any{"type": "dashicons", "value": defaultStr(f("menu_icon"), "dashicons-admin-generic")}
		d["icon_url"] = ""
		d["redirect"] = b("redirect")
		d["capability"] = defaultStr(f("capability"), "edit_posts")
		d["update_button"] = defaultStr(f("update_button"), "Update")
		d["updated_message"] = defaultStr(f("updated_message"), "Options Updated")
		d["data_storage"] = defaultStr(f("data_storage"), "options")
		d["post_id"] = f("post_id")
		// "autoload" is kept as imported: options are always read on demand here.
		d["advanced_configuration"] = b("advanced_configuration")
	}
	return d, errs
}

// Tools ------------------------------------------------------------------------------------------------

type acfgoToolsData struct {
	Groups      []*acfGroup
	Items       []*acfgoItem
	Nonce       string
	ExportJSON  string
	ImportCount int
	ShowAdmin   bool // "Show admin menu" setting (acfgo_show_admin)
}

// acfgoTools is ACF › Tools: export field groups (and post types, taxonomies, options pages) as
// JSON and import JSON files.
func (s *Server) acfgoTools(c *gin.Context) {
	if !s.acfgoCanAdmin(c) {
		return
	}
	ctx := c.Request.Context()
	d := acfgoToolsData{Groups: s.acfgoAllGroups(ctx, false), Nonce: s.nonce(c, "acfgo-tools")}
	d.ShowAdmin = s.settings.String("acfgo_show_admin") != "0"
	for _, k := range []string{acfgoPostType, acfgoTaxType, acfgoOptionsType} {
		d.Items = append(d.Items, s.acfgoItems(ctx, k, false)...)
	}
	var notices []notice
	if c.Request.Method == http.MethodPost {
		if !s.checkNonce(c, "acfgo-tools") {
			s.denyNonce(c)
			return
		}
		switch c.PostForm("tool") {
		case "settings":
			v := "0"
			if c.PostForm("acfgo_show_admin") == "1" {
				v = "1"
			}
			if err := s.settings.Set(ctx, map[string]any{"acfgo_show_admin": v}); err != nil {
				notices = append(notices, notice{Type: "error", Message: "Could not save the settings"})
				break
			}
			c.Redirect(http.StatusFound, "/wp-admin/acf-tools?saved=1")
			return
		case "export":
			out := s.acfgoExport(c, c.PostFormArray("keys[]"))
			if len(out) == 0 {
				notices = append(notices, notice{Type: "error", Message: "No items selected"})
				break
			}
			b, _ := json.MarshalIndent(out, "", "    ")
			if c.PostForm("action") == "download" {
				c.Header("Content-Description", "File Transfer")
				c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="acf-export-%s.json"`, acfgoTodayISO()))
				c.Data(http.StatusOK, "application/json; charset=utf-8", b)
				return
			}
			d.ExportJSON = string(b)
		case "import":
			f, _, err := c.Request.FormFile("acf_import_file")
			if err != nil {
				notices = append(notices, notice{Type: "error", Message: "No file selected"})
				break
			}
			data, _ := io.ReadAll(io.LimitReader(f, 8<<20))
			f.Close()
			n, err := s.acfgoImport(ctx, data)
			if err != nil {
				notices = append(notices, notice{Type: "error", Message: template.HTML(template.HTMLEscapeString(err.Error()))})
				break
			}
			c.Redirect(http.StatusFound, "/wp-admin/acf-tools?imported="+strconv.Itoa(n))
			return
		}
	}
	if c.Query("saved") != "" {
		notices = append(notices, notice{Type: "success", Message: "Settings saved", Dismissible: true})
	}
	if n := c.Query("imported"); n != "" {
		notices = append(notices, notice{Type: "success", Message: template.HTML("Imported " + template.HTMLEscapeString(n) + " item(s)"), Dismissible: true})
	}
	s.pageAssets(c).Style("acfgo")
	s.adminPage(c, "acf-tools", "Tools", "acf-admin-page acf-tools", "admin/acfgo-tools", d, notices...)
}

// acfgoExport returns the selected items in ACF's export format (fields keep their ACF keys).
func (s *Server) acfgoExport(c *gin.Context, keys []string) []map[string]any {
	ctx := c.Request.Context()
	want := map[string]bool{}
	for _, k := range keys {
		want[k] = true
	}
	var out []map[string]any
	for _, g := range s.acfgoAllGroups(ctx, false) {
		if want[g.Key] {
			m := g.toMap()
			delete(m, "_previous_status")
			out = append(out, m)
		}
	}
	for _, k := range []string{acfgoPostType, acfgoTaxType, acfgoOptionsType} {
		for _, it := range s.acfgoItems(ctx, k, false) {
			if want[it.Key()] {
				out = append(out, it.Data)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return acfgoStr(out[i]["key"]) < acfgoStr(out[j]["key"]) })
	return out
}

func acfgoTodayISO() string { return time.Now().Format("2006-01-02") }

// Str and Bool read the edited item in the template ({{.Str "post_type"}}).
func (d acfgoTypeEditData) Str(k string) string { return acfgoStr(d.Data[k]) }
func (d acfgoTypeEditData) Bool(k string) bool {
	if v, ok := d.Data[k]; ok {
		return acfgoBool(v)
	}
	return map[string]bool{"public": true, "show_in_rest": true, "show_ui": true, "show_in_menu": true, "active": true, "show_tagcloud": true, "show_in_quick_edit": true}[k] && d.IsNew
}

// Rewrite is the rewrite slug of a post type or taxonomy.
func (d acfgoTypeEditData) Rewrite() string {
	if m, ok := d.Data["rewrite"].(map[string]any); ok {
		return acfgoStr(m["slug"])
	}
	return ""
}

// Icon is the menu icon (a dashicons class).
func (d acfgoTypeEditData) Icon() string {
	if m, ok := d.Data["menu_icon"].(map[string]any); ok {
		return acfgoStr(m["value"])
	}
	return acfgoStr(d.Data["menu_icon"])
}
