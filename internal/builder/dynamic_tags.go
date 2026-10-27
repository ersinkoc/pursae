package builder

// Golementor Pro dynamic tags: settings carry "[elementor-tag id=".." name=".." settings=".."]"
// texts under __dynamic__; before an element renders, every such control gets the tag's value
// (text tags replace the tag inside the text, data tags return an object for media, gallery and
// icon controls or a property of URL and slider controls). The tags are ports of upstream Pro's
// modules/dynamic-tags/tags/*.php; what they read from the site comes from RenderContext.Pro.

import (
	"encoding/base64"
	"encoding/json"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// UserInfo is a site user as dynamic tags and the author box show them.
type UserInfo struct {
	ID                                       int64
	Login, Email, URL, Nicename, DisplayName string
	FirstName, LastName, Description         string
	AvatarURL, PostsURL                      string
	Roles                                    []string
	Meta                                     map[string]any
	PostCount                                int
}

// CommentData is one approved comment (post-comments widget).
type CommentData struct {
	ID, ParentID              int64
	Author, AuthorURL, Avatar string
	Date, DateISO, Content    string // Content is HTML
	ByPostAuthor              bool
	Children                  []*CommentData
}

// CommentsData is a post's comment list and form state.
type CommentsData struct {
	Count       int
	Open        bool
	Comments    []*CommentData
	FormAction  string
	LoggedIn    *UserInfo
	RequireName bool
	Nonce       string
}

// PostQuery is the query the posts, loop and archive widgets run (Pro's Group_Control_Query).
type PostQuery struct {
	PostTypes       []string
	IncludeTermIDs  []int64
	ExcludeTermIDs  []int64
	IncludeAuthors  []int64
	ExcludeAuthors  []int64
	IncludeIDs      []int64
	ExcludeIDs      []int64
	ExcludeCurrent  bool
	Search          string
	OrderBy, Order  string // post_date, post_title, menu_order, rand, comment_count, modified; asc/desc
	PerPage, Offset int
	Page            int
	IgnoreSticky    bool
	DateMode        string // all, today, week, month, quarter, year, exact
	DateBefore      string
	DateAfter       string
	CurrentQuery    bool // use the page's main query
	RelatedTo       int64
	RelatedTaxonomy string
	Ext             *LoopQueryExt // gap-loops: the rest of Pro's query control (loop_query.go)
}

// PostList is a query result.
type PostList struct {
	Posts       []*PostData
	Total       int
	Pages       int
	CurrentPage int
}

// TermItem is a taxonomy term as the taxonomy loop and filter widgets list them.
type TermItem struct {
	ID       int64
	Taxonomy string
	Name     string
	Slug     string
	URL      string
	Count    int
	ParentID int64
	Image    int64

	Description string         // gap-loops2: term loops (archive-description)
	Meta        map[string]any // gap-loops2: term loops (archive-meta)
}

// ProData is what Pro's dynamic tags, query widgets and documents read from the site. Every
// function may be nil; tags then render nothing.
type ProData struct {
	User            *UserInfo                          // logged-in visitor
	AuthorOf        func(postID int64) *UserInfo       // post author
	UserByID        func(id int64) *UserInfo           // any user
	PostMeta        func(postID int64, key string) any // custom fields
	PostTerms       func(postID int64, taxonomy string) []TermLink
	PostTermIDs     func(postID int64, taxonomy string) []int64
	PostByID        func(id int64) *PostData                                       // any post (internal URL, template widget)
	TermURL         func(termID int64) string                                      // taxonomy archive link
	TermByID        func(termID int64) *TermItem                                   // term for internal URL / archive
	AttachmentURL   func(id int64) string                                          // attachment page link
	Request         func(kind, name string) string                                 // get, post, query_var
	CurrentURL      string                                                         // request URL (reload page)
	DateFormat      string                                                         // site date_format
	TimeFormat      string                                                         // site time_format
	Now             time.Time                                                      // current site time
	Query           func(q *PostQuery) *PostList                                   // posts for the posts/loop widgets
	Terms           func(taxonomy string, args map[string]any) []*TermItem         // terms for taxonomy loops and filters
	CommentsHTML    func(postID int64) string                                      // post-comments widget: the theme comment list and form
	Template        func(templateID int64) *Document                               // library templates (template widget, global widgets, loop items)
	Previous, Next  func(postID int64, sameTerm string, taxonomy string) *PostData // post navigation
	Breadcrumbs     func() []TermLink                                              // current page breadcrumbs (home first)
	PostTypeLabel   func(typ string) string
	Sitemap         func(kind string, args map[string]any) []*SitemapNode
	AdminAjaxURL    string
	RestURL         string
	LoopItemClasses func(postID int64, typ string) string // post_class() for loop items
	// ArchiveTermID / ArchiveAuthorID identify a term or author archive being shown.
	ArchiveTermID, ArchiveAuthorID int64
	IsEditor                       bool
	Loop                           *LoopState // gap-loops: per-request query state and site data (loop_query.go)
}

// SitemapNode is one line of the sitemap widget (posts or terms, nested).
type SitemapNode struct {
	Title, URL string
	Children   []*SitemapNode
}

func (r *renderer) pro() *ProData {
	if r.rc.Pro == nil {
		return &ProData{}
	}
	return r.rc.Pro
}

// --- tag text parsing ----------------------------------------------------------------------------

var tagTextRe = regexp.MustCompile(`\[elementor-tag[^\]]*\]`)

// dynamicTag is one parsed tag text.
type dynamicTag struct {
	ID, Name string
	Settings map[string]any
}

var (
	tagIDRe       = regexp.MustCompile(`id="(.*?)"`)
	tagNameRe     = regexp.MustCompile(`name="(.*?)"`)
	tagSettingsRe = regexp.MustCompile(`settings="(.*?)"\]`)
)

// parseTagText mirrors Manager::tag_text_to_tag_data.
func parseTagText(text string) *dynamicTag {
	id := tagIDRe.FindStringSubmatch(text)
	name := tagNameRe.FindStringSubmatch(text)
	st := tagSettingsRe.FindStringSubmatch(text)
	if id == nil || name == nil || st == nil {
		return nil
	}
	t := &dynamicTag{ID: id[1], Name: name[1], Settings: map[string]any{}}
	dec, err := url.QueryUnescape(st[1])
	if err != nil {
		dec = st[1]
	}
	_ = json.Unmarshal([]byte(dec), &t.Settings)
	return t
}

// dataTags return structured values (Data_Tag); everything else renders text.
var dataTags = map[string]bool{"post-featured-image": true, "site-logo": true, "user-profile-picture": true, "author-profile-picture": true,
	"post-url": true, "internal-url": true, "post-gallery": true, "archive-url": true, "author-url": true, "comments-url": true,
	"site-url": true, "reload-page": true}

// resolveDynamic replaces the dynamic controls of every element (and of the document settings)
// with the tags' values. It runs once per render, before settings are read.
func (r *renderer) resolveDynamic(doc *Document) {
	if doc == nil {
		return
	}
	if dyn := asMap(doc.Settings["__dynamic__"]); len(dyn) > 0 {
		kind, _ := documentCSSKind(doc)
		if st := stackFor(kind, r.bps); st != nil {
			r.resolveDynamicMap(doc.Settings, dyn, st.byName)
		}
		delete(doc.Settings, "__dynamic__")
	}
	var walk func(list []*Element)
	walk = func(list []*Element) {
		for _, el := range list {
			if el == nil {
				continue
			}
			if el.ElType == "e-collection-loop" { // gap-loops2: resolved per item (loop_atomic.go loopRenderChild)
				continue
			}
			r.resolveElementDynamic(el)
			walk(el.Elements)
		}
	}
	walk(doc.Elements)
}

func (r *renderer) resolveElementDynamic(el *Element) {
	if el.Settings == nil {
		return
	}
	_, st := elementStack(el, r.bps)
	if st == nil {
		return
	}
	// repeater items may carry their own __dynamic__
	for _, c := range st.all {
		if !c.isRepeater() {
			continue
		}
		for _, it := range asList(el.Settings[c.Name]) {
			im := asMap(it)
			if dyn := asMap(im["__dynamic__"]); len(dyn) > 0 {
				r.resolveDynamicMap(im, dyn, c.FieldsByKey)
				delete(im, "__dynamic__")
			}
		}
	}
	dyn := asMap(el.Settings["__dynamic__"])
	if len(dyn) == 0 {
		return
	}
	r.resolveDynamicMap(el.Settings, dyn, st.byName)
	delete(el.Settings, "__dynamic__")
	el.dropSettingKey("__dynamic__")
}

// resolveDynamicMap writes each dynamic control's value into settings.
func (r *renderer) resolveDynamicMap(settings map[string]any, dyn map[string]any, byName map[string]*ctrl) {
	for name, raw := range dyn {
		text := phpString(raw)
		if text == "" {
			continue
		}
		c := byName[name]
		typ := ""
		if c != nil {
			typ = c.Type
		}
		switch typ {
		case "media", "gallery", "icons":
			if t := parseTagText(text); t != nil {
				v := r.renderTag(t)
				if typ == "media" {
					m := asMap(v)
					if m == nil {
						m = map[string]any{"id": "", "url": phpString(v)}
					}
					cur := asMap(settings[name])
					out := map[string]any{"url": "", "id": "", "size": ""}
					for k, x := range cur {
						out[k] = x
					}
					for k, x := range m {
						out[k] = x
					}
					settings[name] = out
				} else {
					settings[name] = v
				}
			}
		case "url", "slider":
			prop := "url"
			if typ == "slider" {
				prop = "size"
			}
			cur := asMap(settings[name])
			out := map[string]any{}
			for k, x := range cur {
				out[k] = x
			}
			out[prop] = r.replaceTagsText(text)
			settings[name] = out
		default:
			settings[name] = r.replaceTagsText(text)
		}
	}
}

// replaceTagsText mirrors parse_tags_text for text controls.
func (r *renderer) replaceTagsText(text string) string {
	return tagTextRe.ReplaceAllStringFunc(text, func(m string) string {
		t := parseTagText(m)
		if t == nil {
			return ""
		}
		return phpString(r.renderTag(t))
	})
}

// RenderDynamicTag renders one tag text for the editor's render_tags action.
func RenderDynamicTag(text string, doc *Document, rc *RenderContext) any {
	t := parseTagText(text)
	if t == nil {
		return ""
	}
	return newRenderer(doc, rc).renderTag(t)
}

// renderTag mirrors Tag::get_content (before/after/fallback) and Data_Tag::get_content.
func (r *renderer) renderTag(t *dynamicTag) any {
	fn := dynamicTagFns[t.Name]
	if fn == nil {
		return ""
	}
	tc := &tagCtx{r: r, s: t.Settings, id: t.ID}
	v := fn(tc)
	if dataTags[t.Name] {
		return v
	}
	out := phpString(v)
	if strings.TrimSpace(out) != "" {
		if b := tc.str("before"); b != "" {
			out = b + out
		}
		if a := tc.str("after"); a != "" {
			out += a
		}
	} else if f := tc.str("fallback"); f != "" {
		out = f
	}
	return out
}

type tagCtx struct {
	r  *renderer
	s  map[string]any
	id string
}

func (t *tagCtx) str(k string) string { return phpString(t.s[k]) }
func (t *tagCtx) yes(k string) bool {
	v := t.s[k]
	return v == true || phpString(v) == "yes"
}
func (t *tagCtx) post() *PostData {
	th := t.r.rc.Theme
	if th == nil || th.Post == nil {
		return nil
	}
	return th.Post()
}
func (t *tagCtx) archive() *ArchiveData {
	th := t.r.rc.Theme
	if th == nil || th.Archive == nil {
		return nil
	}
	return th.Archive()
}
func (t *tagCtx) site() *ThemeData {
	if t.r.rc.Theme == nil {
		return &ThemeData{}
	}
	return t.r.rc.Theme
}

// actionHash mirrors Frontend::create_action_hash.
func actionHash(action string, settings any) string {
	b, _ := json.Marshal(settings)
	return "#" + url.PathEscape("elementor-action:action="+action+"&settings="+base64.StdEncoding.EncodeToString(b))
}

// phpDate formats a time with a PHP date() format (wp.FormatDate is in internal/wp; the renderer
// keeps its own small formatter so the builder has no server dependency).
func phpDate(t time.Time, format string) string {
	if format == "" {
		format = "F j, Y"
	}
	var b strings.Builder
	for i := 0; i < len(format); i++ {
		c := format[i]
		switch c {
		case '\\':
			if i+1 < len(format) {
				i++
				b.WriteByte(format[i])
			}
		case 'd':
			b.WriteString(pad2(t.Day()))
		case 'D':
			b.WriteString(t.Weekday().String()[:3])
		case 'j':
			b.WriteString(strconv.Itoa(t.Day()))
		case 'l':
			b.WriteString(t.Weekday().String())
		case 'N':
			wd := int(t.Weekday())
			if wd == 0 {
				wd = 7
			}
			b.WriteString(strconv.Itoa(wd))
		case 'S':
			b.WriteString(ordinalSuffix(t.Day()))
		case 'w':
			b.WriteString(strconv.Itoa(int(t.Weekday())))
		case 'z':
			b.WriteString(strconv.Itoa(t.YearDay() - 1))
		case 'W':
			_, w := t.ISOWeek()
			b.WriteString(pad2(w))
		case 'F':
			b.WriteString(t.Month().String())
		case 'm':
			b.WriteString(pad2(int(t.Month())))
		case 'M':
			b.WriteString(t.Month().String()[:3])
		case 'n':
			b.WriteString(strconv.Itoa(int(t.Month())))
		case 't':
			b.WriteString(strconv.Itoa(time.Date(t.Year(), t.Month()+1, 0, 0, 0, 0, 0, t.Location()).Day()))
		case 'L':
			y := t.Year()
			if (y%4 == 0 && y%100 != 0) || y%400 == 0 {
				b.WriteString("1")
			} else {
				b.WriteString("0")
			}
		case 'o':
			y, _ := t.ISOWeek()
			b.WriteString(strconv.Itoa(y))
		case 'Y':
			b.WriteString(strconv.Itoa(t.Year()))
		case 'y':
			b.WriteString(pad2(t.Year() % 100))
		case 'a':
			if t.Hour() < 12 {
				b.WriteString("am")
			} else {
				b.WriteString("pm")
			}
		case 'A':
			if t.Hour() < 12 {
				b.WriteString("AM")
			} else {
				b.WriteString("PM")
			}
		case 'g':
			h := t.Hour() % 12
			if h == 0 {
				h = 12
			}
			b.WriteString(strconv.Itoa(h))
		case 'G':
			b.WriteString(strconv.Itoa(t.Hour()))
		case 'h':
			h := t.Hour() % 12
			if h == 0 {
				h = 12
			}
			b.WriteString(pad2(h))
		case 'H':
			b.WriteString(pad2(t.Hour()))
		case 'i':
			b.WriteString(pad2(t.Minute()))
		case 's':
			b.WriteString(pad2(t.Second()))
		case 'u':
			b.WriteString("000000")
		case 'v':
			b.WriteString("000")
		case 'e':
			b.WriteString(t.Location().String())
		case 'T':
			z, _ := t.Zone()
			b.WriteString(z)
		case 'P':
			b.WriteString(t.Format("-07:00"))
		case 'O':
			b.WriteString(t.Format("-0700"))
		case 'Z':
			_, off := t.Zone()
			b.WriteString(strconv.Itoa(off))
		case 'c':
			b.WriteString(t.Format("2006-01-02T15:04:05-07:00"))
		case 'r':
			b.WriteString(t.Format("Mon, 02 Jan 2006 15:04:05 -0700"))
		case 'U':
			b.WriteString(strconv.FormatInt(t.Unix(), 10))
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func pad2(n int) string {
	if n < 10 && n >= 0 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

func ordinalSuffix(n int) string {
	if n%100 >= 11 && n%100 <= 13 {
		return "th"
	}
	switch n % 10 {
	case 1:
		return "st"
	case 2:
		return "nd"
	case 3:
		return "rd"
	}
	return "th"
}

// humanTimeDiff mirrors human_time_diff.
func humanTimeDiff(from, to time.Time) string {
	d := to.Sub(from)
	if d < 0 {
		d = -d
	}
	plural := func(n int, one, many string) string {
		if n == 1 {
			return "1 " + one
		}
		return strconv.Itoa(n) + " " + many
	}
	switch {
	case d < time.Hour:
		m := int(d.Minutes())
		if m < 1 {
			m = 1
		}
		return plural(m, "min", "mins")
	case d < 24*time.Hour:
		return plural(int(d.Hours()), "hour", "hours")
	case d < 7*24*time.Hour:
		return plural(int(d.Hours()/24), "day", "days")
	case d < 30*24*time.Hour:
		return plural(int(d.Hours()/24/7), "week", "weeks")
	case d < 365*24*time.Hour:
		return plural(int(d.Hours()/24/30), "month", "months")
	}
	return plural(int(d.Hours()/24/365), "year", "years")
}

// --- the tags -------------------------------------------------------------------------------------

var dynamicTagFns = map[string]func(t *tagCtx) any{}

func init() {
	dynamicTagFns["post-title"] = func(t *tagCtx) any {
		if p := t.post(); p != nil {
			return p.Title
		}
		return ""
	}
	dynamicTagFns["post-excerpt"] = func(t *tagCtx) any {
		p := t.post()
		if p == nil {
			return ""
		}
		ex := p.Excerpt
		if ex == "" && t.yes("apply_to_post_content") {
			if th := t.r.rc.Theme; th != nil && th.PostContent != nil {
				ex = strings.Join(strings.Fields(html.UnescapeString(stripTags(th.PostContent()))), " ")
			}
		}
		if n := toInt(t.s["max_length"]); n > 0 {
			ex = trimWords(ex, n)
		}
		return escHTML(ex)
	}
	dynamicTagFns["post-id"] = func(t *tagCtx) any {
		if p := t.post(); p != nil {
			return strconv.FormatInt(p.ID, 10)
		}
		return ""
	}
	dynamicTagFns["post-url"] = func(t *tagCtx) any {
		if p := t.post(); p != nil {
			return p.URL
		}
		return ""
	}
	dynamicTagFns["post-date"] = func(t *tagCtx) any {
		p := t.post()
		if p == nil {
			return ""
		}
		ts, ok := parseISO(p.DateISO)
		if t.str("type") == "post_modified" && t.r.pro().PostMeta != nil {
			if m, ok2 := parseISO(phpString(t.r.pro().PostMeta(p.ID, "_modified_iso"))); ok2 {
				ts, ok = m, true
			}
		}
		format := t.str("format")
		if format == "human" {
			if !ok {
				return p.Date
			}
			return humanTimeDiff(ts, t.r.pro().Now) + " ago"
		}
		if !ok {
			return p.Date
		}
		switch format {
		case "default", "":
			return phpDate(ts, t.r.pro().DateFormat)
		case "custom":
			return phpDate(ts, t.str("custom_format"))
		}
		return phpDate(ts, format)
	}
	dynamicTagFns["post-time"] = func(t *tagCtx) any {
		p := t.post()
		if p == nil {
			return ""
		}
		ts, ok := parseISO(p.DateISO)
		if !ok {
			return p.Time
		}
		format := t.str("format")
		switch format {
		case "default", "":
			return phpDate(ts, t.r.pro().TimeFormat)
		case "custom":
			return phpDate(ts, t.str("custom_format"))
		}
		return phpDate(ts, format)
	}
	dynamicTagFns["post-terms"] = func(t *tagCtx) any {
		p := t.post()
		if p == nil {
			return ""
		}
		tax := t.str("taxonomy")
		if tax == "" {
			tax = "post_tag"
		}
		var terms []TermLink
		switch tax {
		case "category":
			terms = p.Categories
		case "post_tag":
			terms = p.Tags
		default:
			if t.r.pro().PostTerms != nil {
				terms = t.r.pro().PostTerms(p.ID, tax)
			}
		}
		sep := t.str("separator")
		if _, ok := t.s["separator"]; !ok {
			sep = ", "
		}
		var parts []string
		for _, term := range terms {
			if t.yes("link") {
				parts = append(parts, `<a href="`+escURL(term.URL)+`" rel="tag">`+term.Name+`</a>`)
			} else {
				parts = append(parts, "<span>"+term.Name+"</span>")
			}
		}
		return strings.Join(parts, sep)
	}
	dynamicTagFns["post-featured-image"] = func(t *tagCtx) any {
		if p := t.post(); p != nil && p.ImageID > 0 {
			u := p.ImageURL
			if a := t.r.attachment(p.ImageID); a != nil {
				u = a.URL
			}
			return map[string]any{"id": p.ImageID, "url": u}
		}
		if fb := asMap(t.s["fallback"]); fb != nil && truthy(fb["url"]) {
			return fb
		}
		return map[string]any{"id": "", "url": ""}
	}
	dynamicTagFns["post-custom-field"] = func(t *tagCtx) any {
		p := t.post()
		key := t.str("key")
		if key == "" {
			key = t.str("custom_key")
		}
		if p == nil || key == "" || t.r.pro().PostMeta == nil {
			return ""
		}
		return phpString(t.r.pro().PostMeta(p.ID, key))
	}
	dynamicTagFns["post-gallery"] = func(t *tagCtx) any {
		p := t.post()
		if p == nil || t.r.pro().PostMeta == nil {
			return []any{}
		}
		return asList(t.r.pro().PostMeta(p.ID, "_gallery_ids"))
	}
	dynamicTagFns["comments-number"] = func(t *tagCtx) any {
		p := t.post()
		if p == nil {
			return ""
		}
		var out string
		switch {
		case p.CommentCount == 0:
			out = t.str("format_no_comments")
		case p.CommentCount == 1:
			out = t.str("format_one_comments")
		default:
			out = strings.ReplaceAll(t.str("format_many_comments"), "{number}", strconv.Itoa(p.CommentCount))
		}
		if t.str("link_to") == "comments_link" {
			out = `<a href="` + escURL(p.CommentsURL) + `">` + out + `</a>`
		}
		return out
	}
	dynamicTagFns["comments-url"] = func(t *tagCtx) any {
		if p := t.post(); p != nil {
			return p.CommentsURL
		}
		return ""
	}
	dynamicTagFns["site-title"] = func(t *tagCtx) any { return escHTML(t.site().SiteName) }
	dynamicTagFns["site-tagline"] = func(t *tagCtx) any { return escHTML(t.site().Tagline) }
	dynamicTagFns["site-url"] = func(t *tagCtx) any { return t.site().HomeURL }
	dynamicTagFns["site-logo"] = func(t *tagCtx) any {
		s := t.site()
		if s.LogoID > 0 {
			u := s.LogoURL
			if a := t.r.attachment(s.LogoID); a != nil {
				u = a.URL
			}
			return map[string]any{"id": s.LogoID, "url": u}
		}
		if s.LogoURL != "" {
			return map[string]any{"id": "", "url": s.LogoURL}
		}
		return map[string]any{"id": "", "url": strings.TrimRight(t.r.rc.HomeURL, "/") + "/wp-content/plugins/gowebeditor/assets/images/placeholder.png"}
	}
	dynamicTagFns["page-title"] = func(t *tagCtx) any {
		return t.r.pageTitle(t.yes("include_context"), t.yes("show_home_title"))
	}
	dynamicTagFns["archive-title"] = func(t *tagCtx) any {
		if v, ok := t.loopTermTag("title"); ok { // gap-loops2: term loops (loop_terms.go)
			return v
		}
		include := true
		if v, ok := t.s["include_context"]; ok {
			include = v == true || phpString(v) == "yes"
		}
		if a := t.archive(); a != nil {
			if a.Home { // gap-loops2: Utils::get_page_title on the blog home
				return loopHomeTitle(a, true)
			}
			if include {
				return a.Title
			}
			return a.PlainTitle
		}
		return t.r.pageTitle(include, true)
	}
	dynamicTagFns["archive-description"] = func(t *tagCtx) any {
		if v, ok := t.loopTermTag("description"); ok { // gap-loops2: term loops (loop_terms.go)
			return v
		}
		if a := t.archive(); a != nil {
			return a.Description
		}
		return ""
	}
	dynamicTagFns["archive-url"] = func(t *tagCtx) any {
		if v, ok := t.loopTermTag("url"); ok { // gap-loops2: term loops (loop_terms.go)
			return v
		}
		if a := t.archive(); a != nil && a.PageURL != nil {
			return a.PageURL(1)
		}
		return ""
	}
	dynamicTagFns["archive-meta"] = func(t *tagCtx) any {
		if v, ok := t.loopTermTag("meta"); ok { // gap-loops2: term loops (loop_terms.go)
			return v
		}
		return ""
	}
	dynamicTagFns["author-name"] = func(t *tagCtx) any {
		if u := t.author(); u != nil {
			return escHTML(u.DisplayName)
		}
		if p := t.post(); p != nil {
			return escHTML(p.AuthorName)
		}
		return ""
	}
	dynamicTagFns["author-url"] = func(t *tagCtx) any {
		if u := t.author(); u != nil {
			if t.str("url") == "website" {
				return u.URL
			}
			return u.PostsURL
		}
		if p := t.post(); p != nil {
			return p.AuthorURL
		}
		return ""
	}
	dynamicTagFns["author-info"] = func(t *tagCtx) any {
		u := t.author()
		if u == nil {
			return ""
		}
		switch t.str("type") {
		case "description":
			return escHTML(u.Description)
		case "email":
			return escHTML(u.Email)
		case "url":
			return escHTML(u.URL)
		case "nickname":
			return escHTML(u.Nicename)
		}
		return escHTML(u.DisplayName)
	}
	dynamicTagFns["author-meta"] = func(t *tagCtx) any {
		u := t.author()
		key := t.str("key")
		if u == nil || key == "" {
			return ""
		}
		return escHTML(phpString(u.Meta[key]))
	}
	dynamicTagFns["author-profile-picture"] = func(t *tagCtx) any {
		if u := t.author(); u != nil {
			return map[string]any{"id": "", "url": u.AvatarURL}
		}
		return map[string]any{"id": "", "url": ""}
	}
	dynamicTagFns["user-info"] = func(t *tagCtx) any {
		u := t.r.pro().User
		if u == nil {
			return ""
		}
		switch t.str("type") {
		case "login":
			return escHTML(u.Login)
		case "email":
			return escHTML(u.Email)
		case "url":
			return escHTML(u.URL)
		case "nicename":
			return escHTML(u.Nicename)
		case "id":
			return strconv.FormatInt(u.ID, 10)
		case "description":
			return escHTML(u.Description)
		case "first_name":
			return escHTML(u.FirstName)
		case "last_name":
			return escHTML(u.LastName)
		case "display_name":
			return escHTML(u.DisplayName)
		case "meta":
			return escHTML(phpString(u.Meta[t.str("meta_key")]))
		}
		return ""
	}
	dynamicTagFns["user-profile-picture"] = func(t *tagCtx) any {
		if u := t.r.pro().User; u != nil {
			return map[string]any{"id": "", "url": u.AvatarURL}
		}
		return map[string]any{"id": "", "url": ""}
	}
	dynamicTagFns["current-date-time"] = func(t *tagCtx) any {
		now := t.r.pro().Now
		if now.IsZero() {
			now = time.Now()
		}
		if t.str("date_format") == "custom" {
			return phpDate(now, t.str("custom_format"))
		}
		df, tf := t.str("date_format"), t.str("time_format")
		if df == "default" {
			df = t.r.pro().DateFormat
			if df == "" {
				df = "F j, Y"
			}
		}
		if tf == "default" {
			tf = t.r.pro().TimeFormat
			if tf == "" {
				tf = "g:i a"
			}
		}
		format := df
		if tf != "" {
			if format != "" {
				format += " "
			}
			format += tf
		}
		return phpDate(now, format)
	}
	dynamicTagFns["request-arg"] = func(t *tagCtx) any {
		kind := strings.ToLower(t.str("request_type"))
		name := t.str("param_name")
		if name == "" || t.r.pro().Request == nil {
			return ""
		}
		return escHTML(t.r.pro().Request(kind, name))
	}
	dynamicTagFns["reload-page"] = func(t *tagCtx) any { return t.r.pro().CurrentURL }
	dynamicTagFns["internal-url"] = func(t *tagCtx) any {
		pd := t.r.pro()
		switch t.str("type") {
		case "post":
			if id := toInt64(t.s["post_id"]); id > 0 && pd.PostByID != nil {
				if p := pd.PostByID(id); p != nil {
					return p.URL
				}
			}
		case "taxonomy":
			if id := toInt64(t.s["taxonomy_id"]); id > 0 && pd.TermURL != nil {
				return pd.TermURL(id)
			}
		case "attachment":
			if id := toInt64(t.s["attachment_id"]); id > 0 && pd.AttachmentURL != nil {
				return pd.AttachmentURL(id)
			}
		case "author":
			if id := toInt64(t.s["author_id"]); id > 0 && pd.UserByID != nil {
				if u := pd.UserByID(id); u != nil {
					return u.PostsURL
				}
			}
		}
		return ""
	}
	dynamicTagFns["contact-url"] = func(t *tagCtx) any { return contactURL(t.s) }
	dynamicTagFns["shortcode"] = func(t *tagCtx) any {
		sc := t.str("shortcode")
		if sc == "" || t.r.rc.Shortcode == nil {
			return ""
		}
		return t.r.rc.Shortcode(sc)
	}
	dynamicTagFns["featured-image-data"] = func(t *tagCtx) any {
		p := t.post()
		if p == nil || p.ImageID == 0 {
			return ""
		}
		a := t.r.attachment(p.ImageID)
		if a == nil {
			return ""
		}
		switch t.str("attachment_data") {
		case "alt":
			return escHTML(a.Alt)
		case "caption":
			return escHTML(a.Caption)
		case "description":
			return escHTML(a.Description)
		case "src":
			return a.URL
		case "href":
			if t.r.pro().AttachmentURL != nil {
				return t.r.pro().AttachmentURL(a.ID)
			}
			return a.URL
		}
		return escHTML(a.Title)
	}
	dynamicTagFns["lightbox"] = func(t *tagCtx) any {
		switch t.str("type") {
		case "image":
			img := asMap(t.s["image"])
			if img == nil || !truthy(img["url"]) {
				return ""
			}
			v := map[string]any{"type": "image", "url": phpString(img["url"])}
			if id := toInt64(img["id"]); id > 0 {
				v["id"] = id
				if a := t.r.attachment(id); a != nil {
					v["url"] = a.URL
				}
			}
			return actionHash("lightbox", v)
		case "video":
			u := t.str("video_url")
			if u == "" {
				return ""
			}
			v := map[string]any{"type": "video"}
			if strings.Contains(u, "vimeo.com") {
				v["videoType"] = "vimeo"
				v["url"] = u
			} else if strings.Contains(u, "youtu") {
				v["videoType"] = "youtube"
				v["url"] = u
			} else {
				v["videoType"] = "hosted"
				v["url"] = u
			}
			return actionHash("lightbox", v)
		}
		return ""
	}
	dynamicTagFns["popup"] = func(t *tagCtx) any {
		if t.str("action") == "close" {
			return actionHash("popup:close", map[string]any{"do_not_show_again": t.str("do_not_show_again")})
		}
		id := t.str("popup")
		if id == "" {
			return ""
		}
		return actionHash("popup:open", map[string]any{"id": id, "toggle": t.str("action") == "toggle"})
	}
	dynamicTagFns["off-canvas"] = func(t *tagCtx) any {
		id := t.str("off_canvas")
		if id == "" {
			return ""
		}
		action := t.str("action")
		if action == "" {
			action = "open"
		}
		return actionHash("off_canvas:"+action, map[string]any{"id": id})
	}
}

func (t *tagCtx) author() *UserInfo {
	p := t.post()
	if p == nil || t.r.pro().AuthorOf == nil {
		return nil
	}
	return t.r.pro().AuthorOf(p.ID)
}

// pageTitle mirrors upstream Pro\Core\Utils::get_page_title.
func (r *renderer) pageTitle(includeContext, showHome bool) string {
	th := r.rc.Theme
	if th == nil {
		return proFEditorPageTitle(r, includeContext) // gap-widgets: render_tags of a page/post in the editor
	}
	if th.Archive != nil {
		if a := th.Archive(); a != nil {
			if a.Home { // gap-loops2: is_home(): nothing unless "show home title", then the document title
				return loopHomeTitle(a, showHome)
			}
			if includeContext {
				return a.Title
			}
			return a.PlainTitle
		}
	}
	if th.Post != nil {
		if p := th.Post(); p != nil {
			return proFSingularTitle(r, p, includeContext) // gap-widgets: "Post: Title" with include context
		}
	}
	if t := proFEditorPageTitle(r, includeContext); t != "" { // gap-widgets: render_tags of a page/post in the editor
		return t
	}
	if r.doc != nil {
		if t, ok := r.doc.Settings["post_title"]; ok {
			return escHTML(phpString(t))
		}
	}
	return ""
}

func parseISO(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func toInt64(v any) int64 {
	switch t := v.(type) {
	case float64:
		return int64(t)
	case int:
		return int64(t)
	case int64:
		return t
	case json.Number:
		n, _ := t.Int64()
		return n
	case string:
		n, _ := strconv.ParseInt(strings.TrimSpace(t), 10, 64)
		return n
	}
	return 0
}

// contactURL mirrors the Contact URL tag's link builders.
func contactURL(s map[string]any) string {
	get := func(k string) string { return strings.TrimSpace(phpString(s[k])) }
	q := func(m [][2]string) string {
		var parts []string
		for _, kv := range m {
			if kv[1] != "" {
				parts = append(parts, kv[0]+"="+url.QueryEscape(kv[1]))
			}
		}
		return strings.Join(parts, "&")
	}
	switch get("link_type") {
	case "email":
		to := get("mail_to")
		if to == "" {
			return ""
		}
		params := q([][2]string{{"subject", get("mail_subject")}, {"body", get("mail_body")}})
		if params != "" {
			return "mailto:" + to + "?" + strings.ReplaceAll(params, "+", "%20")
		}
		return "mailto:" + to
	case "tel":
		if n := get("tel_number"); n != "" {
			return "tel:" + n
		}
	case "sms":
		if n := get("tel_number"); n != "" {
			return "sms:" + n
		}
	case "messenger":
		if u := get("messenger_username"); u != "" {
			return "https://m.me/" + u
		}
	case "whatsapp":
		if n := get("tel_number"); n != "" {
			return "https://wa.me/" + strings.NewReplacer("+", "", " ", "", "-", "").Replace(n)
		}
	case "viber":
		if n := get("viber_number"); n != "" {
			if get("viber_action") == "chat" {
				return "viber://chat?number=" + url.QueryEscape(n)
			}
			return "viber://add?number=" + url.QueryEscape(n)
		}
	case "waze":
		if loc := get("waze_location"); loc != "" {
			return "https://waze.com/ul?ll=" + url.QueryEscape(loc) + "&navigate=yes"
		}
	case "google_calendar":
		return "https://www.google.com/calendar/render?action=TEMPLATE&" + q([][2]string{{"text", get("event_title")}, {"details", get("event_description")}, {"location", get("event_location")}})
	case "outlook_calendar":
		return "https://outlook.live.com/calendar/0/deeplink/compose?path=/calendar/action/compose&rru=addevent&" + q([][2]string{{"subject", get("event_title")}, {"body", get("event_description")}, {"location", get("event_location")}})
	case "yahoo_calendar":
		return "https://calendar.yahoo.com/?v=60&" + q([][2]string{{"title", get("event_title")}, {"desc", get("event_description")}, {"in_loc", get("event_location")}})
	}
	return ""
}
