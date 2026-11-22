package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"wpgo/internal/auth"
	"wpgo/internal/store"
)

// pursae/v1/migrate: the WP to Pursae plugin's push mode. The plugin uploads its package in chunks
// and asks Pursae to import it:
//
//	POST /pursae/v1/migrate/start          {size, sha256[, key]} -> {id, received, chunk_size}
//	PUT  /pursae/v1/migrate/{id}/chunk?offset=N   raw bytes      -> {received}
//	POST /pursae/v1/migrate/{id}/finish    {}                    -> {state}
//	GET  /pursae/v1/migrate/{id}                                 -> state, progress, report
//
// Authentication is either the one-time migration token the administrator generated on Tools ›
// Import › From WordPress (header X-Pursae-Migration-Token; it works for these routes and its own
// import only, and expires 24 hours after it was made), or an administrator's Application Password,
// in which case start carries the package key the plugin generated. A chunk at an offset below what
// was received rewrites from there, so an interrupted upload resumes from GET's "received". While an
// upload is active every chunk moves the token's expiry to 24 hours later (at most 7 days after the
// token was made), so a slow upload is not cut off. GET keeps answering with the token until the
// hourly wp2p_cleanup forgets it, an hour after the import ended, so the plugin can show the result.

const (
	wp2pTokenHeader  = "X-Pursae-Migration-Token" //nolint:gosec // a header name, not a credential
	wp2pTokenPrefix  = "pmt_"
	wp2pTokenTTL     = 24 * time.Hour
	wp2pChunkMax     = 32 << 20 // largest chunk a request may carry
	wp2pChunkSize    = 8 << 20  // the chunk size the plugin is told to use
	wp2pMaxPackage   = int64(64) << 30
	wp2pIDPattern    = `(?P<id>[a-f0-9]{32})`
	wp2pRouteMigrate = "/pursae/v1/migrate"
	// wp2pUploadMax is how long after the token was made an active upload can go on.
	wp2pUploadMax = 7 * 24 * time.Hour
	// wp2pChunkTimeout is how long a chunk's body may take to arrive; wp2pLeaseTTL, longer, is how long
	// a chunk holds the upload (two requests never write the package at once).
	wp2pChunkTimeout = 5 * time.Minute
	wp2pLeaseTTL     = wp2pChunkTimeout + time.Minute
)

func (s *Server) registerWP2PREST() {
	api := s.rest
	api.register(pursaeNS, wp2pRouteMigrate+"/start", map[string]restHandler{http.MethodPost: s.wp2pRESTStart}, nil)
	api.register(pursaeNS, wp2pRouteMigrate+"/"+wp2pIDPattern, map[string]restHandler{http.MethodGet: s.wp2pRESTStatus}, nil)
	api.register(pursaeNS, wp2pRouteMigrate+"/"+wp2pIDPattern+"/chunk", map[string]restHandler{
		http.MethodPut: s.wp2pRESTChunk, http.MethodPost: s.wp2pRESTChunk}, nil)
	api.register(pursaeNS, wp2pRouteMigrate+"/"+wp2pIDPattern+"/finish", map[string]restHandler{http.MethodPost: s.wp2pRESTFinish}, nil)
}

// wp2pNewToken makes a migration token and its import row; the token is returned once and only its
// hash is stored.
func (s *Server) wp2pNewToken(ctx context.Context, u *store.User) (token, id string, expires time.Time, err error) {
	var b [32]byte
	if _, err = rand.Read(b[:]); err != nil {
		return
	}
	token = wp2pTokenPrefix + base64.RawURLEncoding.EncodeToString(b[:])
	id = wp2pNewID()
	expires = time.Now().Add(wp2pTokenTTL)
	key := wp2pTokenKey(token)
	_, err = s.store.Pool.Exec(ctx, `INSERT INTO wp2p_migrations (id, token_hash, package_key, created_by, state, expires_at) VALUES ($1, $2, $3, $4, 'waiting', $5)`,
		id, wp2pTokenHash(token), key[:], u.ID, expires)
	return
}

