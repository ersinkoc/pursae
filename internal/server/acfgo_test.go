package server

import (
	"bytes"
	"context"
	"encoding/json"
	"html"
	"html/template"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"wpgo/internal/store"
)

// Unit tests ------------------------------------------------------------------------------------

func TestACFGoLocationRules(t *testing.T) {
	post := &acfScreen{isPost: true, PostID: 7, PostType: "post", PostStatus: "publish", PostFormat: "standard", PostTemplate: "default",
		PostTerms: map[string][]*store.Term{"category": {{ID: 3, Slug: "news", Taxonomy: "category"}}}, User: &store.User{ID: 1, Roles: []string{"editor"}}}
	page := &acfScreen{isPost: true, PostID: 9, PostType: "page", PostParent: 0, HasChildren: true, frontPage: 9, PostTemplate: "templates/full.html"}
	user := &acfScreen{UserForm: "edit", UserRoles: []string{"author"}}
	cases := []struct {
		rule acfRule
		sc   *acfScreen
		want bool
	}{
		{acfRule{"post_type", "==", "post"}, post, true},
		{acfRule{"post_type", "!=", "post"}, post, false},
		{acfRule{"post_type", "==", "page"}, post, false},
		{acfRule{"post_type", "==", "post"}, user, false}, // a post rule never matches a user form
		{acfRule{"post_type", "!=", "post"}, user, false},
		{acfRule{"post_category", "==", "category:news"}, post, true},
		{acfRule{"post_taxonomy", "!=", "category:news"}, post, false},
		{acfRule{"post_status", "==", "publish"}, post, true},
		{acfRule{"post_format", "==", "standard"}, post, true},
		{acfRule{"post", "==", "7"}, post, true},
		{acfRule{"page_type", "==", "front_page"}, page, true},
		{acfRule{"page_type", "==", "top_level"}, page, true},
		{acfRule{"page_type", "==", "parent"}, page, true},
		{acfRule{"page_type", "==", "child"}, page, false},
		{acfRule{"page_template", "==", "templates/full.html"}, page, true},
		{acfRule{"page_template", "==", "default"}, post, false}, // page rules need a page
		{acfRule{"current_user_role", "==", "editor"}, post, true},
		{acfRule{"current_user", "==", "logged_in"}, post, true},
		{acfRule{"user_form", "==", "all"}, user, true},
		{acfRule{"user_role", "==", "author"}, user, true},
		{acfRule{"user_role", "==", "editor"}, user, false},
		{acfRule{"taxonomy", "==", "category"}, &acfScreen{Taxonomy: "category"}, true},
		{acfRule{"taxonomy", "==", "all"}, &acfScreen{Taxonomy: "genre"}, true},
		{acfRule{"attachment", "==", "image"}, &acfScreen{Attachment: "image/png"}, true},
		{acfRule{"options_page", "==", "site-settings"}, &acfScreen{OptionsPage: "site-settings"}, true},
		{acfRule{"block", "==", "acf/hero"}, &acfScreen{Block: "acf/hero"}, true},
		{acfRule{"widget", "==", "all"}, post, false},
	}
	for _, tc := range cases {
		if got := acfgoMatchRule(tc.rule, tc.sc); got != tc.want {
			t.Errorf("%v on %+v = %v, want %v", tc.rule, tc.sc.PostType+tc.sc.UserForm+tc.sc.Taxonomy, got, tc.want)
		}
	}
	// OR groups of AND rules.
	s := &Server{}
	g := &acfGroup{Active: true, Status: "publish", Location: [][]acfRule{
		{{"post_type", "==", "post"}, {"post_status", "==", "draft"}},
		{{"post_type", "==", "page"}},
	}}
	if s.acfgoMatchGroup(g, post) {
		t.Error("AND group matched a published post")
	}
	if !s.acfgoMatchGroup(g, page) {
		t.Error("OR group did not match the page")
	}
	g.Active = false
	if s.acfgoMatchGroup(g, page) {
		t.Error("inactive group matched")
	}
}

func TestACFGoParseForm(t *testing.T) {
	form := url.Values{
		"acf[field_a]":                                       {"hello"},
		"acf[field_cb]":                                      {""},
		"acf[field_cb][]":                                    {"red", "blue"},
		"acf[field_rep]":                                     {""},
		"acf[field_rep][r2][_order]":                         {"1"},
		"acf[field_rep][r2][field_sub]":                      {"second"},
		"acf[field_rep][row-0][_order]":                      {"0"},
		"acf[field_rep][row-0][field_sub]":                   {"first"},
		"acf[field_rep][acfcloneindex-field_rep][field_sub]": {"template"},
		"other": {"x"},
	}
	in := acfgoParseForm(form, "acf")
	if in["field_a"] != "hello" {
		t.Errorf("scalar: %v", in["field_a"])
	}
	if l, ok := in["field_cb"].([]any); !ok || len(l) != 2 || l[0] != "red" {
		t.Errorf("list: %#v", in["field_cb"])
	}
	rows := acfgoRows(in["field_rep"])
	if len(rows) != 2 || rows[0]["field_sub"] != "first" || rows[1]["field_sub"] != "second" {
		t.Errorf("rows: %#v", rows)
	}
	if _, ok := in["other"]; ok {
		t.Error("non-acf input parsed")
	}
}

