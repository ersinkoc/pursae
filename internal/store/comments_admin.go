package store

import (
	"context"
	"fmt"
	"strings"
)

// CommentQuery filters the admin comments list (WP_Comment_Query as edit-comments.php uses it).
type CommentQuery struct {
	Status string // all (pending+approved), pending, approved, spam, trash
	PostID int64
	// PostAuthorID limits the result to comments on posts written by this user (the comments screen for users who
	// cannot moderate comments).
	PostAuthorID int64
	UserID       int64  // "mine": comments by this user
	Type         string // comment, pings
	Search       string
	// SearchPublic limits Search to the comment text and the author's name. The REST API uses it for callers who
	// may not see commenter e-mail addresses or IPs: matching on them would reveal them.
	SearchPublic bool
	OrderBy      string // date, author, post
	Order        string
	Page         int
	PerPage      int
}

// QueryComments returns one page of comments and the total count.
func (s *Store) QueryComments(ctx context.Context, q CommentQuery) ([]*Comment, int, error) {
	var where []string
	var args []any
	arg := func(v any) string { args = append(args, v); return fmt.Sprintf("$%d", len(args)) }
	switch q.Status {
	case "", "all":
		where = append(where, "c.status IN ('pending','approved')")
	default:
		where = append(where, "c.status = "+arg(q.Status))
	}
	if q.PostID > 0 {
		where = append(where, "c.post_id = "+arg(q.PostID))
	}
	if q.PostAuthorID > 0 {
		where = append(where, "c.post_id IN (SELECT id FROM posts WHERE author_id = "+arg(q.PostAuthorID)+")")
	}
	if q.UserID > 0 {
		where = append(where, "c.user_id = "+arg(q.UserID))
	}
	switch q.Type {
	case "comment":
		where = append(where, "c.type = 'comment'")
	case "pings":
		where = append(where, "c.type IN ('pingback','trackback')")
	}
	if q.Search != "" {
		p := arg("%" + strings.ToLower(q.Search) + "%")
		if q.SearchPublic {
			where = append(where, "(lower(c.content) LIKE "+p+" OR lower(c.author_name) LIKE "+p+")")
		} else {
			where = append(where, "(lower(c.content) LIKE "+p+" OR lower(c.author_name) LIKE "+p+" OR lower(c.author_email) LIKE "+p+" OR c.author_ip LIKE "+p+")")
		}
	}
	cond := strings.Join(where, " AND ")
	var total int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM comments c WHERE `+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	order := "c.created_at"
	switch q.OrderBy {
	case "author":
		order = "lower(c.author_name)"
	case "post":
		order = "c.post_id"
	}
	dir := "DESC"
	if strings.EqualFold(q.Order, "asc") {
		dir = "ASC"
	}
	if q.PerPage <= 0 {
		q.PerPage = 20
	}
	if q.Page < 1 {
		q.Page = 1
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+prefixed("c", commentCols)+` FROM comments c WHERE `+cond+
		` ORDER BY `+order+` `+dir+`, c.id `+dir+fmt.Sprintf(` LIMIT %d OFFSET %d`, q.PerPage, (q.Page-1)*q.PerPage), args...)
	if err != nil {
		return nil, 0, err
	}
	list, err := scanComments(rows)
	return list, total, err
}

// SetCommentStatus is wp_set_comment_status(); trashing remembers the previous status for untrash.
func (s *Store) SetCommentStatus(ctx context.Context, id int64, status string) error {
	c, err := s.GetComment(ctx, id)
	if err != nil {
		return err
	}
	switch status {
	case "trash", "spam":
		_, err = s.Pool.Exec(ctx, `UPDATE comments SET status = $2, meta = meta || jsonb_build_object('_wp_trash_meta_status', status::text)
			WHERE id = $1`, id, status)
	case "restore":
		_, err = s.Pool.Exec(ctx, `UPDATE comments SET status = coalesce(nullif(meta->>'_wp_trash_meta_status', ''), 'pending'),
			meta = meta - '_wp_trash_meta_status' WHERE id = $1`, id)
	default:
		_, err = s.Pool.Exec(ctx, `UPDATE comments SET status = $2 WHERE id = $1`, id, status)
	}
	if err != nil {
		return err
	}
	return s.RecountComments(ctx, c.PostID)
}

// UpdateComment saves the fields editable on comment.php.
func (s *Store) UpdateComment(ctx context.Context, c *Comment) error {
	_, err := s.Pool.Exec(ctx, `UPDATE comments SET author_name = $2, author_email = $3, author_url = $4, content = $5,
		status = $6, created_at = $7 WHERE id = $1`, c.ID, c.AuthorName, c.AuthorEmail, c.AuthorURL, c.Content, c.Status, c.CreatedAt)
	if err != nil {
		return err
	}
	return s.RecountComments(ctx, c.PostID)
}

// DeleteComment removes a comment permanently; replies are re-parented to its parent.
func (s *Store) DeleteComment(ctx context.Context, id int64) error {
	c, err := s.GetComment(ctx, id)
	if err != nil {
		return err
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE comments SET parent_id = $2 WHERE parent_id = $1`, id, nullable(c.ParentID)); err != nil {
		return err
	}
	if _, err := s.Pool.Exec(ctx, `DELETE FROM comments WHERE id = $1`, id); err != nil {
		return err
	}
	return s.RecountComments(ctx, c.PostID)
}
