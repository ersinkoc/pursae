package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"wpgo/internal/gocdn"
)

// Changing the bucket, provider, endpoint or path. Every gocdn_items row keeps the provider,
// region, bucket, endpoint and key it was written with, so nothing breaks when the Storage screen
// changes: the connection that was replaced is kept (sealed) in the settings' "previous-connections"
// as long as items still live there, and those items keep being delivered, fetched back and deleted
// through it (gocdnItemClient / gocdnItemURL) with their old URLs. The Media Offload screen then
// offers the "move" job: each such file is copied to the configured bucket under the current path
// (CopyObject when the old and new bucket share the endpoint and the access key, otherwise
// downloaded and uploaded again, multipart when large), its row is updated (so its URL changes to
// the new bucket) and, if asked, the old object is deleted. Old connections with no items left are
// dropped when the job finishes.

// gocdnConn is a storage connection that was replaced on the Storage screen.
type gocdnConn struct {
	Provider  string `json:"provider"`
	Endpoint  string `json:"endpoint"` // resolved (gocdnEndpoint)
	Region    string `json:"region"`
	Bucket    string `json:"bucket"`
	AccessKey string `json:"access-key-id"`
	SecretKey string `json:"secret-access-key"` // sealed
	PathStyle bool   `json:"use-path-style"`
	Prefix    string `json:"object-prefix"`
	CDNDomain string `json:"delivery-domain"`
}

func gocdnConnOf(st gocdnSettings) gocdnConn {
	return gocdnConn{Provider: st.Provider, Endpoint: gocdnEndpoint(st), Region: gocdnRegion(st), Bucket: st.Bucket, AccessKey: st.AccessKey,
		SecretKey: st.SecretKey, PathStyle: st.PathStyle, Prefix: st.Prefix, CDNDomain: st.CDNDomain}
}

// same reports whether two connections reach the same bucket.
func (c gocdnConn) same(o gocdnConn) bool {
	return c.Provider == o.Provider && c.Endpoint == o.Endpoint && c.Bucket == o.Bucket
}

// gocdnItemOnCurrent reports whether an item lives in the configured bucket.
func gocdnItemOnCurrent(st gocdnSettings, it *gocdnItem) bool {
	return it.Bucket == st.Bucket && it.Provider == st.Provider && (it.Endpoint == "" || it.Endpoint == gocdnEndpoint(st))
}

// gocdnConnFor is the settings to reach an item of another bucket: the saved previous connection
// it was written with, else (no record: the settings came from WPGO_CDN_SETTINGS, or rows older
// than the endpoint column) the current credentials with the item's provider, region, bucket and
// endpoint and no delivery domain.
func gocdnConnFor(st gocdnSettings, it *gocdnItem) gocdnSettings {
	out := st
	for i := len(st.Previous) - 1; i >= 0; i-- {
		c := st.Previous[i]
		if c.Bucket == it.Bucket && c.Provider == it.Provider && (it.Endpoint == "" || c.Endpoint == it.Endpoint) {
			out.Provider, out.Endpoint, out.Region, out.Bucket = c.Provider, c.Endpoint, c.Region, c.Bucket
			out.AccessKey, out.SecretKey, out.PathStyle, out.Prefix, out.CDNDomain = c.AccessKey, c.SecretKey, c.PathStyle, c.Prefix, c.CDNDomain
			return out
		}
	}
	out.Bucket, out.Provider, out.Region, out.CDNDomain = it.Bucket, it.Provider, it.Region, ""
	if it.Endpoint != "" {
		out.Endpoint = it.Endpoint
	}
	return out
}

// gocdnRememberConn keeps the connection being replaced when items still live in it.
func (s *Server) gocdnRememberConn(ctx context.Context, prev, next gocdnSettings) gocdnSettings {
	pc, nc := gocdnConnOf(prev), gocdnConnOf(next)
	var keep []gocdnConn
	for _, c := range next.Previous {
		if !c.same(nc) && !c.same(pc) {
			keep = append(keep, c)
		}
	}
	if prev.Bucket != "" && !pc.same(nc) {
		var n int
		_ = s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM gocdn_items WHERE bucket = $1 AND provider = $2 AND (endpoint = '' OR endpoint = $3)`,
			pc.Bucket, pc.Provider, pc.Endpoint).Scan(&n)
		if n > 0 {
			keep = append(keep, pc)
		}
	}
	next.Previous = keep
	return next
}

// gocdnMoveFilter is the SQL condition (on gocdn_items g) of items not in the configured bucket
// under the configured path; its parameters start at $first.
func gocdnMoveFilter(st gocdnSettings, first int) (string, []any) {
	p := func(i int) string { return fmt.Sprintf("$%d", first+i) }
	return `(g.bucket <> ` + p(0) + ` OR g.provider <> ` + p(1) + ` OR (g.endpoint <> '' AND g.endpoint <> ` + p(2) + `) OR g.object_key <> ` + p(3) + ` || g.path)`,
		[]any{st.Bucket, st.Provider, gocdnEndpoint(st), st.Prefix}
}

// gocdnMoveCount is the number of files (and attachments) that the move job would copy.
func (s *Server) gocdnMoveCount(ctx context.Context, st gocdnSettings) (files int, buckets []string) {
	if st.Bucket == "" {
		return 0, nil
	}
	cond, args := gocdnMoveFilter(st, 1)
	_ = s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM gocdn_items g WHERE `+cond, args...).Scan(&files)
	if files > 0 {
		rows, err := s.store.Pool.Query(ctx, `SELECT DISTINCT g.bucket || CASE WHEN g.object_key <> $5 || g.path AND g.bucket = $1 THEN ' (old path)' ELSE '' END
			FROM gocdn_items g WHERE `+cond+` ORDER BY 1 LIMIT 5`, append(args, st.Prefix)...)
		if err == nil {
			for rows.Next() {
				var b string
				if rows.Scan(&b) == nil {
					buckets = append(buckets, b)
				}
			}
			rows.Close()
		}
	}
	return files, buckets
}

