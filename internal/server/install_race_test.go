package server

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"testing"
)

// Installer submissions that race create one administrator: the others find the site installed.
func TestInstallRaceCreatesOneAdmin(t *testing.T) {
	ts, _, srv := testServerBuilderOff(t)
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			resp, err := noRedirect.PostForm(ts.URL+"/wp-admin/install?step=2", url.Values{
				"weblog_title": {"Race"}, "user_name": {"admin" + strconv.Itoa(i)}, "admin_password": {"correct horse"},
				"admin_email": {"admin" + strconv.Itoa(i) + "@example.test"},
			})
			if err == nil {
				resp.Body.Close()
			}
		}(i)
	}
	wg.Wait()
	var users, posts int
	ctx := context.Background()
	if err := srv.store.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM users), (SELECT count(*) FROM posts WHERE slug = 'hello-world')`).Scan(&users, &posts); err != nil {
		t.Fatal(err)
	}
	if users != 1 || posts != 1 {
		t.Fatalf("after 4 racing installs: %d users, %d Hello world posts; want 1 and 1", users, posts)
	}
}
