package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

// Background work of HeadlessGo: scheduled publishing / unpublishing of entries and the webhook
// delivery queue. Both live in the database (hg_entries.publish_at / unpublish_at and
// hg_webhook_queue), so nothing is lost on a restart: the recurring headlessgo_tick job of the
// site's job runner (scheduler.go, every minute, claimed with FOR UPDATE SKIP LOCKED so several
// instances never run it twice) picks up whatever is due. Webhook retries are also run in-process
// as soon as their backoff ends; the job catches up after a restart.

const hgTickJob = "headlessgo_tick"

// hgEnsureTick schedules the recurring job (activation, and whenever something is scheduled).
func (s *Server) hgEnsureTick(ctx context.Context) {
	_, _ = s.store.Pool.Exec(ctx, `INSERT INTO jobs(name, run_at, recurrence)
		SELECT $1, now() + interval '1 minute', 'every:60' WHERE NOT EXISTS (SELECT 1 FROM jobs WHERE name = $1 AND recurrence <> '')`, hgTickJob)
}

func (s *Server) hgDropTick(ctx context.Context) {
	_, _ = s.store.Pool.Exec(ctx, `DELETE FROM jobs WHERE name = $1`, hgTickJob)
}

// hgTick is the job: due schedules, then due webhook deliveries.
func (s *Server) hgTick(ctx context.Context) error {
	if !s.hgOn() {
		return nil
	}
	s.hgRunSchedules(ctx)
	s.hgProcessQueue(ctx)
	return nil
}

// Scheduled publishing --------------------------------------------------------------------------------

// hgSetSchedule sets (or, with nil, clears) when a document version is published and unpublished.
func (s *Server) hgSetSchedule(ctx context.Context, sc *hgSchema, doc, locale string, publishAt, unpublishAt *time.Time) (*hgEntry, error) {
	if !sc.DraftAndPublish {
		return nil, errors.New("This content type does not use draft & publish: its entries are always published.")
	}
	if publishAt != nil && unpublishAt != nil && !unpublishAt.After(*publishAt) {
		return nil, errors.New("The unpublication must come after the publication.")
	}
	e, err := hgScanEntry(s.store.Pool.QueryRow(ctx, `UPDATE hg_entries SET publish_at = $4, unpublish_at = $5, schedule_error = ''
		WHERE type_uid = $1 AND document_id = $2 AND locale = $3 RETURNING `+hgEntryCols, sc.UID, doc, locale, publishAt, unpublishAt))
	if err != nil {
		return nil, errHGNotFound
	}
	if publishAt != nil || unpublishAt != nil {
		s.hgEnsureTick(ctx)
	}
	return e, nil
}

// hgRunSchedules publishes and unpublishes the entries whose time has come. Each row is claimed
// in its own transaction (FOR UPDATE SKIP LOCKED); a publication that fails validation is cleared
// and its reason kept in schedule_error. It returns how many entries changed.
func (s *Server) hgRunSchedules(ctx context.Context) int {
	n := 0
	for _, kind := range []string{"publish_at", "unpublish_at"} {
		rows, err := s.store.Pool.Query(ctx, `SELECT id FROM hg_entries WHERE `+kind+` IS NOT NULL AND `+kind+` <= now() ORDER BY `+kind+` LIMIT 200`)
		if err != nil {
			continue
		}
		var ids []int64
		for rows.Next() {
			var id int64
			if rows.Scan(&id) == nil {
				ids = append(ids, id)
			}
		}
		rows.Close()
		for _, id := range ids {
			if s.hgRunSchedule(ctx, id, kind == "publish_at") {
				n++
			}
		}
	}
	return n
}

