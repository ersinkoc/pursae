package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Pursae Web Builder storage (docs/developer/web-builder-contract.md §6): the page doc and its
// sanitized snapshot, per-user drafts, doc revisions linked to core post revisions, and the
// site's saved components. Docs, pages and sections are kept as raw JSON: validation and
// sanitizing belong to internal/webbuilder and the REST layer, the store only persists.

// ErrRevConflict is returned (wrapped in a *RevConflictError) when a publish names a base rev
// that is no longer the stored one.
var ErrRevConflict = errors.New("web builder: rev conflict")

// ErrLibraryFull is returned when the site already holds the maximum number of saved components.
var ErrLibraryFull = errors.New("web builder: library is full")

// RevConflictError carries the stored state a conflicting publish lost against, for the 409
// answer (data: rev, modified, modifiedBy). Rev is 0 and UpdatedAt zero when no doc exists.
type RevConflictError struct {
	Rev          int
	UpdatedAt    time.Time
	SnapshotUser int64
}

func (e *RevConflictError) Error() string {
	return fmt.Sprintf("web builder: rev conflict (stored rev %d)", e.Rev)
}

func (e *RevConflictError) Is(target error) bool { return target == ErrRevConflict }

// DBTX is what both *pgxpool.Pool and pgx.Tx offer, so the web builder store runs alone or inside
// the caller's transaction (publish updates the post, its revision and the doc together).
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Begin(ctx context.Context) (pgx.Tx, error)
}

// WebBuilderDB is the web builder store on one connection, pool or transaction.
type WebBuilderDB struct{ q DBTX }

// WebBuilder returns the web builder store on the pool.
func (s *Store) WebBuilder() WebBuilderDB { return WebBuilderDB{q: s.Pool} }

// WebBuilderIn returns the web builder store inside a transaction.
func WebBuilderIn(tx pgx.Tx) WebBuilderDB { return WebBuilderDB{q: tx} }

// WebBuilderContentHash is the content_hash of a posts.content string: sha256, lower-case hex.
func WebBuilderContentHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// rawOr returns raw, or def when raw is empty or JSON null.
func rawOr(raw json.RawMessage, def string) []byte {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return []byte(def)
	}
	return raw
}

func requireDoc(doc json.RawMessage) error {
	trimmed := bytes.TrimSpace(doc)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return errors.New("web builder: doc is required")
	}
	return nil
}

// Docs ------------------------------------------------------------------------------------------

