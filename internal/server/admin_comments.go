package server

import (
	"fmt"
	"html/template"
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

type commentRow struct {
	ID                                int64
	Status, Class                     string
	Avatar                            template.HTML
	AuthorName, AuthorURL, AuthorHost string
	AuthorEmail, AuthorIP             string
	Content                           template.HTML
	ParentNote                        template.HTML
	PostTitle, PostEditURL, PostURL   string
	PostCount                         int
	Date                              string
	Permalink                         string
	Actions                           []commentAction
}

type commentAction struct {
	Class, URL, Label, Aria string
}

type commentsListData struct {
	Rows       []commentRow
	Status     string
	Counts     map[string]int
	All        int
	Search     string
	Pagination pagination
	BulkNonce  string
	PostTitle  string
	SortLinks  map[string]sortLink
	Type       string // comment_type filter
}

// commentsScreen is edit-comments.php.
func (s *Server) commentsScreen(c *gin.Context) {
	u := currentUser(c)
	if !u.Can("edit_posts", nil) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to edit comments.")
		return
	}
	if a := c.Query("action"); a != "" && a != "-1" || c.Query("action2") != "" && c.Query("action2") != "-1" {
		s.commentsBulk(c)
		return
	}
	ctx := c.Request.Context()
	page, _ := strconv.Atoi(c.Query("paged"))
	q := store.CommentQuery{Status: c.DefaultQuery("comment_status", "all"), Search: strings.TrimSpace(c.Query("s")), Page: page, PerPage: 20,
		OrderBy: map[string]string{"comment_author": "author", "comment_post_ID": "post", "author": "author", "post": "post"}[c.Query("orderby")], Order: c.Query("order"), Type: c.Query("comment_type")}
	if q.Status == "mine" {
		q.Status, q.UserID = "all", u.ID
	}
	d := commentsListData{Status: c.DefaultQuery("comment_status", "all"), Search: q.Search, BulkNonce: s.nonce(c, "bulk-comments"), Type: q.Type,
		SortLinks: sortLinks(c, map[string]bool{"comment_author": false, "comment_post_ID": false, "comment_date": true}, "comment_date", "desc")}
	if pid, _ := strconv.ParseInt(c.Query("p"), 10, 64); pid > 0 {
		q.PostID = pid
		if p, err := s.store.GetPost(ctx, pid); err == nil {
			d.PostTitle = p.Title
		}
	}
	if !u.Can("moderate_comments", nil) {
		q.UserID = 0 // authors see comments on their own posts only; simplified: list all approved
		if q.Status == "all" {
			q.Status = "approved"
		}
	}
	list, total, err := s.store.QueryComments(ctx, q)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	d.Counts, _ = s.store.CommentCounts(ctx)
	d.All = d.Counts["pending"] + d.Counts["approved"]
	lb := s.links(c)
	loc := s.loc()
	posts := map[int64]*store.Post{}
	for i, cm := range list {
		p := posts[cm.PostID]
		if p == nil {
			p, _ = s.store.GetPost(ctx, cm.PostID)
			posts[cm.PostID] = p
		}
		r := commentRow{ID: cm.ID, Status: cm.Status, AuthorName: defaultStr(cm.AuthorName, "Anonymous"), AuthorURL: cm.AuthorURL,
			AuthorEmail: cm.AuthorEmail, AuthorIP: cm.AuthorIP, Content: template.HTML(wp.Autop(commentText(cm.Content))),
			Date:   wp.FormatDate(cm.CreatedAt.In(loc), "Y/m/d") + " at " + wp.FormatDate(cm.CreatedAt.In(loc), "g:i a"),
			Avatar: template.HTML(s.avatarImg(ctx, cm.AuthorEmail, 32))}
		if pu, err := url.Parse(cm.AuthorURL); err == nil && pu.Host != "" {
			r.AuthorHost = strings.TrimPrefix(pu.Host, "www.")
		}
		parity := "even"
		if i%2 == 1 {
			parity = "odd alt"
		}
		r.Class = fmt.Sprintf("comment %s thread-%s depth-1 %s", parity, strings.Fields(parity)[0], map[string]string{"pending": "unapproved", "approved": "approved", "spam": "spam", "trash": "trash"}[cm.Status])
		if p != nil {
			r.PostTitle, r.PostEditURL, r.PostURL, r.PostCount = defaultStr(p.Title, "(no title)"), links.EditLink(p.ID), lb.Permalink(p), p.CommentCount
			r.Permalink = r.PostURL + "#comment-" + itoa64(cm.ID)
		}
		if cm.ParentID > 0 {
			if parent, err := s.store.GetComment(ctx, cm.ParentID); err == nil {
				r.ParentNote = template.HTML(fmt.Sprintf(`In reply to <a href="%s#comment-%d">%s</a>.`, template.HTMLEscapeString(r.PostURL), parent.ID, template.HTMLEscapeString(parent.AuthorName)))
			}
		}
		if u.Can("moderate_comments", nil) {
			r.Actions = s.commentActions(c, cm)
		}
		d.Rows = append(d.Rows, r)
	}
	d.Pagination = newPagination(total, 20, max(page, 1), *c.Request.URL)
	var notices []notice
	for _, k := range []struct{ key, one, many string }{
		{"approved", "%d comment approved.", "%d comments approved."}, {"unapproved", "%d comment unapproved.", "%d comments unapproved."},
		{"spammed", "%d comment marked as spam.", "%d comments marked as spam."}, {"unspammed", "%d comment restored from the spam.", "%d comments restored from the spam."},
		{"trashed", "%d comment moved to the Trash.", "%d comments moved to the Trash."}, {"untrashed", "%d comment restored from the Trash.", "%d comments restored from the Trash."},
		{"deleted", "%d comment permanently deleted.", "%d comments permanently deleted."}, {"updated", "Comment updated.", "Comment updated."},
	} {
		if n, _ := strconv.Atoi(c.Query(k.key)); n > 0 {
			msg := k.one
			if n > 1 {
				msg = k.many
			}
			if strings.Contains(msg, "%d") {
				msg = fmt.Sprintf(msg, n)
			}
			notices = append(notices, notice{Type: "success", Message: template.HTML(msg), Dismissible: true})
		}
	}
	s.adminPage(c, "edit-comments", "Comments", "edit-comments-php", "admin/comments-list", d, notices...)
}

