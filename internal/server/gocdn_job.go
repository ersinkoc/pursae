package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"wpgo/internal/gocdn"
)

// The bulk tools of the Media Offload screen run as one background job at a time: "offload"
// copies the attachments that are not in the bucket yet, "download" brings removed files back to
// the server, "remove" deletes the library from the bucket (downloading removed files first).
// Progress is kept in the option gocdn_job after every attachment, so a job interrupted by a
// restart is offered to be resumed from the last attachment done; the screen polls
// /wp-admin/gocdn?action=progress.

const gocdnJobOption = "gocdn_job"

type gocdnJobState struct {
	Kind     string    `json:"kind"`
	Running  bool      `json:"running"`
	Total    int       `json:"total"`
	Done     int       `json:"done"`
	Failed   int       `json:"failed"`
	Files    int       `json:"files"`
	LastID   int64     `json:"last_id"`
	Started  time.Time `json:"started"`
	Updated  time.Time `json:"updated"`
	Finished bool      `json:"finished"`
	Stopped  bool      `json:"stopped"`
	Errors   []string  `json:"errors"`
	// Options: "move" deletes the old objects; "download" deactivates the plugin when it finished
	// without failures (the deactivation screen's "download back first").
	DeleteOld      bool `json:"delete_old,omitempty"`
	ThenDeactivate bool `json:"then_deactivate,omitempty"`

	cancel bool
	live   bool // a goroutine of this process runs it
}

var gocdnJobKinds = map[string]string{"offload": "Offload existing media", "download": "Download back to the server", "remove": "Remove from the bucket",
	"move": "Move to the current bucket"}

// gocdnJobOpts are a job's options.
type gocdnJobOpts struct {
	DeleteOld, ThenDeactivate bool
}

// gocdnJobFilter selects the attachments a job still has to process; its parameters start at $2
// ($1 is the last attachment done).
func gocdnJobFilter(kind string, st gocdnSettings) (string, []any) {
	switch kind {
	case "download":
		return `EXISTS (SELECT 1 FROM gocdn_items g WHERE g.media_id = m.id AND g.local_removed)`, nil
	case "remove":
		return `EXISTS (SELECT 1 FROM gocdn_items g WHERE g.media_id = m.id)`, nil
	case "move":
		cond, args := gocdnMoveFilter(st, 2)
		return `EXISTS (SELECT 1 FROM gocdn_items g WHERE g.media_id = m.id AND ` + cond + `)`, args
	}
	return `NOT EXISTS (SELECT 1 FROM gocdn_items g WHERE g.media_id = m.id AND g.path = m.file_path)`, nil
}

// gocdnJob returns a copy of the current (or last) job's state.
func (s *Server) gocdnJob() gocdnJobState {
	rt := s.gocdnRT()
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.job != nil {
		j := *rt.job
		j.Errors = append([]string(nil), rt.job.Errors...)
		return j
	}
	return s.gocdnJobStored()
}

// gocdnStartJob starts a job (or resumes the interrupted one of the same kind).
func (s *Server) gocdnStartJob(ctx context.Context, kind string, resume bool) error {
	return s.gocdnStartJobWith(ctx, kind, resume, gocdnJobOpts{})
}