// WebBuilderDoc is a web_builder_docs row: the published doc and the snapshot rendered from it.
type WebBuilderDoc struct {
	PostID             int64
	Doc                json.RawMessage // DocState
	Page               json.RawMessage // PageSettings
	HTML               string          // sanitized RenderResult.html
	CSS                string          // sanitized utility CSS + section CSS
	Fonts              json.RawMessage // FontNeed[]
	Features           []string
	Renderer           int
	Rev                int
	ContentHash        string // sha256 hex of the posts.content written with this doc
	Detached           bool
	SnapshotUser       int64 // 0 = unknown / deleted user
	SnapshotUnfiltered bool  // the snapshot was written by a user with unfiltered_html
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

const wbDocCols = `post_id, doc, page, html, css, fonts, features, renderer, rev, content_hash, detached,
	coalesce(snapshot_user,0), snapshot_unfiltered, created_at, updated_at`

func scanWBDoc(row pgx.Row) (*WebBuilderDoc, error) {
	d := &WebBuilderDoc{}
	var doc, page, fonts []byte
	err := row.Scan(&d.PostID, &doc, &page, &d.HTML, &d.CSS, &fonts, &d.Features, &d.Renderer, &d.Rev,
		&d.ContentHash, &d.Detached, &d.SnapshotUser, &d.SnapshotUnfiltered, &d.CreatedAt, &d.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	d.Doc, d.Page, d.Fonts = doc, page, fonts
	if d.Features == nil {
		d.Features = []string{}
	}
	return d, nil
}

// Doc loads a post's builder doc; ErrNotFound when the post was never published from the builder.
func (w WebBuilderDB) Doc(ctx context.Context, postID int64) (*WebBuilderDoc, error) {
	return scanWBDoc(w.q.QueryRow(ctx, `SELECT `+wbDocCols+` FROM web_builder_docs WHERE post_id = $1`, postID))
}

// DocForUpdate is Doc with a row lock (SELECT … FOR UPDATE); use it inside a transaction so a
// publish can check the rev before doing the rest of its work.
func (w WebBuilderDB) DocForUpdate(ctx context.Context, postID int64) (*WebBuilderDoc, error) {
	return scanWBDoc(w.q.QueryRow(ctx, `SELECT `+wbDocCols+` FROM web_builder_docs WHERE post_id = $1 FOR UPDATE`, postID))
}

// DocRev is the stored rev of a post's doc, 0 when it has none.
func (w WebBuilderDB) DocRev(ctx context.Context, postID int64) (int, error) {
	var rev int
	err := w.q.QueryRow(ctx, `SELECT rev FROM web_builder_docs WHERE post_id = $1`, postID).Scan(&rev)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return rev, err
}

// PublishDoc writes d as the post's doc when baseRev is still the stored rev (0 = no doc yet).
// On success d.Rev is baseRev+1, d.Detached is false and the timestamps are filled in. When the
// stored rev differs it returns a *RevConflictError (errors.Is(err, ErrRevConflict)) and writes
// nothing. The check and the write are one statement, so two publishes from the same base rev
// cannot both win.
func (w WebBuilderDB) PublishDoc(ctx context.Context, d *WebBuilderDoc, baseRev int) error {
	if err := requireDoc(d.Doc); err != nil {
		return err
	}
	if baseRev < 0 {
		return &RevConflictError{}
	}
	features := d.Features
	if features == nil {
		features = []string{}
	}
	args := []any{d.PostID, []byte(d.Doc), rawOr(d.Page, "{}"), d.HTML, d.CSS, rawOr(d.Fonts, "[]"), features,
		d.Renderer, d.ContentHash, nullable(d.SnapshotUser), d.SnapshotUnfiltered}
	var row pgx.Row
	if baseRev == 0 {
		// First publish: insert; a row that appeared meanwhile is only replaced while its rev is
		// still 0, so a concurrent first publish that already won makes this one conflict.
		row = w.q.QueryRow(ctx, `INSERT INTO web_builder_docs AS d (post_id, doc, page, html, css, fonts, features,
				renderer, rev, content_hash, detached, snapshot_user, snapshot_unfiltered)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 1, $9, false, $10, $11)
			ON CONFLICT (post_id) DO UPDATE SET doc = $2, page = $3, html = $4, css = $5, fonts = $6, features = $7,
				renderer = $8, rev = d.rev + 1, content_hash = $9, detached = false, snapshot_user = $10,
				snapshot_unfiltered = $11, updated_at = now()
			WHERE d.rev = 0
			RETURNING rev, created_at, updated_at`, args...)
	} else {
		row = w.q.QueryRow(ctx, `UPDATE web_builder_docs SET doc = $2, page = $3, html = $4, css = $5, fonts = $6,
				features = $7, renderer = $8, rev = rev + 1, content_hash = $9, detached = false, snapshot_user = $10,
				snapshot_unfiltered = $11, updated_at = now()
			WHERE post_id = $1 AND rev = $12
			RETURNING rev, created_at, updated_at`, append(args, baseRev)...)
	}
	err := row.Scan(&d.Rev, &d.CreatedAt, &d.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return w.revConflict(ctx, d.PostID)
	}
	if err != nil {
		return err
	}
	d.Detached = false
	d.Features = features
	return nil
}

func (w WebBuilderDB) revConflict(ctx context.Context, postID int64) error {
	c := &RevConflictError{}
	err := w.q.QueryRow(ctx, `SELECT rev, updated_at, coalesce(snapshot_user,0) FROM web_builder_docs WHERE post_id = $1`,
		postID).Scan(&c.Rev, &c.UpdatedAt, &c.SnapshotUser)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	return c
}

// MarkDetachedIfDrifted is called after a save from outside the builder: when content (the new
// posts.content) no longer hashes to the stored content_hash, the doc is marked detached. It
// never clears the flag (only a publish with reattach does) and reports whether the doc is
// detached now; a post without a doc reports false.
func (w WebBuilderDB) MarkDetachedIfDrifted(ctx context.Context, postID int64, content string) (bool, error) {
	var detached bool
	err := w.q.QueryRow(ctx, `UPDATE web_builder_docs SET detached = detached OR content_hash <> $2
		WHERE post_id = $1 RETURNING detached`, postID, WebBuilderContentHash(content)).Scan(&detached)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return detached, err
}

// SetDetached sets the detached flag directly.
func (w WebBuilderDB) SetDetached(ctx context.Context, postID int64, detached bool) error {
	_, err := w.q.Exec(ctx, `UPDATE web_builder_docs SET detached = $2 WHERE post_id = $1`, postID, detached)
	return err
}

// DeleteDoc removes a post's doc and every user's draft of it ("leave the builder for this page").
// The doc revisions stay with their core revisions. ErrNotFound when there was no doc.
func (w WebBuilderDB) DeleteDoc(ctx context.Context, postID int64) error {
	return pgx.BeginFunc(ctx, w.q, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM web_builder_drafts WHERE post_id = $1`, postID); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM web_builder_docs WHERE post_id = $1`, postID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// Drafts ----------------------------------------------------------------------------------------

// WebBuilderDraft is one user's autosave of a post (web_builder_drafts).
type WebBuilderDraft struct {
	PostID    int64
	UserID    int64
	Doc       json.RawMessage
	Page      json.RawMessage
	BaseRev   int
	UpdatedAt time.Time
}

// Draft loads a user's draft of a post; ErrNotFound when there is none.
func (w WebBuilderDB) Draft(ctx context.Context, postID, userID int64) (*WebBuilderDraft, error) {
	d := &WebBuilderDraft{}
	var doc, page []byte
	err := w.q.QueryRow(ctx, `SELECT post_id, user_id, doc, page, base_rev, updated_at FROM web_builder_drafts
		WHERE post_id = $1 AND user_id = $2`, postID, userID).Scan(&d.PostID, &d.UserID, &doc, &page, &d.BaseRev, &d.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	d.Doc, d.Page = doc, page
	return d, nil
}

// SaveDraft writes (inserts or replaces) the user's draft of the post and sets d.UpdatedAt.
func (w WebBuilderDB) SaveDraft(ctx context.Context, d *WebBuilderDraft) error {
	if err := requireDoc(d.Doc); err != nil {
		return err
	}
	return w.q.QueryRow(ctx, `INSERT INTO web_builder_drafts (post_id, user_id, doc, page, base_rev)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (post_id, user_id) DO UPDATE SET doc = $3, page = $4, base_rev = $5, updated_at = now()
		RETURNING updated_at`,
		d.PostID, d.UserID, []byte(d.Doc), rawOr(d.Page, "{}"), d.BaseRev).Scan(&d.UpdatedAt)
}

// DeleteDraft removes the user's draft of the post, if any.
func (w WebBuilderDB) DeleteDraft(ctx context.Context, postID, userID int64) error {
	_, err := w.q.Exec(ctx, `DELETE FROM web_builder_drafts WHERE post_id = $1 AND user_id = $2`, postID, userID)
	return err
}

// Revisions -------------------------------------------------------------------------------------

// WebBuilderRevision is the doc a builder publish stored next to its core post revision.
type WebBuilderRevision struct {
	RevisionID int64 // post_revisions.id
	PostID     int64
	Rev        int
	Doc        json.RawMessage
	Page       json.RawMessage
	Renderer   int
	CreatedAt  time.Time
}

// WebBuilderRevisionSummary is a revision list row: the builder rev plus the core revision's
// author, date and title.
type WebBuilderRevisionSummary struct {
	RevisionID int64
	Rev        int
	AuthorID   int64 // 0 = unknown / deleted user
	Title      string
	Date       time.Time // post_revisions.created_at
	Renderer   int
}

// AddRevision links a doc to a core post revision of the same post. The core revision must exist
// (foreign key); r.CreatedAt is filled in.
func (w WebBuilderDB) AddRevision(ctx context.Context, r *WebBuilderRevision) error {
	if err := requireDoc(r.Doc); err != nil {
		return err
	}
	var postID int64
	err := w.q.QueryRow(ctx, `SELECT post_id FROM post_revisions WHERE id = $1`, r.RevisionID).Scan(&postID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if postID != r.PostID {
		return fmt.Errorf("web builder: revision %d belongs to post %d, not %d", r.RevisionID, postID, r.PostID)
	}
	return w.q.QueryRow(ctx, `INSERT INTO web_builder_revisions (revision_id, post_id, rev, doc, page, renderer)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (revision_id) DO UPDATE SET rev = $3, doc = $4, page = $5, renderer = $6
		RETURNING created_at`,
		r.RevisionID, r.PostID, r.Rev, []byte(r.Doc), rawOr(r.Page, "{}"), r.Renderer).Scan(&r.CreatedAt)
}

// PruneRevisions deletes the builder docs of a post's revisions beyond the newest keep (<= 0 keeps
// all) and returns how many it removed. The core post revisions stay: only the stored doc (up to
// MaxDocBytes each) goes, so the older revisions can still be read as plain content.
func (w WebBuilderDB) PruneRevisions(ctx context.Context, postID int64, keep int) (int64, error) {
	if keep <= 0 {
		return 0, nil
	}
	tag, err := w.q.Exec(ctx, `DELETE FROM web_builder_revisions WHERE post_id = $1 AND revision_id NOT IN (
		SELECT revision_id FROM web_builder_revisions WHERE post_id = $1 ORDER BY rev DESC, revision_id DESC LIMIT $2)`, postID, keep)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// LatestPostRevisionID is the newest non-autosave core revision of a post (0 when there is none),
// the row a publish links its doc to after the core update recorded it.
func (w WebBuilderDB) LatestPostRevisionID(ctx context.Context, postID int64) (int64, error) {
	var id int64
	err := w.q.QueryRow(ctx, `SELECT id FROM post_revisions WHERE post_id = $1 AND NOT is_autosave
		ORDER BY created_at DESC, id DESC LIMIT 1`, postID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

// Revisions lists a post's builder revisions, newest rev first, with limit (<= 0: no limit) and
// offset for paging.
func (w WebBuilderDB) Revisions(ctx context.Context, postID int64, limit, offset int) ([]WebBuilderRevisionSummary, error) {
	q := `SELECT r.revision_id, r.rev, coalesce(p.author_id,0), p.title, p.created_at, r.renderer
		FROM web_builder_revisions r JOIN post_revisions p ON p.id = r.revision_id
		WHERE r.post_id = $1 ORDER BY r.rev DESC, r.revision_id DESC`
	args := []any{postID}
	if limit > 0 {
		q += ` LIMIT $2`
		args = append(args, limit)
	}
	if offset > 0 {
		args = append(args, offset)
		q += fmt.Sprintf(` OFFSET $%d`, len(args))
	}
	rows, err := w.q.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WebBuilderRevisionSummary{}
	for rows.Next() {
		var s WebBuilderRevisionSummary
		if err := rows.Scan(&s.RevisionID, &s.Rev, &s.AuthorID, &s.Title, &s.Date, &s.Renderer); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// CountRevisions is the number of builder revisions of a post.
func (w WebBuilderDB) CountRevisions(ctx context.Context, postID int64) (int, error) {
	var n int
	err := w.q.QueryRow(ctx, `SELECT count(*) FROM web_builder_revisions WHERE post_id = $1`, postID).Scan(&n)
	return n, err
}

// Revision loads one builder revision of a post; ErrNotFound when it does not exist or belongs to
// another post.
func (w WebBuilderDB) Revision(ctx context.Context, postID, revisionID int64) (*WebBuilderRevision, error) {
	r := &WebBuilderRevision{}
	var doc, page []byte
	err := w.q.QueryRow(ctx, `SELECT revision_id, post_id, rev, doc, page, renderer, created_at FROM web_builder_revisions
		WHERE revision_id = $1 AND post_id = $2`, revisionID, postID).
		Scan(&r.RevisionID, &r.PostID, &r.Rev, &doc, &page, &r.Renderer, &r.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	r.Doc, r.Page = doc, page
	return r, nil
}

// Library ---------------------------------------------------------------------------------------

// WebBuilderLibraryItem is a saved component (web_builder_library); Data is a Section.
type WebBuilderLibraryItem struct {
	ID        int64
	Name      string
	Data      json.RawMessage
	AuthorID  int64 // 0 = unknown / deleted user
	CreatedAt time.Time
	UpdatedAt time.Time
}

const wbLibCols = `id, name, data, coalesce(author_id,0), created_at, updated_at`

func scanWBLib(row pgx.Row) (*WebBuilderLibraryItem, error) {
	it := &WebBuilderLibraryItem{}
	var data []byte
	err := row.Scan(&it.ID, &it.Name, &data, &it.AuthorID, &it.CreatedAt, &it.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	it.Data = data
	return it, nil
}

// LibraryItems lists the saved components, newest first.
func (w WebBuilderDB) LibraryItems(ctx context.Context) ([]*WebBuilderLibraryItem, error) {
	rows, err := w.q.Query(ctx, `SELECT `+wbLibCols+` FROM web_builder_library ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*WebBuilderLibraryItem{}
	for rows.Next() {
		it, err := scanWBLib(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// LibraryItem loads one saved component; ErrNotFound when it does not exist.
func (w WebBuilderDB) LibraryItem(ctx context.Context, id int64) (*WebBuilderLibraryItem, error) {
	return scanWBLib(w.q.QueryRow(ctx, `SELECT `+wbLibCols+` FROM web_builder_library WHERE id = $1`, id))
}

// CountLibraryItems is the number of saved components on the site.
func (w WebBuilderDB) CountLibraryItems(ctx context.Context) (int, error) {
	var n int
	err := w.q.QueryRow(ctx, `SELECT count(*) FROM web_builder_library`).Scan(&n)
	return n, err
}

// wbLibraryLock is the advisory lock key that serializes library inserts, so the max-items check
// holds under concurrent saves.
const wbLibraryLock = 0x57424c4942 // "WBLIB"

// AddLibraryItem saves a component and fills in it.ID and the timestamps. With max > 0 it returns
// ErrLibraryFull when the site already holds max items.
func (w WebBuilderDB) AddLibraryItem(ctx context.Context, it *WebBuilderLibraryItem, max int) error {
	if len(it.Data) == 0 || string(it.Data) == "null" {
		return errors.New("web builder: library item data is required")
	}
	return pgx.BeginFunc(ctx, w.q, func(tx pgx.Tx) error {
		if max > 0 {
			if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(wbLibraryLock)); err != nil {
				return err
			}
			var n int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM web_builder_library`).Scan(&n); err != nil {
				return err
			}
			if n >= max {
				return ErrLibraryFull
			}
		}
		return tx.QueryRow(ctx, `INSERT INTO web_builder_library (name, data, author_id) VALUES ($1, $2, $3)
			RETURNING id, created_at, updated_at`, it.Name, []byte(it.Data), nullable(it.AuthorID)).
			Scan(&it.ID, &it.CreatedAt, &it.UpdatedAt)
	})
}

// UpdateLibraryItem replaces a saved component's name and data; ErrNotFound when it does not exist.
func (w WebBuilderDB) UpdateLibraryItem(ctx context.Context, it *WebBuilderLibraryItem) error {
	if len(it.Data) == 0 || string(it.Data) == "null" {
		return errors.New("web builder: library item data is required")
	}
	err := w.q.QueryRow(ctx, `UPDATE web_builder_library SET name = $2, data = $3, updated_at = now() WHERE id = $1
		RETURNING coalesce(author_id,0), created_at, updated_at`, it.ID, it.Name, []byte(it.Data)).
		Scan(&it.AuthorID, &it.CreatedAt, &it.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// DeleteLibraryItem removes a saved component; ErrNotFound when it does not exist.
func (w WebBuilderDB) DeleteLibraryItem(ctx context.Context, id int64) error {
	tag, err := w.q.Exec(ctx, `DELETE FROM web_builder_library WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
