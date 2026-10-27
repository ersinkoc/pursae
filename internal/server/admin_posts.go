package server

import (
	"errors"
	"fmt"
	"html/template"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/links"
	"wpgo/internal/store"
	"wpgo/internal/wp"
)

func itoa64(n int64) string { return strconv.FormatInt(n, 10) }

type typeLabels struct {
	Type, Name, Singular, AddNew, Search, NotFound, NotFoundTrash string
}

func labelsFor(typ string) typeLabels {
	if typ == "page" {
		return typeLabels{"page", "Pages", "Page", "Add Page", "Search Pages", "No pages found.", "No pages found in Trash."}
	}
	return typeLabels{"post", "Posts", "Post", "Add Post", "Search Posts", "No posts found.", "No posts found in Trash."}
}

type statusLink struct {
	Key, Label, URL string
	Count           int
	Current         bool
}

type postRow struct {
	ID                   int64
	Title                string
	Depth                string // "— " per hierarchy level for pages
	States               []string
	EditURL, ViewURL     string
	TrashURL, RestoreURL string
	DeleteURL            string
	Author               string
	AuthorFilterURL      string
	Categories, Tags     template.HTML
	Comments, Pending    int
	DateLabel, DateValue string
	Classes              string
	CanEdit, CanDelete   bool
	Status               string
	IsTrash              bool
	ViewLabel            string
	BuilderURL           string // "Edit with Golementor"
}

type postsListData struct {
	Labels       typeLabels
	Statuses     []statusLink
	Rows         []postRow
	Search       string
	Status       string
	Months       []monthOption
	Month        string
	Categories   []*store.Term
	Cat          int64
	Pagination   pagination
	BulkNonce    string
	CanCreate    bool
	IsTrash      bool
	Orderby      string
	Order        string
	SortLinks    map[string]sortLink
	ShowCatCols  bool
	EditorSwitch bool
}

type monthOption struct {
	Value, Label string
}

type sortLink struct {
	URL, Class, AriaSort, SRText string
}

type pagination struct {
	Total, Pages, Page      int
	First, Prev, Next, Last string
	ItemsLabel              string
}

func newPagination(total, perPage, page int, base url.URL) pagination {
	pages := int(math.Ceil(float64(total) / float64(perPage)))
	if pages < 1 {
		pages = 1
	}
	p := pagination{Total: total, Pages: pages, Page: page}
	if total == 1 {
		p.ItemsLabel = "1 item"
	} else {
		p.ItemsLabel = formatNumber(total) + " items"
	}
	link := func(n int) string {
		q := base.Query()
		if n <= 1 {
			q.Del("paged")
		} else {
			q.Set("paged", strconv.Itoa(n))
		}
		base.RawQuery = q.Encode()
		return base.String()
	}
	if page > 2 {
		p.First = link(1)
	}
	if page > 1 {
		p.Prev = link(page - 1)
	}
	if page < pages {
		p.Next = link(page + 1)
	}
	if page < pages-1 {
		p.Last = link(pages)
	}
	return p
}

