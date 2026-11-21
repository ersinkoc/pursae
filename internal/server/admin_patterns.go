package server

import (
	"html/template"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"wpgo/internal/links"
	"wpgo/internal/store"
	"wpgo/internal/wp"
)

// The Patterns screen: edit.php?post_type=wp_block, WP_Posts_List_Table for synced and unsynced
// patterns (reusable blocks). WordPress maps the wp_block capabilities onto the post ones
// (register_post_type( 'wp_block' ): read and edit_posts are edit_posts, edit_others_posts,
// delete_others_posts, … stay the post caps), so the screen checks edit_posts and the per-post
// meta caps resolve like a post's.

// patternListColumns are the Screen Options column toggles of the Patterns list.
var patternListColumns = []screenColumn{{"sync_status", "Sync status"}, {"author", "Author"},
	{"taxonomy-wp_pattern_category", "Pattern Categories"}, {"date", "Date"}}

type patternRow struct {
	ID                   int64
	Title                string
	States               []string
	EditURL              string
	TrashURL, RestoreURL string
	DeleteURL            string
	Author               string
	AuthorFilterURL      string
	Categories           template.HTML
	Synced               bool
	DateLabel, DateValue string
	Classes              string
	CanEdit, CanDelete   bool
	IsTrash              bool
}

type patternsListData struct {
	Labels        typeLabels
	Statuses      []statusLink
	Rows          []patternRow
	Search        string
	Status        string
	Category      string        // the wp_pattern_category filter (term slug)
	Categories    []*store.Term // its dropdown
	Pagination    pagination
	BulkNonce     string
	CanCreate     bool
	IsTrash       bool
	CanEmptyTrash bool
	Orderby       string
	Order         string
	SortLinks     map[string]sortLink
	Hidden        map[string]bool
	ColCount      int
}

// patternsList is edit.php?post_type=wp_block.
func (s *Server) patternsList(c *gin.Context) {
	ctx := c.Request.Context()
	u := currentUser(c)
	const typ = "wp_block"
	if !u.Can("edit_posts", nil) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to edit posts in this post type.")
		return
	}
	if a := bulkAction(c); a != "" {
		switch a {
		case "trash", "untrash", "delete", "delete_all":
			s.postsBulkAction(c, typ, a)
		default: // no Bulk Edit for patterns
			c.Redirect(http.StatusFound, "/wp-admin/edit?post_type="+typ)
		}
		return
	}
	lab := labelsFor(typ)
	status := c.DefaultQuery("post_status", "all")
	search := strings.TrimSpace(c.Query("s"))
	page, _ := strconv.Atoi(c.Query("paged"))
	if page < 1 {
		page = 1
	}
	perPage := s.userOptionInt(u, "edit_"+typ+"_per_page", 20)
	q := store.PostQuery{Types: []string{typ}, Search: search, Page: page, PerPage: perPage, NoContent: true,
		OrderBy: c.Query("orderby"), Order: c.DefaultQuery("order", "desc"), Loc: s.loc()}
	if search != "" && c.Query("orderby") == "" {
		q.OrderBy = "relevance"
	}
	switch status {
	case "all", "":
		q.Statuses = []string{"publish", "future", "draft", "pending", "private"}
	default:
		q.Statuses = []string{status}
	}
	if !u.Can("read_private_posts", nil) && (status == "all" || status == "") {
		q.Statuses = []string{"publish", "future", "draft", "pending"}
	}
	// Without edit_others_posts only published patterns and the user's own are listed.
	othersVisible := u.Can("edit_others_posts", nil)
	if !othersVisible {
		q.ReadableBy, q.ReadablePrivate = u.ID, u.Can("read_private_posts", nil)
	}
	if a, _ := strconv.ParseInt(c.Query("author"), 10, 64); a > 0 {
		q.AuthorID = a
	}
	cat := c.Query("wp_pattern_category")
	if cat != "" {
		if t, err := s.store.TermBySlug(ctx, "wp_pattern_category", cat); err == nil {
			q.TermIDs = []int64{t.ID}
		} else {
			q.TermIDs = []int64{-1}
		}
	}
	posts, total, err := s.store.QueryPosts(ctx, q)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}

	d := patternsListData{Labels: lab, Search: search, Status: status, Category: cat, BulkNonce: s.nonce(c, "bulk-posts"),
		CanCreate: u.Can("edit_posts", nil), IsTrash: status == "trash",
		CanEmptyTrash: othersVisible, Orderby: q.OrderBy, Order: strings.ToLower(q.Order), Hidden: s.hiddenColumns(u, "edit-"+typ)}
	d.Categories, _, _ = s.store.QueryTerms(ctx, store.TermQuery{Taxonomy: "wp_pattern_category", OrderBy: "name", Order: "asc", PerPage: 1000, Page: 1})

	counts, _ := s.store.StatusCounts(ctx, typ, 0)
	if !othersVisible {
		counts, _ = s.store.StatusCountsReadable(ctx, typ, u.ID, q.ReadablePrivate)
	}
	base := "/wp-admin/edit?post_type=" + typ
	all := counts["publish"] + counts["future"] + counts["draft"] + counts["pending"] + counts["private"]
	d.Statuses = append(d.Statuses, statusLink{"all", "All", base, all, status == "all" || status == ""})
	mine, _ := s.store.StatusCounts(ctx, typ, u.ID)
	if n := mine["publish"] + mine["future"] + mine["draft"] + mine["pending"] + mine["private"]; n > 0 && n != all {
		d.Statuses = append(d.Statuses, statusLink{"mine", "Mine", base + "&author=" + itoa64(u.ID), n, q.AuthorID == u.ID})
	}
	for _, st := range []struct{ key, label string }{
		{"publish", "Published"}, {"future", "Scheduled"}, {"draft", "Drafts"}, {"pending", "Pending"},
		{"private", "Private"}, {"trash", "Trash"},
	} {
		if counts[st.key] > 0 {
			d.Statuses = append(d.Statuses, statusLink{st.key, st.label, base + "&post_status=" + st.key, counts[st.key], status == st.key})
		}
	}
	s.patternRows(c, &d, posts, base)

	d.Pagination = newPagination(total, perPage, page, *c.Request.URL)
	d.SortLinks = map[string]sortLink{}
	for _, col := range []string{"title", "author", "date"} {
		d.SortLinks[col] = s.sortLinkFor(c, col, q.OrderBy, strings.ToLower(q.Order))
	}
	d.ColCount = 2 // cb + title
	for _, col := range patternListColumns {
		if !d.Hidden[col.ID] {
			d.ColCount++
		}
	}
	s.setScreenOptions(c, screenOptions{Screen: "edit-" + typ, Columns: patternListColumns, Hidden: d.Hidden,
		PerPageOption: "edit_" + typ + "_per_page", PerPage: perPage})
	// edit.php enqueues wp-list-reusable-blocks for wp_block: "Export as JSON" on each row and the
	// "Import from JSON" form next to the Add button.
	s.pageAssets(c).Script("wp-list-reusable-blocks").Style("wp-list-reusable-blocks")
	s.adminPage(c, "edit?post_type="+typ, lab.Name, "edit-php post-type-"+typ, "admin/patterns-list", d, postNotices(c, lab, d.BulkNonce)...)
}