func wp2pTokenHash(token string) string {
	h := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return hex.EncodeToString(h[:])
}

// wp2pRow is what the REST routes need of an import.
type wp2pRow struct {
	ID, TokenHash, State, SHA256, Error string
	Size, Received, CreatedBy           int64
	Expires                             *time.Time
}

const wp2pRowCols = `id, token_hash, state, sha256, error, size, received, created_by, expires_at`

func wp2pScanRow(row pgx.Row) (*wp2pRow, error) {
	r := &wp2pRow{}
	err := row.Scan(&r.ID, &r.TokenHash, &r.State, &r.SHA256, &r.Error, &r.Size, &r.Received, &r.CreatedBy, &r.Expires)
	return r, err
}

// wp2pAdmin is the REST user when it may run imports.
func wp2pAdmin(c *gin.Context) *store.User {
	if u := restCurrent(c); u != nil && u.Can("manage_options", nil) && u.Can("import", nil) {
		return u
	}
	return nil
}

// wp2pAuthorize checks the request against an import: its own token, or an administrator. It
// answers the error itself and returns nil when the request is not allowed.
func (s *Server) wp2pAuthorize(c *gin.Context, row *wp2pRow, writing bool) bool {
	if tok := strings.TrimSpace(c.GetHeader(wp2pTokenHeader)); tok != "" {
		if row.TokenHash == "" || subtle.ConstantTimeCompare([]byte(wp2pTokenHash(tok)), []byte(row.TokenHash)) != 1 {
			restErr(c, http.StatusUnauthorized, "pursae_migration_token_invalid", "The migration token is not valid for this import.")
			return false
		}
		if writing && row.Expires != nil && time.Now().After(*row.Expires) {
			restErr(c, http.StatusUnauthorized, "pursae_migration_token_expired", "The migration token has expired. Generate a new one in Pursae (Tools › Import › From WordPress).")
			return false
		}
		return true
	}
	if wp2pAdmin(c) == nil {
		if restCurrent(c) == nil {
			restErr(c, http.StatusUnauthorized, "rest_not_logged_in", "Send the migration token, or sign in with an administrator's Application Password.")
		} else {
			restErr(c, http.StatusForbidden, "rest_forbidden", "Sorry, you are not allowed to import into this site.")
		}
		return false
	}
	return true
}

