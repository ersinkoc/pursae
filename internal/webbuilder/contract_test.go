package webbuilder

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// A doc in the upstream shape decodes into the Go mirror and encodes back without losing the
// fields the mirror knows, including explicit zeros and false.
func TestDocRoundTrip(t *testing.T) {
	in := `{"sections":[{"id":"s1","kind":"hero","name":"Hero","hidden":false,
		"content":{"title":"Hi","variant":"split"},
		"items":[{"id":"i1","title":"A","text":"B","href":"#x","lay":{"gap":0,"wrap":false},
			"sub":{"id":"s2","kind":"layout","name":"L","hidden":true,"content":{},"items":[],
				"style":{"palette":"dark","align":"left","padding":0,"titleSize":32,"hideOn":["mobile"]}}}],
		"style":{"palette":"brand","align":"center","padding":96,"titleSize":56,"hideOn":[],
			"motion":{"entrance":"fade-up","delay":0,"repeat":false}},
		"els":{"title":{"mt":0,"color":"#fff","iconColors":{"#000000":"#ff0000"},"removed":true}},
		"grid":{"cols":3,"border":false},
		"bg":{"type":"gradient","decorX":0}}],
		"site":{"brand":"#2271b1","fontPair":"modern","corners":"soft","headingWeight":700}}`
	var d DocState
	if err := json.Unmarshal([]byte(in), &d); err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var a, b any
	_ = json.Unmarshal([]byte(in), &a)
	_ = json.Unmarshal(out, &b)
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if string(ja) != string(jb) {
		t.Fatalf("round trip changed the doc\n in: %s\nout: %s", ja, jb)
	}
}

// The AI-off boot block has exactly the documented shape.
func TestBootAIOff(t *testing.T) {
	j, _ := json.Marshal(BootAI{Providers: []AIProviderInfo{}})
	if got, want := string(j), `{"enabled":false,"providers":[],"llmBase":"","imagesUrl":"","allowCode":false}`; got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

// Library ids are strings on the wire, like upstream SavedComponent.id.
func TestLibraryItemFlat(t *testing.T) {
	j, _ := json.Marshal(LibraryItem{SavedComponent: SavedComponent{ID: "42", Name: "x"}})
	if !strings.HasPrefix(string(j), `{"id":"42","name":"x","section":`) {
		t.Fatalf("got %s", j)
	}
}

// Constants shared with builder/host/contract.ts must agree.
func TestTSContractInSync(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "builder", "host", "contract.ts"))
	if err != nil {
		t.Skip("builder/host/contract.ts not found:", err)
	}
	ts := string(src)
	want := map[string]string{
		`API_VERSION = (\d+)`:                 strconv.Itoa(APIVersion),
		`RENDERER_VERSION = (\d+)`:            strconv.Itoa(RendererVersion),
		`API_HEADER = '([^']+)'`:              APIHeader,
		`UPSTREAM_TYPES_SHA = '([^']+)'`:      UpstreamTypesSHA,
		`REST_NAMESPACE = '([^']+)'`:          RESTNamespace,
		`EDITOR_ASSETS_PATH = '([^']+)'`:      EditorAssetsPath,
		`EDITOR_NOAI_ASSETS_PATH = '([^']+)'`: EditorNoAIAssetsPath,
		`SITE_ASSETS_PATH = '([^']+)'`:        SiteAssetsPath,
		`FORM_HONEYPOT = '([^']+)'`:           FormHoneypot,
		`FORM_TIMING = '([^']+)'`:             FormTiming,
	}
	for re, v := range want {
		m := regexp.MustCompile(re).FindStringSubmatch(ts)
		if m == nil || m[1] != v {
			t.Errorf("contract.ts %s: got %v, Go has %q", re, m, v)
		}
	}
	for _, f := range []Feature{FeatureMenu, FeatureAcc, FeatureTabs, FeatureDismiss, FeatureForm, FeatureMotion, FeatureEmbed} {
		if !strings.Contains(ts, "'"+string(f)+"'") {
			t.Errorf("contract.ts lacks feature %q", f)
		}
	}
	for _, code := range []string{ErrAPIMismatch, ErrRevConflict, ErrDetached, ErrLocked, ErrTooLarge, ErrInvalidDoc, ErrFormInvalid, ErrRateLimited, ErrAIBudget, ErrAIPath, ErrAIModel, ErrAINoKey, ErrAIUpstream, ErrAITimeout} {
		if !strings.Contains(ts, "'"+code+"'") {
			t.Errorf("contract.ts lacks error code %q", code)
		}
	}
	for p, paths := range AIProviderPaths {
		for _, path := range paths {
			if !strings.Contains(ts, string(p)+": ['"+path+"']") {
				t.Errorf("contract.ts AI_PROVIDER_PATHS lacks %s: %s", p, path)
			}
		}
	}
}

