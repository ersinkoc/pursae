package server

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
	"wpgo/internal/webbuilder"
)

// Classic Editor (built-in plugin): switching, per-user default editor, Heartbeat autosave and post locks.

// classicSwitchAllowed reports whether users may switch editors: the Classic Editor plugin must be
// active and "Allow users to switch editors" set. Every switch link and setting goes through it.
func (s *Server) classicSwitchAllowed() bool {
	return s.settings.Bool("native_classic_editor") && s.settings.String("classic-editor-allow-users") == "allow"
}

// userDefaultEditor is the "Default Editor" of a user: their own choice, else the site default.
func (s *Server) userDefaultEditor(u *store.User) string {
	if u != nil {
		if v, ok := u.Meta["classic-editor-settings"].(string); ok && (v == "classic" || v == "block") {
			return v
		}
	}
	if s.settings.String("classic-editor-replace") == "classic" {
		return "classic"
	}
	return "block"
}

// heartbeatAutosave is wp_autosave() on the heartbeat: the classic editor sends title, content and
// excerpt every minute; they go through the same autosave as Preview (saveAutosave).
func (s *Server) heartbeatAutosave(c *gin.Context, out gin.H) {
	data := c.PostFormMap("data[wp_autosave]")
	if len(data) == 0 {
		return
	}
	u := currentUser(c)
	id, _ := strconv.ParseInt(data["post_id"], 10, 64)
	if u == nil || id <= 0 {
		return
	}
	fail := func(msg string) { out["wp_autosave"] = gin.H{"success": false, "message": msg} }
	p, err := s.store.GetPost(c, id)
	if err != nil || !u.Can("edit_post", p) || !s.checkNonceValue(c, data["_wpnonce"], "update-post_"+itoa64(p.ID)) {
		fail("Error while saving.")
		return
	}
	excerpt := p.Excerpt
	if v, ok := data["excerpt"]; ok {
		excerpt = v
	}
	title := strings.TrimSpace(data["post_title"])
	content := strings.ReplaceAll(data["content"], "\r\n", "\n")
	if _, _, err := s.saveAutosave(c, p, u, title, content, excerpt); err != nil {
		fail("Error while saving.")
		return
	}
	out["wp_autosave"] = gin.H{"success": true, "message": "Draft saved at " + time.Now().In(s.loc()).Format("3:04:05 pm") + "."}
}

// heartbeatPostLock is wp_refresh_post_lock(): another user's live lock becomes a lock_error (the
// editor then shows the "is currently editing" dialog), otherwise the lock is renewed.
func (s *Server) heartbeatPostLock(c *gin.Context, out gin.H) {
	data := c.PostFormMap("data[wp-refresh-post-lock]")
	u := currentUser(c)
	id, _ := strconv.ParseInt(data["post_id"], 10, 64)
	if u == nil || id <= 0 {
		return
	}
	p, err := s.store.GetPost(c, id)
	if err != nil || !u.Can("edit_post", p) {
		return
	}
	if wbLockedByOther(p, u.ID) {
		uid, _, _ := wbParseLock(p.Meta[webbuilder.EditLockMeta])
		name := "Someone"
		lockErr := gin.H{}
		if lu, err := s.store.UserByID(c, uid); err == nil {
			name = wbUserName(lu)
			lockErr["avatar_src"] = s.avatarURL(c, lu.Email, 64)
			lockErr["avatar_src_2x"] = s.avatarURL(c, lu.Email, 128)
		}
		lockErr["text"] = name + " is currently editing"
		out["wp-refresh-post-lock"] = gin.H{"lock_error": lockErr}
		return
	}
	if ok, err := s.wbTakeLock(c, p.ID, u.ID, false); err == nil && ok {
		out["wp-refresh-post-lock"] = gin.H{"new_lock": s.currentLockValue(c, p.ID)}
	}
}

// currentLockValue reads back the "<unix>:<uid>" just written.
func (s *Server) currentLockValue(ctx context.Context, postID int64) string {
	var v string
	_ = s.store.Pool.QueryRow(ctx, `SELECT coalesce(meta->>$2, '') FROM posts WHERE id = $1`, postID, webbuilder.EditLockMeta).Scan(&v)
	return v
}

