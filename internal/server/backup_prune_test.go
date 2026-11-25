package server

import (
	"fmt"
	"testing"
	"time"
)

// Scheduled backups must not grow the in-memory job list without bound: only the most recently
// finished jobs are kept, and running jobs never go.
func TestBmgrPruneKeepsRecentFinishedAndRunning(t *testing.T) {
	s := &Server{}
	s.bmgr.jobs = map[string]*bmgrJob{}
	base := time.Now()
	for i := 0; i < bmgrKeepJobs+15; i++ {
		id := fmt.Sprintf("done-%02d", i)
		s.bmgr.jobs[id] = &bmgrJob{ID: id, State: "done", Finished: base.Add(time.Duration(i) * time.Minute)}
	}
	s.bmgr.jobs["running"] = &bmgrJob{ID: "running", State: "running"}
	s.bmgr.jobs["failed-new"] = &bmgrJob{ID: "failed-new", State: "error", Finished: base.Add(time.Hour)}

	s.bmgr.mu.Lock()
	s.bmgrPruneJobsLocked()
	s.bmgr.mu.Unlock()

	if got := len(s.bmgr.jobs); got != bmgrKeepJobs+1 {
		t.Fatalf("jobs kept = %d, want %d finished + 1 running", got, bmgrKeepJobs)
	}
	if s.bmgr.jobs["running"] == nil {
		t.Fatal("running job was dropped")
	}
	if s.bmgr.jobs["failed-new"] == nil {
		t.Fatal("newest finished (error) job was dropped")
	}
	if s.bmgr.jobs["done-00"] != nil {
		t.Fatal("oldest finished job was kept")
	}
	if s.bmgr.jobs[fmt.Sprintf("done-%02d", bmgrKeepJobs+14)] == nil {
		t.Fatal("a recent finished job was dropped")
	}
}
