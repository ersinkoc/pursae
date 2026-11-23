package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

// Tools › Import › From WordPress (/wp-admin/import-wordpress): the Pursae side of WP to Pursae. The
// administrator either uploads a package downloaded from the plugin (with the key the plugin showed),
// or generates a one-time migration token for the plugin's "Send to Pursae" mode. Either way the
// import runs as the "wp2p_import" job and this screen shows its progress and report.

const (
	wp2pNonceUpload = "wp2p-upload"
	wp2pNonceAction = "wp2p-action"
)

// wp2pListRow is a line of the imports table.
type wp2pListRow struct {
	ID, State, Source, Phase, Error string
	Size, Received                  int64
	Created                         time.Time
	Token                           bool
	Expired                         bool
	CLI                             bool // run by `pursae import wordpress`
	Stale                           bool // a command-line run that stopped writing progress (killed): it can be removed
}

// wp2pReportView is the report as the screen shows it.
type wp2pReportView struct {
	Counts   []wp2pCount
	Existing []wp2pCount
	Unmapped []wp2pCount
	Skipped  []wp2pSkip
	Notes    []string
	Log      []string
	More     int // skipped items not listed
}

type wp2pCount struct {
	Label string
	N     int
}

// wp2pCountLabels name the report's counters.
var wp2pCountLabels = map[string]string{ //nolint:gosec // labels, not credentials
	"users": "Users", "posts": "Posts", "pages": "Pages", "patterns": "Patterns", "templates": "Theme Builder templates", "terms": "Categories, tags and terms",
	"comments": "Comments and reviews", "media": "Media files", "menu_items": "Menu items", "products": "Products", "variations": "Variations",
	"settings": "Settings", "tax_rates": "Tax rates", "coupons": "Coupons", "orders": "Orders", "refunds": "Refunds", "customers": "Customer accounts",
	"guest_customers": "Guest customers (listed from their orders)", "users_password_reset": "Users who must reset their password",
}

