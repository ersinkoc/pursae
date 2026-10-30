package server

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"wpgo/internal/links"
	"wpgo/internal/store"
	"wpgo/internal/wp"
)

// Products › Attributes (WC_Admin_Attributes) and Products › Reviews (WooCommerce's Product Reviews
// list, Automattic\WooCommerce\Internal\Admin\ProductReviews\ReviewsListTable).

// Attributes ---------------------------------------------------------------------------------------------

type gooAttrListRow struct {
	A            *gooAttribute
	Terms        string
	MoreTerms    bool
	TermsURL     string
	EditURL      string
	DeleteURL    string
	OrderByLabel string
}

type gooAttributesData struct {
	Rows      []gooAttrListRow
	Edit      *gooAttribute
	Form      gooAttribute
	AddNonce  string
	EditNonce string
	OrderBy   []gooOpt
}

var gooAttrOrderBy = []gooOpt{{"menu_order", "Custom ordering"}, {"name", "Name"}, {"name_num", "Name (numeric)"}, {"id", "Term ID"}}

// gooReservedTerms are wc_check_if_attribute_name_is_reserved()'s query vars.
var gooReservedTerms = map[string]bool{}

func init() {
	for _, t := range strings.Fields(`attachment attachment_id author author_name calendar cat category category__and category__in category__not_in
		category_name comments_per_page comments_popup cpage day debug error exact feed hour link_category m minute monthnum more name nav_menu
		nopaging offset order orderby p page page_id paged pagename pb perm post post__in post__not_in post_format post_mime_type post_status
		post_tag post_type posts posts_per_archive_page posts_per_page preview robots s search second sentence showposts static subpost
		subpost_id tag tag__and tag__in tag__not_in tag_id tag_slug__and tag_slug__in taxonomy tb term type w withcomments withoutcomments year`) {
		gooReservedTerms[t] = true
	}
}

