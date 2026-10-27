package server

import (
	"context"
	"strings"

	"github.com/gin-gonic/gin"
)

// defaultAvatar is WordPress Go's built-in "mystery person": no third-party avatar service is contacted.
const defaultAvatar = "/wp-includes/images/wpgo-avatar.svg"

// avatarURL is get_avatar_url(): a user's chosen avatar from the media library, or the local default.
// Users pick their avatar on the Profile screen with the same media modal used for featured images.
func (s *Server) avatarURL(c *gin.Context, email string, size int) string {
	return s.avatarFor(c, email, size)
}

func (s *Server) avatarFor(ctx context.Context, email string, size int) string {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return defaultAvatar
	}
	var id int64
	_ = s.store.Pool.QueryRow(ctx, `SELECT coalesce((meta->>'avatar_id')::bigint, 0) FROM users WHERE lower(email) = $1`, email).Scan(&id)
	if id == 0 {
		return defaultAvatar
	}
	a, err := s.store.GetAttachment(ctx, id)
	if err != nil {
		return defaultAvatar
	}
	// Smallest generated size that still covers the requested pixel size (2x for retina).
	best := a.FilePath
	bestW := a.Width
	for _, v := range a.Variants {
		if v.Width >= size*2 && (bestW == 0 || v.Width < bestW) {
			best, bestW = v.FilePath, v.Width
		}
	}
	return s.uploadURL(ctx, best)
}