func (s *Server) hgRunSchedule(ctx context.Context, id int64, publish bool) bool {
	col := "unpublish_at"
	if publish {
		col = "publish_at"
	}
	tx, err := s.store.Pool.Begin(ctx)
	if err != nil {
		return false
	}
	defer tx.Rollback(ctx)
	e, err := hgScanEntry(tx.QueryRow(ctx, `SELECT `+hgEntryCols+` FROM hg_entries WHERE id = $1 AND `+col+` <= now() FOR UPDATE SKIP LOCKED`, id))
	if err != nil {
		return false // done by another instance, or rescheduled
	}
	sc := s.hgReg(ctx).Get(e.TypeUID)
	if sc == nil || !sc.DraftAndPublish {
		_, _ = tx.Exec(ctx, `UPDATE hg_entries SET publish_at = NULL, unpublish_at = NULL WHERE id = $1`, id)
		_ = tx.Commit(ctx)
		return false
	}
	event, action := "entry.unpublish", "unschedule"
	if publish {
		if err := hgReviewGate(sc, e); err != nil {
			_, _ = tx.Exec(ctx, `UPDATE hg_entries SET publish_at = NULL, schedule_error = $2 WHERE id = $1`, id, "The scheduled publication failed: "+err.Error())
			_ = tx.Commit(ctx)
			return false
		}
		h := &hgCoercer{s: s, ctx: ctx, reg: s.hgReg(ctx), required: true}
		h.object(sc, map[string]any{}, e.Data, nil, true)
		if len(h.errs) > 0 {
			_, _ = tx.Exec(ctx, `UPDATE hg_entries SET publish_at = NULL, schedule_error = $2 WHERE id = $1`, id, "The scheduled publication failed: "+h.errs.Error())
			_ = tx.Commit(ctx)
			return false
		}
		e, err = hgScanEntry(tx.QueryRow(ctx, `UPDATE hg_entries SET published_data = data, published_at = now(), publish_at = NULL, schedule_error = ''
			WHERE id = $1 RETURNING `+hgEntryCols, id))
		event, action = "entry.publish", "schedule"
	} else {
		e, err = hgScanEntry(tx.QueryRow(ctx, `UPDATE hg_entries SET published_data = NULL, published_at = NULL, unpublish_at = NULL
			WHERE id = $1 RETURNING `+hgEntryCols, id))
	}
	if err != nil {
		return false
	}
	if err := tx.Commit(ctx); err != nil {
		log.Printf("headlessgo: schedule: %v", err)
		return false
	}
	s.hgRecordVersion(ctx, e, action, 0)
	s.hgEmit(ctx, event, sc, e)
	return true
}

// Bulk actions -------------------------------------------------------------------------------------------

// hgBulkResult counts what a bulk action did.
type hgBulkResult struct {
	Done   int
	Failed []string // "<document id>: <reason>"
}

func (r hgBulkResult) Message(action string) string {
	verb := map[string]string{"publish": "published", "unpublish": "unpublished", "delete": "deleted"}[action]
	msg := fmt.Sprintf("%d entries %s.", r.Done, verb)
	if r.Done == 1 {
		msg = fmt.Sprintf("1 entry %s.", verb)
	}
	if len(r.Failed) > 0 {
		msg += fmt.Sprintf(" %d failed: %s", len(r.Failed), strings.Join(r.Failed, "; "))
	}
	return msg
}

// hgBulk publishes, unpublishes or deletes documents: their version in locale, or every locale
// version when allLocales is set (and for types without i18n).
func (s *Server) hgBulk(ctx context.Context, sc *hgSchema, action string, docs []string, locale string, allLocales bool, userID int64) (hgBulkResult, error) {
	var res hgBulkResult
	switch action {
	case "publish", "unpublish", "delete":
	default:
		return res, errors.New("Choose a bulk action.")
	}
	if action != "delete" && !sc.DraftAndPublish {
		return res, errors.New("This content type does not use draft & publish.")
	}
	seen := map[string]bool{}
	for _, doc := range docs {
		if doc == "" || seen[doc] {
			continue
		}
		seen[doc] = true
		var locales []string
		if allLocales || !sc.Localized {
			for l := range s.hgDocLocales(ctx, sc.UID, doc) {
				locales = append(locales, l)
			}
		} else {
			locales = []string{locale}
		}
		if len(locales) == 0 {
			res.Failed = append(res.Failed, doc+": not found")
			continue
		}
		if action == "delete" {
			target := locale
			if allLocales || !sc.Localized {
				target = "*"
			}
			deleted, err := s.hgDeleteEntries(ctx, sc, doc, target, userID)
			if err != nil {
				res.Failed = append(res.Failed, doc+": not found")
				continue
			}
			for _, e := range deleted {
				s.hgEmit(ctx, "entry.delete", sc, e)
			}
			res.Done++
			continue
		}
		ok := true
		for _, l := range locales {
			var e *hgEntry
			var err error
			if action == "publish" {
				e, err = s.hgPublishEntry(ctx, sc, doc, l, userID)
			} else {
				cur, gerr := s.hgGetEntry(ctx, sc.UID, doc, l)
				if gerr != nil {
					err = errHGNotFound
				} else if cur.Published == nil {
					continue // already a draft
				} else {
					e, err = s.hgUnpublishEntry(ctx, sc, doc, l)
				}
			}
			if err != nil {
				ok = false
				msg := err.Error()
				if errors.Is(err, errHGNotFound) {
					msg = "not found"
				}
				label := doc
				if sc.Localized {
					label += " (" + l + ")"
				}
				res.Failed = append(res.Failed, label+": "+msg)
				continue
			}
			s.hgEmit(ctx, "entry."+action, sc, e)
		}
		if ok {
			res.Done++
		}
	}
	return res, nil
}
