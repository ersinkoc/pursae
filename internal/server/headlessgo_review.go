package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"wpgo/internal/store"
)

// Review flow. A type with reviewWorkflow (needs draft & publish) cannot publish a draft before
// someone with the review capability approved that very draft:
//
//	draft ── submit ──▶ pending ── approve ──▶ approved ── publish ──▶ live
//	                       └──── reject ────▶ rejected ── (edit, submit again)
//
// The status is bound to the draft it was given for (review_hash): any change to the draft, from
// the form, the API, a restore or a relation link, makes a pending or approved draft "not
// reviewed" again. Submit, approve and reject are recorded in the entry's history with the
// reviewer's note. Publishing through the API, GraphQL, bulk actions and the scheduler follows the
// same rule; the REST and GraphQL APIs have no user, so they can neither approve nor reject.

const (
	hgReviewPending  = "pending"
	hgReviewApproved = "approved"
	hgReviewRejected = "rejected"
)

// hgCanReview says whether the user may approve or reject drafts (an editor or above).
func hgCanReview(u *store.User) bool { return u != nil && u.Can("edit_others_posts", nil) }

// hgDraftHash identifies a draft (encoding/json writes map keys sorted, so it is stable).
func hgDraftHash(data map[string]any) string {
	raw, _ := json.Marshal(data)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// ReviewState is the review status of the current draft: "" when it was never submitted or changed
// since, else pending, approved or rejected.
func (e *hgEntry) ReviewState() string {
	switch e.ReviewStatus {
	case hgReviewPending, hgReviewApproved:
		if e.ReviewHash == hgDraftHash(e.Data) {
			return e.ReviewStatus
		}
	case hgReviewRejected:
		return hgReviewRejected
	}
	return ""
}

var errHGNotApproved = errors.New("This content type needs an approved review before publishing: submit the draft for review and have it approved.")

// hgReviewGate refuses to publish a draft that is not approved when the type uses the review flow.
func hgReviewGate(sc *hgSchema, e *hgEntry) error {
	if !sc.ReviewWorkflow || !sc.DraftAndPublish {
		return nil
	}
	if e == nil || e.ReviewState() != hgReviewApproved {
		return errHGNotApproved
	}
	return nil
}

// hgSubmitForReview puts the draft up for review.
func (s *Server) hgSubmitForReview(ctx context.Context, sc *hgSchema, doc, locale string, userID int64) (*hgEntry, error) {
	if !sc.ReviewWorkflow {
		return nil, errors.New("This content type does not use a review flow.")
	}
	e, err := s.hgGetEntry(ctx, sc.UID, doc, locale)
	if err != nil {
		return nil, errHGNotFound
	}
	if e.ReviewState() == hgReviewPending {
		return nil, errors.New("This draft is already waiting for review.")
	}
	// A draft that does not validate for publishing would only be bounced later: say so now.
	h := &hgCoercer{s: s, ctx: ctx, reg: s.hgReg(ctx), required: true}
	h.object(sc, map[string]any{}, e.Data, nil, true)
	if len(h.errs) > 0 {
		return nil, h.errs
	}
	return s.hgSetReview(ctx, e, hgReviewPending, userID, "", "submit")
}

// hgDecideReview approves or rejects a pending draft; the caller checked hgCanReview.
func (s *Server) hgDecideReview(ctx context.Context, sc *hgSchema, doc, locale string, userID int64, approve bool, note string) (*hgEntry, error) {
	if !sc.ReviewWorkflow {
		return nil, errors.New("This content type does not use a review flow.")
	}
	e, err := s.hgGetEntry(ctx, sc.UID, doc, locale)
	if err != nil {
		return nil, errHGNotFound
	}
	if e.ReviewState() != hgReviewPending {
		return nil, errors.New("This draft is not waiting for review (it may have been changed since it was submitted).")
	}
	if approve {
		return s.hgSetReview(ctx, e, hgReviewApproved, userID, note, "approve")
	}
	return s.hgSetReview(ctx, e, hgReviewRejected, userID, note, "reject")
}

func (s *Server) hgSetReview(ctx context.Context, e *hgEntry, status string, userID int64, note, action string) (*hgEntry, error) {
	ne, err := hgScanEntry(s.store.Pool.QueryRow(ctx, `UPDATE hg_entries SET review_status = $2, review_hash = $3, review_by = $4, review_note = $5, review_at = now()
		WHERE id = $1 RETURNING `+hgEntryCols, e.ID, status, hgDraftHash(e.Data), userID, note))
	if err != nil {
		return nil, errHGNotFound
	}
	s.hgRecordVersionNote(ctx, ne, action, userID, note)
	return ne, nil
}