// gooAttributesScreen is /wp-admin/goocommerce?page=attributes: add, edit (&edit=<id>) and delete.
func (s *Server) gooAttributesScreen(c *gin.Context) {
	ctx := c.Request.Context()
	var notices []notice
	d := gooAttributesData{AddNonce: s.nonce(c, "woocommerce-add-new_attribute"), OrderBy: gooAttrOrderBy, Form: gooAttribute{OrderBy: "menu_order"}}
	editID, _ := strconv.ParseInt(c.Query("edit"), 10, 64)

	if c.Request.Method == http.MethodPost {
		a := &gooAttribute{Name: strings.TrimSpace(c.PostForm("attribute_label")), Slug: strings.TrimSpace(c.PostForm("attribute_name")),
			OrderBy: c.DefaultPostForm("attribute_orderby", "menu_order"), HasArchives: c.PostForm("attribute_public") == "1", Type: c.DefaultPostForm("attribute_type", "select")}
		var err error
		switch {
		case c.PostForm("save_attribute") != "" && editID > 0:
			if !s.checkNonce(c, "woocommerce-save-attribute_"+itoa64(editID)) {
				s.denyNonce(c)
				return
			}
			a.ID = editID
			if err = s.gooValidateAttribute(a); err == nil {
				err = s.gooSaveAttribute(ctx, a)
			}
			if err == nil {
				s.purgeAll(ctx)
				c.Redirect(http.StatusFound, "/wp-admin/goocommerce?page=attributes&updated=1")
				return
			}
		case c.PostForm("add_new_attribute") != "":
			if !s.checkNonce(c, "woocommerce-add-new_attribute") {
				s.denyNonce(c)
				return
			}
			if err = s.gooValidateAttribute(a); err == nil {
				err = s.gooSaveAttribute(ctx, a)
			}
			if err == nil {
				c.Redirect(http.StatusFound, "/wp-admin/goocommerce?page=attributes&added=1")
				return
			}
			d.Form = *a
		default:
			err = fmt.Errorf("Invalid request.")
		}
		notices = append(notices, notice{Type: "error", Message: template.HTML(template.HTMLEscapeString(err.Error())), Dismissible: true})
	}
	if del, _ := strconv.ParseInt(c.Query("delete"), 10, 64); del > 0 {
		if !s.checkNonce(c, "woocommerce-delete-attribute_"+itoa64(del)) {
			s.denyNonce(c)
			return
		}
		if err := s.gooDeleteAttribute(ctx, del); err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
		s.purgeAll(ctx)
		c.Redirect(http.StatusFound, "/wp-admin/goocommerce?page=attributes&deleted=1")
		return
	}
	switch {
	case c.Query("added") == "1":
		notices = append(notices, notice{Type: "success", Message: "Attribute added successfully", Dismissible: true})
	case c.Query("updated") == "1":
		notices = append(notices, notice{Type: "success", Message: "Attribute updated successfully", Dismissible: true})
	case c.Query("deleted") == "1":
		notices = append(notices, notice{Type: "success", Message: "Attribute deleted successfully", Dismissible: true})
	}

	attrs, err := s.gooAttributes(ctx)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	if editID > 0 {
		for _, a := range attrs {
			if a.ID == editID {
				cp := *a
				d.Edit = &cp
			}
		}
		if d.Edit == nil {
			c.String(http.StatusNotFound, "Error: non-existing attribute ID.")
			return
		}
		d.EditNonce = s.nonce(c, "woocommerce-save-attribute_"+itoa64(editID))
		s.gooCatalogAssets(c)
		s.adminPage(c, "goocommerce?page=attributes", "Edit attribute", "product_page_product_attributes woocommerce", "admin/goo-attributes", d, notices...)
		return
	}
	for _, a := range attrs {
		r := gooAttrListRow{A: a, TermsURL: "/wp-admin/edit-tags?taxonomy=" + a.Taxonomy() + "&post_type=product",
			EditURL:   "/wp-admin/goocommerce?page=attributes&edit=" + itoa64(a.ID),
			DeleteURL: "/wp-admin/goocommerce?page=attributes&delete=" + itoa64(a.ID) + "&_wpnonce=" + s.nonce(c, "woocommerce-delete-attribute_"+itoa64(a.ID))}
		for _, o := range gooAttrOrderBy {
			if o.Value == a.OrderBy {
				r.OrderByLabel = o.Label
			}
		}
		terms, _ := s.gooAttributeTerms(ctx, a.Taxonomy())
		var names []string
		for i, t := range terms {
			if i >= 10 {
				r.MoreTerms = true
				break
			}
			names = append(names, t.Name)
		}
		r.Terms = strings.Join(names, ", ")
		d.Rows = append(d.Rows, r)
	}
	s.gooCatalogAssets(c)
	s.adminPage(c, "goocommerce?page=attributes", "Attributes", "product_page_product_attributes woocommerce", "admin/goo-attributes", d, notices...)
}

// gooValidateAttribute is WC_Admin_Attributes::valid_attribute_name() and friends.
func (s *Server) gooValidateAttribute(a *gooAttribute) error {
	if a.Name == "" {
		return fmt.Errorf("Please, provide an attribute name.")
	}
	slug := gooSanitizeKey(defaultStr(a.Slug, a.Name))
	if gooReservedTerms[strings.TrimPrefix(slug, "pa_")] {
		return fmt.Errorf("Slug \"%s\" is not allowed because it is a reserved term. Change it, please.", slug)
	}
	switch a.OrderBy {
	case "menu_order", "name", "name_num", "id":
	default:
		a.OrderBy = "menu_order"
	}
	return nil
}

// Reviews ------------------------------------------------------------------------------------------------

type gooReviewRow struct {
	ID, PostID               int64
	Type                     string
	Status                   string
	Avatar                   string
	Author, AuthorEmail      string
	AuthorURL, AuthorIP      string
	Rating                   int
	Verified                 bool
	Content                  template.HTML
	ParentNote               template.HTML
	ProductName, ProductEdit string
	ProductView              string
	Date                     string
	Actions                  template.HTML
	Class                    string
}

type gooReviewsData struct {
	Rows        []gooReviewRow
	Status      string
	Counts      map[string]int
	Search      string
	ReviewType  string
	Rating      string
	ProductType string
	ProductID   int64
	ProductName string
	Types       []gooOpt
	Pagination  pagination
	BulkNonce   string
}