// commentActions are the row actions of WP_Comments_List_Table::handle_row_actions().
func (s *Server) commentActions(c *gin.Context, cm *store.Comment) []commentAction {
	id := itoa64(cm.ID)
	approve := s.nonce(c, "approve-comment_"+id)
	del := s.nonce(c, "delete-comment_"+id)
	u := func(action, nonce string) string {
		return "/wp-admin/comment?c=" + id + "&action=" + action + "&_wpnonce=" + nonce + "&_wp_http_referer=" + url.QueryEscape(c.Request.URL.RequestURI())
	}
	var out []commentAction
	switch cm.Status {
	case "pending":
		out = append(out, commentAction{"approve", u("approvecomment", approve), "Approve", "Approve this comment"})
	case "approved":
		out = append(out, commentAction{"unapprove", u("unapprovecomment", approve), "Unapprove", "Unapprove this comment"})
	}
	if cm.Status == "pending" || cm.Status == "approved" {
		out = append(out, commentAction{"edit", "/wp-admin/comment?action=editcomment&c=" + id, "Edit", "Edit this comment"},
			commentAction{"spam", u("spamcomment", del), "Spam", "Mark this comment as spam"},
			commentAction{"trash", u("trashcomment", del), "Trash", "Move this comment to the Trash"})
	}
	switch cm.Status {
	case "spam":
		out = append(out, commentAction{"unspam", u("unspamcomment", del), "Not Spam", "Restore this comment from the spam"},
			commentAction{"delete", u("deletecomment", del), "Delete Permanently", "Delete this comment permanently"})
	case "trash":
		out = append(out, commentAction{"untrash", u("untrashcomment", del), "Restore", "Restore this comment from the Trash"},
			commentAction{"delete", u("deletecomment", del), "Delete Permanently", "Delete this comment permanently"})
	}
	return out
}

func (s *Server) commentsBulk(c *gin.Context) {
	if !currentUser(c).Can("moderate_comments", nil) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to moderate comments.")
		return
	}
	if !s.checkNonce(c, "bulk-comments") {
		s.denyNonce(c)
		return
	}
	action := c.Query("action")
	if action == "" || action == "-1" {
		action = c.Query("action2")
	}
	n := 0
	for _, raw := range c.QueryArray("delete_comments[]") {
		id, _ := strconv.ParseInt(raw, 10, 64)
		if s.applyCommentAction(c, id, action) == nil {
			n++
		}
	}
	key := map[string]string{"approve": "approved", "unapprove": "unapproved", "spam": "spammed", "unspam": "unspammed",
		"trash": "trashed", "untrash": "untrashed", "delete": "deleted"}[action]
	c.Redirect(http.StatusFound, "/wp-admin/edit-comments?comment_status="+url.QueryEscape(c.DefaultQuery("comment_status", "all"))+"&"+key+"="+strconv.Itoa(n))
}