// patternRows builds the rows of the Patterns list (WP_Posts_List_Table::single_row()).
func (s *Server) patternRows(c *gin.Context, d *patternsListData, posts []*store.Post, base string) {
	ctx := c.Request.Context()
	u := currentUser(c)
	ids := make([]int64, 0, len(posts))
	authorIDs := make([]int64, 0, len(posts))
	for _, p := range posts {
		ids = append(ids, p.ID)
		authorIDs = append(authorIDs, p.AuthorID)
	}
	authors, _ := s.store.UserNames(ctx, authorIDs)
	cats, _ := s.store.TermsForPosts(ctx, ids, "wp_pattern_category")
	loc := s.loc()
	for _, p := range posts {
		r := patternRow{ID: p.ID, Title: p.Title, IsTrash: p.Status == "trash", EditURL: links.EditLink(p.ID),
			CanEdit: u.Can("edit_post", p), CanDelete: u.Can("delete_post", p),
			Synced: metaString(p.Meta, "wp_pattern_sync_status") != "unsynced"}
		if strings.TrimSpace(r.Title) == "" {
			r.Title = "(no title)"
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
		id := itoa64(p.ID)
		r.TrashURL = "/wp-admin/post?post=" + id + "&action=trash&_wpnonce=" + s.nonce(c, "trash-post_"+id)
		r.RestoreURL = "/wp-admin/post?post=" + id + "&action=untrash&_wpnonce=" + s.nonce(c, "untrash-post_"+id)
		r.DeleteURL = "/wp-admin/post?post=" + id + "&action=delete&_wpnonce=" + s.nonce(c, "delete-post_"+id)
		if a := authors[p.AuthorID]; a != nil {
			r.Author = a.DisplayName
			r.AuthorFilterURL = base + "&author=" + itoa64(a.ID)
		}
		r.Categories = termLinksHTML(cats[p.ID], base+"&wp_pattern_category=")
		t := p.Date().In(loc)
		stamp := wp.FormatDate(t, "Y/m/d") + " at " + wp.FormatDate(t, "g:i a")
		switch p.Status {
		case "publish", "private":
			r.DateLabel = "Published"
		case "future":
			r.DateLabel = "Scheduled"
		default:
			r.DateLabel = "Last Modified"
			m := p.ModifiedAt.In(loc)
			stamp = wp.FormatDate(m, "Y/m/d") + " at " + wp.FormatDate(m, "g:i a")
		}
		r.DateValue = stamp
		classes := []string{"iedit", "level-0", "post-" + id, "type-wp_block", "status-" + p.Status}
		if p.AuthorID == u.ID {
			classes = append(classes, "author-self")
		} else {
			classes = append(classes, "author-other")
		}
		for _, ct := range cats[p.ID] {
			classes = append(classes, "wp_pattern_category-"+ct.Slug)
		}
		r.Classes = strings.Join(classes, " ")
		d.Rows = append(d.Rows, r)
	}
}