// wp2pRESTStart opens (or resumes) an upload.
func (s *Server) wp2pRESTStart(c *gin.Context, req *restRequest) {
	ctx := c.Request.Context()
	size, _ := req.Int(c, "size")
	sum, _ := req.Str(c, "sha256")
	sum = strings.ToLower(strings.TrimSpace(sum))
	if size <= 0 || size > wp2pMaxPackage {
		restErr(c, http.StatusBadRequest, "rest_invalid_param", "size must be the package size in bytes (at most 64 GB).")
		return
	}
	if sum != "" && (len(sum) != 64 || strings.Trim(sum, "0123456789abcdef") != "") {
		restErr(c, http.StatusBadRequest, "rest_invalid_param", "sha256 must be 64 hex digits.")
		return
	}
	var id string
	if tok := strings.TrimSpace(c.GetHeader(wp2pTokenHeader)); tok != "" {
		row, err := wp2pScanRow(s.store.Pool.QueryRow(ctx, `SELECT `+wp2pRowCols+` FROM wp2p_migrations WHERE token_hash = $1`, wp2pTokenHash(tok)))
		if err != nil {
			restErr(c, http.StatusUnauthorized, "pursae_migration_token_invalid", "The migration token is not valid. Copy it again from Pursae (Tools › Import › From WordPress).")
			return
		}
		if !s.wp2pAuthorize(c, row, true) {
			return
		}
		switch {
		case row.State == "receiving" && row.Size == size && row.SHA256 == sum:
			// The same package again: resume.
			restJSON(c, http.StatusOK, map[string]any{"id": row.ID, "received": row.Received, "chunk_size": wp2pChunkSize})
			return
		case row.State != "waiting" && row.State != "receiving":
			restErr(c, http.StatusConflict, "pursae_migration_token_used", "This migration token was already used. Generate a new one for another import.")
			return
		}
		if _, err := s.store.Pool.Exec(ctx, `UPDATE wp2p_migrations SET state = 'receiving', size = $2, sha256 = $3, received = 0, updated_at = now() WHERE id = $1`,
			row.ID, size, sum); err != nil {
			restErr(c, http.StatusInternalServerError, "rest_db_error", err.Error())
			return
		}
		_ = os.Remove(s.wp2pPackagePath(row.ID))
		id = row.ID
	} else {
		u := wp2pAdmin(c)
		if u == nil {
			s.wp2pAuthorize(c, &wp2pRow{}, true)
			return
		}
		keyText, _ := req.Str(c, "key")
		key, err := wp2pParseKey(keyText)
		if err != nil {
			restErr(c, http.StatusBadRequest, "rest_invalid_param", "key: "+err.Error())
			return
		}
		id = wp2pNewID()
		if _, err := s.store.Pool.Exec(ctx, `INSERT INTO wp2p_migrations (id, package_key, created_by, state, size, sha256) VALUES ($1, $2, $3, 'receiving', $4, $5)`,
			id, key[:], u.ID, size, sum); err != nil {
			restErr(c, http.StatusInternalServerError, "rest_db_error", err.Error())
			return
		}
	}
	if err := os.MkdirAll(s.wp2pImportDir(), 0o700); err != nil {
		restErr(c, http.StatusInternalServerError, "pursae_migration_storage", err.Error())
		return
	}
	restJSON(c, http.StatusOK, map[string]any{"id": id, "received": 0, "chunk_size": wp2pChunkSize})
}

