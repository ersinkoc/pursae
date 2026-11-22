package db

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"wpgo/internal/config"
	"wpgo/internal/coreupdate"
)

// Schema guard. Migrations are forward-only and a newer binary may have applied migrations an older
// one does not know: after a core update was rolled back, after Dashboard › Updates › Reinstall, or
// when an operator puts an older binary back by hand. Releases normally only add schema
// (expand/contract), so the older binary still works; when a release removes or changes something
// an older one relies on, it raises MinCompatibleBinary, Migrate records that floor in schema_meta,
// and from then on a binary below the floor refuses to start on this database instead of running
// against a schema it does not understand.
//
// The check only bites when the database holds migrations this binary does not know: a binary that
// knows every applied migration understands the schema whatever the floor says.

// MinCompatibleBinary is the oldest Pursae release that may run on the schema this binary migrates
// to. Raise it only together with a migration that removes or changes something older releases
// still use (the "contract" step of expand/contract), and to the newest release that no longer
// uses it: never above the previous release, or a rolled-back update could not start again.
//
// 0.2.4: migration 0098_oauth_resource changed what an empty Application Password scope list
// means (it was full access and became content and media, with full access the explicit
// full_access marker). A 0.2.3 or older binary reads the scope lists 0.2.4+ writes with the old
// meaning, so an application password limited to content and media would get full access there.
// Every later migration only adds tables, columns, indexes or functions.
const MinCompatibleBinary = "0.2.4"

// BinaryVersion is the running binary's release (internal/server sets it from server.Version). It
// is recorded as migrated_by and compared with min_binary_version; empty (tests of this package)
// skips both.
var BinaryVersion string

// AllowOlderBinaryEnv, set to 1, lets a binary below the schema's floor start anyway.
const AllowOlderBinaryEnv = "PURSAE_ALLOW_OLDER_BINARY"

const (
	metaMinBinary  = "min_binary_version"
	metaMigratedBy = "migrated_by"
)

// SchemaStatus compares the database schema with a binary.
type SchemaStatus struct {
	Binary     string   // the binary's version
	Unknown    []string // applied migrations the binary does not know (a newer binary applied them)
	MinBinary  string   // min_binary_version, "" when never recorded
	MigratedBy string   // the newest release that migrated the database, "" when never recorded
}

// Newer reports whether a newer binary migrated the database: it has migrations this one lacks.
func (s SchemaStatus) Newer() bool { return len(s.Unknown) > 0 }

// TooOld reports whether the binary must not run on the database: the schema has migrations it does
// not know and its version is below the recorded floor.
func (s SchemaStatus) TooOld() bool {
	return s.Newer() && s.MinBinary != "" && s.Binary != "" && coreupdate.Less(s.Binary, s.MinBinary)
}

// Warning is the one-line notice for a binary that may run on a newer schema ("" when the schema
// is not newer).
func (s SchemaStatus) Warning() string {
	if !s.Newer() {
		return ""
	}
	by := "a newer Pursae release"
	if s.MigratedBy != "" {
		by = "Pursae " + s.MigratedBy
	}
	return fmt.Sprintf("the database was migrated by %s and has %d migration(s) Pursae %s does not know (%s); "+
		"this release is still compatible (the schema needs %s or newer), but update to the newest release soon",
		by, len(s.Unknown), s.Binary, listShort(s.Unknown), orAny(s.MinBinary))
}

func orAny(v string) string {
	if v == "" {
		return "any release"
	}
	return v
}

func listShort(l []string) string {
	if len(l) <= 3 {
		return strings.Join(l, ", ")
	}
	return strings.Join(l[:3], ", ") + fmt.Sprintf(" and %d more", len(l)-3)
}

// BinaryTooOldError is the refusal of a binary below the schema's floor.
type BinaryTooOldError struct{ Status SchemaStatus }