// postsList is edit.php: the posts or pages list table.
func (s *Server) postsList(c *gin.Context) {
	ctx := c.Request.Context()
	u := currentUser(c)
	if c.Query("action") == "elementor_new_post" && s.golementorOn() { // Golementor: create a draft and open the builder
		s.gweNewPost(c)
		return
	}
	typ := c.DefaultQuery("post_type", "post")
	if typ != "post" && typ != "page" {
		c.String(http.StatusBadRequest, "Invalid post type.")
		return
	}
	if !u.Can("edit_"+typ+"s", nil) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to edit posts in this post type.")
		return
	}
	if a := bulkAction(c); a != "" {
		s.postsBulkAction(c, typ, a)
		return
	}
	lb := s.links(ctx)
	lab := labelsFor(typ)
	status := c.DefaultQuery("post_status", "all")
	search := strings.TrimSpace(c.Query("s"))
	page, _ := strconv.Atoi(c.Query("paged"))
	if page < 1 {
		page = 1
	}
	perPage := 20

	q := store.PostQuery{Types: []string{typ}, Search: search, Page: page, PerPage: perPage,
		OrderBy: c.Query("orderby"), Order: c.DefaultQuery("order", "desc"), Loc: s.loc()}
	if typ == "page" && c.Query("orderby") == "" {
		q.OrderBy, q.Order = "title", c.DefaultQuery("order", "asc")
	}
	switch status {
	case "all", "":
		q.Statuses = []string{"publish", "future", "draft", "pending", "private"}
	default:
		q.Statuses = []string{status}
	}
	if !u.Can("read_private_"+typ+"s", nil) && status == "all" {
		q.Statuses = []string{"publish", "future", "draft", "pending"}
	}
	if a, _ := strconv.ParseInt(c.Query("author"), 10, 64); a > 0 {
		q.AuthorID = a
	}
	if m := c.Query("m"); len(m) == 6 {
		q.Year, _ = strconv.Atoi(m[:4])
		q.Month, _ = strconv.Atoi(m[4:])
	}
	var cat int64
	if v, _ := strconv.ParseInt(c.Query("cat"), 10, 64); v > 0 {
		cat = v
		q.TermIDs = []int64{v}
	}
	for param, tax := range map[string]string{"category_name": "category", "tag": "post_tag"} {
		if slug := c.Query(param); slug != "" {
			if t, err := s.store.TermBySlug(ctx, tax, slug); err == nil {
				q.TermIDs = []int64{t.ID}
			} else {
				q.TermIDs = []int64{-1}
			}
		}
	}
	posts, total, err := s.store.QueryPosts(ctx, q)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}

	// Pages list as a tree when browsing all pages without search or custom order.
	if typ == "page" && search == "" && c.Query("orderby") == "" && (status == "all" || status == "") {
		posts, total, _ = s.pageTree(c, q, page, perPage)
	}

	d := postsListData{Labels: lab, Search: search, Status: status, BulkNonce: s.nonce(c, "bulk-posts"),
		CanCreate: u.Can("edit_"+typ+"s", nil), IsTrash: status == "trash", Month: c.Query("m"), Cat: cat,
		Orderby: q.OrderBy, Order: strings.ToLower(q.Order), ShowCatCols: typ == "post", EditorSwitch: s.settings.String("classic-editor-allow-users") == "allow"}

	counts, _ := s.store.StatusCounts(ctx, typ, 0)
	all := counts["publish"] + counts["future"] + counts["draft"] + counts["pending"] + counts["private"]
	base := "/wp-admin/edit"
	if typ == "page" {
		base += "?post_type=page"
	}
	withStatus := func(st string) string {
		if typ == "page" {
			return base + "&post_status=" + st
		}
		return base + "?post_status=" + st
	}
	d.Statuses = append(d.Statuses, statusLink{"all", "All", base, all, status == "all" || status == ""})
	mine, _ := s.store.StatusCounts(ctx, typ, u.ID)
	mineCount := mine["publish"] + mine["future"] + mine["draft"] + mine["pending"] + mine["private"]
	if mineCount > 0 && mineCount != all {
		sep := "?"
		if strings.Contains(base, "?") {
			sep = "&"
		}
		d.Statuses = append(d.Statuses, statusLink{"mine", "Mine", base + sep + "author=" + itoa64(u.ID), mineCount, q.AuthorID == u.ID})
	}
	for _, st := range []struct{ key, label string }{
		{"publish", "Published"}, {"future", "Scheduled"}, {"draft", "Drafts"}, {"pending", "Pending"},
		{"private", "Private"}, {"trash", "Trash"},
	} {
		if counts[st.key] > 0 {
			d.Statuses = append(d.Statuses, statusLink{st.key, st.label, withStatus(st.key), counts[st.key], status == st.key})
		}
	}

	ids := make([]int64, 0, len(posts))
	authorIDs := []int64{}
	for _, p := range posts {
		ids = append(ids, p.ID)
		authorIDs = append(authorIDs, p.AuthorID)
	}
	authors, _ := s.store.UserNames(ctx, authorIDs)
	cats, _ := s.store.TermsForPosts(ctx, ids, "category")
	tags, _ := s.store.TermsForPosts(ctx, ids, "post_tag")
	loc := s.loc()
	frontID := int64(s.settings.Int("page_on_front"))
	postsPageID := int64(s.settings.Int("page_for_posts"))
	privacyID := int64(s.settings.Int("wp_page_for_privacy_policy"))
	builderOn := s.golementorOn()

	for _, p := range posts {
		r := postRow{ID: p.ID, Title: p.Title, Status: p.Status, IsTrash: p.Status == "trash",
			EditURL: links.EditLink(p.ID), Comments: p.CommentCount,
			CanEdit: u.Can("edit_post", p), CanDelete: u.Can("delete_post", p)}
		if strings.TrimSpace(r.Title) == "" {
			r.Title = "(no title)"
		}
		if depth, ok := p.Meta["_depth"].(int); ok && depth > 0 {
			r.Depth = strings.Repeat("&#8212; ", depth)
		}
		switch p.Status {
		case "draft":
			r.States = append(r.States, "Draft")
		case "pending":
			r.States = append(r.States, "Pending")
		case "future":
			r.States = append(r.States, "Scheduled")
		case "private":
			r.States = append(r.States, "Private")
		}
		if p.Password != "" {
			r.States = append(r.States, "Password protected")
		}
		if p.Sticky && typ == "post" {
			r.States = append(r.States, "Sticky")
		}
		if typ == "page" {
			switch p.ID {
			case frontID:
				r.States = append(r.States, "Front Page")
			case postsPageID:
				r.States = append(r.States, "Posts Page")
			case privacyID:
				r.States = append(r.States, "Privacy Policy Page")
			}
		}
		nonce := s.nonce(c, "trash-post_"+itoa64(p.ID))
		r.TrashURL = "/wp-admin/post?post=" + itoa64(p.ID) + "&action=trash&_wpnonce=" + nonce
		r.RestoreURL = "/wp-admin/post?post=" + itoa64(p.ID) + "&action=untrash&_wpnonce=" + s.nonce(c, "untrash-post_"+itoa64(p.ID))
		r.DeleteURL = "/wp-admin/post?post=" + itoa64(p.ID) + "&action=delete&_wpnonce=" + s.nonce(c, "delete-post_"+itoa64(p.ID))
		r.BuilderURL = s.gweRowAction(c, p)
		if builderOn && gweBuilt(p) {
			r.States = append(r.States, "Golementor")
		}
		if p.Status == "publish" || p.Status == "private" {
			r.ViewURL, r.ViewLabel = lb.Permalink(p), "View"
		} else if p.Status != "trash" {
			r.ViewURL, r.ViewLabel = lb.Permalink(p)+previewSep(lb.Permalink(p))+"preview=true", "Preview"
		}
		if a := authors[p.AuthorID]; a != nil {
			r.Author = a.DisplayName
			r.AuthorFilterURL = base + map[bool]string{true: "&", false: "?"}[strings.Contains(base, "?")] + "author=" + itoa64(a.ID)
		}
		r.Categories = termLinksHTML(cats[p.ID], "/wp-admin/edit?category_name=")
		r.Tags = termLinksHTML(tags[p.ID], "/wp-admin/edit?tag=")

		t := p.Date().In(loc)
		stamp := wp.FormatDate(t, "Y/m/d") + " at " + wp.FormatDate(t, "g:i a")
		switch p.Status {
		case "publish", "private":
			r.DateLabel = "Published"
		case "future":
			r.DateLabel = "Scheduled"
		default:
			r.DateLabel = "Last Modified"
			stamp = wp.FormatDate(p.ModifiedAt.In(loc), "Y/m/d") + " at " + wp.FormatDate(p.ModifiedAt.In(loc), "g:i a")
		}
		r.DateValue = stamp
		classes := []string{"iedit", "level-0", "post-" + itoa64(p.ID), "type-" + p.Type, "status-" + p.Status}
		if p.AuthorID == u.ID {
			classes = append(classes, "author-self")
		} else {
			classes = append(classes, "author-other")
		}
		if typ == "post" {
			classes = append(classes, "format-"+p.Format)
		}
		classes = append(classes, "hentry")
		for _, ct := range cats[p.ID] {
			classes = append(classes, "category-"+ct.Slug)
		}
		r.Classes = strings.Join(classes, " ")
		d.Rows = append(d.Rows, r)
	}

	if typ == "post" {
		months, _ := s.store.MonthlyArchives(ctx, loc)
		for _, m := range months {
			d.Months = append(d.Months, monthOption{fmt.Sprintf("%d%02d", m.Year, m.Month), time.Month(m.Month).String() + " " + strconv.Itoa(m.Year)})
		}
		d.Categories, _ = s.store.HierarchicalTerms(ctx, "category", false)
	}
	cur := *c.Request.URL
	d.Pagination = newPagination(total, perPage, page, cur)
	d.SortLinks = map[string]sortLink{}
	for _, col := range []string{"title", "author", "comment_count", "date"} {
		d.SortLinks[col] = s.sortLinkFor(c, col, q.OrderBy, strings.ToLower(q.Order))
	}

	screen := "edit"
	if typ == "page" {
		screen = "edit?post_type=page"
	}
	s.adminPage(c, screen, lab.Name, "edit-php post-type-"+typ, "admin/posts-list", d, postNotices(c, lab)...)
}

