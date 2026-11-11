package server

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
)

// Duplicate (Posts, Pages and the Custom Fields post types): the row action makes a new draft with
// the post's content, excerpt, featured image, meta (Custom Fields, Pursae SEO), categories, tags
// and other terms, page template and parent, its Pursae Web Builder doc and, with Pursae Multi
// Language, its language. WordPress leaves this to plugins (Yoast Duplicate Post); here it is core.

// duplicateURL is the row action's link.
func duplicateURL(id int64, nonce string) string {
	return "/wp-admin/post?post=" + itoa64(id) + "&action=duplicate&_wpnonce=" + nonce
}

// canDuplicate: the user may read the post in the editor (edit_post) and create posts of its type.
// Trashed posts, auto-drafts and the types with screens of their own (products, Theme Builder
// templates) are not duplicated here.
func (s *Server) canDuplicate(u *store.User, p *store.Post) bool {
	if u == nil || p == nil || p.Status == "trash" || p.Status == "auto-draft" {
		return false
	}
	if p.Type != "post" && p.Type != "page" && !acfgoIsPostType(p.Type) {
		return false
	}
	return u.Can("edit_post", p) && u.Can("edit_"+p.Type+"s", nil)
}

// duplicatePostAction is post?action=duplicate: the copy is made and the list shows it.
func (s *Server) duplicatePostAction(c *gin.Context, p *store.Post) {
	u := currentUser(c)
	if !s.checkNonce(c, "duplicate-post_"+itoa64(p.ID)) {
		s.denyNonce(c)
		return
	}
	if !s.canDuplicate(u, p) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to duplicate this item.")
		return
	}
	title := p.Title
	if title != "" {
		title += " (Copy)"
	}
	cp, err := s.store.DuplicatePost(c, p, u.ID, title)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	s.afterPostSave(c, cp) // the same chain as every save: languages, SEO, Web Builder, save_post
	c.Redirect(http.StatusFound, "/wp-admin/edit?post_type="+p.Type+"&duplicated="+itoa64(cp.ID))
}