func (e *BinaryTooOldError) Error() string {
	s := e.Status
	by := "a newer Pursae release"
	if s.MigratedBy != "" {
		by = "Pursae " + s.MigratedBy
	}
	return fmt.Sprintf("this database was migrated by %s and needs Pursae %s or newer; this binary is Pursae %s "+
		"and does not know %d of its migrations (%s). Install Pursae %s or newer (Dashboard › Updates cannot run "+
		"from this binary: replace the binary or pull the newer container image), or restore a database backup taken "+
		"before the update. To start anyway at your own risk, set %s=1",
		by, s.MinBinary, s.Binary, len(s.Unknown), listShort(s.Unknown), s.MinBinary, AllowOlderBinaryEnv)
}

// AllowOlderBinary reports whether PURSAE_ALLOW_OLDER_BINARY overrides the schema guard.
func AllowOlderBinary() bool {
	switch strings.ToLower(strings.TrimSpace(config.Env(AllowOlderBinaryEnv))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// querier is what CheckSchema reads through: a pool, a pooled connection or a transaction.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// CheckSchema compares the database with the binary of version binary. A database that was never
// migrated, or migrated before schema_meta existed, has no floor.
func CheckSchema(ctx context.Context, q querier, binary string) (SchemaStatus, error) {
	st := SchemaStatus{Binary: binary}
	known, err := Versions()
	if err != nil {
		return st, err
	}
	rows, err := q.Query(ctx, `SELECT version FROM schema_migrations WHERE NOT (version = ANY($1)) ORDER BY version`, known)
	if err != nil {
		if IsUndefinedTable(err) {
			return st, nil
		}
		return st, err
	}
	st.Unknown, err = pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return st, err
	}
	meta, err := readSchemaMeta(ctx, q)
	if err != nil {
		return st, err
	}
	st.MinBinary, st.MigratedBy = meta[metaMinBinary], meta[metaMigratedBy]
	return st, nil
}

// GuardSchema is CheckSchema plus the refusal: a *BinaryTooOldError when the binary is below the
// floor and PURSAE_ALLOW_OLDER_BINARY is not set. The status is returned either way, so the caller
// can warn about a newer schema (SchemaStatus.Warning).
func GuardSchema(ctx context.Context, q querier, binary string) (SchemaStatus, error) {
	st, err := CheckSchema(ctx, q, binary)
	if err != nil {
		return st, err
	}
	if st.TooOld() && !AllowOlderBinary() {
		return st, &BinaryTooOldError{Status: st}
	}
	return st, nil
}

// MinBinary is the schema's recorded floor ("" when none).
func MinBinary(ctx context.Context, q querier) (string, error) {
	meta, err := readSchemaMeta(ctx, q)
	return meta[metaMinBinary], err
}

func readSchemaMeta(ctx context.Context, q querier) (map[string]string, error) {
	out := map[string]string{}
	rows, err := q.Query(ctx, `SELECT key, value FROM schema_meta WHERE key = ANY($1)`, []string{metaMinBinary, metaMigratedBy})
	if err != nil {
		if IsUndefinedTable(err) {
			return out, nil
		}
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return out, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// execer is what recordSchemaMeta writes through.
type execer interface {
	querier
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// recordSchemaMeta raises min_binary_version to floor and migrated_by to binary; neither is ever
// lowered, so an older (still compatible) binary that migrates does not undo a newer one's floor.
// Without the schema_meta table (a migration fixture) it does nothing.
func recordSchemaMeta(ctx context.Context, q execer, floor, binary string) error {
	meta, err := readSchemaMeta(ctx, q)
	if err != nil {
		return err
	}
	var exists bool
	if err := q.QueryRow(ctx, `SELECT to_regclass('schema_meta') IS NOT NULL`).Scan(&exists); err != nil || !exists {
		return err
	}
	set := func(key, v string) error {
		if v == "" || (meta[key] != "" && !coreupdate.Less(meta[key], v)) {
			return nil // unknown, or not newer than what is recorded
		}
		_, err := q.Exec(ctx, `INSERT INTO schema_meta(key, value) VALUES ($1, $2)
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`, key, v)
		return err
	}
	return errors.Join(set(metaMinBinary, floor), set(metaMigratedBy, binary))
}