func previewSep(u string) string {
	if strings.Contains(u, "?") {
		return "&"
	}
	return "?"
}

// sortLinkFor builds a sortable column header link like WP_List_Table::print_column_headers().
func (s *Server) sortLinkFor(c *gin.Context, col, orderby, order string) sortLink {
	if orderby == "" {
		orderby = "date"
	}
	u := *c.Request.URL
	q := u.Query()
	q.Del("paged")
	q.Set("orderby", col)
	cur := orderby == col
	next := "asc"
	if cur && order == "asc" {
		next = "desc"
	}
	if !cur && (col == "date" || col == "comment_count") {
		next = "desc"
	}
	q.Set("order", next)
	u.RawQuery = q.Encode()
	sl := sortLink{URL: u.String()}
	if cur {
		sl.Class = "sorted " + order
		if order == "asc" {
			sl.AriaSort, sl.SRText = "ascending", "Sort descending."
		} else {
			sl.AriaSort, sl.SRText = "descending", "Sort ascending."
		}
	} else {
		if next == "asc" {
			sl.Class, sl.SRText = "sortable desc", "Sort ascending."
		} else {
			sl.Class, sl.SRText = "sortable asc", "Sort descending."
		}
	}
	return sl
}

// pageTree orders pages parent-first with their depth recorded in Meta["_depth"].
func (s *Server) pageTree(c *gin.Context, q store.PostQuery, page, perPage int) ([]*store.Post, int, error) {
	q.PerPage, q.Page = -1, 1
	q.OrderBy, q.Order = "menu_order", "asc"
	all, _, err := s.store.QueryPosts(c, q)
	if err != nil {
		return nil, 0, err
	}
	children := map[int64][]*store.Post{}
	ids := map[int64]bool{}
	for _, p := range all {
		ids[p.ID] = true
	}
	for _, p := range all {
		parent := p.ParentID
		if !ids[parent] {
			parent = 0
		}
		children[parent] = append(children[parent], p)
	}
	for k := range children {
		list := children[k]
		// menu_order, then title, like WordPress' page list.
		for i := 1; i < len(list); i++ {
			for j := i; j > 0 && (list[j].MenuOrder < list[j-1].MenuOrder ||
				(list[j].MenuOrder == list[j-1].MenuOrder && strings.ToLower(list[j].Title) < strings.ToLower(list[j-1].Title))); j-- {
				list[j], list[j-1] = list[j-1], list[j]
			}
		}
	}
	var ordered []*store.Post
	var walk func(parent int64, depth int)
	walk = func(parent int64, depth int) {
		for _, p := range children[parent] {
			p.Meta["_depth"] = depth
			ordered = append(ordered, p)
			walk(p.ID, depth+1)
		}
	}
	walk(0, 0)
	total := len(ordered)
	start := (page - 1) * perPage
	if start > total {
		start = total
	}
	end := start + perPage
	if end > total {
		end = total
	}
	return ordered[start:end], total, nil
}