// gooReviewsScreen is /wp-admin/goocommerce?page=reviews.
func (s *Server) gooReviewsScreen(c *gin.Context) {
	ctx := c.Request.Context()
	u := currentUser(c)
	if a := c.Query("action"); a != "" && a != "-1" && c.Query("c") != "" {
		s.gooReviewAction(c, a)
		return
	}
	if a := gooReviewBulkAction(c); a != "" {
		s.gooReviewsBulk(c, a)
		return
	}
	status := c.DefaultQuery("comment_status", "all")
	page, _ := strconv.Atoi(c.Query("paged"))
	page = max(page, 1)
	perPage := s.userOptionInt(u, "edit_comments_per_page", 20)
	d := gooReviewsData{Status: status, Search: strings.TrimSpace(c.Query("s")), ReviewType: c.DefaultQuery("review_type", "all"),
		Rating: c.Query("review_rating"), ProductType: c.Query("product_type"), BulkNonce: s.nonce(c, "bulk-product-reviews"), Types: s.gooProductTypeOptions()}
	d.ProductID, _ = strconv.ParseInt(c.Query("product_id"), 10, 64)
	if d.ProductID > 0 {
		if p, err := s.store.GetPost(ctx, d.ProductID); err == nil {
			d.ProductName = p.Title
		}
	}

	args := []any{}
	add := func(v any) string { args = append(args, v); return "$" + strconv.Itoa(len(args)) }
	conds := []string{"p.type = 'product'", "c.type IN ('review','comment')"}
	switch status {
	case "all", "":
		conds = append(conds, "c.status IN ('pending','approved')")
	case "moderated", "pending":
		conds = append(conds, "c.status = 'pending'")
	default:
		conds = append(conds, "c.status = "+add(status))
	}
	switch d.ReviewType {
	case "review":
		conds = append(conds, "c.type = 'review'")
	case "comment":
		conds = append(conds, "c.type = 'comment'")
	}
	if n, _ := strconv.Atoi(d.Rating); n >= 1 && n <= 5 {
		conds = append(conds, "c.meta->>'rating' = "+add(strconv.Itoa(n)))
	}
	if d.ProductType != "" {
		conds = append(conds, "coalesce(gp.type,'simple') = "+add(d.ProductType))
	}
	if d.ProductID > 0 {
		conds = append(conds, "c.post_id = "+add(d.ProductID))
	}
	if d.Search != "" {
		like := add("%" + d.Search + "%")
		conds = append(conds, "(c.content ILIKE "+like+" OR c.author_name ILIKE "+like+" OR c.author_email ILIKE "+like+")")
	}
	from := ` FROM comments c JOIN posts p ON p.id = c.post_id LEFT JOIN goo_products gp ON gp.post_id = p.id WHERE ` + strings.Join(conds, " AND ")
	var total int
	if err := s.store.Pool.QueryRow(ctx, `SELECT count(*)`+from, args...).Scan(&total); err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	order := "c.created_at DESC, c.id DESC"
	switch c.Query("orderby") {
	case "comment_author", "author":
		order = "lower(c.author_name)"
	case "rating":
		order = "coalesce(c.meta->>'rating','0')"
	case "product":
		order = "lower(p.title)"
	}
	if strings.EqualFold(c.Query("order"), "asc") && c.Query("orderby") != "" {
		order += " ASC"
	} else if c.Query("orderby") != "" {
		order += " DESC"
	}
	rows, err := s.store.Pool.Query(ctx, `SELECT c.id`+from+` ORDER BY `+order+fmt.Sprintf(" LIMIT %d OFFSET %d", perPage, (page-1)*perPage), args...)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	posts := map[int64]*store.Post{}
	for i, id := range ids {
		cm, err := s.store.GetComment(ctx, id)
		if err != nil {
			continue
		}
		d.Rows = append(d.Rows, s.gooReviewRow(c, cm, posts, i%2 == 1))
	}
	d.Counts = s.gooReviewCounts(ctx)
	d.Pagination = newPagination(total, perPage, page, *c.Request.URL)

	var notices []notice
	for _, k := range []struct{ key, one, many string }{
		{"approved", "%d review approved.", "%d reviews approved."}, {"unapproved", "%d review unapproved.", "%d reviews unapproved."},
		{"spammed", "%d review marked as spam.", "%d reviews marked as spam."}, {"unspammed", "%d review restored from the spam.", "%d reviews restored from the spam."},
		{"trashed", "%d review moved to the Trash.", "%d reviews moved to the Trash."}, {"untrashed", "%d review restored from the Trash.", "%d reviews restored from the Trash."},
		{"deleted", "%d review permanently deleted.", "%d reviews permanently deleted."},
	} {
		if n, _ := strconv.Atoi(c.Query(k.key)); n > 0 {
			msg := k.one
			if n > 1 {
				msg = k.many
			}
			notices = append(notices, notice{Type: "success", Message: template.HTML(fmt.Sprintf(msg, n)), Dismissible: true})
		}
	}
	s.gooCatalogAssets(c)
	s.adminPage(c, "goocommerce?page=reviews", "Reviews", "product_page_product-reviews woocommerce", "admin/goo-reviews", d, notices...)
}

