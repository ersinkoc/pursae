package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// Edit locks, in the style of WordPress' post locks. Opening an entry in the Content Manager takes
// its lock; the page's script renews it every hgLockBeat (heartbeat) and gives it up when the page
// closes. A lock nobody renewed for hgLockTTL is free. While someone else holds it the entry opens
// read-only with a take-over button, and the server refuses every change made from the admin by
// another user, so nothing is overwritten silently. Taking over moves the lock: the previous editor's
// next heartbeat tells them, and their save is refused too. A form also carries the version it was
// loaded from (`base`): a save made on top of a newer version is refused even when no lock was involved.
// The REST and GraphQL APIs are not locked (tokens have no user); the save stamp covers the admin only.

const (
	hgLockTTL  = 90 * time.Second
	hgLockBeat = 15 // seconds between heartbeats
)

// hgLock is a lock held by another user.
type hgLock struct {
	UserID int64
	Name   string
	Since  time.Time
}

// hgLockHolder returns who holds the entry's lock when it is not userID (and still fresh).
func (s *Server) hgLockHolder(ctx context.Context, entryID, userID int64) *hgLock {
	l := &hgLock{}
	err := s.store.Pool.QueryRow(ctx, `SELECT l.user_id, coalesce(u.display_name, ''), l.taken_at FROM hg_entry_locks l LEFT JOIN users u ON u.id = l.user_id
		WHERE l.entry_id = $1 AND l.user_id <> $2 AND l.touched_at > now() - make_interval(secs => $3)`, entryID, userID, hgLockTTL.Seconds()).Scan(&l.UserID, &l.Name, &l.Since)
	if err != nil {
		return nil
	}
	if l.Name == "" {
		l.Name = "Another user"
	}
	return l
}

// hgAcquireLock takes (or renews) the entry's lock for userID. When another user holds it, it
// returns that user's lock and changes nothing.
func (s *Server) hgAcquireLock(ctx context.Context, entryID, userID int64) *hgLock {
	tag, err := s.store.Pool.Exec(ctx, `INSERT INTO hg_entry_locks(entry_id, user_id) VALUES ($1, $2)
		ON CONFLICT (entry_id) DO UPDATE SET user_id = $2, touched_at = now(),
			taken_at = CASE WHEN hg_entry_locks.user_id = $2 THEN hg_entry_locks.taken_at ELSE now() END
		WHERE hg_entry_locks.user_id = $2 OR hg_entry_locks.touched_at <= now() - make_interval(secs => $3)`, entryID, userID, hgLockTTL.Seconds())
	if err == nil && tag.RowsAffected() == 1 {
		return nil
	}
	return s.hgLockHolder(ctx, entryID, userID)
}

// hgTakeOverLock moves the lock to userID whoever holds it.
func (s *Server) hgTakeOverLock(ctx context.Context, entryID, userID int64) error {
	_, err := s.store.Pool.Exec(ctx, `INSERT INTO hg_entry_locks(entry_id, user_id) VALUES ($1, $2)
		ON CONFLICT (entry_id) DO UPDATE SET user_id = $2, taken_at = now(), touched_at = now()`, entryID, userID)
	return err
}

// hgReleaseLock gives the lock up (only the holder's own).
func (s *Server) hgReleaseLock(ctx context.Context, entryID, userID int64) {
	_, _ = s.store.Pool.Exec(ctx, `DELETE FROM hg_entry_locks WHERE entry_id = $1 AND user_id = $2`, entryID, userID)
}

// hgLockStamp is the form's `base` value: the version of the entry the form was loaded from.
func hgLockStamp(e *hgEntry) string { return strconv.FormatInt(e.UpdatedAt.UnixMicro(), 10) }

// hgGuardWrite refuses a change made by userID to an entry another user is editing, or (when base
// is given) made on top of a version that has changed since.
func (s *Server) hgGuardWrite(ctx context.Context, sc *hgSchema, doc, locale string, userID int64, base string) error {
	if doc == "" {
		return nil
	}
	e, err := s.hgGetEntry(ctx, sc.UID, doc, locale)
	if err != nil {
		return nil // a new locale version or a missing entry: nothing to protect
	}
	if l := s.hgLockHolder(ctx, e.ID, userID); l != nil {
		return errors.New(l.Name + " is editing this entry. Take over editing to change it.")
	}
	if base != "" && base != hgLockStamp(e) {
		return errors.New("This entry was changed by someone else after you opened it. Nothing was saved: reload the entry to see their version.")
	}
	return nil
}

// hgAdminLock answers the entry page's heartbeat: POST /wp-admin/headlessgo/lock with type, doc,
// locale and op = heartbeat | release.
func (s *Server) hgAdminLock(c *gin.Context) {
	u := currentUser(c)
	if !u.Can("edit_posts", nil) || !s.checkNonceValue(c, c.PostForm("_wpnonce"), hgNonce) {
		c.JSON(http.StatusForbidden, gin.H{"locked": false, "error": "Sorry, you are not allowed to do that."})
		return
	}
	c.Header("Cache-Control", "no-store")
	sc := s.hgReg(c).Get(c.PostForm("type"))
	if sc == nil || sc.IsComponent() {
		c.JSON(http.StatusNotFound, gin.H{"locked": false})
		return
	}
	loc, err := s.hgEntryLocale(c, sc, c.PostForm("locale"))
	doc := c.PostForm("doc")
	if sc.IsSingle() {
		doc = s.hgSingleDoc(c, sc.UID)
	}
	var e *hgEntry
	if err == nil && doc != "" {
		e, err = s.hgGetEntry(c, sc.UID, doc, loc)
	}
	if err != nil || e == nil {
		c.JSON(http.StatusNotFound, gin.H{"locked": false})
		return
	}
	if c.PostForm("op") == "release" {
		s.hgReleaseLock(c, e.ID, u.ID)
		c.JSON(http.StatusOK, gin.H{"locked": false})
		return
	}
	if l := s.hgAcquireLock(c, e.ID, u.ID); l != nil {
		c.JSON(http.StatusOK, gin.H{"locked": true, "by": l.Name, "stamp": hgLockStamp(e)})
		return
	}
	c.JSON(http.StatusOK, gin.H{"locked": false, "stamp": hgLockStamp(e)})
}
