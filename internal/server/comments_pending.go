package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
)

// pendingCommentsCookie lists the comments this browser submitted that are held for moderation,
// each signed with the site secret (WordPress' unapproved + moderation-hash preview). The
// comment_author_ prefix keeps the page cache bypassed while it is set.
const pendingCommentsCookie = "comment_author_pending"

// pendingCommentsMax caps how many pending comment IDs the cookie remembers.
const pendingCommentsMax = 20

func (s *Server) pendingCommentMAC(id int64) string {
	m := hmac.New(sha256.New, []byte("pursae-pending-comment|"+s.siteSecret()))
	m.Write([]byte(strconv.FormatInt(id, 10)))
	return hex.EncodeToString(m.Sum(nil))[:32]
}

// pendingCommentIDs returns the comment IDs carried by a validly signed pending-comments cookie.
func (s *Server) pendingCommentIDs(c *gin.Context) []int64 {
	raw, err := c.Cookie(pendingCommentsCookie)
	if err != nil || raw == "" {
		return nil
	}
	var ids []int64
	for _, part := range strings.Split(raw, "-") {
		idStr, mac, ok := strings.Cut(part, ".")
		if !ok {
			continue
		}
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil || id <= 0 {
			continue
		}
		if hmac.Equal([]byte(mac), []byte(s.pendingCommentMAC(id))) {
			ids = append(ids, id)
		}
	}
	return ids
}

// rememberPendingComment adds a just-submitted pending comment to the signed cookie.
func (s *Server) rememberPendingComment(c *gin.Context, id int64, maxAge int) {
	ids := append(s.pendingCommentIDs(c), id)
	if len(ids) > pendingCommentsMax {
		ids = ids[len(ids)-pendingCommentsMax:]
	}
	parts := make([]string, 0, len(ids))
	for _, v := range ids {
		parts = append(parts, strconv.FormatInt(v, 10)+"."+s.pendingCommentMAC(v))
	}
	ck := &http.Cookie{Name: pendingCommentsCookie, Value: strings.Join(parts, "-"), Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode}
	if maxAge > 0 {
		ck.MaxAge = maxAge
	}
	http.SetCookie(c.Writer, ck)
}

// visiblePendingComments drops pending comments the viewer has not proven to have written:
// a pending comment stays only when it belongs to the logged-in user or its ID is in the
// signed pending-comments cookie. Knowing the author's e-mail is not enough.
func (s *Server) visiblePendingComments(c *gin.Context, list []*store.Comment, u *store.User) []*store.Comment {
	allowed := map[int64]bool{}
	for _, id := range s.pendingCommentIDs(c) {
		allowed[id] = true
	}
	out := list[:0]
	for _, cm := range list {
		if cm.Status == "pending" && !allowed[cm.ID] && (u == nil || cm.UserID != u.ID) {
			continue
		}
		out = append(out, cm)
	}
	return out
}