// gocdnMoveItem copies one item into the configured bucket and updates its row.
func (s *Server) gocdnMoveItem(ctx context.Context, st gocdnSettings, dst *gocdn.Client, it *gocdnItem, deleteOld bool) error {
	dstKey := gocdnObjectKey(st, it.Path)
	if gocdnItemOnCurrent(st, it) && it.Key == dstKey {
		_, err := s.store.Pool.Exec(ctx, `UPDATE gocdn_items SET endpoint = $2 WHERE id = $1`, it.ID, gocdnEndpoint(st))
		return err
	}
	src, err := s.gocdnItemClient(st, it)
	if err != nil {
		return err
	}
	opts := gocdnUploadOptions(st, it.Path)
	if it.Mime != "" {
		opts.ContentType = it.Mime
	}
	copied := false
	if src.Endpoint == dst.Endpoint && src.AccessKey == dst.AccessKey && it.Size < 5<<30 {
		copied = dst.Copy(ctx, src.Bucket, it.Key, dstKey, opts.PutOptions) == nil
	}
	if !copied {
		if err := s.gocdnStreamCopy(ctx, src, dst, it, dstKey, opts); err != nil {
			return err
		}
	}
	size := it.Size
	if o, err := dst.Head(ctx, dstKey); err == nil {
		size = o.Size
	}
	if _, err := s.store.Pool.Exec(ctx, `UPDATE gocdn_items SET provider = $2, region = $3, bucket = $4, object_key = $5, endpoint = $6, size = $7 WHERE id = $1`,
		it.ID, st.Provider, gocdnRegion(st), st.Bucket, dstKey, gocdnEndpoint(st), size); err != nil {
		return err
	}
	if deleteOld && !(src.Endpoint == dst.Endpoint && src.Bucket == dst.Bucket && it.Key == dstKey) {
		if err := src.Delete(ctx, it.Key); err != nil {
			return fmt.Errorf("copied, but the old object could not be deleted: %w", err)
		}
	}
	return nil
}

// gocdnStreamCopy downloads an object to a temporary file and uploads it to the new bucket; an
// object missing from the old bucket is uploaded from the server's copy when there is one.
func (s *Server) gocdnStreamCopy(ctx context.Context, src, dst *gocdn.Client, it *gocdnItem, dstKey string, opts gocdn.UploadOptions) error {
	rc, _, err := src.Get(ctx, it.Key)
	if err != nil {
		if gocdn.IsNotFound(err) && !it.LocalRemoved {
			if f, ferr := os.Open(s.gocdnLocalPath(it.Path)); ferr == nil {
				defer f.Close()
				fi, _ := f.Stat()
				_, err = dst.Upload(ctx, dstKey, f, fi.Size(), opts)
				return err
			}
		}
		return fmt.Errorf("reading the old object: %w", err)
	}
	defer rc.Close()
	if err := os.MkdirAll(s.gocdnCacheDir(), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.gocdnCacheDir(), "move-*")
	if err != nil {
		return err
	}
	defer func() { tmp.Close(); os.Remove(tmp.Name()) }()
	n, err := io.Copy(tmp, io.LimitReader(rc, 5<<40))
	if err != nil {
		return fmt.Errorf("reading the old object: %w", err)
	}
	if n == 0 && it.Size > 0 {
		return errors.New("the old object is empty")
	}
	_, err = dst.Upload(ctx, dstKey, tmp, n, opts)
	return err
}

// gocdnPrunePrevious drops remembered connections that no item uses any more.
func (s *Server) gocdnPrunePrevious(ctx context.Context) {
	st := s.gocdnLoad()
	if len(st.Previous) == 0 {
		return
	}
	var keep []gocdnConn
	for _, c := range st.Previous {
		var n int
		_ = s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM gocdn_items WHERE bucket = $1 AND provider = $2 AND (endpoint = '' OR endpoint = $3)`,
			c.Bucket, c.Provider, c.Endpoint).Scan(&n)
		if n > 0 && !c.same(gocdnConnOf(st)) {
			keep = append(keep, c)
		}
	}
	if len(keep) != len(st.Previous) {
		st.Previous = keep
		_ = s.gocdnSave(ctx, st)
	}
}
