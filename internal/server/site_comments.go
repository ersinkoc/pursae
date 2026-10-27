package server

import (
	"context"
	"fmt"
	"html"
	"html/template"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/links"
	"wpgo/internal/store"
	"wpgo/internal/wp"
)

type commentsView struct {
	Title        template.HTML
	List         template.HTML
	Count        int
	Open         bool
	ClosedNotice bool
	Form         template.HTML
}

// commentText is comment_text(): kses-filtered, texturized and auto-paragraphed.
func commentText(content string) string {
	return wp.Autop(wp.Texturize(wp.KsesPost(content)))
}

// commentsView renders comments.php: the comment list and comment_form().
func (s *Server) commentsView(c *gin.Context, p *store.Post, lb *links.Builder, u *store.User) *commentsView {
	if s.passwordRequired(c, p) {
		return nil
	}
	cv := &commentsView{Open: p.CommentStatus == "open"}
	pendingEmail, _ := c.Cookie("comment_author_email")
	if u != nil {
		pendingEmail = u.Email
	}
	if pendingEmail, err := url.QueryUnescape(pendingEmail); err == nil {
		comments, _ := s.store.ApprovedComments(c, p.ID, s.settings.String("comment_order"), pendingEmail)
		cv.Count = len(comments)
		depth := 1
		if s.settings.Bool("thread_comments") {
			depth = s.settings.Int("thread_comments_depth")
		}
		tree := store.CommentTree(comments, depth)
		if cv.Count > 0 {
			title := theTitle(p)
			if cv.Count == 1 {
				cv.Title = template.HTML("One thought on &ldquo;" + title + "&rdquo;")
			} else {
				cv.Title = template.HTML(formatNumber(cv.Count) + " thoughts on &ldquo;" + title + "&rdquo;")
			}
			var sb strings.Builder
			counter := 0
			s.listComments(&sb, tree, p, lb, u, depth, &counter)
			cv.List = template.HTML(sb.String())
		}
	}
	cv.ClosedNotice = !cv.Open && cv.Count > 0
	if cv.Open {
		cv.Form = s.commentForm(c, p, lb, u)
	}
	return cv
}

// listComments is wp_list_comments() with the html5 comment markup Twenty Sixteen uses (avatar size 42).
func (s *Server) listComments(sb *strings.Builder, list []*store.Comment, p *store.Post, lb *links.Builder, u *store.User, maxDepth int, counter *int) {
	loc := s.loc()
	permalink := lb.Permalink(p)
	for _, cm := range list {
		parity := "even"
		if *counter%2 == 1 {
			parity = "odd"
		}
		*counter++
		classes := []string{"comment", parity}
		if cm.Depth == 1 {
			classes = append(classes, "thread-"+parity)
		}
		classes = append(classes, "depth-"+strconv.Itoa(cm.Depth))
		if len(cm.Children) > 0 {
			classes = append(classes, "parent")
		}
		if cm.UserID != 0 && cm.UserID == p.AuthorID {
			classes = append(classes, "byuser", "bypostauthor")
		} else if cm.UserID != 0 {
			classes = append(classes, "byuser")
		}
		name := cm.AuthorName
		if name == "" {
			name = "Anonymous"
		}
		author := html.EscapeString(name)
		if cm.AuthorURL != "" {
			author = fmt.Sprintf(`<a href="%s" class="url" rel="ugc external nofollow">%s</a>`, html.EscapeString(cm.AuthorURL), author)
		}
		t := cm.CreatedAt.In(loc)
		fmt.Fprintf(sb, `<li id="comment-%d" class="%s">
				<article id="div-comment-%d" class="comment-body">
					<footer class="comment-meta">
						<div class="comment-author vcard">
							%s						<b class="fn">%s</b> <span class="says">says:</span>					</div><!-- .comment-author -->

						<div class="comment-metadata">
							<a href="%s#comment-%d"><time datetime="%s">%s at %s</time></a>`,
			cm.ID, strings.Join(classes, " "), cm.ID, s.avatarImg(context.Background(), cm.AuthorEmail, 42), author,
			html.EscapeString(permalink), cm.ID, t.Format(time.RFC3339), wp.FormatDate(t, s.settings.String("date_format")), wp.FormatDate(t, s.settings.String("time_format")))
		if u.Can("moderate_comments", nil) {
			fmt.Fprintf(sb, `<span class="edit-link"><a class="comment-edit-link" href="/wp-admin/comment?action=editcomment&amp;c=%d">Edit</a></span>`, cm.ID)
		}
		sb.WriteString("						</div><!-- .comment-metadata -->\n\n")
		if cm.Status == "pending" {
			sb.WriteString("											<em class=\"comment-awaiting-moderation\">Your comment is awaiting moderation.</em>\n")
		}
		sb.WriteString("									</footer><!-- .comment-meta -->\n\n				<div class=\"comment-content\">\n")
		sb.WriteString(commentText(cm.Content))
		sb.WriteString("				</div><!-- .comment-content -->\n\n")
		if p.CommentStatus == "open" && cm.Status == "approved" && maxDepth > 1 && cm.Depth < maxDepth {
			replyURL := permalink + previewSep(permalink) + "replytocom=" + itoa64(cm.ID) + "#respond"
			fmt.Fprintf(sb, `<div class="reply"><a rel="nofollow" class="comment-reply-link" href="%s" data-commentid="%d" data-postid="%d" data-belowelement="div-comment-%d" data-respondelement="respond" data-replyto="Reply to %s" aria-label="Reply to %s">Reply</a></div>`,
				html.EscapeString(replyURL), cm.ID, p.ID, cm.ID, html.EscapeString(name), html.EscapeString(name))
		}
		sb.WriteString("			</article><!-- .comment-body -->\n")
		if len(cm.Children) > 0 {
			sb.WriteString("<ol class=\"children\">\n")
			s.listComments(sb, cm.Children, p, lb, u, maxDepth, counter)
			sb.WriteString("</ol><!-- .children -->\n")
		}
		sb.WriteString("</li><!-- #comment-## -->\n")
	}
}