func TestACFGoGroupJSONRoundTrip(t *testing.T) {
	src := `{"key":"group_x","title":"T","fields":[{"key":"field_fc","label":"FC","name":"fc","type":"flexible_content",
		"layouts":{"layout_b":{"key":"layout_b","name":"b","label":"B","sub_fields":[{"key":"field_b1","name":"b1","type":"text"}]}}}],
		"location":[[{"param":"post_type","operator":"==","value":"post"}]],"menu_order":2,"show_in_rest":1,"custom_key":"kept"}`
	var m map[string]any
	if err := json.Unmarshal([]byte(src), &m); err != nil {
		t.Fatal(err)
	}
	g := acfgoGroupFromMap(m)
	if g.Key != "group_x" || g.Order != 2 || !g.ShowInREST || len(g.Location) != 1 {
		t.Fatalf("group: %+v", g)
	}
	layouts := acfgoList(g.Fields[0]["layouts"])
	if len(layouts) != 1 || acfgoLayout(g.Fields[0], "b") == nil {
		t.Fatalf("layouts keyed by layout key were not read: %#v", g.Fields[0]["layouts"])
	}
	out := g.toMap()
	if out["custom_key"] != "kept" || out["position"] != "normal" {
		t.Errorf("unknown keys or defaults lost: %v", out)
	}
	if c := acfgoParseChoices("red : Red\nblue"); len(c) != 2 || c[0] != [2]string{"red", "Red"} || c[1] != [2]string{"blue", "blue"} {
		t.Errorf("choices: %v", c)
	}
}

// Integration ---------------------------------------------------------------------------------------

func acfgoTestGroup() map[string]any {
	return map[string]any{
		"title": "Product Details", "show_in_rest": 1, "position": "normal",
		"location": []any{[]any{map[string]any{"param": "post_type", "operator": "==", "value": "post"}}},
		"fields": []any{
			map[string]any{"key": "field_subtitle", "label": "Subtitle", "name": "subtitle", "type": "text", "required": 1, "maxlength": 40},
			map[string]any{"key": "field_price", "label": "Price", "name": "price", "type": "number", "min": 0},
			map[string]any{"key": "field_featured", "label": "Featured", "name": "featured", "type": "true_false"},
			map[string]any{"key": "field_colors", "label": "Colors", "name": "colors", "type": "checkbox", "return_format": "label",
				"choices": []any{map[string]any{"value": "red", "label": "Red"}, map[string]any{"value": "blue", "label": "Blue"}}},
			map[string]any{"key": "field_launch", "label": "Launch", "name": "launch", "type": "date_picker", "return_format": "F j, Y"},
			map[string]any{"key": "field_specs", "label": "Specs", "name": "specs", "type": "repeater", "min": 0, "sub_fields": []any{
				map[string]any{"key": "field_spec_label", "label": "Label", "name": "label", "type": "text"},
				map[string]any{"key": "field_spec_value", "label": "Value", "name": "value", "type": "number"},
			}},
			map[string]any{"key": "field_sections", "label": "Sections", "name": "sections", "type": "flexible_content", "layouts": []any{
				map[string]any{"key": "layout_hero", "name": "hero", "label": "Hero", "sub_fields": []any{
					map[string]any{"key": "field_hero_title", "label": "Title", "name": "title", "type": "text"}}},
				map[string]any{"key": "layout_quote", "name": "quote", "label": "Quote", "sub_fields": []any{
					map[string]any{"key": "field_quote_text", "label": "Text", "name": "text", "type": "textarea"},
					map[string]any{"key": "field_quote_items", "label": "Items", "name": "items", "type": "repeater", "sub_fields": []any{
						map[string]any{"key": "field_item", "label": "Item", "name": "item", "type": "text"}}}}},
			}},
			map[string]any{"key": "field_seller", "label": "Seller", "name": "seller", "type": "group", "sub_fields": []any{
				map[string]any{"key": "field_seller_name", "label": "Name", "name": "name", "type": "text"},
				map[string]any{"key": "field_seller_mail", "label": "Email", "name": "email", "type": "email"},
			}},
		},
	}
}

