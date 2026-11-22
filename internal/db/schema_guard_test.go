package db

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestSchemaStatus(t *testing.T) {
	cases := []struct {
		name          string
		st            SchemaStatus
		newer, tooOld bool
	}{
		{"same schema", SchemaStatus{Binary: "0.3.0", MinBinary: "0.4.0"}, false, false},
		{"newer, compatible", SchemaStatus{Binary: "0.4.0", MinBinary: "0.4.0", Unknown: []string{"0110_x"}}, true, false},
		{"newer, below the floor", SchemaStatus{Binary: "0.3.9", MinBinary: "0.4.0", Unknown: []string{"0110_x"}}, true, true},
		{"prerelease below its final", SchemaStatus{Binary: "0.4.0-beta.1", MinBinary: "0.4.0", Unknown: []string{"0110_x"}}, true, true},
		{"newer, no floor recorded", SchemaStatus{Binary: "0.1.0", Unknown: []string{"0110_x"}}, true, false},
		{"unknown binary version", SchemaStatus{MinBinary: "0.4.0", Unknown: []string{"0110_x"}}, true, false},
	}
	for _, c := range cases {
		if got := c.st.Newer(); got != c.newer {
			t.Errorf("%s: Newer = %v", c.name, got)
		}
		if got := c.st.TooOld(); got != c.tooOld {
			t.Errorf("%s: TooOld = %v", c.name, got)
		}
		if w := c.st.Warning(); (w != "") != c.newer {
			t.Errorf("%s: Warning = %q", c.name, w)
		}
	}
	err := (&BinaryTooOldError{Status: SchemaStatus{Binary: "0.3.9", MinBinary: "0.4.0", MigratedBy: "0.4.2",
		Unknown: []string{"0110_a", "0111_b", "0112_c", "0113_d"}}}).Error()
	for _, want := range []string{"Pursae 0.4.0 or newer", "Pursae 0.3.9", "migrated by Pursae 0.4.2", "0110_a, 0111_b, 0112_c and 1 more", AllowOlderBinaryEnv + "=1"} {
		if !strings.Contains(err, want) {
			t.Errorf("refusal %q lacks %q", err, want)
		}
	}
}

func TestAllowOlderBinary(t *testing.T) {
	for v, want := range map[string]bool{"": false, "0": false, "no": false, "1": true, "true": true, " YES ": true} {
		t.Setenv(AllowOlderBinaryEnv, v)
		if got := AllowOlderBinary(); got != want {
			t.Errorf("%s=%q: %v", AllowOlderBinaryEnv, v, got)
		}
	}
}

// useBinary sets the running binary's version and its compatibility floor for the rest of the test.
func useBinary(t *testing.T, version, floor string) {
	t.Helper()
	prevV, prevF := BinaryVersion, migrationFloor
	BinaryVersion, migrationFloor = version, floor
	t.Cleanup(func() { BinaryVersion, migrationFloor = prevV, prevF })
}