// gooReviewCounts are the views' counts of reviews and replies on products.
func (s *Server) gooReviewCounts(ctx context.Context) map[string]int {
	out := map[string]int{}
	rows, err := s.store.Pool.Query(ctx, `SELECT c.status, count(*) FROM comments c JOIN posts p ON p.id = c.post_id
		WHERE p.type = 'product' AND c.type IN ('review','comment') GROUP BY c.status`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var st string
		var n int
		if rows.Scan(&st, &n) == nil {
			out[st] = n
		}
	}
	out["all"] = out["pending"] + out["approved"]
	return out
}

func (s *Server) gooReviewRow(c *gin.Context, cm *store.Comment, posts map[int64]*store.Post, odd bool) gooReviewRow {
	ctx := c.Request.Context()
	r := gooReviewRow{ID: cm.ID, PostID: cm.PostID, Status: cm.Status, Author: defaultStr(cm.AuthorName, "Anonymous"), AuthorEmail: cm.AuthorEmail,
		AuthorURL: cm.AuthorURL, AuthorIP: cm.AuthorIP, Avatar: s.avatarURL(c, cm.AuthorEmail, 32)}
	r.Type = "Review"
	if cm.Type == "comment" || cm.ParentID != 0 {
		r.Type = "Reply"
	}
	r.Rating = gooMetaInt(cm.Meta["rating"])
	r.Verified = gooMetaBool(cm.Meta["verified"])
	r.Content = template.HTML(wp.Autop(template.HTMLEscapeString(cm.Content)))
	if cm.ParentID != 0 {
		if parent, err := s.store.GetComment(ctx, cm.ParentID); err == nil {
			r.ParentNote = template.HTML(`In reply to <a href="` + template.HTMLEscapeString(s.links(ctx).HomeURL("/?p="+itoa64(cm.PostID))+"#comment-"+itoa64(parent.ID)) + `">` + template.HTMLEscapeString(defaultStr(parent.AuthorName, "Anonymous")) + `</a>.`)
		}
	}
	p := posts[cm.PostID]
	if p == nil {
		p, _ = s.store.GetPost(ctx, cm.PostID)
		posts[cm.PostID] = p
	}
	if p != nil {
		r.ProductName, r.ProductEdit = p.Title, links.EditLink(p.ID)
		if gp, err := s.gooGetProduct(ctx, p.ID); err == nil {
			r.ProductView = s.gooPermalink(ctx, gp)
		}
	}
	r.Date = wp.FormatDate(cm.CreatedAt.In(s.loc()), "Y/m/d") + " at " + wp.FormatDate(cm.CreatedAt.In(s.loc()), "g:i a")
	classes := []string{"comment", "review"}
	switch cm.Status {
	case "pending":
		classes = append(classes, "unapproved")
	case "approved":
		classes = append(classes, "approved")
	default:
		classes = append(classes, cm.Status)
	}
	if odd {
		classes = append(classes, "alt")
	}
	r.Class = strings.Join(classes, " ")
	r.Actions = s.gooReviewActions(c, cm)
	return r
}

func gooMetaInt(v any) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case int64:
		return int(t)
	case string:
		n, _ := strconv.Atoi(t)
		return n
	}
	return 0
}

func gooMetaBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case float64:
		return t != 0
	case string:
		return t == "1" || t == "true" || t == "yes"
	}
	return false
}