// wp2pRESTChunk stores bytes at ?offset=N. A chunk claims the upload with a short lease on its row
// (no transaction or row lock is held while the body arrives, which can take minutes), writes the
// package, then records what was received if it still holds the lease. A second request for the same
// import meanwhile gets 409 and retries.
func (s *Server) wp2pRESTChunk(c *gin.Context, req *restRequest) {
	ctx := c.Request.Context()
	pool := s.store.Pool
	offset, err := strconv.ParseInt(c.Query("offset"), 10, 64)
	if err != nil || offset < 0 {
		restErr(c, http.StatusBadRequest, "rest_invalid_param", "offset must be the byte offset of the chunk.")
		return
	}
	row, err := wp2pScanRow(pool.QueryRow(ctx, `SELECT `+wp2pRowCols+` FROM wp2p_migrations WHERE id = $1`, req.Params["id"]))
	if err != nil {
		restErr(c, http.StatusNotFound, "pursae_migration_not_found", "Unknown import.")
		return
	}
	if !s.wp2pAuthorize(c, row, true) {
		return
	}
	if row.State != "receiving" {
		restErr(c, http.StatusConflict, "pursae_migration_not_receiving", "This import is not receiving data (state: "+row.State+").")
		return
	}
	if offset > row.Received {
		restErr(c, http.StatusConflict, "pursae_migration_offset", "The chunk does not continue the upload.", map[string]any{"received": row.Received})
		return
	}
	lease := auth.RandomToken(16)
	tag, err := pool.Exec(ctx, `UPDATE wp2p_migrations SET lease = $2, lease_until = now() + make_interval(secs => $3)
		WHERE id = $1 AND state = 'receiving' AND received >= $4 AND (lease = '' OR lease_until IS NULL OR lease_until < now())`,
		row.ID, lease, wp2pLeaseTTL.Seconds(), offset)
	if err != nil {
		restErr(c, http.StatusInternalServerError, "rest_db_error", err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		restErr(c, http.StatusConflict, "pursae_migration_busy", "Another chunk of this upload is being received; send this one again in a moment.", map[string]any{"received": row.Received})
		return
	}
	wctx := context.WithoutCancel(ctx)
	// release gives the lease back; after a failed write the package ends at offset, and so does received.
	release := func(truncated bool) {
		q := `UPDATE wp2p_migrations SET lease = '', lease_until = NULL WHERE id = $1 AND lease = $2`
		if truncated {
			q = `UPDATE wp2p_migrations SET lease = '', lease_until = NULL, received = least(received, $3) WHERE id = $1 AND lease = $2`
			_, _ = pool.Exec(wctx, q, row.ID, lease, offset)
			return
		}
		_, _ = pool.Exec(wctx, q, row.ID, lease)
	}
	// The body must be in before the lease lapses: a read deadline on the connection, or, where the
	// writer offers none, closing the body when the time is up.
	deadline := time.Now().Add(wp2pChunkTimeout)
	if http.NewResponseController(c.Writer).SetReadDeadline(deadline) != nil {
		t := time.AfterFunc(wp2pChunkTimeout, func() { _ = c.Request.Body.Close() })
		defer t.Stop()
	}
	n, werr := s.wp2pWriteChunk(row, offset, c.Request.Body)
	switch {
	case errors.Is(werr, errWP2PChunkTooLarge):
		release(true)
		restErr(c, http.StatusRequestEntityTooLarge, "pursae_migration_chunk_too_large", "The chunk is too large, or goes past the announced size.", map[string]any{"received": offset})
		return
	case errors.Is(werr, errWP2PChunkBody):
		release(true)
		restErr(c, http.StatusBadRequest, "pursae_migration_chunk", "The chunk was not received completely: "+werr.Error(), map[string]any{"received": offset})
		return
	case werr != nil:
		release(true)
		restErr(c, http.StatusInternalServerError, "pursae_migration_storage", werr.Error())
		return
	}
	// Recorded only while this request still holds the upload (not revoked, the lease not lapsed). An
	// active upload keeps its token alive: 24 hours from now, at most wp2pUploadMax after it was made.
	tag, err = pool.Exec(wctx, `UPDATE wp2p_migrations SET received = $3, lease = '', lease_until = NULL, updated_at = now(),
		expires_at = CASE WHEN expires_at IS NULL THEN NULL
			ELSE least(created_at + make_interval(secs => $4), greatest(expires_at, now() + make_interval(secs => $5))) END
		WHERE id = $1 AND lease = $2 AND state = 'receiving'`, row.ID, lease, offset+n, wp2pUploadMax.Seconds(), wp2pTokenTTL.Seconds())
	if err != nil {
		release(true)
		restErr(c, http.StatusInternalServerError, "rest_db_error", err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		restErr(c, http.StatusConflict, "pursae_migration_busy", "The upload changed while this chunk was received; ask for its state and continue from there.")
		return
	}
	restJSON(c, http.StatusOK, map[string]any{"received": offset + n, "size": row.Size})
}

var (
	errWP2PChunkTooLarge = errors.New("the chunk is too large")
	errWP2PChunkBody     = errors.New("the request body failed")
)

// wp2pWriteChunk writes body into the package at offset (the package ends there first) and returns
// how many bytes it wrote. On an error the package is cut back to offset.
func (s *Server) wp2pWriteChunk(row *wp2pRow, offset int64, body io.Reader) (int64, error) {
	f, err := os.OpenFile(s.wp2pPackagePath(row.ID), os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	if err := f.Truncate(offset); err != nil {
		return 0, err
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return 0, err
	}
	limit := min(int64(wp2pChunkMax), row.Size-offset)
	n, err := io.Copy(f, io.LimitReader(body, limit+1))
	if err != nil {
		_ = f.Truncate(offset)
		return 0, fmt.Errorf("%w: %w", errWP2PChunkBody, err)
	}
	if n > limit {
		_ = f.Truncate(offset)
		return 0, errWP2PChunkTooLarge
	}
	if err := f.Sync(); err != nil {
		_ = f.Truncate(offset)
		return 0, err
	}
	return n, nil
}

// wp2pRESTFinish checks the upload and queues the import.
func (s *Server) wp2pRESTFinish(c *gin.Context, req *restRequest) {
	ctx := c.Request.Context()
	row, err := wp2pScanRow(s.store.Pool.QueryRow(ctx, `SELECT `+wp2pRowCols+` FROM wp2p_migrations WHERE id = $1`, req.Params["id"]))
	if err != nil {
		restErr(c, http.StatusNotFound, "pursae_migration_not_found", "Unknown import.")
		return
	}
	if !s.wp2pAuthorize(c, row, true) {
		return
	}
	if row.State != "receiving" {
		if row.State == "queued" || row.State == "running" || row.State == "done" {
			restJSON(c, http.StatusOK, map[string]any{"id": row.ID, "state": row.State})
			return
		}
		restErr(c, http.StatusConflict, "pursae_migration_not_receiving", "This import is not receiving data (state: "+row.State+").")
		return
	}
	if row.Received != row.Size {
		restErr(c, http.StatusConflict, "pursae_migration_incomplete", "The upload is not complete.", map[string]any{"received": row.Received, "size": row.Size})
		return
	}
	if row.SHA256 != "" {
		f, err := os.Open(s.wp2pPackagePath(row.ID))
		if err != nil {
			restErr(c, http.StatusInternalServerError, "pursae_migration_storage", err.Error())
			return
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		f.Close()
		if err != nil || hex.EncodeToString(h.Sum(nil)) != row.SHA256 {
			_, _ = s.store.Pool.Exec(ctx, `UPDATE wp2p_migrations SET received = 0 WHERE id = $1`, row.ID)
			restErr(c, http.StatusConflict, "pursae_migration_checksum", "The uploaded package does not match its checksum; upload it again.", map[string]any{"received": 0})
			return
		}
	}
	if _, err := s.store.Pool.Exec(ctx, `UPDATE wp2p_migrations SET state = 'queued', updated_at = now() WHERE id = $1`, row.ID); err != nil {
		restErr(c, http.StatusInternalServerError, "rest_db_error", err.Error())
		return
	}
	if err := s.queueJob(ctx, "wp2p_import", map[string]any{"id": row.ID}, time.Time{}); err != nil {
		restErr(c, http.StatusInternalServerError, "rest_db_error", err.Error())
		return
	}
	restJSON(c, http.StatusAccepted, map[string]any{"id": row.ID, "state": "queued", "status_url": s.links(c).HomeURL("/wp-admin/import-wordpress?run=" + row.ID)})
}

// wp2pRESTStatus reports an import's state, progress and, once it is over, its report.
func (s *Server) wp2pRESTStatus(c *gin.Context, req *restRequest) {
	ctx := c.Request.Context()
	row, err := wp2pScanRow(s.store.Pool.QueryRow(ctx, `SELECT `+wp2pRowCols+` FROM wp2p_migrations WHERE id = $1`, req.Params["id"]))
	if err != nil {
		restErr(c, http.StatusNotFound, "pursae_migration_not_found", "Unknown import.")
		return
	}
	if !s.wp2pAuthorize(c, row, false) {
		return
	}
	out := map[string]any{"id": row.ID, "state": row.State, "size": row.Size, "received": row.Received, "error": row.Error}
	var prog, rep []byte
	if s.store.Pool.QueryRow(ctx, `SELECT progress, report FROM wp2p_migrations WHERE id = $1`, row.ID).Scan(&prog, &rep) == nil {
		var p wp2pProgress
		if json.Unmarshal(prog, &p) == nil {
			out["phase"], out["phase_label"], out["phases_done"] = p.Phase, wp2pPhaseLabels[p.Phase], p.Done
		}
		if row.State == "done" || row.State == "failed" {
			var r wp2pReport
			if json.Unmarshal(rep, &r) == nil {
				// Counts only: the per-item lists and the content log stay on the admin screen.
				r.Content, r.Skipped = nil, nil
				out["report"] = r
			}
		}
	}
	restJSON(c, http.StatusOK, out)
}