func (s *Server) gocdnStartJobWith(ctx context.Context, kind string, resume bool, opts gocdnJobOpts) error {
	if _, ok := gocdnJobKinds[kind]; !ok {
		return fmt.Errorf("unknown job %q", kind)
	}
	st := s.gocdnLoad()
	if _, err := s.gocdnClient(st); err != nil {
		return fmt.Errorf("the storage is not configured: %v", err)
	}
	rt := s.gocdnRT()
	rt.mu.Lock()
	if rt.job != nil && rt.job.live && rt.job.Running {
		rt.mu.Unlock()
		return fmt.Errorf("a job is already running")
	}
	prev := s.gocdnJobStored()
	j := &gocdnJobState{Kind: kind, Running: true, Started: time.Now(), Updated: time.Now(), live: true, DeleteOld: opts.DeleteOld,
		ThenDeactivate: opts.ThenDeactivate}
	if resume && prev.Kind == kind && prev.Running && !prev.Finished {
		j.LastID, j.Done, j.Failed, j.Files, j.Started = prev.LastID, prev.Done, prev.Failed, prev.Files, prev.Started
		j.DeleteOld, j.ThenDeactivate = prev.DeleteOld || opts.DeleteOld, prev.ThenDeactivate || opts.ThenDeactivate
	}
	var remaining int
	cond, args := gocdnJobFilter(kind, st)
	_ = s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM media m WHERE m.id > $1 AND `+cond, append([]any{j.LastID}, args...)...).Scan(&remaining)
	j.Total = j.Done + j.Failed + remaining
	rt.job = j
	rt.mu.Unlock()
	s.gocdnPersistJob()
	go s.gocdnRunJob(j)
	return nil
}

func (s *Server) gocdnJobStored() gocdnJobState {
	var j gocdnJobState
	if v := s.settings.Get(gocdnJobOption); v != nil {
		raw, _ := json.Marshal(v)
		_ = json.Unmarshal(raw, &j)
	}
	return j
}

// gocdnStopJob asks the running job to stop after the current attachment.
func (s *Server) gocdnStopJob() {
	rt := s.gocdnRT()
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.job != nil && rt.job.Running {
		rt.job.cancel = true
	}
}

func (s *Server) gocdnPersistJob() {
	rt := s.gocdnRT()
	rt.mu.Lock()
	if rt.job == nil {
		rt.mu.Unlock()
		return
	}
	j := *rt.job
	rt.mu.Unlock()
	if err := s.settings.Set(context.Background(), map[string]any{gocdnJobOption: j}); err != nil {
		slog.Warn("gocdn: saving the job state", "err", err)
	}
}

func (s *Server) gocdnRunJob(j *gocdnJobState) {
	ctx := context.Background()
	rt := s.gocdnRT()
	st := s.gocdnLoad()
	cl, err := s.gocdnClient(st)
	fail := func(msg string) {
		rt.mu.Lock()
		j.Errors = append(j.Errors, msg)
		if len(j.Errors) > 50 {
			j.Errors = j.Errors[len(j.Errors)-50:]
		}
		rt.mu.Unlock()
	}
	for err == nil {
		rt.mu.Lock()
		cancel, last := j.cancel, j.LastID
		rt.mu.Unlock()
		if cancel || !s.gocdnOn() {
			rt.mu.Lock()
			j.Stopped = true
			rt.mu.Unlock()
			break
		}
		var ids []int64
		cond, args := gocdnJobFilter(j.Kind, st)
		rows, qerr := s.store.Pool.Query(ctx, `SELECT m.id FROM media m WHERE m.id > $1 AND `+cond+` ORDER BY m.id LIMIT 1`, append([]any{last}, args...)...)
		if qerr != nil {
			fail(qerr.Error())
			break
		}
		for rows.Next() {
			var id int64
			if rows.Scan(&id) == nil {
				ids = append(ids, id)
			}
		}
		rows.Close()
		if qerr := rows.Err(); qerr != nil { // not the end of the list: the job stops unfinished
			fail(qerr.Error())
			break
		}
		if len(ids) == 0 {
			rt.mu.Lock()
			j.Finished = true
			rt.mu.Unlock()
			break
		}
		id := ids[0]
		n, perr := s.gocdnJobOne(ctx, j, st, cl, id)
		rt.mu.Lock()
		j.LastID = id
		j.Files += n
		if perr != nil {
			j.Failed++
		} else {
			j.Done++
		}
		j.Updated = time.Now()
		rt.mu.Unlock()
		if perr != nil {
			fail(fmt.Sprintf("#%d: %v", id, perr))
		}
		s.gocdnPersistJob()
	}
	if err != nil {
		fail(err.Error())
	}
	rt.mu.Lock()
	j.Running, j.live = false, false
	j.Updated = time.Now()
	finished, failed := j.Finished, j.Failed
	rt.mu.Unlock()
	s.gocdnPersistJob()
	switch {
	case j.Kind == "move" && finished:
		s.gocdnPrunePrevious(ctx)
	case j.Kind == "download" && finished && !s.gocdnHasRemoved(ctx):
		_ = s.settings.Set(ctx, map[string]any{gocdnFallbackOption: false})
		if j.ThenDeactivate && failed == 0 {
			_ = s.settings.Set(ctx, map[string]any{gocdnSetting: false})
		}
	}
	s.gocdnPurge(ctx, true) // URLs on the site changed
}

// gocdnJobOne processes one attachment; it returns the number of files moved.
func (s *Server) gocdnJobOne(ctx context.Context, j *gocdnJobState, st gocdnSettings, cl *gocdn.Client, id int64) (int, error) {
	kind := j.Kind
	a, err := s.store.GetAttachment(ctx, id)
	if err != nil {
		return 0, err
	}
	switch kind {
	case "move":
		items, err := s.gocdnItemsForMedia(ctx, id)
		if err != nil {
			return 0, err
		}
		n := 0
		var errs []error
		for _, it := range items {
			if gocdnItemOnCurrent(st, it) && it.Key == gocdnObjectKey(st, it.Path) {
				continue
			}
			if err := s.gocdnMoveItem(ctx, st, cl, it, j.DeleteOld); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", it.Path, err))
				continue
			}
			n++
		}
		return n, errors.Join(errs...)
	case "offload":
		return s.gocdnOffloadAttachment(ctx, st, cl, a)
	case "download":
		items, err := s.gocdnItemsForMedia(ctx, id)
		if err != nil {
			return 0, err
		}
		n := 0
		for _, it := range items {
			if !it.LocalRemoved {
				continue
			}
			if err := s.gocdnDownload(ctx, st, it); err != nil {
				return n, fmt.Errorf("%s: %w", it.Path, err)
			}
			_, _ = s.store.Pool.Exec(ctx, `UPDATE gocdn_items SET local_removed = false WHERE id = $1`, it.ID)
			n++
		}
		return n, nil
	case "remove":
		items, err := s.gocdnItemsForMedia(ctx, id)
		if err != nil {
			return 0, err
		}
		n := 0
		for _, it := range items {
			if it.LocalRemoved {
				if err := s.gocdnDownload(ctx, st, it); err != nil {
					return n, fmt.Errorf("%s: %w", it.Path, err)
				}
			} else if _, err := os.Stat(s.gocdnLocalPath(it.Path)); err != nil {
				return n, fmt.Errorf("%s: not on the server, kept in the bucket", it.Path)
			}
			icl, err := s.gocdnItemClient(st, it)
			if err != nil {
				return n, err
			}
			if err := icl.Delete(ctx, it.Key); err != nil {
				return n, fmt.Errorf("%s: %w", it.Path, err)
			}
			_, _ = s.store.Pool.Exec(ctx, `DELETE FROM gocdn_items WHERE id = $1`, it.ID)
			n++
		}
		return n, nil
	}
	return 0, fmt.Errorf("unknown job %q", kind)
}

// Percent is the share of the job done.
func (j gocdnJobState) Percent() int {
	if j.Total == 0 {
		return 0
	}
	return (j.Done + j.Failed) * 100 / j.Total
}