// commentForm is comment_form() with html5 markup and Twenty Sixteen's h2 reply title.
func (s *Server) commentForm(c *gin.Context, p *store.Post, lb *links.Builder, u *store.User) template.HTML {
	esc := html.EscapeString
	permalink := lb.Permalink(p)
	replyTo, _ := strconv.ParseInt(c.Query("replytocom"), 10, 64)
	var sb strings.Builder
	sb.WriteString(`<div id="respond" class="comment-respond">` + "\n")
	title := "Leave a Reply"
	if replyTo > 0 {
		if parent, err := s.store.GetComment(c, replyTo); err == nil && parent.PostID == p.ID {
			title = `Reply to <a href="#comment-` + itoa64(parent.ID) + `">` + esc(defaultStr(parent.AuthorName, "Anonymous")) + `</a>`
		} else {
			replyTo = 0
		}
	}
	cancelStyle := ` style="display:none;"`
	if replyTo > 0 {
		cancelStyle = ""
	}
	fmt.Fprintf(&sb, `<h2 id="reply-title" class="comment-reply-title">%s <small><a rel="nofollow" id="cancel-comment-reply-link" href="%s#respond"%s>Cancel reply</a></small></h2>`, title, esc(permalink), cancelStyle)

	if s.settings.Bool("comment_registration") && u == nil {
		fmt.Fprintf(&sb, `<p class="must-log-in">You must be <a href="/wp-login?redirect_to=%s">logged in</a> to post a comment.</p>`, url.QueryEscape(permalink))
		sb.WriteString("\t</div><!-- #respond -->\n")
		return template.HTML(sb.String())
	}
	sb.WriteString(`<form action="/wp-comments-post" method="post" id="commentform" class="comment-form">`)
	required := s.settings.Bool("require_name_email")
	req := func(on bool) string {
		if on {
			return ` <span class="required">*</span>`
		}
		return ""
	}
	if u != nil {
		fmt.Fprintf(&sb, `<p class="logged-in-as"><a href="/wp-admin/profile" aria-label="Logged in as %s. Edit your profile.">Logged in as %s</a>. <a href="/wp-login?action=logout&amp;_wpnonce=%s">Log out?</a> <span class="required-field-message">Required fields are marked <span class="required">*</span></span></p>`,
			esc(u.DisplayName), esc(u.DisplayName), s.nonce(c, "log-out"))
	} else {
		sb.WriteString(`<p class="comment-notes"><span id="email-notes">Your email address will not be published.</span> <span class="required-field-message">Required fields are marked <span class="required">*</span></span></p>`)
	}
	sb.WriteString(`<p class="comment-form-comment"><label for="comment">Comment <span class="required">*</span></label> <textarea id="comment" name="comment" cols="45" rows="8" maxlength="65525" required></textarea></p>`)
	if u == nil {
		author, _ := c.Cookie("comment_author")
		email, _ := c.Cookie("comment_author_email")
		site, _ := c.Cookie("comment_author_url")
		author, _ = url.QueryUnescape(author)
		email, _ = url.QueryUnescape(email)
		site, _ = url.QueryUnescape(site)
		reqAttr := ""
		if required {
			reqAttr = " required"
		}
		fmt.Fprintf(&sb, `<p class="comment-form-author"><label for="author">Name%s</label> <input id="author" name="author" type="text" value="%s" size="30" maxlength="245" autocomplete="name"%s /></p>`, req(required), esc(author), reqAttr)
		fmt.Fprintf(&sb, "\n"+`<p class="comment-form-email"><label for="email">Email%s</label> <input id="email" name="email" type="email" value="%s" size="30" maxlength="100" aria-describedby="email-notes" autocomplete="email"%s /></p>`, req(required), esc(email), reqAttr)
		fmt.Fprintf(&sb, "\n"+`<p class="comment-form-url"><label for="url">Website</label> <input id="url" name="url" type="url" value="%s" size="30" maxlength="200" autocomplete="url" /></p>`, esc(site))
		if s.settings.Bool("show_comments_cookies_opt_in") {
			checked := ""
			if email != "" {
				checked = ` checked="checked"`
			}
			fmt.Fprintf(&sb, "\n"+`<p class="comment-form-cookies-consent"><input id="wp-comment-cookies-consent" name="wp-comment-cookies-consent" type="checkbox" value="yes"%s /> <label for="wp-comment-cookies-consent">Save my name, email, and website in this browser for the next time I comment.</label></p>`, checked)
		}
	}
	fmt.Fprintf(&sb, "\n"+`<p class="form-submit"><input name="submit" type="submit" id="submit" class="submit" value="Post Comment" /> <input type='hidden' name='comment_post_ID' value='%d' id='comment_post_ID' />
<input type='hidden' name='comment_parent' id='comment_parent' value='%d' />
</p></form>	</div><!-- #respond -->
	`, p.ID, replyTo)
	return template.HTML(sb.String())
}

