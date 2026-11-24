package metrics

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

// SlowQueryTracer is a pgx.QueryTracer that logs and counts the queries that take at least
// Threshold. The log line carries the duration and the SQL text with whitespace collapsed and cut
// to 300 bytes, never the arguments (they may hold passwords or personal data). At most 10 lines
// are written per second so a database in trouble cannot flood the log; the counter still counts
// every slow query. OnSlow may be nil.
type SlowQueryTracer struct {
	Threshold time.Duration
	OnSlow    func()

	mu     sync.Mutex
	window int64 // unix second of the current rate window
	logged int   // lines written in that second

	unixNow func() int64 // clock for the rate window; nil is time.Now().Unix (tests fix it)
}

type startKey struct{}

type queryStart struct {
	at  time.Time
	sql string
}

func (t *SlowQueryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, startKey{}, &queryStart{at: time.Now(), sql: data.SQL})
}

func (t *SlowQueryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	q, ok := ctx.Value(startKey{}).(*queryStart)
	if !ok {
		return
	}
	d := time.Since(q.at)
	if d < t.Threshold {
		return
	}
	if t.OnSlow != nil {
		t.OnSlow()
	}
	var now int64
	if t.unixNow != nil {
		now = t.unixNow()
	} else {
		now = time.Now().Unix()
	}
	t.mu.Lock()
	if t.logged == 0 || now > t.window {
		t.window, t.logged = now, 0
	}
	canLog := now == t.window && t.logged < 10
	if canLog {
		t.logged++
	}
	t.mu.Unlock()
	if canLog {
		log.Printf("slow query %s: %s", d.Round(time.Millisecond), shortSQL(q.sql))
	}
}

func shortSQL(sql string) string {
	sql = strings.Join(strings.Fields(sql), " ")
	if len(sql) > 300 {
		sql = strings.ToValidUTF8(sql[:300], "") + "…"
	}
	return sql
}