// ---------------------------------------------------------------------------------------------
// Theme Builder contract (§11).

func readTSContract(t *testing.T) string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("..", "..", "builder", "host", "contract.ts"))
	if err != nil {
		t.Skip("builder/host/contract.ts not found:", err)
	}
	return string(src)
}

// tsList returns the quoted strings of `export const NAME = [ ... ]`.
func tsList(t *testing.T, ts, name string) []string {
	t.Helper()
	m := regexp.MustCompile(`(?s)export const ` + name + ` = \[(.*?)\]`).FindStringSubmatch(ts)
	if m == nil {
		t.Fatalf("contract.ts has no list %s", name)
	}
	var out []string
	for _, q := range regexp.MustCompile(`'([^']*)'`).FindAllStringSubmatch(m[1], -1) {
		out = append(out, q[1])
	}
	return out
}

// tsObject returns the 'key': 'value' / key: 'value' pairs of `export const NAME[: T] = { ... }`.
func tsObject(t *testing.T, ts, name string) map[string]string {
	t.Helper()
	m := regexp.MustCompile(`(?s)export const ` + name + `(?:: [^=]+)? = \{(.*?)\n\}`).FindStringSubmatch(ts)
	if m == nil {
		t.Fatalf("contract.ts has no object %s", name)
	}
	out := map[string]string{}
	for _, kv := range regexp.MustCompile(`'?([A-Za-z0-9._-]+)'?: '([^']*)'`).FindAllStringSubmatch(m[1], -1) {
		out[kv[1]] = kv[2]
	}
	return out
}

