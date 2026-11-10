package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type Comment struct {
	ID          int64
	PostID      int64
	ParentID    int64
	UserID      int64
	AuthorName  string
	AuthorEmail string
	AuthorURL   string
	AuthorIP    string
	UserAgent   string
	Content     string
	Type        string
	Status      string
	Meta        map[string]any
	CreatedAt   time.Time
	Children    []*Comment // filled by CommentTree
	Depth       int
}

const commentCols = `id, post_id, coalesce(parent_id,0), coalesce(user_id,0), author_name, author_email, author_url,
	author_ip, user_agent, content, type, status, meta, created_at`

func scanComment(row pgx.Row) (*Comment, error) {
	c := &Comment{}
	var meta []byte
	err := row.Scan(&c.ID, &c.PostID, &c.ParentID, &c.UserID, &c.AuthorName, &c.AuthorEmail, &c.AuthorURL,
		&c.AuthorIP, &c.UserAgent, &c.Content, &c.Type, &c.Status, &meta, &c.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	c.Meta = map[string]any{}
	_ = json.Unmarshal(meta, &c.Meta)
	return c, nil
}

func scanComments(rows pgx.Rows) ([]*Comment, error) {
	defer rows.Close()
	var out []*Comment
	for rows.Next() {
		c, err := scanComment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) GetComment(ctx context.Context, id int64) (*Comment, error) {
	return scanComment(s.Pool.QueryRow(ctx, `SELECT `+commentCols+` FROM comments WHERE id = $1`, id))
}

// CommentsByIDs loads several comments in one query; missing ids are absent from the map.
func (s *Store) CommentsByIDs(ctx context.Context, ids []int64) (map[int64]*Comment, error) {
	out := map[int64]*Comment{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+commentCols+` FROM comments WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	list, err := scanComments(rows)
	for _, c := range list {
		out[c.ID] = c
	}
	return out, err
}

// ApprovedCommentsByPost loads the approved comments of several posts in one query, oldest first
// per post (the export's wxr comments).
func (s *Store) ApprovedCommentsByPost(ctx context.Context, postIDs []int64) (map[int64][]*Comment, error) {
	out := map[int64][]*Comment{}
	if len(postIDs) == 0 {
		return out, nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+commentCols+` FROM comments WHERE post_id = ANY($1) AND status = 'approved'
		ORDER BY post_id, created_at, id`, postIDs)
	if err != nil {
		return nil, err
	}
	list, err := scanComments(rows)
	for _, c := range list {
		out[c.PostID] = append(out[c.PostID], c)
	}
	return out, err
}

// ApprovedComments returns a post's approved comments, plus pending ones by the given author email
// (WordPress shows a commenter their own comment awaiting moderation).
func (s *Store) ApprovedComments(ctx context.Context, postID int64, order string, pendingEmail string) ([]*Comment, error) {
	dir := "ASC"
	if order == "desc" {
		dir = "DESC"
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+commentCols+` FROM comments WHERE post_id = $1
		AND (status = 'approved' OR (status = 'pending' AND $2 <> '' AND author_email = $2))
		ORDER BY created_at `+dir+`, id `+dir, postID, pendingEmail)
	if err != nil {
		return nil, err
	}
	return scanComments(rows)
}

// CommentTree nests comments under their parents up to maxDepth (deeper replies attach to the last level).
func CommentTree(flat []*Comment, maxDepth int) []*Comment {
	byID := map[int64]*Comment{}
	for _, c := range flat {
		c.Children = nil
		byID[c.ID] = c
	}
	var roots []*Comment
	for _, c := range flat {
		parent, ok := byID[c.ParentID]
		if !ok || maxDepth <= 1 {
			c.Depth = 1
			roots = append(roots, c)
			continue
		}
		depth := parent.Depth + 1
		for depth > maxDepth && parent != nil {
			parent = byID[parent.ParentID]
			depth--
		}
		if parent == nil {
			c.Depth = 1
			roots = append(roots, c)
			continue
		}
		c.Depth = depth
		parent.Children = append(parent.Children, c)
	}
	return roots
}

// RecentComments returns the newest approved comments on published posts (Recent Comments widget, dashboard).
func (s *Store) RecentComments(ctx context.Context, limit int, includePending bool) ([]*Comment, error) {
	statuses := []string{"approved"}
	if includePending {
		statuses = append(statuses, "pending")
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+prefixed("c", commentCols)+` FROM comments c JOIN posts p ON p.id = c.post_id
		WHERE c.status = ANY($1) AND p.status = 'publish' ORDER BY c.created_at DESC, c.id DESC LIMIT $2`, statuses, limit)
	if err != nil {
		return nil, err
	}
	return scanComments(rows)
}

func (s *Store) InsertComment(ctx context.Context, c *Comment) error {
	if c.Type == "" {
		c.Type = "comment"
	}
	meta, _ := json.Marshal(orEmpty(c.Meta))
	err := s.Pool.QueryRow(ctx, `INSERT INTO comments(post_id, parent_id, user_id, author_name, author_email, author_url,
		author_ip, user_agent, content, type, status, meta) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		RETURNING id, created_at`, c.PostID, nullable(c.ParentID), nullable(c.UserID), c.AuthorName, c.AuthorEmail,
		c.AuthorURL, c.AuthorIP, c.UserAgent, c.Content, c.Type, c.Status, meta).Scan(&c.ID, &c.CreatedAt)
	if err != nil {
		return err
	}
	return s.RecountComments(ctx, c.PostID)
}

// RecountComments refreshes posts.comment_count from approved comments (wp_update_comment_count()).
func (s *Store) RecountComments(ctx context.Context, postID int64) error {
	_, err := s.Pool.Exec(ctx, `UPDATE posts SET comment_count = (SELECT count(*) FROM comments
		WHERE post_id = $1 AND status = 'approved') WHERE id = $1`, postID)
	return err
}

// HasApprovedComment is check_comment()'s comment_previously_approved test: the author already has
// an approved comment. Like WordPress it needs both a name and an e-mail (an anonymous visitor never
// counts as known), and when the e-mail belongs to a user it looks at that user's comments.
func (s *Store) HasApprovedComment(ctx context.Context, name, email string) (bool, error) {
	if name == "" || email == "" {
		return false, nil
	}
	var ok bool
	err := s.Pool.QueryRow(ctx, `WITH u AS (SELECT id FROM users WHERE lower(email) = lower($2) ORDER BY id LIMIT 1)
		SELECT CASE WHEN EXISTS(SELECT 1 FROM u)
			THEN EXISTS(SELECT 1 FROM comments WHERE user_id = (SELECT id FROM u) AND status = 'approved')
			ELSE EXISTS(SELECT 1 FROM comments WHERE author_name = $1 AND author_email = $2 AND status = 'approved') END`,
		name, email).Scan(&ok)
	return ok, err
}

// IsDuplicateComment is wp_allow_comment()'s duplicate test: the same text by the same author (the
// same user, or the same name and, when one was given, e-mail) in reply to the same comment.
func (s *Store) IsDuplicateComment(ctx context.Context, c *Comment) (bool, error) {
	var ok bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM comments WHERE post_id = $1 AND coalesce(parent_id, 0) = $5
		AND content = $2 AND status <> 'trash'
		AND ((user_id IS NOT NULL AND user_id = $4) OR (author_name = $6 AND ($3 = '' OR author_email = $3))))`,
		c.PostID, c.Content, c.AuthorEmail, c.UserID, c.ParentID, c.AuthorName).Scan(&ok)
	return ok, err
}

// LastCommentAt is the query behind wp_check_comment_flood(): the newest comment of the last hour by this
// visitor (IP, or user id when logged in) or e-mail address. ok is false when there is none.
func (s *Store) LastCommentAt(ctx context.Context, ip, email string, userID int64) (time.Time, bool, error) {
	var t time.Time
	var err error
	if userID != 0 {
		err = s.Pool.QueryRow(ctx, `SELECT created_at FROM comments WHERE created_at >= now() - interval '1 hour'
			AND (user_id = $1 OR author_email = $2) ORDER BY created_at DESC LIMIT 1`, userID, email).Scan(&t)
	} else {
		err = s.Pool.QueryRow(ctx, `SELECT created_at FROM comments WHERE created_at >= now() - interval '1 hour'
			AND (author_ip = $1 OR author_email = $2) ORDER BY created_at DESC LIMIT 1`, ip, email).Scan(&t)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, false, nil
	}
	return t, err == nil, err
}

func (s *Store) CommentCounts(ctx context.Context) (map[string]int, error) {
	return s.commentCounts(ctx, 0)
}

// CommentCountsForPostAuthor counts only the comments on posts written by authorID.
func (s *Store) CommentCountsForPostAuthor(ctx context.Context, authorID int64) (map[string]int, error) {
	return s.commentCounts(ctx, authorID)
}

func (s *Store) commentCounts(ctx context.Context, postAuthorID int64) (map[string]int, error) {
	q, args := `SELECT status, count(*) FROM comments GROUP BY status`, []any{}
	if postAuthorID > 0 {
		q, args = `SELECT status, count(*) FROM comments WHERE post_id IN (SELECT id FROM posts WHERE author_id = $1) GROUP BY status`, []any{postAuthorID}
	}
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return nil, err
		}
		out[st] = n
	}
	return out, rows.Err()
}