func wp2pCounts(m map[string]int) []wp2pCount {
	out := []wp2pCount{}
	for k, n := range m {
		if n == 0 {
			continue
		}
		label := wp2pCountLabels[k]
		if label == "" {
			if rest, ok := strings.CutPrefix(k, "skipped_"); ok {
				label = "Skipped: " + strings.ReplaceAll(rest, "_", " ")
			} else {
				label = strings.ReplaceAll(k, "_", " ")
			}
		}
		out = append(out, wp2pCount{Label: label, N: n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

func (s *Server) wp2pScreen(c *gin.Context) {
	u := currentUser(c)
	if !u.Can("manage_options", nil) || !u.Can("import", nil) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to import content into this site.")
		return
	}
	ctx := c.Request.Context()
	d := map[string]any{"UploadNonce": s.nonce(c, wp2pNonceUpload), "ActionNonce": s.nonce(c, wp2pNonceAction), "MaxUpload": "64 GB"}
	if c.Request.Method == http.MethodPost {
		if c.Query("action") == "upload" {
			if s.wp2pUpload(c, d) {
				return
			}
		} else {
			if !s.checkNonce(c, wp2pNonceAction) {
				s.denyNonce(c)
				return
			}
			id := c.PostForm("id")
			switch c.PostForm("action") {
			case "token":
				token, _, expires, err := s.wp2pNewToken(ctx, u)
				if err != nil {
					d["Error"] = "The token could not be created: " + err.Error()
				} else {
					d["NewToken"], d["TokenExpires"] = token, expires.Local().Format("2006-01-02 15:04")
				}
			case "revoke":
				if wxrTokenRE.MatchString(id) {
					_, _ = s.store.Pool.Exec(ctx, `UPDATE wp2p_migrations SET state = 'revoked', package_key = NULL, updated_at = now()
						WHERE id = $1 AND state IN ('waiting', 'receiving')`, id)
					_ = os.Remove(s.wp2pPackagePath(id))
				}
				c.Redirect(http.StatusSeeOther, "/wp-admin/import-wordpress")
				return
			case "resume":
				// A command-line run has no package or key here: it is resumed by running the command again.
				if wxrTokenRE.MatchString(id) {
					tag, err := s.store.Pool.Exec(ctx, `UPDATE wp2p_migrations SET state = 'queued', error = '', updated_at = now() WHERE id = $1 AND state = 'failed' AND NOT cli`, id)
					if err == nil && tag.RowsAffected() == 1 {
						_ = s.queueJob(ctx, "wp2p_import", map[string]any{"id": id}, time.Time{})
					}
				}
				c.Redirect(http.StatusSeeOther, "/wp-admin/import-wordpress?run="+id)
				return
			case "delete":
				if wxrTokenRE.MatchString(id) {
					_, _ = s.store.Pool.Exec(ctx, `DELETE FROM wp2p_migrations WHERE id = $1 AND (state NOT IN ('queued', 'running') OR (cli AND updated_at < now() - make_interval(secs => $2)))`,
						id, wp2pStaleCLI.Seconds())
					_ = os.Remove(s.wp2pPackagePath(id))
					_ = os.RemoveAll(s.wp2pWorkDir(id))
				}
				c.Redirect(http.StatusSeeOther, "/wp-admin/import-wordpress")
				return
			}
		}
	}
	if run := c.Query("run"); run != "" {
		s.wp2pRunView(c, run, d)
	}
	d["Rows"] = s.wp2pList(c)
	d["ImportURL"] = s.links(c).HomeURL("/")
	s.adminPage(c, "import", "Import from WordPress", "import-php", "admin/import-wordpress", d)
}

// wp2pList is the recent imports and tokens.
func (s *Server) wp2pList(c *gin.Context) []wp2pListRow {
	rows, err := s.store.Pool.Query(c.Request.Context(), `SELECT id, state, source, progress->>'phase', error, size, received, created_at, token_hash <> '',
		coalesce(expires_at < now(), false), cli, cli AND state = 'running' AND updated_at < now() - make_interval(secs => $1)
		FROM wp2p_migrations ORDER BY created_at DESC LIMIT 20`, wp2pStaleCLI.Seconds())
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []wp2pListRow
	for rows.Next() {
		var r wp2pListRow
		var phase *string
		if rows.Scan(&r.ID, &r.State, &r.Source, &phase, &r.Error, &r.Size, &r.Received, &r.Created, &r.Token, &r.Expired, &r.CLI, &r.Stale) == nil {
			if phase != nil {
				r.Phase = wp2pPhaseLabels[*phase]
			}
			if r.Expired && r.State == "waiting" {
				r.State = "expired"
			}
			out = append(out, r)
		}
	}
	return out
}

// wp2pRunView fills the progress or report of one import.
func (s *Server) wp2pRunView(c *gin.Context, id string, d map[string]any) {
	if !wxrTokenRE.MatchString(id) {
		d["Error"] = "Invalid import."
		return
	}
	var state, errMsg string
	var prog, rep []byte
	var cli bool
	if err := s.store.Pool.QueryRow(c.Request.Context(), `SELECT state, error, progress, report, cli FROM wp2p_migrations WHERE id = $1`, id).
		Scan(&state, &errMsg, &prog, &rep, &cli); err != nil {
		d["Error"] = "This import is not known."
		return
	}
	var p wp2pProgress
	_ = json.Unmarshal(prog, &p)
	var r wp2pReport
	_ = json.Unmarshal(rep, &r)
	phases := []map[string]any{}
	for _, ph := range wp2pPhases {
		st := "pending"
		switch {
		case p.finished(ph):
			st = "done"
		case ph == p.Phase && (state == "running" || state == "failed"):
			st = state
		}
		phases = append(phases, map[string]any{"Label": wp2pPhaseLabels[ph], "State": st, "Items": p.Items})
	}
	view := wp2pReportView{Counts: wp2pCounts(r.Counts), Existing: wp2pCounts(r.Existing), Unmapped: wp2pCounts(r.Unmapped), Skipped: r.Skipped, Notes: r.Notes}
	if r.Content != nil {
		view.Log = r.Content.Log
	}
	d["Run"] = map[string]any{"ID": id, "State": state, "Error": errMsg, "Phases": phases, "Report": view, "Source": r.Source,
		"Active": state == "queued" || state == "running", "CLI": cli}
}

// wp2pUpload stores an uploaded package (streamed: it can be gigabytes) and queues its import. The
// form sends the key before the file, so the key is known when the file arrives; the nonce is in the
// query string, since reading it from the form would buffer the whole upload.
// It reports whether it answered the request itself (a redirect, or the expired-link screen).
func (s *Server) wp2pUpload(c *gin.Context, d map[string]any) bool {
	var uid int64
	var token string
	if u := currentUser(c); u != nil {
		uid = u.ID
	}
	if sess := currentSession(c); sess != nil {
		token = sess.Token
	}
	if s.auth.VerifyNonce(c.Query("_wpnonce"), wp2pNonceUpload, uid, token) <= 0 {
		s.denyNonce(c)
		return true
	}
	ctx := c.Request.Context()
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, wp2pMaxPackage+1<<20)
	mr, err := c.Request.MultipartReader()
	if err != nil {
		d["Error"] = "No file was received."
		return false
	}
	if err := os.MkdirAll(s.wp2pImportDir(), 0o700); err != nil {
		d["Error"] = err.Error()
		return false
	}
	var keyText string
	id := wp2pNewID()
	path := s.wp2pPackagePath(id)
	got := false
	for {
		part, err := mr.NextPart()
		if err != nil {
			break
		}
		switch part.FormName() {
		case "key":
			b, _ := io.ReadAll(io.LimitReader(part, 512))
			keyText = string(b)
		case "package":
			if part.FileName() == "" {
				continue
			}
			f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
			if err != nil {
				d["Error"] = err.Error()
				return false
			}
			_, err = io.Copy(f, part)
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				_ = os.Remove(path)
				d["Error"] = "The upload failed: " + err.Error()
				return false
			}
			got = true
		}
	}
	if !got {
		d["Error"] = "No file was received."
		return false
	}
	key, err := wp2pParseKey(keyText)
	if err == nil {
		err = wp2pCheckPackage(path, key)
	}
	if err != nil {
		_ = os.Remove(path)
		d["Error"] = ucfirstStr(err.Error()) + "."
		return false
	}
	if _, err := s.store.Pool.Exec(ctx, `INSERT INTO wp2p_migrations (id, package_key, created_by, state, size, received) VALUES ($1, $2, $3, 'queued', $4, $4)`,
		id, key[:], uid, fileSize(path)); err != nil {
		_ = os.Remove(path)
		d["Error"] = err.Error()
		return false
	}
	if err := s.queueJob(ctx, "wp2p_import", map[string]any{"id": id}, time.Time{}); err != nil {
		d["Error"] = err.Error()
		return false
	}
	c.Redirect(http.StatusSeeOther, "/wp-admin/import-wordpress?run="+id)
	return true
}

// wp2pCheckPackage opens the first chunk of a package, so a wrong key is reported at once instead of
// by the background run.
func wp2pCheckPackage(path string, key wp2pKey) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	dec, err := newWP2PReader(f, key)
	if err != nil {
		return err
	}
	var b [1]byte
	if _, err := dec.Read(b[:]); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func fileSize(path string) int64 {
	if st, err := os.Stat(path); err == nil {
		return st.Size()
	}
	return 0
}

// Retention of WP to Pursae imports (wp2p_cleanup, hourly). A package and its extracted files hold the
// site's users with their password hashes and the customers' orders: they never stay longer than an
// import needs them.
const (
	// wp2pAbandonAfter: an upload without activity, or an import that failed and was not resumed,
	// loses its package and extracted files (and with them Resume).
	wp2pAbandonAfter = 7 * 24 * time.Hour
	// wp2pReportKeep: the report's itemized lists (skipped items, the content log) are dropped; the
	// counts and notes stay.
	wp2pReportKeep = 30 * 24 * time.Hour
	// wp2pTokenGrace: a token keeps answering GET this long after its import ended.
	wp2pTokenGrace = time.Hour
	// wp2pStaleCLI: a command-line run whose row was not touched for this long was killed (SIGKILL, a
	// crash, a lost terminal). A live run touches its row every wp2pHeartbeat, whatever it is doing
	// (also during a long extraction or one huge file), so this is many missed heartbeats.
	wp2pStaleCLI = 30 * time.Minute
)

// wp2pHeartbeat is how often a running import touches its row (updated_at). A variable for tests.
var wp2pHeartbeat = 30 * time.Second

// wp2pCleanup is the wp2p_cleanup job: it closes abandoned uploads, deletes the files of imports that
// will not go on, forgets the tokens of finished imports, trims old reports and removes files in the
// import directory that no import owns.
func (s *Server) wp2pCleanup(ctx context.Context) error {
	pool := s.store.Pool
	var gone []string
	collect := func(sql string, args ...any) error {
		rows, err := pool.Query(ctx, sql, args...)
		if err != nil {
			return err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
		gone = append(gone, ids...)
		return err
	}
	// Tokens that expired unused, and uploads that stopped: closed, key forgotten.
	if err := collect(`UPDATE wp2p_migrations SET state = 'expired', package_key = NULL, lease = '', updated_at = now(),
			error = 'The upload did not finish in time; generate a new token to start again.'
		WHERE (state = 'waiting' AND expires_at < now())
			OR (state = 'receiving' AND (expires_at < now() OR (expires_at IS NULL AND updated_at < now() - make_interval(secs => $1))))
		RETURNING id`, wp2pAbandonAfter.Seconds()); err != nil {
		return err
	}
	// Command-line runs that were killed: failed, their extracted files deleted (they are resumed by
	// running the command again).
	stale, err := s.wp2pFailStaleCLI(ctx)
	if err != nil {
		return err
	}
	gone = append(gone, stale...)
	// Failed imports nobody resumed: their files go, and Resume with them. A command-line import has
	// no package here to delete (the file is the person's own) and no Resume: it is only closed.
	if err := collect(`UPDATE wp2p_migrations SET state = 'expired', package_key = NULL, updated_at = now(),
			error = error || CASE WHEN cli THEN ' (Closed after 7 days; run the command again to import.)'
				ELSE ' (Not resumed within 7 days: the package and its extracted files were deleted.)' END
		WHERE state = 'failed' AND updated_at < now() - make_interval(secs => $1) RETURNING id`, wp2pAbandonAfter.Seconds()); err != nil {
		return err
	}
	for _, id := range gone {
		_ = os.Remove(s.wp2pPackagePath(id))
		_ = os.RemoveAll(s.wp2pWorkDir(id))
	}
	// Tokens of imports that ended.
	if _, err := pool.Exec(ctx, `UPDATE wp2p_migrations SET token_hash = '' WHERE token_hash <> '' AND state IN ('done', 'failed', 'expired', 'revoked')
		AND updated_at < now() - make_interval(secs => $1)`, wp2pTokenGrace.Seconds()); err != nil {
		return err
	}
	// Old reports keep their counts and notes only.
	if _, err := pool.Exec(ctx, `UPDATE wp2p_migrations SET report = (report - 'skipped') #- '{content,Log}'
		WHERE state IN ('done', 'failed', 'expired', 'revoked') AND updated_at < now() - make_interval(secs => $1)
			AND (report ? 'skipped' OR coalesce(report->'content', '{}'::jsonb) ? 'Log')`, wp2pReportKeep.Seconds()); err != nil {
		return err
	}
	return s.wp2pRemoveOrphans(ctx)
}

// wp2pFailStaleCLI closes the command-line imports that are "running" but whose process is gone (no
// heartbeat for wp2pStaleCLI): their state becomes failed, and the files the run extracted (users with
// password hashes, orders) are deleted. It returns their ids. `import wordpress` calls it before it starts,
// so running the command again after a kill cleans up after the one that died; the wp2p_cleanup job
// calls it hourly.
func (s *Server) wp2pFailStaleCLI(ctx context.Context) ([]string, error) {
	rows, err := s.store.Pool.Query(ctx, `UPDATE wp2p_migrations SET state = 'failed', package_key = NULL, updated_at = now(),
			error = 'The command stopped before the import was finished (the process was killed); run it again to continue.'
		WHERE cli AND state = 'running' AND updated_at < now() - make_interval(secs => $1) RETURNING id`, wp2pStaleCLI.Seconds())
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		_ = os.RemoveAll(s.wp2pWorkDir(id))
	}
	return ids, nil
}

// wp2pRemoveOrphans removes what is in the import directory without an import that may still need it:
// files of deleted rows, and the extracted files of finished ones (older than a day, so a package being
// stored right now is not taken).
func (s *Server) wp2pRemoveOrphans(ctx context.Context) error {
	entries, err := os.ReadDir(s.wp2pImportDir())
	if err != nil {
		return nil //nolint:nilerr // no import directory: nothing to remove
	}
	keep := map[string]bool{}
	rows, err := s.store.Pool.Query(ctx, `SELECT id FROM wp2p_migrations WHERE state IN ('waiting', 'receiving', 'queued', 'running', 'failed')`)
	if err != nil {
		return err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	for _, id := range ids {
		keep[id] = true
	}
	for _, e := range entries {
		id := strings.TrimSuffix(e.Name(), ".wp2p")
		if keep[id] {
			continue
		}
		info, err := e.Info()
		if err != nil || time.Since(info.ModTime()) < 24*time.Hour {
			continue
		}
		_ = os.RemoveAll(filepath.Join(s.wp2pImportDir(), e.Name()))
	}
	return nil
}