// gooReviewActions are the row actions (ReviewsListTable::handle_row_actions()).
func (s *Server) gooReviewActions(c *gin.Context, cm *store.Comment) template.HTML {
	id := itoa64(cm.ID)
	link := func(action, nonceAction string) string {
		return template.HTMLEscapeString("/wp-admin/goocommerce?page=reviews&action=" + action + "&c=" + id + "&_wpnonce=" + s.nonce(c, nonceAction+"_"+id))
	}
	var parts []string
	a := func(class, href, label, aria string) {
		parts = append(parts, `<span class="`+class+`"><a href="`+href+`" aria-label="`+aria+`">`+label+`</a></span>`)
	}
	switch cm.Status {
	case "pending":
		a("approve", link("approve", "approve-comment"), "Approve", "Approve this review")
	case "approved":
		a("unapprove", link("unapprove", "approve-comment"), "Unapprove", "Unapprove this review")
	}
	switch cm.Status {
	case "spam":
		a("unspam", link("unspam", "delete-comment"), "Not Spam", "Restore this review from the spam")
		a("delete", link("delete", "delete-comment"), "Delete Permanently", "Delete this review permanently")
	case "trash":
		a("untrash", link("untrash", "delete-comment"), "Restore", "Restore this review from the Trash")
		a("delete", link("delete", "delete-comment"), "Delete Permanently", "Delete this review permanently")
	default:
		a("edit", template.HTMLEscapeString("/wp-admin/comment?action=editcomment&c="+id), "Edit", "Edit this review")
		a("spam", link("spam", "delete-comment"), "Spam", "Mark this review as spam")
		a("trash", link("trash", "delete-comment"), "Trash", "Move this review to the Trash")
	}
	return template.HTML(strings.Join(parts, " | "))
}

// gooReviewAction runs a row action, then recalculates the product's rating.
func (s *Server) gooReviewAction(c *gin.Context, action string) {
	id, _ := strconv.ParseInt(c.Query("c"), 10, 64)
	nonceAction := "delete-comment_"
	if action == "approve" || action == "unapprove" {
		nonceAction = "approve-comment_"
	}
	if !s.checkNonce(c, nonceAction+itoa64(id)) {
		s.denyNonce(c)
		return
	}
	n := 0
	if s.gooApplyReviewAction(c, id, action) == nil {
		n = 1
	}
	c.Redirect(http.StatusFound, s.gooReviewsBack(c, action, n))
}

func gooReviewBulkAction(c *gin.Context) string {
	if c.Query("bulk_action") == "" {
		return ""
	}
	for _, k := range []string{"action", "action2"} {
		if v := c.Query(k); v != "" && v != "-1" {
			return v
		}
	}
	return ""
}

func (s *Server) gooReviewsBulk(c *gin.Context, action string) {
	if !s.checkNonce(c, "bulk-product-reviews") {
		s.denyNonce(c)
		return
	}
	n := 0
	for _, raw := range c.QueryArray("delete_comments[]") {
		id, _ := strconv.ParseInt(raw, 10, 64)
		if s.gooApplyReviewAction(c, id, action) == nil {
			n++
		}
	}
	c.Redirect(http.StatusFound, s.gooReviewsBack(c, action, n))
}

// gooApplyReviewAction changes a review on a product and keeps the product's average rating right.
func (s *Server) gooApplyReviewAction(c *gin.Context, id int64, action string) error {
	ctx := c.Request.Context()
	cm, err := s.store.GetComment(ctx, id)
	if err != nil {
		return err
	}
	if p, err := s.store.GetPost(ctx, cm.PostID); err != nil || p.Type != "product" {
		return fmt.Errorf("not a product review")
	}
	if err := s.applyCommentAction(c, id, action); err != nil {
		return err
	}
	s.gooUpdateRating(ctx, cm.PostID)
	if p, err := s.gooGetProduct(ctx, cm.PostID); err == nil {
		s.gooPurgeProduct(ctx, p)
	}
	return nil
}

func (s *Server) gooReviewsBack(c *gin.Context, action string, n int) string {
	key := map[string]string{"approve": "approved", "unapprove": "unapproved", "spam": "spammed", "unspam": "unspammed",
		"trash": "trashed", "untrash": "untrashed", "delete": "deleted"}[action]
	u := "/wp-admin/goocommerce?page=reviews"
	if st := c.Query("comment_status"); st != "" && st != "all" {
		u += "&comment_status=" + url.QueryEscape(st)
	}
	if key != "" {
		u += "&" + key + "=" + strconv.Itoa(n)
	}
	return u
}
