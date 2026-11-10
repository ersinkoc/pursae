package server

import (
	"bytes"
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"wpgo/internal/store"
)

// deadPoolServer is a Server whose every query fails (the context is cancelled before a connection
// is made), for the paths that must fail safe when the database does not answer.
func deadPoolServer(t *testing.T) (*Server, context.Context) {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), "postgres://nobody@127.0.0.1:1/none?connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return &Server{store: store.New(pool)}, ctx
}

// A failed count must not make GoCDN forget the bucket (and credentials) files still live in.
func TestGoCDNPreviousConnSurvivesCountError(t *testing.T) {
	s, ctx := deadPoolServer(t)
	prev := gocdnSettings{Provider: "s3", Bucket: "old-bucket", AccessKey: "AK", SecretKey: "SK"}
	next := gocdnSettings{Provider: "s3", Bucket: "new-bucket"}
	got := s.gocdnRememberConn(ctx, prev, next)
	if len(got.Previous) != 1 || got.Previous[0].Bucket != "old-bucket" || got.Previous[0].AccessKey != "AK" {
		t.Fatalf("previous connection dropped on a failed count: %+v", got.Previous)
	}
	if !s.gocdnHasRemoved(ctx) {
		t.Fatal("a failed count of bucket-only files must count as some (keeps the uploads fallback on)")
	}
}

// Concurrent writers of one cached file never expose a partial file.
func TestGoCDNWriteFileAtomicConcurrent(t *testing.T) {
	file := filepath.Join(t.TempDir(), "ab", "variant.webp")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	data := bytes.Repeat([]byte("0123456789abcdef"), 64<<10) // 1 MiB
	var wg sync.WaitGroup
	stop := make(chan struct{})
	bad := make(chan int, 1)
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			if b, err := os.ReadFile(file); err == nil && len(b) != len(data) {
				select {
				case bad <- len(b):
				default:
				}
			}
		}
	}()
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			gocdnImgStore(file, data)
		}()
	}
	wg.Wait()
	close(stop)
	select {
	case n := <-bad:
		t.Fatalf("a reader saw a partial cache file (%d of %d bytes)", n, len(data))
	default:
	}
	if b, err := os.ReadFile(file); err != nil || !bytes.Equal(b, data) {
		t.Fatalf("final file wrong: %v (%d bytes)", err, len(b))
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(file), ".*")); len(left) != 0 {
		t.Fatalf("temp files left behind: %v", left)
	}
}

// Huge page / offset values of the WooCommerce APIs stay in range: (page-1)*per used to overflow
// into a negative slice index (panic) or a negative SQL offset/limit.
func TestGooPageBounds(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	for _, q := range []string{"page=9223372036854775807&per_page=100", "page=99999999999999999999", "offset=9223372036854775807&per_page=100"} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/?"+q, nil)
		page, per, offset := gooPage(c)
		if page < 1 || page > store.MaxPage || (page-1)*per < 0 || offset+per < 0 {
			t.Fatalf("%s: page %d per %d offset %d", q, page, per, offset)
		}
		if got := gooPaginateSlice(c, []int{1, 2, 3}); len(got) != 0 {
			t.Fatalf("%s: a page past the end must be empty, got %v", q, got)
		}
	}
}