var linkRe = regexp.MustCompile(`(?i)<a\s|https?://`)

// commentPost is wp-comments-post.php: validation, moderation rules and the redirect back to the post.
func (s *Server) commentPost(c *gin.Context) {
	ctx := c.Request.Context()
	u := currentUser(c)
	postID, _ := strconv.ParseInt(c.PostForm("comment_post_ID"), 10, 64)
	p, err := s.store.GetPost(ctx, postID)
	if err != nil || (p.Status != "publish" && p.Status != "private") {
		s.commentDie(c, http.StatusNotFound, "Sorry, comments are closed for this item.")
		return
	}
	if p.CommentStatus != "open" {
		s.commentDie(c, http.StatusForbidden, "Sorry, comments are closed for this item.")
		return
	}
	if p.Password != "" && s.passwordRequired(c, p) {
		s.commentDie(c, http.StatusForbidden, "Sorry, this post is password protected.")
		return
	}
	if s.settings.Bool("comment_registration") && u == nil {
		s.commentDie(c, http.StatusForbidden, "Sorry, you must be logged in to comment.")
		return
	}
	cm := &store.Comment{PostID: p.ID, Content: strings.TrimSpace(c.PostForm("comment")),
		AuthorIP: clientIP(c), UserAgent: c.Request.UserAgent()}
	cm.ParentID, _ = strconv.ParseInt(c.PostForm("comment_parent"), 10, 64)
	if cm.ParentID > 0 {
		if parent, err := s.store.GetComment(ctx, cm.ParentID); err != nil || parent.PostID != p.ID || parent.Status != "approved" {
			cm.ParentID = 0
		}
	}
	if u != nil {
		cm.UserID, cm.AuthorName, cm.AuthorEmail, cm.AuthorURL = u.ID, u.DisplayName, u.Email, u.URL
	} else {
		cm.AuthorName = strings.TrimSpace(c.PostForm("author"))
		cm.AuthorEmail = strings.TrimSpace(c.PostForm("email"))
		cm.AuthorURL = strings.TrimSpace(c.PostForm("url"))
		if cm.AuthorURL != "" && !strings.HasPrefix(cm.AuthorURL, "http://") && !strings.HasPrefix(cm.AuthorURL, "https://") {
			cm.AuthorURL = "http://" + cm.AuthorURL
		}
		if s.settings.Bool("require_name_email") {
			if cm.AuthorName == "" || cm.AuthorEmail == "" {
				s.commentDie(c, http.StatusBadRequest, "<strong>Error:</strong> Please fill the required fields.")
				return
			}
			if !strings.Contains(cm.AuthorEmail, "@") {
				s.commentDie(c, http.StatusBadRequest, "<strong>Error:</strong> Please enter a valid email address.")
				return
			}
		}
	}
	if cm.Content == "" {
		s.commentDie(c, http.StatusBadRequest, "<strong>Error:</strong> Please type your comment text.")
		return
	}
	if len(cm.Content) > 65525 {
		s.commentDie(c, http.StatusRequestEntityTooLarge, "<strong>Error:</strong> Your comment is too long.")
		return
	}
	if !u.Can("unfiltered_html", nil) {
		cm.Content = wp.KsesPost(cm.Content)
	}
	if dup, _ := s.store.IsDuplicateComment(ctx, cm); dup {
		s.commentDie(c, http.StatusConflict, "Duplicate comment detected; it looks as though you&#8217;ve already said that!")
		return
	}
	cm.Status = s.commentStatus(c, cm, u)
	if err := s.store.InsertComment(ctx, cm); err != nil {
		s.commentDie(c, http.StatusInternalServerError, "Could not save the comment.")
		return
	}
	if cm.Status == "approved" {
		s.purgeTags("post:"+itoa64(cm.PostID), "feed", "widgets")
	}
	s.notifyComment(c, cm)

	if u == nil {
		maxAge := -1
		if c.PostForm("wp-comment-cookies-consent") == "yes" {
			maxAge = 30000000
		}
		for k, v := range map[string]string{"comment_author": cm.AuthorName, "comment_author_email": cm.AuthorEmail, "comment_author_url": cm.AuthorURL} {
			http.SetCookie(c.Writer, &http.Cookie{Name: k, Value: url.QueryEscape(v), Path: "/", MaxAge: maxAge, SameSite: http.SameSiteLaxMode})
		}
		// Without consent, keep the email for this browser session so the author still sees the pending comment.
		if maxAge < 0 && cm.Status == "pending" {
			http.SetCookie(c.Writer, &http.Cookie{Name: "comment_author_email", Value: url.QueryEscape(cm.AuthorEmail), Path: "/", SameSite: http.SameSiteLaxMode})
		}
	}
	lb := s.links(ctx)
	c.Redirect(http.StatusFound, lb.Permalink(p)+"#comment-"+itoa64(cm.ID))
}

