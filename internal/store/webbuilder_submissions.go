package store

import (
	"context"
	"encoding/json"
	"time"
)

// WebBuilderSubmission is one stored submit of a builder form (web_builder_form_submissions).
type WebBuilderSubmission struct {
	ID        int64
	PostID    int64
	SectionID string
	FormTitle string
	PageURL   string
	Fields    map[string]string
	IP        string
	Mailed    bool
	CreatedAt time.Time
}

// AddSubmission stores a submission and fills ID and CreatedAt.
func (w WebBuilderDB) AddSubmission(ctx context.Context, s *WebBuilderSubmission) error {
	raw, err := json.Marshal(s.Fields)
	if err != nil {
		return err
	}
	return w.q.QueryRow(ctx, `INSERT INTO web_builder_form_submissions (post_id, section_id, form_title, page_url, fields, ip, mailed)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id, created_at`,
		s.PostID, s.SectionID, s.FormTitle, s.PageURL, raw, s.IP, s.Mailed).Scan(&s.ID, &s.CreatedAt)
}

// SetSubmissionMailed records that the message of a stored submission was queued.
func (w WebBuilderDB) SetSubmissionMailed(ctx context.Context, id int64, mailed bool) error {
	_, err := w.q.Exec(ctx, `UPDATE web_builder_form_submissions SET mailed = $2 WHERE id = $1`, id, mailed)
	return err
}

// Submissions lists stored submissions, newest first.
func (w WebBuilderDB) Submissions(ctx context.Context, limit, offset int) ([]WebBuilderSubmission, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := w.q.Query(ctx, `SELECT id, post_id, section_id, form_title, page_url, fields, ip, mailed, created_at
		FROM web_builder_form_submissions ORDER BY created_at DESC, id DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WebBuilderSubmission{}
	for rows.Next() {
		var s WebBuilderSubmission
		var raw []byte
		if err := rows.Scan(&s.ID, &s.PostID, &s.SectionID, &s.FormTitle, &s.PageURL, &raw, &s.IP, &s.Mailed, &s.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(raw, &s.Fields)
		out = append(out, s)
	}
	return out, rows.Err()
}

// CountSubmissions is the number of stored submissions.
func (w WebBuilderDB) CountSubmissions(ctx context.Context) (int, error) {
	var n int
	err := w.q.QueryRow(ctx, `SELECT count(*) FROM web_builder_form_submissions`).Scan(&n)
	return n, err
}

// DeleteSubmissions deletes the submissions with these ids and returns how many (no ids: none).
func (w WebBuilderDB) DeleteSubmissions(ctx context.Context, ids []int64) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	tag, err := w.q.Exec(ctx, `DELETE FROM web_builder_form_submissions WHERE id = ANY($1)`, ids)
	return tag.RowsAffected(), err
}

// DeleteAllSubmissions empties the list and returns how many rows went.
func (w WebBuilderDB) DeleteAllSubmissions(ctx context.Context) (int64, error) {
	tag, err := w.q.Exec(ctx, `DELETE FROM web_builder_form_submissions`)
	return tag.RowsAffected(), err
}
