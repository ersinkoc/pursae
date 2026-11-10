package server

import (
	"context"
	"fmt"
	"path/filepath"
	"syscall"
)

// bmgrFreeBytes is the space available to this process on the filesystem holding dir; ok is false
// when it cannot be determined.
func bmgrFreeBytes(dir string) (free int64, ok bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, false
	}
	return int64(st.Bavail) * int64(st.Bsize), true
}

// bmgrCheckSpace refuses a backup that cannot fit in the backups directory. The estimate is the size of
// the selected files (mostly already compressed) plus half the selected tables.
func (s *Server) bmgrCheckSpace(ctx context.Context, dir string, sc *bmgrSchema, set map[string]bool, groups []string) error {
	roots := s.bmgrFileRoots()
	var need int64
	for _, g := range groups {
		need += bmgrDirSize(roots[g])
	}
	var tables []string
	for t := range set {
		tables = append(tables, t)
	}
	var db int64
	_ = s.store.Pool.QueryRow(ctx, `SELECT COALESCE(sum(pg_total_relation_size(quote_ident(t)::regclass)), 0)::bigint FROM unnest($1::text[]) t`, tables).Scan(&db)
	need += db / 2
	free, ok := bmgrFreeBytes(dir)
	if ok && need > free {
		return fmt.Errorf("not enough free disk space for this backup: about %s needed, %s free in %s", bmgrHuman(need), bmgrHuman(free), filepath.Clean(dir))
	}
	return nil
}