func termLinksHTML(terms []*store.Term, base string) template.HTML {
	if len(terms) == 0 {
		return ""
	}
	var parts []string
	for _, t := range terms {
		parts = append(parts, `<a href="`+template.HTMLEscapeString(base+url.QueryEscape(t.Slug))+`">`+template.HTMLEscapeString(t.Name)+`</a>`)
	}
	return template.HTML(strings.Join(parts, ", "))
}

func bulkAction(c *gin.Context) string {
	for _, k := range []string{"action", "action2"} {
		if v := c.Query(k); v != "" && v != "-1" && c.Query("bulk_action") != "" {
			return v
		}
	}
	return ""
}

func (s *Server) postsBulk(c *gin.Context) {
	c.Redirect(http.StatusSeeOther, "/wp-admin/edit")
}

// postsBulkAction applies a bulk action from the list table (trash, untrash, delete).
func (s *Server) postsBulkAction(c *gin.Context, typ, action string) {
	if !s.checkNonce(c, "bulk-posts") {
		s.denyNonce(c)
		return
	}
	u := currentUser(c)
	n := 0
	var done []string
	for _, raw := range c.QueryArray("post[]") {
		id, _ := strconv.ParseInt(raw, 10, 64)
		p, err := s.store.GetPost(c, id)
		if err != nil || p.Type != typ || !u.Can("delete_post", p) {
			continue
		}
		s.afterPostSave(c, p)
		switch action {
		case "trash":
			err = s.store.TrashPost(c, id)
		case "untrash":
			err = s.store.UntrashPost(c, id)
		case "delete":
			err = s.store.DeletePost(c, id)
		default:
			continue
		}
		if err == nil {
			n++
			done = append(done, raw)
		}
	}
	key := map[string]string{"trash": "trashed", "untrash": "untrashed", "delete": "deleted"}[action]
	target := "/wp-admin/edit?post_type=" + typ
	if key != "" {
		target += "&" + key + "=" + strconv.Itoa(n) + "&ids=" + strings.Join(done, ",")
	}
	c.Redirect(http.StatusFound, target)
}

func postNotices(c *gin.Context, lab typeLabels) []notice {
	var out []notice
	lower := strings.ToLower(lab.Singular)
	lowers := strings.ToLower(lab.Name)
	one := func(n int, single, multi string) string {
		if n == 1 {
			return "1 " + lower + " " + single
		}
		return strconv.Itoa(n) + " " + lowers + " " + multi
	}
	if n, _ := strconv.Atoi(c.Query("trashed")); n > 0 {
		msg := one(n, "moved to the Trash.", "moved to the Trash.")
		if ids := c.Query("ids"); ids != "" {
			msg += ` <a href="/wp-admin/edit?post_type=` + lab.Type + `&amp;doaction=undo&amp;action=untrash&amp;ids=` + template.HTMLEscapeString(ids) + `">Undo</a>`
		}
		out = append(out, notice{Type: "success", Message: template.HTML(msg), Dismissible: true})
	}
	if n, _ := strconv.Atoi(c.Query("untrashed")); n > 0 {
		out = append(out, notice{Type: "success", Message: template.HTML(one(n, "restored from the Trash.", "restored from the Trash.")), Dismissible: true})
	}
	if n, _ := strconv.Atoi(c.Query("deleted")); n > 0 {
		out = append(out, notice{Type: "success", Message: template.HTML(one(n, "permanently deleted.", "permanently deleted.")), Dismissible: true})
	}
	return out
}

