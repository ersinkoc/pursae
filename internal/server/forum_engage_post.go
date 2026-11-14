package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
)

// v0.2.5 front-end forms (no JavaScript needed; forum.js only makes them quicker). Every form posts
// to the topic URL with forum_action and the forum nonce (forumFrontPost checked both):
//
//	like / unlike        post_id     a member who may post; not their own post; 60 per 10 minutes
//	solve / unsolve      post_id     the topic's author or a moderator (unsolve: post_id optional)
//	subscribe / unsubscribe          follow the topic's replies by email
//	report               post_id, reason (spam|abuse|off_topic|other), note   10 an hour
//
// and, without a login, unsubscribe_link (t, u, k: the reply email's signed link).

// forumPostEngageAction handles like, unlike, solve, unsolve, subscribe, unsubscribe and report.
func (s *Server) forumPostEngageAction(c *gin.Context, rt forumRoute, u *store.User, action string) {
	ctx := c.Request.Context()
	if rt.View != forumViewTopic {
		c.String(http.StatusBadRequest, "Unknown forum action.")
		return
	}
	t, err := s.forumTopicByID(ctx, rt.ID)
	if err != nil {
		s.forumNotFound(c)
		return
	}
	if _, ferr := s.forumByID(ctx, t.ForumID); !forumTopicVisible(u, t, ferr == nil) {
		s.forumNotFound(c)
		return
	}
	postID, _ := strconv.ParseInt(c.PostForm("post_id"), 10, 64)
	back := func(id int64, msg string) {
		dest := t.URL
		if id > 0 && id != t.ID {
			if r, err := s.forumReplyByID(ctx, id); err == nil && r.TopicID == t.ID {
				dest = s.forumReplyAnchor(ctx, t, r, u)
			}
		} else if id == t.ID {
			dest = t.URL + "#post-" + strconv.FormatInt(t.ID, 10)
		}
		c.Redirect(http.StatusFound, forumWithMsg(dest, msg))
	}
	fail := func(status int, msg string) {
		s.forumFail(c, rt, forumViewTopic, status, msg, nil)
	}
	st := s.forumLoad()
	switch action {
	case "like", "unlike":
		if code, msg := forumPostGate(u, st); code != 0 {
			fail(code, msg)
			return
		}
		if !forumAllow("like:"+strconv.FormatInt(u.ID, 10), forumLikeLimit, 10*time.Minute) {
			fail(http.StatusTooManyRequests, "Too many requests. Please try again later.")
			return
		}
		if postID <= 0 {
			postID = t.ID
		}
		if _, topicID, _, err := s.forumParents(ctx, postID); err != nil || topicID != t.ID {
			s.forumNotFound(c)
			return
		}
		if _, _, err := s.forumSetLike(ctx, u, postID, action == "like"); err != nil {
			if errors.Is(err, errForumOwnPost) {
				fail(http.StatusForbidden, "You cannot like your own post.")
				return
			}
			s.forumNotFound(c)
			return
		}
		back(postID, action+"d")
	case "solve", "unsolve":
		if action == "unsolve" {
			postID = 0
		} else if postID <= 0 {
			c.String(http.StatusBadRequest, "Unknown forum action.")
			return
		}
		if err := s.forumSolve(ctx, u, t.ID, postID); err != nil {
			if errors.Is(err, errForumSolveDenied) {
				fail(http.StatusForbidden, "Only the topic's author or a moderator can choose the solution.")
				return
			}
			s.forumNotFound(c)
			return
		}
		back(postID, action+"d")
	case "subscribe", "unsubscribe":
		if err := s.forumSubscribe(ctx, u.ID, t.ID, action == "subscribe"); err != nil {
			fail(http.StatusServiceUnavailable, "Something went wrong. Please try again.")
			return
		}
		back(0, action+"d")
	case "report":
		if code, msg := forumPostGate(u, st); code != 0 { // blocked or unconfirmed members do not report
			fail(code, msg)
			return
		}
		if !forumAllow("report:"+strconv.FormatInt(u.ID, 10), 10, time.Hour) {
			fail(http.StatusTooManyRequests, "Too many requests. Please try again later.")
			return
		}
		if postID <= 0 {
			postID = t.ID
		}
		if _, topicID, _, err := s.forumParents(ctx, postID); err != nil || topicID != t.ID {
			s.forumNotFound(c)
			return
		}
		reason := c.PostForm("reason")
		if _, ok := forumReportReasons[reason]; !ok {
			reason = "other"
		}
		if err := s.forumReport(ctx, u, postID, reason, c.PostForm("note")); err != nil {
			s.forumNotFound(c)
			return
		}
		back(postID, "reported")
	}
}

