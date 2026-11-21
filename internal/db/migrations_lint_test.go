package db

import (
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Migration file rules (docs/developer/getting-started/upgrading.md, "Schema changes"):
//
//   - names are NNNN_name.sql in lower case, so sort.Strings (Migrate's order) is the same everywhere;
//   - a numeric prefix is used once: the duplicates below shipped before the rule and stay, since
//     schema_migrations records them by full name;
//   - a migration after lintMaxGrandfathered that drops a table or column, changes a column's type or
//     renames anything is a contract step: it carries the contractMarker comment and lands only in a
//     release after the one that stopped using what it removes, so the previous release can still run
//     on the new schema (expand/contract).

// lintMaxGrandfathered is the newest migration when these rules were written; the destructive check
// applies to every later one.
const lintMaxGrandfathered = 106

// contractMarker marks a migration whose destructive statements are a planned contract step.
const contractMarker = "-- pursae:contract"

// grandfatheredDuplicates are the numeric prefixes used more than once before the rule, with their count.
var grandfatheredDuplicates = map[string]int{"0016": 9, "0020": 2, "0032": 2, "0098": 2}

var migrationNameRe = regexp.MustCompile(`^(\d{4})_[a-z0-9_]+\.sql$`)

func migrationNames(t *testing.T) []string {
	t.Helper()
	paths, err := fs.Glob(migrationFS, "migrations/*")
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(paths))
	for _, p := range paths {
		names = append(names, strings.TrimPrefix(p, "migrations/"))
	}
	return names
}

func TestMigrationFileNames(t *testing.T) {
	names := migrationNames(t)
	if len(names) == 0 {
		t.Fatal("no migrations embedded")
	}
	prefixes := map[string]int{}
	for _, n := range names {
		m := migrationNameRe.FindStringSubmatch(n)
		if m == nil {
			t.Errorf("migration %q: the name must be NNNN_name.sql (four digits, then lower-case letters, digits and _)", n)
			continue
		}
		prefixes[m[1]]++
	}
	for p, n := range prefixes {
		if n > 1 && grandfatheredDuplicates[p] != n {
			t.Errorf("migration number %s is used by %d files: give the new migration the next free number", p, n)
		}
	}
	for p, n := range grandfatheredDuplicates {
		if prefixes[p] != n {
			t.Errorf("grandfathered duplicate %s: %d files, the list says %d (migrations are never renamed or removed)", p, prefixes[p], n)
		}
	}

	// Migrate applies them in sort.Strings order; the numbers must agree with it, so a new file
	// never runs before an older one.
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	last := -1
	for _, n := range sorted {
		num, _ := strconv.Atoi(n[:4])
		if num < last {
			t.Errorf("migration %s sorts after number %04d", n, last)
		}
		last = num
	}
}

var (
	sqlLineComment  = regexp.MustCompile(`--[^\n]*`)
	sqlBlockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
	destructiveSQL  = []struct {
		what string
		re   *regexp.Regexp
	}{
		{"DROP TABLE", regexp.MustCompile(`(?i)\bDROP\s+TABLE\b`)},
		{"DROP COLUMN", regexp.MustCompile(`(?i)\bDROP\s+COLUMN\b`)},
		{"ALTER COLUMN ... TYPE", regexp.MustCompile(`(?i)\bALTER\s+(COLUMN\s+)?("[^"]+"|\w+)\s+(SET\s+DATA\s+)?TYPE\b`)},
		{"ALTER TYPE", regexp.MustCompile(`(?i)\bALTER\s+TYPE\b`)},
		{"RENAME", regexp.MustCompile(`(?i)\bRENAME\b`)},
	}
)

// destructiveStatements lists the destructive operations in sql, comments ignored.
func destructiveStatements(sql string) []string {
	code := sqlLineComment.ReplaceAllString(sqlBlockComment.ReplaceAllString(sql, ""), "")
	var out []string
	for _, d := range destructiveSQL {
		if d.re.MatchString(code) {
			out = append(out, d.what)
		}
	}
	return out
}

func TestMigrationsExpandContract(t *testing.T) {
	for _, n := range migrationNames(t) {
		num, err := strconv.Atoi(n[:min(4, len(n))])
		if err != nil || num <= lintMaxGrandfathered {
			continue
		}
		b, err := fs.ReadFile(migrationFS, "migrations/"+n)
		if err != nil {
			t.Fatal(err)
		}
		sql := string(b)
		if found := destructiveStatements(sql); len(found) > 0 && !strings.Contains(sql, contractMarker) {
			t.Errorf("migration %s uses %s: the previous release must still run on the new schema, so drop, retype or rename only "+
				"what a released version already stopped using, and mark the file with a %q comment "+
				"(docs/developer/getting-started/upgrading.md)", n, strings.Join(found, ", "), contractMarker)
		}
	}
}

func TestDestructiveStatements(t *testing.T) {
	for _, tc := range []struct {
		sql  string
		want string
	}{
		{"CREATE TABLE a (id int);\nCREATE INDEX IF NOT EXISTS a_id ON a (id);", ""},
		{"ALTER TABLE a ADD COLUMN b text NOT NULL DEFAULT '';", ""},
		{"-- we no longer DROP TABLE here\n/* nor RENAME */ SELECT 1;", ""},
		{"DROP INDEX IF EXISTS a_b;", ""},
		{"ALTER TABLE a ALTER COLUMN b SET DEFAULT 1;", ""},
		{"DROP TABLE IF EXISTS old;", "DROP TABLE"},
		{"alter table a drop column if exists b;", "DROP COLUMN"},
		{"ALTER TABLE a ALTER COLUMN b TYPE bigint;", "ALTER COLUMN ... TYPE"},
		{"ALTER TABLE a ALTER b SET DATA TYPE bigint;", "ALTER COLUMN ... TYPE"},
		{"ALTER TYPE mood ADD VALUE 'meh';", "ALTER TYPE"},
		{"ALTER TABLE a RENAME COLUMN b TO c;", "RENAME"},
		{"ALTER TABLE a RENAME TO b;", "RENAME"},
	} {
		if got := strings.Join(destructiveStatements(tc.sql), ", "); got != tc.want {
			t.Errorf("%q: %q, want %q", tc.sql, got, tc.want)
		}
	}
	// The destructive migrations that shipped before the rule are caught by it.
	for name, want := range map[string]string{
		"0028_drop_old_builder.sql":               "DROP TABLE",
		"0038_menu_locations_comment_indexes.sql": "DROP COLUMN",
		"0105_search_dotless_i.sql":               "DROP COLUMN",
	} {
		b, err := fs.ReadFile(migrationFS, "migrations/"+name)
		if err != nil {
			t.Fatal(err)
		}
		if got := destructiveStatements(string(b)); !strings.Contains(strings.Join(got, ","), want) {
			t.Errorf("%s: found %v, want %s", name, got, want)
		}
	}
}