func TestTSThemeBuilderInSync(t *testing.T) {
	ts := readTSContract(t)

	consts := map[string]string{
		"TEMPLATE_POST_TYPE":    TemplatePostType,
		"TEMPLATE_CAP":          TemplateCap,
		"HELLO_PURSAE_THEME":    HelloPursaeTheme,
		"PREVIEW_QUERY":         PreviewQuery,
		"THEME_BUILDER_PAGE":    ThemeBuilderPage,
		"MENU_LOCATION_PRIMARY": MenuLocationPrimary,
		"MENU_LOCATION_FOOTER":  MenuLocationFooter,
		"CONTENT_MENU":          ContentMenu,
		"CONTENT_LOGO_SOURCE":   ContentLogoSource,
		"LOGO_SOURCE_SITE":      LogoSourceSite,
		"CLASS_CURRENT":         ClassCurrent,
		"SEARCH_INPUT_NAME":     SearchInputName,
	}
	for name, v := range consts {
		m := regexp.MustCompile(`export const ` + name + ` = '([^']*)'`).FindStringSubmatch(ts)
		if m == nil || m[1] != v {
			t.Errorf("contract.ts %s: got %v, Go has %q", name, m, v)
		}
	}

	eqList := func(name string, got []string, want []string) {
		t.Helper()
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("contract.ts %s = %v, Go has %v", name, got, want)
		}
	}
	var types, starters, widgets []string
	for _, v := range TemplateTypes {
		types = append(types, string(v))
	}
	for _, v := range StarterIDs {
		starters = append(starters, string(v))
	}
	for _, v := range DynWidgets {
		widgets = append(widgets, string(v))
	}
	eqList("TEMPLATE_TYPES", tsList(t, ts, "TEMPLATE_TYPES"), types)
	eqList("STARTER_IDS", tsList(t, ts, "STARTER_IDS"), starters)
	eqList("DYN_WIDGETS", tsList(t, ts, "DYN_WIDGETS"), widgets)
	eqList("SLOT_ATTRS", tsList(t, ts, "SLOT_ATTRS"), SlotAttrs)
	eqList("RESERVED_SINGULAR_WORDS", tsList(t, ts, "RESERVED_SINGULAR_WORDS"), ReservedSingularWords)
	eqList("RESERVED_ARCHIVE_WORDS", tsList(t, ts, "RESERVED_ARCHIVE_WORDS"), ReservedArchiveWords)
	eqList("SHOP_SOURCES", tsList(t, ts, "SHOP_SOURCES"), ShopSources)
	eqList("SHOP_ORDER_BYS", tsList(t, ts, "SHOP_ORDER_BYS"), ShopOrderBys)
	var shop []string
	for _, v := range ShopWidgets {
		shop = append(shop, string(v))
	}
	eqList("SHOP_WIDGETS", tsList(t, ts, "SHOP_WIDGETS"), shop)
	sm := regexp.MustCompile(`(?s)export const STORE_TEMPLATE_KINDS[^=]*= \{(.*?)\n\}`).FindStringSubmatch(ts)
	if sm == nil {
		t.Fatal("contract.ts has no STORE_TEMPLATE_KINDS")
	}
	for typ, kinds := range StoreTemplateKinds {
		if !strings.Contains(sm[1], "'"+string(typ)+"': ['"+strings.Join(kinds, "', '")+"']") {
			t.Errorf("STORE_TEMPLATE_KINDS lacks %s: %v", typ, kinds)
		}
	}

	labels := tsObject(t, ts, "TEMPLATE_TYPE_LABELS")
	starterMap := tsObject(t, ts, "DEFAULT_STARTERS")
	previews := tsObject(t, ts, "DEFAULT_PREVIEWS")
	for _, typ := range TemplateTypes {
		k := string(typ)
		if labels[k] != TemplateTypeLabels[typ] {
			t.Errorf("TEMPLATE_TYPE_LABELS[%s] = %q, Go %q", k, labels[k], TemplateTypeLabels[typ])
		}
		if starterMap[k] != string(DefaultStarters[typ]) {
			t.Errorf("DEFAULT_STARTERS[%s] = %q, Go %q", k, starterMap[k], DefaultStarters[typ])
		}
		if previews[k] != DefaultPreviews[typ] {
			t.Errorf("DEFAULT_PREVIEWS[%s] = %q, Go %q", k, previews[k], DefaultPreviews[typ])
		}
	}
	m := regexp.MustCompile(`(?s)export const DEFAULT_CONDITIONS[^=]*= \{(.*?)\n\}`).FindStringSubmatch(ts)
	if m == nil {
		t.Fatal("contract.ts has no DEFAULT_CONDITIONS")
	}
	for typ, rules := range DefaultConditions {
		if !strings.Contains(m[1], string(typ)+"': ['"+strings.Join(rules, "', '")+"']") && !strings.Contains(m[1], string(typ)+": ['"+strings.Join(rules, "', '")+"']") {
			t.Errorf("DEFAULT_CONDITIONS lacks %s: %v", typ, rules)
		}
	}

	slots := tsObject(t, ts, "SLOTS")
	if len(slots) != len(Slots) {
		t.Errorf("SLOTS has %d entries, Go %d", len(slots), len(Slots))
	}
	for name, mode := range Slots {
		if slots[string(name)] != string(mode) {
			t.Errorf("SLOTS[%s] = %q, Go %q", name, slots[string(name)], mode)
		}
	}

	attrs := tsObject(t, ts, "DYN_ATTR")
	for _, a := range []string{AttrSlot, AttrIf, AttrIfNot, AttrLoop, AttrLoopItem, AttrLoopEmpty, AttrMenuLoc, AttrLoopPart, AttrFormat, AttrSize, AttrTax, AttrSep, AttrWords, AttrSearch, AttrItem} {
		found := false
		for _, v := range attrs {
			found = found || v == a
		}
		if !found {
			t.Errorf("DYN_ATTR lacks %s", a)
		}
	}
	if len(attrs) != 15 {
		t.Errorf("DYN_ATTR has %d entries, want 15", len(attrs))
	}

	// Regular expressions: the TS literal equals the Go pattern once "\/" is unescaped.
	for name, want := range map[string]string{"CONDITION_PATTERN": ConditionPattern, "DYN_FORMAT_PATTERN": DynFormatPattern, "SHOP_IDS_PATTERN": ShopIDsPattern} {
		m := regexp.MustCompile(`export const ` + name + ` =\s*/(.*)/\n`).FindStringSubmatch(ts)
		if m == nil || strings.ReplaceAll(m[1], `\/`, "/") != want {
			t.Errorf("contract.ts %s = %v, Go %q", name, m, want)
		}
	}

	for _, code := range []string{ErrBadCondition, ErrBadTemplate, ErrBadPreview, VErrCondition} {
		if !strings.Contains(ts, "'"+code+"'") {
			t.Errorf("contract.ts lacks %q", code)
		}
	}
	for _, r := range []string{RouteTemplates, RouteTemplateConditions, RouteTemplatePreview, RouteTemplateConflicts, RoutePreviewContext} {
		js := strings.TrimPrefix(strings.ReplaceAll(r, "{id}", "${id}"), "/")
		if !strings.Contains(ts, js) {
			t.Errorf("contract.ts routes lack %s", js)
		}
	}
	lim := map[string]int{"maxConditions": MaxConditions, "maxConditionBytes": MaxConditionBytes, "priorityMin": PriorityMin, "priorityMax": PriorityMax,
		"maxPerPage": MaxLoopPerPage, "maxTerms": MaxLoopTerms, "maxOffset": MaxLoopOffset, "maxDepth": MaxLoopDepth, "maxIterations": MaxLoopIterations,
		"maxSepRunes": MaxDynSepRunes, "maxEmptyRunes": MaxDynEmptyRunes, "minWords": MinDynWords, "maxWords": MaxDynWords,
		"maxColumns": MaxShopColumns, "maxLimit": MaxShopLimit, "maxIds": MaxShopIDs}
	for k, v := range lim {
		if !regexp.MustCompile(`\b` + k + `: ` + regexp.QuoteMeta(strconv.Itoa(v)) + `\b`).MatchString(ts) {
			t.Errorf("contract.ts lacks %s: %d", k, v)
		}
	}
}