// commentStatus is wp_allow_comment(): disallowed keys go to the trash, moderation rules hold, everything else is approved.
func (s *Server) commentStatus(c *gin.Context, cm *store.Comment, u *store.User) string {
	if u.Can("moderate_comments", nil) {
		return "approved"
	}
	haystack := strings.ToLower(strings.Join([]string{cm.AuthorName, cm.AuthorEmail, cm.AuthorURL, cm.Content, cm.AuthorIP, cm.UserAgent}, "\n"))
	matches := func(keys string) bool {
		for _, k := range strings.Split(keys, "\n") {
			k = strings.ToLower(strings.TrimSpace(k))
			if k != "" && strings.Contains(haystack, k) {
				return true
			}
		}
		return false
	}
	if matches(s.settings.String("disallowed_keys")) {
		return "trash"
	}
	if s.settings.Bool("comment_moderation") {
		return "pending"
	}
	if max := s.settings.Int("comment_max_links"); max > 0 && len(linkRe.FindAllString(cm.Content, -1)) >= max {
		return "pending"
	}
	if matches(s.settings.String("moderation_keys")) {
		return "pending"
	}
	if s.settings.Bool("comment_previously_approved") {
		if ok, _ := s.store.HasApprovedComment(c, cm.AuthorName, cm.AuthorEmail); !ok {
			return "pending"
		}
	}
	return "approved"
}

func (s *Server) commentDie(c *gin.Context, status int, msg string) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.String(status, `<!DOCTYPE html><html lang="en-US"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>Comment Submission Failure</title>`+
		`<link rel="stylesheet" href="/wp-admin/assets/css/wp/install.min.css"></head><body id="error-page"><div class="wp-die-message"><p>`+msg+
		`</p></div><p><a href="javascript:history.back()">&laquo; Back</a></p></body></html>`)
}