// TestSchemaGuard plays a downgrade: a newer binary migrates and raises the floor, a compatible older
// one still starts (and does not lower the floor), one below the floor is refused unless
// PURSAE_ALLOW_OLDER_BINARY=1. Skipped without PURSAE_TEST_ADMIN_DB.
func TestSchemaGuard(t *testing.T) {
	ctx := context.Background()
	pool, err := Connect(ctx, tempDatabase(t, "wpgo_test_guard"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	meta := `CREATE TABLE IF NOT EXISTS schema_meta (key text PRIMARY KEY, value text NOT NULL, updated_at timestamptz NOT NULL DEFAULT now())`
	older := map[string]string{"0001_a.sql": `CREATE TABLE a (id int)`, "0002_meta.sql": meta}
	newer := map[string]string{"0001_a.sql": `CREATE TABLE a (id int)`, "0002_meta.sql": meta,
		"0003_b.sql": `CREATE TABLE b (id int)`, "0004_drop_a.sql": `DROP TABLE a`}

	// Never migrated: nothing to guard.
	if st, err := GuardSchema(ctx, pool, "0.1.0"); err != nil || st.Newer() || st.MinBinary != "" {
		t.Fatalf("empty database: %+v %v", st, err)
	}

	// 0.4.1 contracts the schema (drops a) and declares 0.4.0 its floor.
	useMigrations(t, newer)
	useBinary(t, "0.4.1", "0.4.0")
	if _, err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if st, err := CheckSchema(ctx, pool, "0.4.1"); err != nil || st.Newer() || st.MinBinary != "0.4.0" || st.MigratedBy != "0.4.1" {
		t.Fatalf("after the newer binary: %+v %v", st, err)
	}

	// 0.4.0 (floor 0.3.0) does not know 0003 and 0004 but is at the floor: it starts, with a warning,
	// and its own lower floor and version do not overwrite the recorded ones.
	useMigrations(t, older)
	useBinary(t, "0.4.0", "0.3.0")
	if applied, err := Migrate(ctx, pool); err != nil || len(applied) != 0 {
		t.Fatalf("compatible older binary: %v %v", applied, err)
	}
	st, err := GuardSchema(ctx, pool, "0.4.0")
	if err != nil || !st.Newer() || st.TooOld() || strings.Join(st.Unknown, ",") != "0003_b,0004_drop_a" || st.Warning() == "" {
		t.Fatalf("compatible older binary: %+v %v", st, err)
	}
	if floor, err := MinBinary(ctx, pool); err != nil || floor != "0.4.0" {
		t.Fatalf("the floor was lowered to %q (%v)", floor, err)
	}
	if st.MigratedBy != "0.4.1" {
		t.Errorf("migrated_by went back to %q", st.MigratedBy)
	}

	// 0.3.9 is below the floor: Migrate and the guard refuse it.
	useBinary(t, "0.3.9", "0.3.0")
	_, err = Migrate(ctx, pool)
	var tooOld *BinaryTooOldError
	if !errors.As(err, &tooOld) || tooOld.Status.MinBinary != "0.4.0" || !strings.Contains(err.Error(), "needs Pursae 0.4.0 or newer") {
		t.Fatalf("Migrate with a binary below the floor: %v", err)
	}
	if _, err := GuardSchema(ctx, pool, "0.3.9"); !errors.As(err, &tooOld) {
		t.Fatalf("GuardSchema with a binary below the floor: %v", err)
	}

	// The override lets it start; the floor stays.
	t.Setenv(AllowOlderBinaryEnv, "1")
	if _, err := Migrate(ctx, pool); err != nil {
		t.Fatalf("with %s=1: %v", AllowOlderBinaryEnv, err)
	}
	if st, err := GuardSchema(ctx, pool, "0.3.9"); err != nil || !st.TooOld() {
		t.Fatalf("with %s=1: %+v %v", AllowOlderBinaryEnv, st, err)
	}
	if floor, _ := MinBinary(ctx, pool); floor != "0.4.0" {
		t.Fatalf("the override changed the floor to %q", floor)
	}
	t.Setenv(AllowOlderBinaryEnv, "")

	// A binary below the floor that knows every applied migration understands the schema.
	useMigrations(t, newer)
	if _, err := Migrate(ctx, pool); err != nil {
		t.Fatalf("a binary that knows every migration: %v", err)
	}

	// A newer release raises the floor.
	useBinary(t, "0.5.0", "0.4.1")
	if _, err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if st, _ := CheckSchema(ctx, pool, "0.5.0"); st.MinBinary != "0.4.1" || st.MigratedBy != "0.5.0" {
		t.Fatalf("the floor was not raised: %+v", st)
	}
}

// A database migrated before schema_meta existed has no floor.
func TestSchemaGuardWithoutMeta(t *testing.T) {
	ctx := context.Background()
	pool, err := Connect(ctx, tempDatabase(t, "wpgo_test_guard"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	useMigrations(t, map[string]string{"0001_a.sql": `CREATE TABLE a (id int)`, "0002_b.sql": `CREATE TABLE b (id int)`})
	useBinary(t, "0.3.0", "0.3.0")
	if _, err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	useMigrations(t, map[string]string{"0001_a.sql": `CREATE TABLE a (id int)`})
	st, err := GuardSchema(ctx, pool, "0.1.0")
	if err != nil || !st.Newer() || st.TooOld() || st.MinBinary != "" {
		t.Fatalf("no schema_meta: %+v %v", st, err)
	}
}
