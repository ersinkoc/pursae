package server

import (
	"strings"
	"testing"
	"time"
)

// The exact-import queue: at most wbImportUserJobs open jobs per user and wbImportMaxBytes of HTML
// in all; expired jobs make room again.
func TestWBImportQueueLimits(t *testing.T) {
	st := &wbImportStore{jobs: map[string]*wbImportJob{}}
	for i := 0; i < wbImportUserJobs; i++ {
		if _, err := st.create(7, "<p>x</p>", nil); err != nil {
			t.Fatalf("job %d: %v", i, err)
		}
	}
	if _, err := st.create(7, "<p>x</p>", nil); err == nil || !strings.Contains(err.Error(), "waiting for a browser") {
		t.Errorf("a sixth job of one user: %v", err)
	}
	if _, err := st.create(8, "<p>x</p>", nil); err != nil {
		t.Errorf("another user: %v", err)
	}
	// The byte budget: 2 MB jobs of many users.
	big := strings.Repeat("a", wbImportMaxHTML)
	var err error
	for u := int64(100); u < 200 && err == nil; u++ {
		_, err = st.create(u, big, nil)
	}
	if err == nil || !strings.Contains(err.Error(), "too many HTML imports") {
		t.Fatalf("no byte budget: %v", err)
	}
	held := 0
	for _, j := range st.jobs {
		held += j.size()
	}
	if held > wbImportMaxBytes {
		t.Errorf("the queue holds %d bytes", held)
	}
	// Expired jobs leave room.
	for _, j := range st.jobs {
		j.created = time.Now().Add(-wbImportTTL - time.Minute)
	}
	if _, err := st.create(7, big, nil); err != nil {
		t.Errorf("after expiry: %v", err)
	}
}