// Editing ----------------------------------------------------------------------

type editData struct {
	Post         *store.Post
	Labels       typeLabels
	IsNew        bool
	Nonce        string
	Categories   []*store.Term
	Checked      map[int64]bool
	TagNames     string
	Authors      []*store.User
	Pages        []*store.Post
	CanPublish   bool
	CanAuthor    bool
	Permalink    string
	ViewURL      string
	StatusLabel  string
	Visibility   string
	DateLabel    string
	DateInput    string
	TimeInput    string
	TrashURL     string
	Revisions    int
	Formats      []string
	PublishLabel string
	SlugBase     string
	SampleLink   template.HTML
	SampleNonce  string
	Editor       template.HTML
	Thumbnail    template.HTML
	BlockURL     string
	LastEdit     string
	TS           touchTime
	PluginStats  string
}

// touchTime holds the fields of touch_time(): the classic editor's date picker.
type touchTime struct {
	MM, JJ, AA, HH, MN, SS string
	Months                 []monthChoice
}

type monthChoice struct {
	Value, Label string
	Selected     bool
}

func (s *Server) postNew(c *gin.Context) {
	u := currentUser(c)
	typ := c.DefaultQuery("post_type", "post")
	if typ != "post" && typ != "page" {
		c.String(http.StatusBadRequest, "Invalid post type.")
		return
	}
	if !u.Can("edit_"+typ+"s", nil) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to create posts as this user.")
		return
	}
	p, err := s.store.CreateAutoDraft(c, typ, u.ID)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	p.Title = c.Query("post_title")
	p.Content = c.Query("content")
	s.renderEdit(c, p, true)
}

// postEdit is post.php: action=edit shows the editor, trash/untrash/delete act on the post.
func (s *Server) postEdit(c *gin.Context) {
	u := currentUser(c)
	id, _ := strconv.ParseInt(c.Query("post"), 10, 64)
	p, err := s.store.GetPost(c, id)
	if errors.Is(err, store.ErrNotFound) {
		if a, aerr := s.store.GetAttachment(c, id); aerr == nil {
			if c.Query("action") == "delete" {
				if !s.checkNonce(c, "delete-post_"+itoa64(id)) {
					s.denyNonce(c)
					return
				}
				if a.UploaderID != currentUser(c).ID && !currentUser(c).Can("delete_others_posts", nil) {
					c.String(http.StatusForbidden, "Sorry, you are not allowed to delete this item.")
					return
				}
				s.deleteAttachmentFiles(a)
				_ = s.store.DeleteAttachment(c, id)
				c.Redirect(http.StatusFound, "/wp-admin/upload?deleted=1")
				return
			}
			s.attachmentEdit(c, a)
			return
		}
		c.String(http.StatusNotFound, "You attempted to edit an item that does not exist. Perhaps it was deleted?")
		return
	}
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	switch c.DefaultQuery("action", "edit") {
	case "trash", "untrash", "delete":
		action := c.Query("action")
		if !s.checkNonce(c, action+"-post_"+itoa64(p.ID)) {
			s.denyNonce(c)
			return
		}
		if !u.Can("delete_post", p) {
			c.String(http.StatusForbidden, "Sorry, you are not allowed to move this item to the Trash.")
			return
		}
		key := ""
		s.afterPostSave(c, p)
		switch action {
		case "trash":
			err, key = s.store.TrashPost(c, p.ID), "trashed"
		case "untrash":
			err, key = s.store.UntrashPost(c, p.ID), "untrashed"
		case "delete":
			err, key = s.store.DeletePost(c, p.ID), "deleted"
		}
		if err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
		c.Redirect(http.StatusFound, "/wp-admin/edit?post_type="+p.Type+"&"+key+"=1&ids="+itoa64(p.ID))
		return
	}
	if !u.Can("edit_post", p) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to edit this item.")
		return
	}
	if p.Status == "trash" {
		c.String(http.StatusForbidden, "You cannot edit this item because it is in the Trash. Please restore it and try again.")
		return
	}
	if c.Query("action") == "elementor" && s.golementorOn() { // Golementor
		s.gweEditor(c, p)
		return
	}
	s.renderEdit(c, p, false)
}