// applyCommentAction changes one comment and purges the cached pages that show it.
func (s *Server) applyCommentAction(c *gin.Context, id int64, action string) error {
	ctx := c.Request.Context()
	cm, err := s.store.GetComment(ctx, id)
	if err != nil {
		return err
	}
	switch action {
	case "approve":
		err = s.store.SetCommentStatus(ctx, id, "approved")
	case "unapprove":
		err = s.store.SetCommentStatus(ctx, id, "pending")
	case "spam":
		err = s.store.SetCommentStatus(ctx, id, "spam")
	case "trash":
		err = s.store.SetCommentStatus(ctx, id, "trash")
	case "unspam", "untrash":
		err = s.store.SetCommentStatus(ctx, id, "restore")
	case "delete":
		err = s.store.DeleteComment(ctx, id)
	default:
		return fmt.Errorf("unknown action")
	}
	if err == nil {
		s.purgeTags("post:"+itoa64(cm.PostID), "feed", "widgets")
	}
	return err
}

type commentEditData struct {
	C         *store.Comment
	Nonce     string
	PostTitle string
	PostURL   string
	DateLabel string
	DateInput string
	TimeInput string
	DeleteURL string
}

// commentScreen is comment.php: single-comment actions and the edit form.
func (s *Server) commentScreen(c *gin.Context) {
	u := currentUser(c)
	if !u.Can("moderate_comments", nil) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to edit comments on this post.")
		return
	}
	id, _ := strconv.ParseInt(defaultStr(c.Query("c"), c.PostForm("comment_ID")), 10, 64)
	cm, err := s.store.GetComment(c, id)
	if err != nil {
		c.String(http.StatusNotFound, "Invalid comment ID.")
		return
	}
	action := defaultStr(c.Query("action"), c.PostForm("action"))
	back := c.Query("_wp_http_referer")
	if back == "" || !strings.HasPrefix(back, "/wp-admin/") {
		back = "/wp-admin/edit-comments"
	}
	flag := func(key string) string {
		sep := "?"
		if strings.Contains(back, "?") {
			sep = "&"
		}
		return back + sep + key + "=1"
	}
	switch action {
	case "approvecomment", "unapprovecomment":
		if !s.checkNonce(c, "approve-comment_"+itoa64(id)) {
			s.denyNonce(c)
			return
		}
		a := strings.TrimSuffix(action, "comment")
		_ = s.applyCommentAction(c, id, a)
		c.Redirect(http.StatusFound, flag(map[string]string{"approve": "approved", "unapprove": "unapproved"}[a]))
	case "spamcomment", "unspamcomment", "trashcomment", "untrashcomment", "deletecomment":
		if !s.checkNonce(c, "delete-comment_"+itoa64(id)) {
			s.denyNonce(c)
			return
		}
		a := strings.TrimSuffix(action, "comment")
		_ = s.applyCommentAction(c, id, a)
		c.Redirect(http.StatusFound, flag(map[string]string{"spam": "spammed", "unspam": "unspammed", "trash": "trashed", "untrash": "untrashed", "delete": "deleted"}[a]))
	case "editedcomment":
		if !s.checkNonce(c, "update-comment_"+itoa64(id)) {
			s.denyNonce(c)
			return
		}
		cm.AuthorName = strings.TrimSpace(c.PostForm("newcomment_author"))
		cm.AuthorEmail = strings.TrimSpace(c.PostForm("newcomment_author_email"))
		cm.AuthorURL = strings.TrimSpace(c.PostForm("newcomment_author_url"))
		cm.Content = c.PostForm("content")
		if !u.Can("unfiltered_html", nil) {
			cm.Content = wp.KsesPost(cm.Content)
		}
		if st := c.PostForm("comment_status"); st == "approved" || st == "pending" || st == "spam" {
			cm.Status = st
		}
		if d, t := c.PostForm("comment_date"), c.PostForm("comment_time"); d != "" {
			if when, err := time.ParseInLocation("2006-01-02 15:04", d+" "+defaultStr(t, "00:00"), s.loc()); err == nil {
				cm.CreatedAt = when
			}
		}
		if err := s.store.UpdateComment(c, cm); err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
		s.purgeTags("post:"+itoa64(cm.PostID), "feed", "widgets")
		c.Redirect(http.StatusFound, "/wp-admin/edit-comments?updated=1")
	default: // editcomment
		d := commentEditData{C: cm, Nonce: s.nonce(c, "update-comment_"+itoa64(id)),
			DeleteURL: "/wp-admin/comment?c=" + itoa64(id) + "&action=trashcomment&_wpnonce=" + s.nonce(c, "delete-comment_"+itoa64(id))}
		if p, err := s.store.GetPost(c, cm.PostID); err == nil {
			d.PostTitle, d.PostURL = defaultStr(p.Title, "(no title)"), s.links(c).Permalink(p)
		}
		t := cm.CreatedAt.In(s.loc())
		d.DateLabel = "Submitted on: <b>" + wp.FormatDate(t, "M j, Y") + " at " + wp.FormatDate(t, "H:i") + "</b>"
		d.DateInput, d.TimeInput = t.Format("2006-01-02"), t.Format("15:04")
		s.adminPage(c, "edit-comments", "Edit Comment", "comment-php", "admin/comment-edit", d)
	}
}