// removePostLock is wp_ajax_wp-remove-post-lock: the editor leaves, so the lock it holds is dropped.
func (s *Server) removePostLock(c *gin.Context) {
	u := currentUser(c)
	id, _ := strconv.ParseInt(c.PostForm("post_ID"), 10, 64)
	if u == nil || id <= 0 || !s.checkNonceValue(c, c.PostForm("_wpnonce"), "update-post_"+itoa64(id)) {
		c.String(http.StatusOK, "1")
		return
	}
	if p, err := s.store.GetPost(c, id); err == nil && u.Can("edit_post", p) {
		// Only the holder's own lock is removed.
		_, _ = s.store.Pool.Exec(c, `UPDATE posts SET meta = meta - $2::text WHERE id = $1 AND split_part(coalesce(meta->>$2, ''), ':', 2) = $3`,
			id, webbuilder.EditLockMeta, strconv.FormatInt(u.ID, 10))
	}
	c.String(http.StatusOK, "1")
}

// classicPostLock takes the edit lock when the classic editor opens a post (wp_set_post_lock) or, when
// another user holds a live one, queues the "currently editing" dialog (_admin_notice_post_locked).
// It returns the lock value this editor holds ("" when someone else has it).
func (s *Server) classicPostLock(c *gin.Context, p *store.Post, u *store.User) string {
	nonce := s.nonce(c, "lock-post_"+itoa64(p.ID))
	_, takeover := c.GetQuery("get-post-lock")
	if takeover && !s.checkNonceValue(c, c.Query("_wpnonce"), "lock-post_"+itoa64(p.ID)) {
		takeover = false
	}
	if ok, err := s.wbTakeLock(c, p.ID, u.ID, takeover); err == nil && ok {
		addBodyHTML(c, s.postLockDialog(c, p, nil, nonce))
		return s.currentLockValue(c, p.ID)
	}
	uid, _, _ := wbParseLock(p.Meta[webbuilder.EditLockMeta])
	holder, _ := s.store.UserByID(c, uid)
	addBodyHTML(c, s.postLockDialog(c, p, holder, nonce))
	return ""
}

// postLockDialog is the #post-lock-dialog markup (hidden while nobody else edits the post).
func (s *Server) postLockDialog(c *gin.Context, p *store.Post, holder *store.User, nonce string) template.HTML {
	lab := labelsFor(p.Type)
	all := "/wp-admin/edit"
	if p.Type != "post" {
		all += "?post_type=" + url.QueryEscape(p.Type)
	}
	var b strings.Builder
	cls := "notification-dialog-wrap hidden"
	if holder != nil {
		cls = "notification-dialog-wrap"
	}
	b.WriteString(`<div id="post-lock-dialog" class="` + cls + `"><div class="notification-dialog-background"></div><div class="notification-dialog">`)
	b.WriteString(`<div class="post-locked-message"><div class="post-locked-avatar">`)
	if holder != nil {
		b.WriteString(`<img alt="" src="` + template.HTMLEscapeString(s.avatarURL(c, holder.Email, 64)) + `" class="avatar avatar-64 photo" height="64" width="64" />`)
	}
	b.WriteString(`</div><p class="currently-editing wp-tab-first" tabindex="0">`)
	if holder != nil {
		b.WriteString(template.HTMLEscapeString(wbUserName(holder)) + " is currently editing this " + strings.ToLower(lab.Singular) + ". Do you want to take over?")
	}
	b.WriteString(`<br /><span class="locked-saving hidden">Saving revision&hellip;</span> <span class="locked-saved hidden">Your latest changes were saved as a revision.</span></p>`)
	b.WriteString(`<p><a class="button" href="` + all + `">` + template.HTMLEscapeString("All "+lab.Name) + `</a> `)
	// The take-over link is always in the markup: the dialog is also shown by the heartbeat when
	// someone else opens the post later.
	b.WriteString(`<a class="button button-primary wp-tab-last" href="` + fmt.Sprintf("/wp-admin/post?post=%d&amp;action=edit&amp;get-post-lock=1&amp;_wpnonce=%s", p.ID, nonce) + `">Take over</a>`)
	b.WriteString(`</p></div></div></div>`)
	return template.HTML(b.String())
}