// forumPreview re-renders a new-topic or reply form with the typed text rendered as it would be
// posted (cleaned the same way), nothing saved.
func (s *Server) forumPreview(c *gin.Context, rt forumRoute, view string, u *store.User, raw string, form map[string]string) {
	p := s.forumNewPage(c, view, "")
	for k, v := range form {
		p.Form[k] = v
	}
	clean := forumClean(u, raw)
	if forumTextLen(clean) == 0 {
		p.Error = p.T("Please write a little more (at least 2 characters).")
	} else {
		p.Preview = forumFormat(clean)
	}
	p.Extra["preview"] = true
	if !s.forumServe(c, rt, p) && !c.Writer.Written() {
		s.forumNotFound(c)
	}
}

// forumUnsubPost stops a member following a topic from the signed link of a reply email.
func (s *Server) forumUnsubPost(c *gin.Context, rt forumRoute) {
	ctx := c.Request.Context()
	forumNoStore(c)
	if rt.View != forumViewUnsubscribe {
		c.String(http.StatusBadRequest, "Unknown forum action.")
		return
	}
	tid, _ := strconv.ParseInt(c.PostForm("t"), 10, 64)
	uid, _ := strconv.ParseInt(c.PostForm("u"), 10, 64)
	if !s.forumCheckUnsub(tid, uid, c.PostForm("k")) {
		c.String(http.StatusForbidden, "This link is invalid.")
		return
	}
	if err := s.forumSubscribe(ctx, uid, tid, false); err != nil {
		c.String(http.StatusServiceUnavailable, "Something went wrong. Please try again.")
		return
	}
	dest := s.forumURL(ctx)
	if t, err := s.forumTopicByID(ctx, tid); err == nil && t.Status == "publish" && !t.Spam {
		dest = t.URL
	}
	c.Redirect(http.StatusFound, forumWithMsg(dest, "unsubscribed"))
}

// forumChangeEmailToken signs "this browser registered account id with this address" for the
// "change the address" form of the register view's check-your-inbox step (no login yet).
func (s *Server) forumChangeEmailToken(userID int64, email string, exp time.Time) string {
	e := strconv.FormatInt(exp.Unix(), 10)
	return strconv.FormatInt(userID, 10) + "." + e + "." +
		forumShortMAC(s.siteSecret(), "forum-change-email|"+strconv.FormatInt(userID, 10)+"|"+strings.ToLower(email)+"|"+e)
}

// forumCheckChangeEmailToken returns the account of a valid, unexpired token for email (0 otherwise).
func (s *Server) forumCheckChangeEmailToken(tok, email string, now time.Time) int64 {
	parts := strings.SplitN(tok, ".", 3)
	if len(parts) != 3 {
		return 0
	}
	uid, err := strconv.ParseInt(parts[0], 10, 64)
	exp, err2 := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || err2 != nil || uid <= 0 || now.Unix() > exp {
		return 0
	}
	if !hmac.Equal([]byte(tok), []byte(s.forumChangeEmailToken(uid, email, time.Unix(exp, 0)))) {
		return 0
	}
	return uid
}

// forumShortMAC is the first 32 hex digits of HMAC-SHA256(key, msg).
func forumShortMAC(key, msg string) string {
	m := hmac.New(sha256.New, []byte(key))
	m.Write([]byte(msg))
	return hex.EncodeToString(m.Sum(nil))[:32]
}

// forumRulesLink is the HTML link to the rules (for the register template's checkbox label).
func forumRulesLink(url, label string) template.HTML {
	if url == "" {
		return template.HTML(template.HTMLEscapeString(label))
	}
	return template.HTML(`<a href="` + template.HTMLEscapeString(url) + `" target="_blank" rel="noopener">` + template.HTMLEscapeString(label) + `</a>`)
}
