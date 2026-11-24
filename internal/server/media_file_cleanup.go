package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5"

	"wpgo/internal/store"
)

// recordMediaFileDeletes locks the attachment and records the current file and variant paths.
// The journal and attachment deletion commit together.
func recordMediaFileDeletes(ctx context.Context, tx pgx.Tx, id int64) error {
	var a store.Attachment
	var meta []byte
	err := tx.QueryRow(ctx, `SELECT file_path, meta FROM media WHERE id = $1 FOR UPDATE`, id).Scan(&a.FilePath, &meta)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.ErrNotFound
	}
	if err != nil {
		return err
	}
	_ = json.Unmarshal(meta, &a.Meta)
	rows, err := tx.Query(ctx, `SELECT file_path FROM media_variants WHERE media_id = $1`, id)
	if err != nil {
		return err
	}
	variants, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	for _, rel := range variants {
		a.Variants = append(a.Variants, store.AttachmentVariant{FilePath: rel})
	}
	_, err = tx.Exec(ctx, `INSERT INTO media_file_deletes(path, attachment_id)
		SELECT rel, $1 FROM unnest($2::text[]) AS rel ON CONFLICT (path) DO NOTHING`, id, gocdnAttachmentFiles(&a))
	return err
}

func (s *Server) pendingMediaFileNames(ctx context.Context, prefix string) []string {
	rows, err := s.store.Pool.Query(ctx, `SELECT substr(path, char_length($1) + 1) AS name
		FROM media_file_deletes WHERE starts_with(path, $1)`, prefix)
	if err != nil {
		return nil
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil
	}
	out := names[:0]
	for _, name := range names {
		if name != "" && !strings.Contains(name, "/") {
			out = append(out, name)
		}
	}
	return out
}

// A zero attachment ID selects the whole journal for background recovery.
func (s *Server) cleanupMediaFileDeletes(ctx context.Context, attachmentID int64) {
	const batchSize = 200
	var last string
	var root *os.Root
	for ctx.Err() == nil {
		rows, err := s.store.Pool.Query(ctx, `SELECT path FROM media_file_deletes
			WHERE path > $1 AND ($2::bigint = 0 OR attachment_id = $2) ORDER BY path LIMIT $3`, last, attachmentID, batchSize)
		if err != nil {
			slog.Warn("could not list pending media deletions", "err", err)
			return
		}
		paths, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			slog.Warn("could not read pending media deletions", "err", err)
			return
		}
		if len(paths) == 0 {
			return
		}
		if root == nil {
			root, err = os.OpenRoot(s.cfg.UploadsDir)
			if err != nil {
				slog.Warn("could not open uploads for media cleanup", "err", err)
				return
			}
			defer root.Close()
		}
		for _, rel := range paths {
			if ctx.Err() != nil {
				return
			}
			if err := s.cleanupMediaFileDelete(ctx, root, rel); err != nil {
				slog.Warn("could not finish a pending media deletion", "err", err)
			}
		}
		if len(paths) < batchSize {
			return
		}
		last = paths[len(paths)-1]
	}
}

func (s *Server) cleanupMediaFileDelete(ctx context.Context, root *os.Root, rel string) error {
	return pgx.BeginFunc(ctx, s.store.Pool, func(tx pgx.Tx) error {
		var pending string
		err := tx.QueryRow(ctx, `SELECT path FROM media_file_deletes WHERE path = $1 FOR UPDATE SKIP LOCKED`, rel).Scan(&pending)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}

		var owned bool
		err = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM media WHERE file_path = $1)
			OR EXISTS (SELECT 1 FROM media_variants WHERE file_path = $1)`, pending).Scan(&owned)
		if err != nil {
			return err
		}
		if !owned {
			name := filepath.FromSlash(pending)
			info, err := root.Lstat(name)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if info != nil {
				if info.IsDir() {
					return fmt.Errorf("pending media file %q is a directory", pending)
				}
				if err := root.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
					return err
				}
			}
		}
		// An in-flight remote delete still needs the path reserved, even if the local file is gone.
		_, err = tx.Exec(ctx, `DELETE FROM media_file_deletes WHERE path = $1 AND
			($2::boolean OR NOT EXISTS (SELECT 1 FROM gocdn_items WHERE path = $1 AND media_id IS NULL))`, pending, owned)
		return err
	})
}