// The condition grammar accepts exactly the documented scopes.
func TestConditionPattern(t *testing.T) {
	re := regexp.MustCompile(ConditionPattern)
	good := []string{
		"include/general", "exclude/general", "include/404",
		"include/singular", "include/singular/front_page", "include/singular/post", "include/singular/page/12",
		"include/singular/post/in/category/3", "include/singular/page/child_of/7", "include/singular/post/by_author/2",
		"include/singular/book", "exclude/singular/book/99",
		"include/archive", "include/archive/home", "include/archive/date", "include/archive/search",
		"include/archive/author", "include/archive/author/5", "include/archive/type/book",
		"include/archive/category", "include/archive/category/12", "exclude/archive/post_tag/4",
	}
	bad := []string{
		"", "general", "include", "include/", "both/general", "Include/general", "include/general/1",
		"include/404/1", "include/singular/post/0", "include/singular/post/012", "include/singular/Post",
		"include/singular/post/in/category", "include/singular/post/child_of",
		"include/archive/author/x", "include/archive/category/1/2", "include/singular/post/12/x",
		"include/singular/post/1234567890123456789012", " include/general", "include/general ",
		"include/archive/" + strings.Repeat("a", 33),
	}
	for _, r := range good {
		if !re.MatchString(r) {
			t.Errorf("rejected %q", r)
		}
		if len(r) > MaxConditionBytes {
			t.Errorf("%q longer than MaxConditionBytes", r)
		}
	}
	for _, r := range bad {
		if re.MatchString(r) {
			t.Errorf("accepted %q", r)
		}
	}
	// A reserved word in the <ptype>/<tax> position matches the pattern; the parser rejects it.
	for _, r := range []string{"include/archive/type", "include/singular/in"} {
		if !re.MatchString(r) {
			t.Errorf("pattern should leave reserved %q to the parser", r)
		}
	}
	for typ, rules := range DefaultConditions {
		for _, r := range rules {
			if !re.MatchString(r) || !strings.HasPrefix(r, CondInclude+"/") {
				t.Errorf("DefaultConditions[%s] %q is not a valid include rule", typ, r)
			}
		}
	}
}

// Every template type is fully described; only single has no default conditions.
func TestTemplateTypesComplete(t *testing.T) {
	seen := map[TemplateType]bool{}
	for _, typ := range TemplateTypes {
		if seen[typ] {
			t.Errorf("duplicate type %s", typ)
		}
		seen[typ] = true
		if TemplateTypeLabels[typ] == "" || len(TemplateTypeViews[typ]) == 0 || DefaultStarters[typ] == "" || DefaultPreviews[typ] == "" {
			t.Errorf("type %s lacks a label, views, starter or preview", typ)
		}
		if _, ok := DefaultConditions[typ]; ok == (typ == TemplateSingle) {
			t.Errorf("DefaultConditions[%s] presence wrong", typ)
		}
	}
	for _, m := range []int{len(TemplateTypeLabels), len(TemplateTypeViews), len(DefaultStarters), len(DefaultPreviews)} {
		if m != len(TemplateTypes) {
			t.Errorf("a per-type map has %d entries, want %d", m, len(TemplateTypes))
		}
	}
	if TemplateLocation(TemplateHeader) != LocationHeader || TemplateLocation(TemplateFooter) != LocationFooter || TemplateLocation(TemplateArchive) != LocationMain {
		t.Error("TemplateLocation")
	}
	ok := map[StarterID]bool{}
	for _, s := range StarterIDs {
		ok[s] = true
	}
	for typ, s := range DefaultStarters {
		if !ok[s] {
			t.Errorf("DefaultStarters[%s] = %q is not a StarterID", typ, s)
		}
	}
	if !(ScoreGeneral < ScoreView && ScoreView < ScoreType && ScoreType < ScoreRelation && ScoreRelation < ScoreSpecial && ScoreSpecial < ScoreSpecificID) {
		t.Error("condition scores are not strictly ordered")
	}
}