func acfgoActivate(t *testing.T, c *http.Client, base string) {
	t.Helper()
	_, body, _ := get(t, c, base+"/wp-admin/plugins")
	m := regexp.MustCompile(`href="(/wp-admin/plugins\?action=activate&amp;plugin=acf-go[^"]*)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no Activate link for Pursae Custom Fields")
	}
	if code, _, loc := get(t, c, base+html.UnescapeString(m[1])); code != http.StatusFound || !strings.Contains(loc, "activate=true") {
		t.Fatalf("activate Pursae Custom Fields: %d %s", code, loc)
	}
}

// acfgoSaveGroupForm saves a field group through the field group editor's form.
func acfgoSaveGroupForm(t *testing.T, c *http.Client, base string, id int64, g map[string]any) int64 {
	t.Helper()
	path := "/wp-admin/acf-field-group"
	if id != 0 {
		path += "?post=" + strconv.FormatInt(id, 10)
	}
	_, body, _ := get(t, c, base+path)
	raw, _ := json.Marshal(g)
	code, _, loc := post(t, c, base+"/wp-admin/acf-field-group", url.Values{"_wpnonce": {formNonce(t, body)}, "post_ID": {strconv.FormatInt(id, 10)},
		"post_title": {g["title"].(string)}, "acfgo_group": {string(raw)}})
	if code != http.StatusFound {
		t.Fatalf("save field group: %d", code)
	}
	m := regexp.MustCompile(`post=(\d+)`).FindStringSubmatch(loc)
	if m == nil {
		t.Fatalf("save field group redirect: %s", loc)
	}
	n, _ := strconv.ParseInt(m[1], 10, 64)
	return n
}

func TestACFGoPlugin(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	ctx := context.Background()

	// Off by default: listed as an inactive native plugin, no screens, menu, REST field or output.
	if srv.acfgoOn() {
		t.Fatal("Pursae Custom Fields is on after a fresh install")
	}
	_, body, _ := get(t, c, base+"/wp-admin/plugins")
	if row := regexp.MustCompile(`(?s)<tr class="(\w+)" data-slug="acf-go"`).FindStringSubmatch(body); row == nil || row[1] != "inactive" {
		t.Fatalf("Pursae Custom Fields not listed as inactive: %v", row)
	}
	if code, _, _ := get(t, c, base+"/wp-admin/acf-field-groups"); code != http.StatusNotFound {
		t.Errorf("field groups screen while off: %d", code)
	}
	_, body, _ = get(t, c, base+"/wp-admin/")
	if strings.Contains(body, "acf-field-groups") {
		t.Error("ACF menu shown while the plugin is off")
	}

	acfgoActivate(t, c, base)
	if !srv.acfgoOn() {
		t.Fatal("activation did not switch Pursae Custom Fields on")
	}
	_, body, _ = get(t, c, base+"/wp-admin/")
	if !strings.Contains(body, `href="/wp-admin/acf-field-groups"`) {
		t.Error("ACF menu missing after activation")
	}

	// Field group CRUD.
	gid := acfgoSaveGroupForm(t, c, base, 0, acfgoTestGroup())
	g := srv.acfgoGroupByID(ctx, gid)
	if g == nil || g.Title != "Product Details" || !strings.HasPrefix(g.Key, "group_") || len(g.Fields) != 8 || !g.Active {
		t.Fatalf("saved group: %+v", g)
	}
	code, body, _ := get(t, c, base+"/wp-admin/acf-field-groups")
	if code != http.StatusOK || !strings.Contains(body, "Product Details") || !strings.Contains(body, "Post Type is equal to Post") {
		t.Fatalf("field group list: %d", code)
	}
	if code, body, _ := get(t, c, base+"/wp-admin/acf-field-group?post="+itoa64(gid)); code != http.StatusOK || !strings.Contains(body, "acfgo-schema") || strings.Contains(body, "Template error") {
		t.Fatalf("field group editor: %d", code)
	}
	dup := regexp.MustCompile(`href="(/wp-admin/acf-field-groups\?action=duplicate[^"]*)"`).FindStringSubmatch(body)
	if dup == nil {
		t.Fatal("no Duplicate link")
	}
	get(t, c, base+html.UnescapeString(dup[1]))
	var copyGroup *acfGroup
	for _, x := range srv.acfgoAllGroups(ctx, false) {
		if x.Title == "Product Details (copy)" {
			copyGroup = x
		}
	}
	if copyGroup == nil || copyGroup.Key == g.Key || acfgoStr(copyGroup.Fields[0]["key"]) == "field_subtitle" {
		t.Fatalf("duplicate: %+v", copyGroup)
	}
	_ = srv.acfgoSetGroupStatus(ctx, copyGroup.ID, "trash")
	if x := srv.acfgoGroupByID(ctx, copyGroup.ID); x == nil || x.Status != "trash" {
		t.Fatal("trash failed")
	}
	_ = srv.acfgoSetGroupStatus(ctx, copyGroup.ID, "untrash")
	if x := srv.acfgoGroupByID(ctx, copyGroup.ID); x == nil || x.Status != "publish" {
		t.Fatalf("restore failed: %+v", x)
	}
	_ = srv.acfgoDeleteGroup(ctx, copyGroup.ID)
	if srv.acfgoGroupByID(ctx, copyGroup.ID) != nil {
		t.Fatal("delete failed")
	}

	// Export, then import into a fresh group key space (import updates by key).
	_, body, _ = get(t, c, base+"/wp-admin/acf-tools")
	code, body, _ = post(t, c, base+"/wp-admin/acf-tools", url.Values{"_wpnonce": {formNonce(t, body)}, "tool": {"export"}, "action": {"generate"}, "keys[]": {g.Key}})
	if code != http.StatusOK || !strings.Contains(body, "field_subtitle") || !strings.Contains(body, "flexible_content") {
		t.Fatalf("export: %d", code)
	}
	exported := []map[string]any{g.toMap()}
	exported[0]["key"], exported[0]["title"] = "group_imported1", "Imported"
	raw, _ := json.Marshal(exported)
	if n, err := srv.acfgoImport(ctx, raw); err != nil || n != 1 || srv.acfgoGroupByKey(ctx, "group_imported1") == nil {
		t.Fatalf("import: %d %v", n, err)
	}
	if imp := srv.acfgoGroupByKey(ctx, "group_imported1"); imp != nil {
		_ = srv.acfgoDeleteGroup(ctx, imp.ID)
	}
	// Import through the Tools form (multipart upload).
	_, body, _ = get(t, c, base+"/wp-admin/acf-tools")
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("_wpnonce", formNonce(t, body))
	_ = mw.WriteField("tool", "import")
	fw, _ := mw.CreateFormFile("acf_import_file", "acf.json")
	_, _ = fw.Write([]byte(`{"key":"group_upload1","title":"Uploaded","fields":[{"key":"field_up","name":"up","label":"Up","type":"text"}],"location":[[{"param":"post_type","operator":"==","value":"page"}]]}`))
	mw.Close()
	req, _ := http.NewRequest(http.MethodPost, base+"/wp-admin/acf-tools", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || srv.acfgoGroupByKey(ctx, "group_upload1") == nil {
		t.Fatalf("import upload: %d", resp.StatusCode)
	}

	// Values through the classic edit form: the boxes render, the middleware saves acf[...].
	if err := srv.settings.Set(ctx, map[string]any{"classic-editor-replace": "classic"}); err != nil {
		t.Fatal(err)
	}
	p := &store.Post{Type: "post", Title: "Widget", Status: "publish", AuthorID: 1, CommentStatus: "open", PingStatus: "open", Meta: map[string]any{}}
	if err := srv.store.InsertPost(ctx, p); err != nil {
		t.Fatal(err)
	}
	pid := itoa64(p.ID)
	code, body, _ = get(t, c, base+"/wp-admin/post?post="+pid+"&action=edit&classic-editor")
	if code != http.StatusOK || !strings.Contains(body, `id="acfgo-boxes"`) || !strings.Contains(body, `name="acf[field_subtitle]"`) ||
		!strings.Contains(body, `acfgo-input.js`) || !strings.Contains(body, `template class="acf-clone-tpl"`) {
		t.Fatalf("edit screen boxes: %d", code)
	}
	form := url.Values{"post_ID": {pid}, "post_title": {"Widget"}, "content": {"Body"}, "_wpnonce": {acfgoFormNonce(body)},
		"publish": {"Update"}, "post_status": {"publish"}, "visibility": {"public"},
		"acf[field_subtitle]":                                       {"Best widget"},
		"acf[field_price]":                                          {"19.5"},
		"acf[field_featured]":                                       {"0", "1"},
		"acf[field_colors]":                                         {""},
		"acf[field_colors][]":                                       {"blue", "red"},
		"acf[field_launch]":                                         {"2026-11-05"},
		"acf[field_specs]":                                          {""},
		"acf[field_specs][row-0][_order]":                           {"0"},
		"acf[field_specs][row-0][field_spec_label]":                 {"Weight"},
		"acf[field_specs][row-0][field_spec_value]":                 {"3"},
		"acf[field_specs][rx][_order]":                              {"1"},
		"acf[field_specs][rx][field_spec_label]":                    {"Height"},
		"acf[field_specs][rx][field_spec_value]":                    {"10"},
		"acf[field_sections]":                                       {""},
		"acf[field_sections][a][_order]":                            {"0"},
		"acf[field_sections][a][acf_fc_layout]":                     {"quote"},
		"acf[field_sections][a][field_quote_text]":                  {"Line one\nLine two"},
		"acf[field_sections][a][field_quote_items][i1][field_item]": {"x"},
		"acf[field_sections][a][field_quote_items][i2][field_item]": {"y"},
		"acf[field_sections][b][_order]":                            {"1"},
		"acf[field_sections][b][acf_fc_layout]":                     {"hero"},
		"acf[field_sections][b][field_hero_title]":                  {"Welcome"},
		"acf[field_seller][field_seller_name]":                      {"Gopher"},
		"acf[field_seller][field_seller_mail]":                      {"g@example.test"},
	}
	if code, _, loc := post(t, c, base+"/wp-admin/post", form); code != http.StatusFound || !strings.Contains(loc, "message=") {
		t.Fatalf("classic save: %d %s", code, loc)
	}
	saved, _ := srv.store.GetPost(ctx, p.ID)
	meta := saved.Meta
	want := map[string]any{"subtitle": "Best widget", "_subtitle": "field_subtitle", "price": "19.5", "featured": float64(1), "launch": "20261105",
		"specs": float64(2), "_specs": "field_specs", "specs_0_label": "Weight", "_specs_0_label": "field_spec_label", "specs_1_value": "10",
		"sections_0_text": "Line one\nLine two", "sections_0_items": float64(2), "sections_0_items_1_item": "y", "_sections_0_items_1_item": "field_item",
		"sections_1_title": "Welcome", "seller_name": "Gopher", "_seller_name": "field_seller_name", "post_title_check": nil}
	delete(want, "post_title_check")
	for k, v := range want {
		if got := meta[k]; !jsonEqual(got, v) {
			t.Errorf("meta %s = %#v, want %#v", k, got, v)
		}
	}
	if l, ok := meta["sections"].([]any); !ok || len(l) != 2 || l[0] != "quote" || l[1] != "hero" {
		t.Errorf("flexible content layouts: %#v", meta["sections"])
	}
	if saved.Title != "Widget" || saved.Content != "Body" {
		t.Errorf("core save lost: %q %q", saved.Title, saved.Content)
	}

	// get_field and friends.
	if v := srv.acfGetField(ctx, "subtitle", p.ID, true); v != "Best widget" {
		t.Errorf("get_field subtitle = %#v", v)
	}
	if v := srv.acfGetField(ctx, "price", p.ID, true); v != 19.5 {
		t.Errorf("get_field price = %#v", v)
	}
	if v := srv.acfGetField(ctx, "featured", p.ID, true); v != true {
		t.Errorf("get_field featured = %#v", v)
	}
	if v, _ := srv.acfGetField(ctx, "colors", p.ID, true).([]any); len(v) != 2 || v[0] != "Blue" {
		t.Errorf("get_field colors (labels) = %#v", v)
	}
	if v := srv.acfGetField(ctx, "launch", p.ID, true); v != "November 5, 2026" {
		t.Errorf("get_field launch = %#v", v)
	}
	specs, _ := srv.acfGetField(ctx, "specs", p.ID, true).([]any)
	if len(specs) != 2 || specs[1].(map[string]any)["label"] != "Height" || specs[0].(map[string]any)["value"] != int64(3) {
		t.Errorf("get_field specs = %#v", specs)
	}
	sections, _ := srv.acfGetField(ctx, "sections", p.ID, true).([]any)
	if len(sections) != 2 || sections[0].(map[string]any)["acf_fc_layout"] != "quote" || len(sections[0].(map[string]any)["items"].([]any)) != 2 {
		t.Errorf("get_field sections = %#v", sections)
	}
	if seller, _ := srv.acfGetField(ctx, "seller", p.ID, true).(map[string]any); seller["email"] != "g@example.test" {
		t.Errorf("get_field seller = %#v", seller)
	}
	if fo := srv.acfGetFieldObject(ctx, "subtitle", p.ID, true); fo["label"] != "Subtitle" || fo["value"] != "Best widget" {
		t.Errorf("get_field_object = %#v", fo)
	}
	if all := srv.acfGetFields(ctx, p.ID, true); len(all) < 8 || all["subtitle"] != "Best widget" {
		t.Errorf("get_fields = %#v", all)
	}
	if v := srv.acfGetField(ctx, "specs_1_label", p.ID, true); v != "Height" {
		t.Errorf("get_field by sub field meta name = %#v", v)
	}

	// Template functions, as a theme uses them.
	tpl := template.Must(template.New("t").Funcs(srv.acfgoTemplateFuncs()).Parse(
		`{{the_field "subtitle" .}}|{{range have_rows "specs" .}}{{get_row_index .}}:{{get_sub_field "label" .}}={{get_sub_field "value" .}};{{end}}|` +
			`{{range have_rows "sections" .}}[{{get_row_layout .}}{{range have_rows "items" .}} {{get_sub_field "item" .}}{{end}}]{{end}}|{{the_field "colors" .}}|{{if get_field "featured" .}}featured{{end}}`))
	var out bytes.Buffer
	if err := tpl.Execute(&out, saved); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "Best widget|1:Weight=3;2:Height=10;|[quote x y][hero]|Blue, Red|featured" {
		t.Errorf("template = %q", got)
	}

	// Fewer rows: the old row's meta is deleted.
	if err := srv.acfUpdateField(ctx, "specs", []any{map[string]any{"label": "Only", "value": "1"}}, p.ID); err != nil {
		t.Fatal(err)
	}
	saved, _ = srv.store.GetPost(ctx, p.ID)
	if saved.Meta["specs"] != float64(1) || saved.Meta["specs_0_label"] != "Only" {
		t.Errorf("update_field specs: %v %v", saved.Meta["specs"], saved.Meta["specs_0_label"])
	}
	if _, ok := saved.Meta["specs_1_label"]; ok {
		t.Error("removed repeater row's meta remains")
	}
	if err := srv.acfDeleteField(ctx, "subtitle", p.ID); err != nil {
		t.Fatal(err)
	}
	if v := srv.acfGetField(ctx, "subtitle", p.ID, true); v != nil {
		t.Errorf("delete_field left %v", v)
	}

	// Validation (acf/validate_save_post).
	errs := srv.acfgoValidate(ctx, map[string]any{"field_subtitle": "", "field_price": "-4", "field_seller": map[string]any{"field_seller_mail": "nope"}})
	if len(errs) != 3 {
		t.Errorf("validation errors: %+v", errs)
	}
	if errs := srv.acfgoValidate(ctx, map[string]any{"field_subtitle": strings.Repeat("x", 41)}); len(errs) != 1 || !strings.Contains(errs[0].Message, "40 characters") {
		t.Errorf("maxlength: %+v", errs)
	}

	// REST: the acf field (light and standard formats), writes and validation.
	code, body = restGet(t, c, base, "/wp-json/wp/v2/posts/"+pid)
	var rp map[string]any
	_ = json.Unmarshal([]byte(body), &rp)
	acf, _ := rp["acf"].(map[string]any)
	if code != http.StatusOK || acf == nil || acf["price"] != 19.5 || acf["featured"] != true || acf["seller"].(map[string]any)["name"] != "Gopher" {
		t.Fatalf("REST acf: %d %v", code, rp["acf"])
	}
	_, body = restGet(t, c, base, "/wp-json/wp/v2/posts/"+pid+"?acf_format=standard")
	_ = json.Unmarshal([]byte(body), &rp)
	if rp["acf"].(map[string]any)["launch"] != "November 5, 2026" {
		t.Errorf("REST standard format: %v", rp["acf"])
	}
	code, body = restSend(t, c, base, http.MethodPost, "/wp-json/wp/v2/posts/"+pid, `{"acf":{"subtitle":"From REST","specs":[{"label":"A","value":1},{"label":"B","value":2}]}}`)
	if code != http.StatusOK {
		t.Fatalf("REST write: %d %s", code, body)
	}
	if v := srv.acfGetField(ctx, "subtitle", p.ID, true); v != "From REST" {
		t.Errorf("REST write subtitle = %v", v)
	}
	if v, _ := srv.acfGetField(ctx, "specs", p.ID, true).([]any); len(v) != 2 {
		t.Errorf("REST write specs = %v", v)
	}
	if code, body := restSend(t, c, base, http.MethodPost, "/wp-json/wp/v2/posts/"+pid, `{"acf":{"subtitle":""}}`); code != http.StatusBadRequest || !strings.Contains(body, "required") {
		t.Errorf("REST required: %d %s", code, body)
	}
	if code, _ := restSend(t, c, base, http.MethodPost, "/wp-json/wp/v2/posts/"+pid, `{"acf":{"nope":"x"}}`); code != http.StatusBadRequest {
		t.Errorf("REST unknown field: %d", code)
	}

	// Ajax validation endpoint.
	_, body, _ = get(t, c, base+"/wp-admin/post?post="+pid+"&action=edit&classic-editor")
	vn := regexp.MustCompile(`data-validate="([^"]+)"`).FindStringSubmatch(body)
	if vn == nil {
		t.Fatal("no validation nonce")
	}
	_, body, _ = post(t, c, base+"/wp-admin/admin-ajax", url.Values{"action": {"acfgo/validate_save_post"}, "_acfnonce": {vn[1]}, "acf[field_subtitle]": {""}})
	if !strings.Contains(body, `"valid":false`) || !strings.Contains(body, "acf[field_subtitle]") {
		t.Errorf("ajax validation: %s", body)
	}

	// Options page: created in the UI, a field group located on it, values saved as options_*.
	_, body, _ = get(t, c, base+"/wp-admin/acf-options-page")
	if code, _, _ := post(t, c, base+"/wp-admin/acf-options-page", url.Values{"_wpnonce": {formNonce(t, body)}, "page_title": {"Site Settings"}, "active": {"1"}}); code != http.StatusFound {
		t.Fatalf("create options page: %d", code)
	}
	if op := srv.acfgoOptionsPage(ctx, "site-settings"); op == nil || op.PostID != "options" {
		t.Fatalf("options page: %+v", op)
	}
	acfgoSaveGroupForm(t, c, base, 0, map[string]any{"title": "Contact", "location": []any{[]any{map[string]any{"param": "options_page", "operator": "==", "value": "site-settings"}}},
		"fields": []any{map[string]any{"key": "field_phone", "label": "Phone", "name": "phone", "type": "text"}}})
	code, body, _ = get(t, c, base+"/wp-admin/admin?page=site-settings")
	if code != http.StatusOK || !strings.Contains(body, `name="acf[field_phone]"`) {
		t.Fatalf("options page screen: %d", code)
	}
	_, body, _ = get(t, c, base+"/wp-admin/")
	if !strings.Contains(body, `admin?page=site-settings`) {
		t.Error("options page missing from the menu")
	}
	_, body, _ = get(t, c, base+"/wp-admin/admin?page=site-settings")
	if code, _, _ := post(t, c, base+"/wp-admin/admin?page=site-settings", url.Values{"_wpnonce": {formNonce(t, body)}, "acf[field_phone]": {"+90 555"}}); code != http.StatusFound {
		t.Fatalf("options page save: %d", code)
	}
	if srv.settings.String("options_phone") != "+90 555" || srv.settings.String("_options_phone") != "field_phone" {
		t.Errorf("options storage: %q %q", srv.settings.String("options_phone"), srv.settings.String("_options_phone"))
	}
	if v := srv.acfGetField(ctx, "phone", "option", true); v != "+90 555" {
		t.Errorf("get_field option = %v", v)
	}

	// Term and user fields through the core's forms.
	acfgoSaveGroupForm(t, c, base, 0, map[string]any{"title": "Term & User", "show_in_rest": 1, "location": []any{
		[]any{map[string]any{"param": "taxonomy", "operator": "==", "value": "category"}},
		[]any{map[string]any{"param": "user_form", "operator": "==", "value": "all"}}},
		"fields": []any{map[string]any{"key": "field_color", "label": "Color", "name": "color", "type": "color_picker"}}})
	term, _ := srv.store.TermBySlug(ctx, "category", "uncategorized")
	code, body, _ = get(t, c, base+"/wp-admin/term?taxonomy=category&tag_ID="+itoa64(term.ID))
	if code != http.StatusOK || !strings.Contains(body, `name="acf[field_color]"`) {
		t.Fatalf("term screen: %d", code)
	}
	post(t, c, base+"/wp-admin/term", url.Values{"_wpnonce": {acfgoFormNonce(body)}, "tag_ID": {itoa64(term.ID)}, "taxonomy": {"category"},
		"name": {term.Name}, "slug": {term.Slug}, "description": {""}, "acf[field_color]": {"#ff0000"}})
	if v := srv.acfGetField(ctx, "color", "term_"+itoa64(term.ID), true); v != "#ff0000" {
		t.Errorf("term value = %v", v)
	}
	_, body = restGet(t, c, base, "/wp-json/wp/v2/categories/"+itoa64(term.ID))
	if !strings.Contains(body, `"acf":{"color":"#ff0000"}`) {
		t.Errorf("term REST: %s", body)
	}
	// Add New Term: a refused add (the name exists) leaves the existing term's fields alone; a real add saves them.
	_, body, _ = get(t, c, base+"/wp-admin/edit-tags?taxonomy=category")
	addNonce := regexp.MustCompile(`id="addtag"[\s\S]*?name="_wpnonce" value="([^"]+)"`).FindStringSubmatch(body)
	if addNonce == nil {
		t.Fatal("no add-tag nonce")
	}
	post(t, c, base+"/wp-admin/edit-tags", url.Values{"_wpnonce": {addNonce[1]}, "action": {"add-tag"}, "taxonomy": {"category"},
		"tag-name": {term.Name}, "acf[field_color]": {"#00ff00"}})
	if v := srv.acfGetField(ctx, "color", "term_"+itoa64(term.ID), true); v != "#ff0000" {
		t.Errorf("a refused Add New Term overwrote the existing term's field: %v", v)
	}
	post(t, c, base+"/wp-admin/edit-tags", url.Values{"_wpnonce": {addNonce[1]}, "action": {"add-tag"}, "taxonomy": {"category"},
		"tag-name": {"Fresh Term"}, "acf[field_color]": {"#0000ff"}})
	if fresh, err := srv.store.TermByName(ctx, "category", "Fresh Term"); err != nil {
		t.Errorf("new term: %v", err)
	} else if v := srv.acfGetField(ctx, "color", "term_"+itoa64(fresh.ID), true); v != "#0000ff" {
		t.Errorf("new term value = %v", v)
	}
	code, body, _ = get(t, c, base+"/wp-admin/profile")
	if code != http.StatusOK || !strings.Contains(body, `name="acf[field_color]"`) {
		t.Fatalf("profile screen: %d", code)
	}

	// Theme output: get_field in a site template.
	if v := srv.acfGetField(ctx, "color", "user_1", true); v != nil {
		t.Errorf("user value before save = %v", v)
	}

	// Deactivate: the values stay, nothing shows.
	_, body, _ = get(t, c, base+"/wp-admin/plugins")
	m := regexp.MustCompile(`href="(/wp-admin/plugins\?action=deactivate&amp;plugin=acf-go[^"]*)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no Deactivate link")
	}
	get(t, c, base+html.UnescapeString(m[1]))
	if srv.acfgoOn() {
		t.Fatal("still on")
	}
	_, body = restGet(t, c, base, "/wp-json/wp/v2/posts/"+pid)
	if strings.Contains(body, `"acf"`) {
		t.Error("REST acf field while off")
	}
	if code, body, _ := get(t, c, base+"/wp-admin/post?post="+pid+"&action=edit&classic-editor"); code != http.StatusOK || strings.Contains(body, "acfgo-boxes") {
		t.Error("boxes while off")
	}
	saved, _ = srv.store.GetPost(ctx, p.ID)
	if saved.Meta["specs"] == nil {
		t.Error("values lost on deactivation")
	}
	if _, body, _ := get(t, c, base+"/wp-admin/admin?page=site-settings"); strings.Contains(body, "acf[field_phone]") {
		t.Error("options page while off")
	}
}

// TestACFGoPostTypes covers ACF › Post Types / Taxonomies: registration, menu, list screens.
func TestACFGoPostTypes(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	acfgoActivate(t, c, base)
	ctx := context.Background()

	_, body, _ := get(t, c, base+"/wp-admin/acf-post-type")
	if code, _, loc := post(t, c, base+"/wp-admin/acf-post-type", url.Values{"_wpnonce": {formNonce(t, body)}, "plural_label": {"Books"}, "singular_label": {"Book"},
		"slug": {"book"}, "active": {"1"}, "public": {"1"}, "show_in_rest": {"1"}, "supports[]": {"title", "editor"}}); code != http.StatusFound {
		t.Fatalf("create post type: %d %s", code, loc)
	}
	_, body, _ = get(t, c, base+"/wp-admin/acf-taxonomy")
	if code, _, _ := post(t, c, base+"/wp-admin/acf-taxonomy", url.Values{"_wpnonce": {formNonce(t, body)}, "plural_label": {"Genres"}, "singular_label": {"Genre"},
		"slug": {"genre"}, "active": {"1"}, "public": {"1"}, "hierarchical": {"1"}, "object_type[]": {"book"}}); code != http.StatusFound {
		t.Fatalf("create taxonomy: %d", code)
	}
	var exists bool
	_ = srv.store.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM content_types WHERE slug = 'book') AND EXISTS (SELECT 1 FROM taxonomies WHERE slug = 'genre')`).Scan(&exists)
	if !exists {
		t.Fatal("post type / taxonomy not registered")
	}
	_, body, _ = get(t, c, base+"/wp-admin/")
	if !strings.Contains(body, `href="/wp-admin/edit?post_type=book"`) || !strings.Contains(body, "edit-tags?taxonomy=genre") {
		t.Error("post type menu missing")
	}
	if code, body, _ := get(t, c, base+"/wp-admin/edit?post_type=book"); code != http.StatusOK || !strings.Contains(body, "Books") {
		t.Errorf("book list: %d", code)
	}
	if code, body, _ := get(t, c, base+"/wp-admin/edit-tags?taxonomy=genre&post_type=book"); code != http.StatusOK || !strings.Contains(body, "Genres") {
		t.Errorf("genre terms screen: %d", code)
	}
	if code, body, _ := get(t, c, base+"/wp-admin/post-new?post_type=book"); code != http.StatusOK || strings.Contains(body, "Template error") {
		t.Errorf("new book: %d", code)
	}
	if code, body, _ := get(t, c, base+"/wp-admin/acf-post-types"); code != http.StatusOK || !strings.Contains(body, "<code>book</code>") {
		t.Errorf("post types list: %d", code)
	}
	// Reserved keys are refused.
	_, body, _ = get(t, c, base+"/wp-admin/acf-post-type")
	if _, body, _ := post(t, c, base+"/wp-admin/acf-post-type", url.Values{"_wpnonce": {formNonce(t, body)}, "plural_label": {"Pages"}, "singular_label": {"Page"}, "slug": {"page"}}); !strings.Contains(body, "reserved") {
		t.Error("reserved key accepted")
	}
	// An import cannot take over a reserved type either (wb_template stays the Theme Builder's).
	if _, err := srv.acfgoImport(ctx, []byte(`[{"key":"post_type_wbt","title":"Hijack","post_type":"wb_template","active":true,"public":true,
		"labels":{"name":"Hijack","singular_name":"Hijack"}}]`)); err != nil {
		t.Fatal(err)
	}
	var name string
	var public bool
	if err := srv.store.Pool.QueryRow(ctx, `SELECT name, public FROM content_types WHERE slug = 'wb_template'`).Scan(&name, &public); err != nil || public || name != "Templates" {
		t.Errorf("wb_template after import: %q public=%v err=%v", name, public, err)
	}
	if _, ok := acfgoTypeLabels("wb_template"); ok {
		t.Error("wb_template registered as an ACF post type")
	}
}

