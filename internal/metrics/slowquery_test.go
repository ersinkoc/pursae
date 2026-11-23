package metrics

import (
	"bytes"
	"context"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestSlowQueryTracer(t *testing.T) {
	var n int
	tr := &SlowQueryTracer{Threshold: 5 * time.Millisecond, OnSlow: func() { n++ }}
	run := func(d time.Duration) {
		ctx := tr.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{SQL: "SELECT   1\n FROM t", Args: []any{"secret"}})
		time.Sleep(d)
		tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{})
	}
	run(0)
	if n != 0 {
		t.Fatal("fast query counted")
	}
	run(15 * time.Millisecond)
	if n != 1 {
		t.Fatal("slow query not counted")
	}
	tr.TraceQueryEnd(context.Background(), nil, pgx.TraceQueryEndData{}) // no start in ctx: ignored
}

// The log line is the SQL text only: arguments (passwords, personal data) are not written, and
// after 10 lines in the same second further slow queries are counted but not logged.
func TestSlowQueryLogOmitsArgsAndLimitsLines(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Default().Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })

	tr := &SlowQueryTracer{Threshold: time.Nanosecond}
	ctx := tr.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{
		SQL:  "SELECT $1 FROM accounts",
		Args: []any{"password-secret"},
	})
	time.Sleep(time.Millisecond)
	for range 15 {
		tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{})
	}
	out := buf.String()
	if strings.Contains(out, "password-secret") {
		t.Fatalf("slow query log included an argument:\n%s", out)
	}
	if !strings.Contains(out, "SELECT $1 FROM accounts") {
		t.Fatalf("slow query log dropped the SQL text:\n%s", out)
	}
	if got := strings.Count(out, "slow query"); got != 10 {
		t.Fatalf("%d slow query lines, want 10", got)
	}
}

func TestShortSQL(t *testing.T) {
	if got := shortSQL("SELECT  1\n\tFROM t"); got != "SELECT 1 FROM t" {
		t.Errorf("got %q", got)
	}
	if got := shortSQL(strings.Repeat("é", 400)); len(got) > 310 || !strings.HasSuffix(got, "…") {
		t.Errorf("not cut: %d", len(got))
	}
}