// New placeholder attributes survive the sanitizer's data-wb-* rule and never trip the
// runtime's menu feature detection (" data-wb-menu=").
func TestDynAttrNames(t *testing.T) {
	keep := regexp.MustCompile(`^data-wb-[a-z0-9-]{1,40}$`)
	for _, a := range []string{AttrSlot, AttrIf, AttrIfNot, AttrLoop, AttrLoopItem, AttrLoopEmpty, AttrMenuLoc, AttrLoopPart, AttrFormat, AttrSize, AttrTax, AttrSep, AttrWords, AttrSearch, AttrItem} {
		if !keep.MatchString(a) {
			t.Errorf("%s would be dropped by the sanitizer", a)
		}
		if strings.Contains(" "+a+`="x"`, " "+AttrMenu+"=") {
			t.Errorf("%s collides with the runtime menu feature", a)
		}
	}
	for name := range Slots {
		if !regexp.MustCompile(`^(site|menu|post|archive|search|loop|shop|product)\.[a-z-]{1,20}$`).MatchString(string(name)) {
			t.Errorf("bad slot name %q", name)
		}
	}
}

// Item.dyn and PageSettings.layout round-trip; a doc without them encodes as before.
func TestThemeBuilderDocFields(t *testing.T) {
	in := `{"id":"i1","title":"post-date","text":"","dyn":{"tag":"p","link":false,"format":"F j, Y","fields":["date","author"],"burgerAt":0,"words":0}}`
	var it Item
	if err := json.Unmarshal([]byte(in), &it); err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(it)
	if string(out) != in {
		t.Fatalf("Item round trip\n in: %s\nout: %s", in, out)
	}
	j, _ := json.Marshal(Item{ID: "x"})
	if strings.Contains(string(j), "dyn") {
		t.Errorf("empty Dyn encoded: %s", j)
	}
	j, _ = json.Marshal(PageSettings{Title: "t"})
	if strings.Contains(string(j), "layout") {
		t.Errorf("empty Layout encoded: %s", j)
	}
	j, _ = json.Marshal(PageSettings{Layout: PageLayoutCanvas})
	if !strings.Contains(string(j), `"layout":"canvas"`) {
		t.Errorf("layout: %s", j)
	}

	nav := &DocState{Sections: []Section{{Kind: "hero"}, {Kind: KindNav}}}
	foot := &DocState{Sections: []Section{{Kind: KindFooter}}}
	plain := &DocState{Sections: []Section{{Kind: "hero", Items: []Item{{Sub: &Section{Kind: KindNav}}}}}}
	if DefaultPageLayout(nav) != PageLayoutCanvas || DefaultPageLayout(foot) != PageLayoutCanvas || DefaultPageLayout(plain) != PageLayoutSite || DefaultPageLayout(nil) != PageLayoutSite {
		t.Error("DefaultPageLayout")
	}
}

// The boot config carries template (null on ordinary posts), site and dynamic.
func TestBootThemeBuilderShape(t *testing.T) {
	j, _ := json.Marshal(Boot{})
	var m map[string]any
	_ = json.Unmarshal(j, &m)
	if v, ok := m["template"]; !ok || v != nil {
		t.Errorf("template = %v (present %v), want null", v, ok)
	}
	for _, k := range []string{"site", "dynamic"} {
		if _, ok := m[k].(map[string]any); !ok {
			t.Errorf("boot lacks object %s", k)
		}
	}
	d, _ := json.Marshal(DynContext{})
	for _, k := range []string{`"object":`, `"site":`, `"locations":`, `"menus":`, `"view":`, `"post":null`, `"posts":`, `"pagination":`, `"shop":null`} {
		if !strings.Contains(string(d), k) {
			t.Errorf("DynContext lacks %s: %s", k, d)
		}
	}
}