// TestACFGoBlocks covers block.json registration, the editor ajax and front-end rendering.
func TestACFGoBlocks(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	acfgoActivate(t, c, base)
	ctx := context.Background()
	dir := srv.acfgoBlockDirs()[0] + "/testimonial"
	if err := mkdirWrite(dir+"/block.json", `{"name":"acf/testimonial","title":"Testimonial","category":"text","acf":{"mode":"preview","renderTemplate":"render.html"}}`); err != nil {
		t.Fatal(err)
	}
	if err := mkdirWrite(dir+"/render.html", `<blockquote class="testimonial">{{get_field "quote" .Obj}} — {{.Fields.author}}</blockquote>`); err != nil {
		t.Fatal(err)
	}
	acfgoSaveGroupForm(t, c, base, 0, map[string]any{"title": "Testimonial", "location": []any{[]any{map[string]any{"param": "block", "operator": "==", "value": "acf/testimonial"}}},
		"fields": []any{map[string]any{"key": "field_quote", "label": "Quote", "name": "quote", "type": "textarea"}, map[string]any{"key": "field_author", "label": "Author", "name": "author", "type": "text"}}})
	if b := srv.acfgoBlock("acf/testimonial"); b == nil || b.tmpl == nil {
		t.Fatal("block not loaded")
	}
	// The editor's preview ajax turns the form into the block's data.
	_, body, _ := get(t, c, base+"/wp-admin/post-new")
	bn := regexp.MustCompile(`"nonce":"([^"]+)"`).FindStringSubmatch(body)
	if bn == nil || !strings.Contains(body, "acfgo-blocks.js") {
		t.Fatal("blocks not registered in the block editor")
	}
	_, body, _ = post(t, c, base+"/wp-admin/admin-ajax", url.Values{"action": {"acfgo/block_preview"}, "nonce": {bn[1]}, "name": {"acf/testimonial"}, "data": {"{}"},
		"acf[field_quote]": {"Great"}, "acf[field_author]": {"Ada"}})
	var r struct {
		Data struct {
			Data map[string]any `json:"data"`
			HTML string         `json:"html"`
		} `json:"data"`
	}
	_ = json.Unmarshal([]byte(body), &r)
	if r.Data.Data["quote"] != "Great" || r.Data.Data["_author"] != "field_author" || !strings.Contains(r.Data.HTML, "Great — Ada") {
		t.Fatalf("block preview: %s", body)
	}
	_, body, _ = post(t, c, base+"/wp-admin/admin-ajax", url.Values{"action": {"acfgo/block_form"}, "nonce": {bn[1]}, "name": {"acf/testimonial"}, "data": {`{"quote":"Great","_quote":"field_quote"}`}})
	if !strings.Contains(body, `acf[field_quote]`) || !strings.Contains(body, "Great") {
		t.Errorf("block form: %s", body)
	}
	// Front end: the block in post content renders through its template.
	data, _ := json.Marshal(r.Data.Data)
	p := &store.Post{Type: "post", Title: "With block", Slug: "with-block", Status: "publish", AuthorID: 1, CommentStatus: "open", PingStatus: "open", Meta: map[string]any{},
		Content: `<!-- wp:acf/testimonial {"name":"acf/testimonial","data":` + string(data) + `,"mode":"preview"} /-->`}
	if err := srv.store.InsertPost(ctx, p); err != nil {
		t.Fatal(err)
	}
	_, _, loc := get(t, c, base+"/?p="+itoa64(p.ID))
	_, body, _ = get(t, c, loc)
	if !strings.Contains(body, `<blockquote class="testimonial">Great — Ada</blockquote>`) {
		t.Errorf("front-end block render missing")
	}
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// acfgoFormNonce returns the _wpnonce of the edit form (#post or #edittag) of a screen.
func acfgoFormNonce(body string) string {
	if m := regexp.MustCompile(`id="post"[^>]*>[\s\S]*?name="_wpnonce" value="([^"]+)"`).FindStringSubmatch(body); m != nil {
		return m[1]
	}
	if m := regexp.MustCompile(`id="edittag"[^>]*>[\s\S]*?name="_wpnonce" value="([^"]+)"`).FindStringSubmatch(body); m != nil {
		return m[1]
	}
	if m := nonceRe.FindStringSubmatch(body); m != nil {
		return m[1]
	}
	return ""
}

func mkdirWrite(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}