func (s *Server) renderEdit(c *gin.Context, p *store.Post, isNew bool) {
	block := s.useBlockEditor(c, p)
	s.gweEditorButton(c, p, block)
	if block {
		if _, ok := c.GetQuery("classic-editor__forget"); ok {
			delete(p.Meta, "classic-editor-remember")
		}
		s.rememberEditor(c, p, "block-editor")
		s.renderBlockEditor(c, p, isNew)
		return
	}
	s.rememberEditor(c, p, "classic-editor")
	s.renderClassicEdit(c, p, isNew)
}

// rememberEditor stores the editor last used for a post when users may switch editors.
func (s *Server) rememberEditor(c *gin.Context, p *store.Post, which string) {
	if s.settings.String("classic-editor-allow-users") != "allow" || p.Meta["classic-editor-remember"] == which {
		return
	}
	p.Meta["classic-editor-remember"] = which
	_, _ = s.store.Pool.Exec(c, `UPDATE posts SET meta = meta || jsonb_build_object('classic-editor-remember', $2::text) WHERE id = $1`, p.ID, which)
}

func (s *Server) renderClassicEdit(c *gin.Context, p *store.Post, isNew bool) {
	ctx := c.Request.Context()
	u := currentUser(c)
	lab := labelsFor(p.Type)
	lb := s.links(ctx)
	loc := s.loc()
	d := editData{Post: p, Labels: lab, IsNew: isNew, Nonce: s.nonce(c, "update-post_"+itoa64(p.ID)),
		CanPublish: u.Can("publish_post", p), CanAuthor: u.Can("edit_others_"+p.Type+"s", nil),
		Checked: map[int64]bool{}, TrashURL: "/wp-admin/post?post=" + itoa64(p.ID) + "&action=trash&_wpnonce=" + s.nonce(c, "trash-post_"+itoa64(p.ID)),
		Formats: []string{"standard", "aside", "image", "video", "quote", "link", "gallery", "status", "audio", "chat"}}

	if p.Type == "post" {
		d.Categories, _ = s.store.HierarchicalTerms(ctx, "category", false)
		cats, _ := s.store.TermsForPosts(ctx, []int64{p.ID}, "category")
		for _, t := range cats[p.ID] {
			d.Checked[t.ID] = true
		}
		if isNew {
			d.Checked[int64(s.settings.Int("default_category"))] = true
		}
		tags, _ := s.store.TermsForPosts(ctx, []int64{p.ID}, "post_tag")
		var names []string
		for _, t := range tags[p.ID] {
			names = append(names, t.Name)
		}
		d.TagNames = strings.Join(names, ", ")
	} else {
		d.Pages, _, _ = s.store.QueryPosts(ctx, store.PostQuery{Types: []string{"page"}, Statuses: []string{"publish", "private", "draft", "pending", "future"}, ExcludeID: []int64{p.ID}, OrderBy: "title", Order: "asc", PerPage: -1})
	}
	if d.CanAuthor {
		d.Authors, _ = s.store.Authors(ctx)
	}
	if p.Status != "auto-draft" {
		d.Permalink = lb.Permalink(p)
		if p.Status == "publish" || p.Status == "private" {
			d.ViewURL = d.Permalink
		} else {
			d.ViewURL = d.Permalink + previewSep(d.Permalink) + "preview=true"
		}
	}
	if p.Status != "auto-draft" {
		d.SampleLink = s.samplePermalinkHTML(c, p, p.Title, p.Slug)
	}
	d.SampleNonce = s.nonce(c, "samplepermalink")
	d.SlugBase = strings.TrimSuffix(strings.Replace(lb.PermalinkTemplate(p), "%postname%", "", 1), "/")
	d.SlugBase = strings.Replace(d.SlugBase, "%pagename%", "", 1)
	d.Revisions, _ = s.store.Revisions(ctx, p.ID)

	switch p.Status {
	case "publish":
		d.StatusLabel = "Published"
	case "future":
		d.StatusLabel = "Scheduled"
	case "pending":
		d.StatusLabel = "Pending Review"
	case "private":
		d.StatusLabel = "Privately Published"
	default:
		d.StatusLabel = "Draft"
	}
	switch {
	case p.Status == "private":
		d.Visibility = "private"
	case p.Password != "":
		d.Visibility = "password"
	default:
		d.Visibility = "public"
	}
	if p.PublishedAt != nil {
		t := p.PublishedAt.In(loc)
		d.DateInput, d.TimeInput = t.Format("2006-01-02"), t.Format("15:04")
		switch p.Status {
		case "future":
			d.DateLabel = "Scheduled for: <b>" + wp.FormatDate(t, "M j, Y") + " at " + wp.FormatDate(t, "H:i") + "</b>"
		case "publish", "private":
			d.DateLabel = "Published on: <b>" + wp.FormatDate(t, "M j, Y") + " at " + wp.FormatDate(t, "H:i") + "</b>"
		default:
			d.DateLabel = "Schedule for: <b>" + wp.FormatDate(t, "M j, Y") + " at " + wp.FormatDate(t, "H:i") + "</b>"
		}
	} else {
		d.DateLabel = "Publish <b>immediately</b>"
	}
	switch {
	case p.Status == "publish" || p.Status == "private" || p.Status == "future":
		d.PublishLabel = "Update"
	case !d.CanPublish:
		d.PublishLabel = "Submit for Review"
	case p.PublishedAt != nil && p.PublishedAt.After(time.Now()):
		d.PublishLabel = "Schedule"
	default:
		d.PublishLabel = "Publish"
	}

	title := "Edit " + lab.Singular
	screen := "post?post_type=" + p.Type
	if isNew {
		title = lab.AddNew
		screen = "post-new"
		if p.Type == "page" {
			screen = "post-new?post_type=page"
		}
	}
	ts := time.Now().In(loc)
	if p.PublishedAt != nil {
		ts = p.PublishedAt.In(loc)
	}
	d.TS = touchTime{MM: ts.Format("01"), JJ: ts.Format("02"), AA: ts.Format("2006"), HH: ts.Format("15"), MN: ts.Format("04"), SS: ts.Format("05")}
	for m := 1; m <= 12; m++ {
		v := fmt.Sprintf("%02d", m)
		d.TS.Months = append(d.TS.Months, monthChoice{Value: v, Label: v + "-" + time.Month(m).String()[:3], Selected: v == d.TS.MM})
	}
	d.PluginStats = statsLine(s.pluginPostStats(c, p))
	d.Editor = s.wpEditor(c, p)
	d.Thumbnail = s.postThumbnailHTML(c, p, p.FeaturedMediaID)
	if s.settings.String("classic-editor-allow-users") == "allow" {
		d.BlockURL = "/wp-admin/post?post=" + itoa64(p.ID) + "&action=edit&classic-editor__forget"
	}
	if !isNew {
		d.LastEdit = "Last edited on " + wp.FormatDate(p.ModifiedAt.In(loc), "F j, Y") + " at " + wp.FormatDate(p.ModifiedAt.In(loc), "g:i a")
		if ed, err := s.store.UserByID(c, p.AuthorID); err == nil {
			d.LastEdit = "Last edited by " + ed.DisplayName + " on " + wp.FormatDate(p.ModifiedAt.In(loc), "F j, Y") + " at " + wp.FormatDate(p.ModifiedAt.In(loc), "g:i a")
		}
	}
	s.enqueueMedia(c, p.ID)
	s.adminPage(c, screen, title, "post-php post-type-"+p.Type, "admin/post-edit", d, editNotices(c, lab, d.ViewURL)...)
}

func editNotices(c *gin.Context, lab typeLabels, view string) []notice {
	msg := map[string]string{
		"1":  lab.Singular + " updated. <a href=\"" + template.HTMLEscapeString(view) + "\">View " + strings.ToLower(lab.Singular) + "</a>",
		"6":  lab.Singular + " published. <a href=\"" + template.HTMLEscapeString(view) + "\">View " + strings.ToLower(lab.Singular) + "</a>",
		"7":  lab.Singular + " saved.",
		"8":  lab.Singular + " submitted. <a target=\"_blank\" href=\"" + template.HTMLEscapeString(view) + "\">Preview " + strings.ToLower(lab.Singular) + "</a>",
		"9":  lab.Singular + " scheduled. <a target=\"_blank\" href=\"" + template.HTMLEscapeString(view) + "\">Preview " + strings.ToLower(lab.Singular) + "</a>",
		"10": lab.Singular + " draft updated. <a target=\"_blank\" href=\"" + template.HTMLEscapeString(view) + "\">Preview " + strings.ToLower(lab.Singular) + "</a>",
	}[c.Query("message")]
	if msg == "" {
		return nil
	}
	return []notice{{Type: "success", Message: template.HTML(msg), Dismissible: true}}
}

// postSave handles the classic edit form (editpost()).
func (s *Server) postSave(c *gin.Context) {
	ctx := c.Request.Context()
	u := currentUser(c)
	id, _ := strconv.ParseInt(c.PostForm("post_ID"), 10, 64)
	if c.PostForm("post_type") == "attachment" {
		if a, err := s.store.GetAttachment(ctx, id); err == nil {
			s.attachmentSave(c, a)
			return
		}
	}
	p, err := s.store.GetPost(ctx, id)
	if err != nil {
		c.String(http.StatusNotFound, "You attempted to edit an item that does not exist. Perhaps it was deleted?")
		return
	}
	if !s.checkNonce(c, "update-post_"+itoa64(p.ID)) {
		s.denyNonce(c)
		return
	}
	if !u.Can("edit_post", p) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to edit this item.")
		return
	}
	wasPublished := p.Status == "publish" || p.Status == "private" || p.Status == "future"
	p.Title = strings.TrimSpace(c.PostForm("post_title"))
	content := c.PostForm("content")
	if !u.Can("unfiltered_html", nil) {
		content = wp.KsesPost(content)
	}
	// Classic content is stored as written; wpautop runs on render for content without blocks.
	p.Content = strings.ReplaceAll(content, "\r\n", "\n")
	if raw := c.PostForm("_thumbnail_id"); raw != "" {
		if id, _ := strconv.ParseInt(raw, 10, 64); id > 0 {
			p.FeaturedMediaID = id
		} else {
			p.FeaturedMediaID = 0
		}
	}
	p.Excerpt = c.PostForm("excerpt")
	p.Slug = strings.TrimSpace(c.PostForm("post_name"))
	p.CommentStatus = map[bool]string{true: "open", false: "closed"}[c.PostForm("comment_status") == "open"]
	p.PingStatus = map[bool]string{true: "open", false: "closed"}[c.PostForm("ping_status") == "open"]
	if p.Type == "page" {
		p.ParentID, _ = strconv.ParseInt(c.PostForm("parent_id"), 10, 64)
		p.MenuOrder, _ = strconv.Atoi(c.PostForm("menu_order"))
	} else {
		p.Format = c.DefaultPostForm("post_format", "standard")
	}
	if a, _ := strconv.ParseInt(c.PostForm("post_author_override"), 10, 64); a > 0 && u.Can("edit_others_"+p.Type+"s", nil) {
		p.AuthorID = a
	}
	if p.AuthorID == 0 {
		p.AuthorID = u.ID
	}

	// Date: the form carries a local date and time; empty means "immediately".
	if c.PostForm("edit_date") == "1" && c.PostForm("aa") != "" {
		num := func(k string) int { n, _ := strconv.Atoi(c.PostForm(k)); return n }
		when := time.Date(num("aa"), time.Month(num("mm")), num("jj"), num("hh"), num("mn"), num("ss"), 0, s.loc())
		p.PublishedAt = &when
	} else if d, t := c.PostForm("post_date"), c.PostForm("post_time"); d != "" {
		if when, err := time.ParseInLocation("2006-01-02 15:04", d+" "+defaultStr(t, "00:00"), s.loc()); err == nil {
			p.PublishedAt = &when
		}
	}

	visibility := c.DefaultPostForm("visibility", "public")
	p.Password = ""
	if visibility == "password" {
		p.Password = c.PostForm("post_password")
	}
	p.Sticky = p.Type == "post" && c.PostForm("sticky") == "sticky" && visibility == "public"

	// Status from the button that was pressed, like _wp_translate_postdata().
	switch {
	case c.PostForm("publish") != "":
		if u.Can("publish_post", p) {
			p.Status = "publish"
		} else {
			p.Status = "pending"
		}
	case c.PostForm("save") != "":
		if !wasPublished {
			p.Status = "draft"
			if c.PostForm("post_status") == "pending" {
				p.Status = "pending"
			}
		} else if st := c.PostForm("post_status"); st == "draft" || st == "pending" {
			// Status › Edit on a published post can switch it back to Draft or Pending Review.
			p.Status = st
		}
	}
	if visibility == "private" && u.Can("publish_post", p) && (p.Status == "publish" || wasPublished) {
		p.Status = "private"
	} else if p.Status == "private" && visibility != "private" {
		p.Status = "publish"
	}
	if p.Status == "auto-draft" {
		p.Status = "draft"
	}

	if err := s.store.UpdatePost(ctx, p, u.ID); err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	if p.Type == "post" {
		var catIDs []int64
		for _, raw := range c.PostFormArray("post_category[]") {
			if id, err := strconv.ParseInt(raw, 10, 64); err == nil && id > 0 {
				catIDs = append(catIDs, id)
			}
		}
		if len(catIDs) == 0 {
			catIDs = []int64{int64(s.settings.Int("default_category"))}
		}
		_ = s.store.SetPostTerms(ctx, p.ID, "category", catIDs)
		tagIDs, _ := s.store.TermIDsByNames(ctx, "post_tag", strings.Split(c.PostForm("tax_input[post_tag]"), ","))
		_ = s.store.SetPostTerms(ctx, p.ID, "post_tag", tagIDs)
	}

	msg := "1"
	switch {
	case !wasPublished && (p.Status == "publish" || p.Status == "private"):
		msg = "6"
	case p.Status == "future":
		msg = "9"
	case p.Status == "pending":
		msg = "8"
	case p.Status == "draft":
		msg = "10"
	}
	s.afterPostSave(ctx, p)
	if target, ok := s.gweClassicRedirect(c, p.ID); ok { // classic editor's "Edit with Golementor" on a new post
		c.Redirect(http.StatusFound, target)
		return
	}
	c.Redirect(http.StatusFound, links.EditLink(p.ID)+"&message="+msg)
}

func defaultStr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
