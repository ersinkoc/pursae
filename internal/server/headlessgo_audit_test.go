package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// Regression tests for the Pursae Headless audit fixes. The TestHeadlessGoUnit* tests need no
// database.

func hgParseArg(t *testing.T, lit string) (any, error) {
	t.Helper()
	doc, err := hgGQLParse(`{ f(a: ` + lit + `) }`)
	if err != nil {
		return nil, err
	}
	return doc.ops[0].sel[0].args["a"], nil
}

func TestHeadlessGoUnitGQLStrings(t *testing.T) {
	for lit, want := range map[string]string{
		`"Satır 1\r\nSatır 2"`:          "Satır 1\r\nSatır 2",
		`"q\" b\\ s\/ \b\f\t"`:          "q\" b\\ s/ \b\f\t",
		`"ı é"`:                         "ı é",
		`"😀!"`:                          "\U0001F600!",
		`"\u{1F600}"`:                   "\U0001F600",
		`"""raw \n "quoted" \"""end"""`: `raw \n "quoted" """end`,
		"\"\"\"\n    Hello,\n      World!\n\n    Yours\n  \"\"\"": "Hello,\n  World!\n\nYours",
		"\"\"\"  first\n    second\"\"\"":                         "  first\nsecond",
	} {
		v, err := hgParseArg(t, lit)
		if err != nil || v != want {
			t.Errorf("%s: got %q, %v; want %q", lit, v, err, want)
		}
	}
	for _, bad := range []string{`"\x"`, `"\u12"`, `"\uD83D"`, `"\uDE00"`, `"\uZZZZ"`, "\"line\nbreak\"", `"open`, `"""open`} {
		if _, err := hgParseArg(t, bad); err == nil {
			t.Errorf("%s: parsed, want a syntax error", bad)
		}
	}
	// An integer a float64 cannot hold keeps its digits; small ones stay numbers.
	if v, err := hgParseArg(t, "9007199254740993"); err != nil || v != json.Number("9007199254740993") {
		t.Errorf("big integer literal: %#v %v", v, err)
	}
	if v, _ := hgParseArg(t, "42"); v != float64(42) {
		t.Errorf("small integer literal: %#v", v)
	}
}

func TestHeadlessGoUnitBigIntegers(t *testing.T) {
	h := &hgCoercer{}
	big := &hgField{Type: "number", Format: "biginteger"}
	integer := &hgField{Type: "number", Format: "integer"}
	if _, ok := h.number(big, float64(9007199254740993), []string{"n"}); ok {
		t.Error("a rounded biginteger JSON number was accepted")
	}
	if v, ok := h.number(big, json.Number("9007199254740993"), []string{"n"}); !ok || v != "9007199254740993" {
		t.Errorf("biginteger from a literal: %v %v", v, ok)
	}
	if v, ok := h.number(big, "12345678901234567890", []string{"n"}); !ok || v != "12345678901234567890" {
		t.Errorf("biginteger string: %v %v", v, ok)
	}
	if _, ok := h.number(integer, float64(1<<53), []string{"n"}); ok {
		t.Error("an integer above 2^53 was accepted")
	}
	if v, ok := h.number(integer, float64(12), []string{"n"}); !ok || v != float64(12) {
		t.Errorf("integer: %v %v", v, ok)
	}
}

func TestHeadlessGoUnitReadLimits(t *testing.T) {
	b := newHGReadBudget()
	for i := 0; i < hgReadMaxQueries; i++ {
		if err := b.query(); err != nil {
			t.Fatalf("query %d: %v", i, err)
		}
	}
	if err := b.query(); !errors.Is(err, errHGTooExpensive) {
		t.Fatalf("over the budget: %v", err)
	}
	if err := (*hgReadBudget)(nil).query(); err != nil {
		t.Fatal("a nil budget (the admin) must not limit")
	}
	if err := newHGReadBudget().objects(hgReadMaxObjects + 1); err == nil {
		t.Fatal("object budget not enforced")
	}
	if _, err := hgParsePagination(map[string]any{"page": "92233720368547758"}, 25, 100); err == nil {
		t.Error("a page that overflows the offset was accepted")
	}
	q := &hgQuery{sql: &hgSQL{}}
	for _, bad := range []string{"1.5", "1e3", "NaN"} {
		if _, err := q.typed("bigint", bad); err == nil {
			t.Errorf("bigint %q accepted", bad)
		}
	}
	for _, bad := range []string{"NaN", "Infinity", "-inf", "0x1p-2", "1_000"} {
		if _, err := q.typed("numeric", bad); err == nil {
			t.Errorf("numeric %q accepted", bad)
		}
	}
	for _, ok := range []string{"1.5", "-2", "1e3", ".5"} {
		if _, err := q.typed("numeric", ok); err != nil {
			t.Errorf("numeric %q refused: %v", ok, err)
		}
	}
}

func TestHeadlessGoUnitErrorMessages(t *testing.T) {
	if m := hgGQLErrorMessage(fmt.Errorf("find: %w", &pgconn.PgError{Code: "42P01", Message: "relation \"secret_table\" does not exist"})); strings.Contains(m, "secret") {
		t.Errorf("database error leaked: %q", m)
	}
	if m := hgGQLErrorMessage(&pgconn.PgError{Code: "22P02", Message: "invalid input syntax for type bigint"}); strings.Contains(m, "bigint") || strings.Contains(m, "SQLSTATE") {
		t.Errorf("data exception leaked: %q", m)
	}
	if m := hgGQLErrorMessage(context.Canceled); m != "Internal server error" {
		t.Errorf("context error: %q", m)
	}
	if m := hgGQLErrorMessage(errors.New("The argument documentId is required")); m != "The argument documentId is required" {
		t.Errorf("executor message changed: %q", m)
	}
}

func TestHeadlessGoUnitPreviewReads(t *testing.T) {
	a := &hgAuth{Kind: "preview", PrevUID: "page", PrevDoc: "d1", Public: map[string][]string{"tag": {"find"}}}
	if a.reads("page") {
		t.Error("a preview token reads other entries of its own type through relations")
	}
	if !a.reads("tag") || a.reads("author") {
		t.Error("a preview token's related reads must follow the public role")
	}
	if !a.can("page", "findOne") {
		t.Error("the preview token lost its own document")
	}
}

func TestHeadlessGoUnitSubscriptionCaps(t *testing.T) {
	b := &hgBroker{subs: map[*hgSub]struct{}{}, clients: map[string]int{}, conns: map[string]int{}}
	var mine []*hgSub
	for i := 0; i < hgSubPerClient; i++ {
		if s := b.subscribe("ip:192.0.2.1", false); s == nil {
			t.Fatalf("subscription %d refused", i)
		} else {
			mine = append(mine, s)
		}
	}
	if b.subscribe("ip:192.0.2.1", false) != nil {
		t.Fatal("one address took more than its share")
	}
	if b.subscribe("ip:192.0.2.2", false) == nil || b.subscribe("token:1", true) == nil {
		t.Fatal("other clients were locked out")
	}
	b.unsubscribe(mine[0])
	if b.subscribe("ip:192.0.2.1", false) == nil {
		t.Fatal("a released slot was not given back")
	}
	// Callers without a token cannot take the slots kept for tokens.
	for i := 0; len(b.subs) < hgSubMaxConn-hgSubReserved; i++ {
		b.subscribe(fmt.Sprintf("ip:10.0.%d.%d", i/200, i%200), false)
	}
	if b.subscribe("ip:198.51.100.1", false) != nil {
		t.Fatal("the public callers took the reserved slots")
	}
	if b.subscribe("token:2", true) == nil {
		t.Fatal("a token caller found no reserved slot")
	}
	for i := 0; i < hgWSPerAddr; i++ {
		if !b.openConn("192.0.2.9") {
			t.Fatalf("connection %d refused", i)
		}
	}
	if b.openConn("192.0.2.9") {
		t.Fatal("too many WebSocket connections from one address")
	}
	b.closeConn("192.0.2.9")
	if !b.openConn("192.0.2.9") {
		t.Fatal("a closed connection was not given back")
	}
}

// gql posts a GraphQL query.
func hgGQL(t *testing.T, base, token, query string) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"query": query})
	req, _ := http.NewRequest(http.MethodPost, base+hgAPIPrefix+"/graphql", strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out
}

func TestHeadlessGoAuditGraphQL(t *testing.T) {
	base, _, srv := hgSite(t)
	hgModel(t, srv)
	full := hgNewToken(t, srv, &hgToken{Name: "Full", Kind: "full-access"})
	one := hgDocOf(t, hgCall(t, base, "POST", "/categories?status=published", full, map[string]any{"data": map[string]any{"name": "One"}}))
	two := hgDocOf(t, hgCall(t, base, "POST", "/categories?status=published", full, map[string]any{"data": map[string]any{"name": "Two"}}))
	ada := hgDocOf(t, hgCall(t, base, "POST", "/authors", full, map[string]any{"data": map[string]any{"name": "Ada", "email": "ada@example.test"}}))
	if r := hgCall(t, base, "POST", "/articles?status=published", full, map[string]any{"data": map[string]any{"title": "Linked", "categories": []any{one, two}, "author": ada}}); r.Code != 201 && r.Code != 200 {
		t.Fatalf("article: %d %s", r.Code, r.Raw)
	}

	// Aliases with different arguments get their own data.
	out := hgGQL(t, base, full, `{ articles { a: categories(filters: {name: {eq: "One"}}) { name } b: categories(sort: "name:desc") { name } } }`)
	raw, _ := json.Marshal(out)
	if !strings.Contains(string(raw), `"a":[{"name":"One"}]`) || !strings.Contains(string(raw), `"b":[{"name":"Two"},{"name":"One"}]`) {
		t.Errorf("aliased relations: %s", raw)
	}
	// The same response key with different arguments is an error.
	if out := hgGQL(t, base, full, `{ articles { categories(filters: {name: {eq: "One"}}) { name } categories { name } } }`); out["errors"] == nil {
		t.Errorf("conflicting arguments accepted: %v", out)
	}
	// Selections of one relation through a fragment are merged, not replaced.
	out = hgGQL(t, base, full, `{ articles { author { name } ...X } } fragment X on Article { author { email } }`)
	raw, _ = json.Marshal(out)
	if !strings.Contains(string(raw), `"name":"Ada"`) || !strings.Contains(string(raw), `"email":"ada@example.test"`) {
		t.Errorf("fragment merge: %s", raw)
	}

	// Escapes in literals are stored as the spec reads them.
	out = hgGQL(t, base, full, `mutation { createArticle(data: {title: "Satır 1\r\nSatır 2"}) { documentId title } }`)
	raw, _ = json.Marshal(out)
	if !strings.Contains(string(raw), `"title":"Satır 1\r\nSatır 2"`) {
		t.Errorf("escapes: %s", raw)
	}

	// Thousands of root aliases are refused (root field cap, then the read budget) instead of running a query each.
	var b strings.Builder
	b.WriteString("{")
	for i := 0; i <= hgReadMaxQueries; i++ {
		fmt.Fprintf(&b, " a%d: articles { title }", i)
	}
	b.WriteString(" }")
	out = hgGQL(t, base, full, b.String())
	if raw, _ := json.Marshal(out); out["data"] != nil || (!strings.Contains(string(raw), "too expensive") && !strings.Contains(string(raw), "root fields")) {
		t.Errorf("root aliases over the budget: %.300s", raw)
	}

	// Database errors are not shown; bad numeric filters are a 400.
	out = hgGQL(t, base, full, `{ articles(filters: {id: {eq: "1.5"}}) { title } }`)
	if raw, _ := json.Marshal(out); strings.Contains(string(raw), "SQLSTATE") || out["errors"] == nil {
		t.Errorf("bad id filter: %s", raw)
	}
	for _, path := range []string{"/articles?filters[id][$eq]=1e3", "/articles?filters[id][$eq]=NaN", "/articles?pagination[page]=92233720368547758"} {
		if r := hgCall(t, base, "GET", path, full, nil); r.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", path, r.Code, r.Raw)
		}
	}
}

func TestHeadlessGoAuditWrites(t *testing.T) {
	base, _, srv := hgSite(t)
	ctx := context.Background()
	hgLibrary(t, srv)
	hgMemo(t, srv, true)
	hgMustSchema(t, srv, &hgSchema{Kind: hgKindCollection, DisplayName: "Counter", SingularName: "counter", PluralName: "counters", Fields: []hgField{
		{Name: "a", Type: "number", Format: "integer"}, {Name: "b", Type: "number", Format: "integer"}, {Name: "ext", Type: "number", Format: "biginteger"}}})
	full := hgNewToken(t, srv, &hgToken{Name: "Full", Kind: "full-access"})

	// Review workflow: publishing on write is a 400 with the reason, not a 500.
	if r := hgCall(t, base, "POST", "/memos?status=published", full, map[string]any{"data": map[string]any{"title": "x"}}); r.Code != http.StatusBadRequest || !strings.Contains(r.Raw, "review") {
		t.Errorf("review gate on write: %d %s", r.Code, r.Raw)
	}
	// Large integers as JSON numbers are refused; as strings (or GraphQL literals) they are exact.
	if r := hgCall(t, base, "POST", "/counters", full, map[string]any{"data": map[string]any{"ext": json.Number("9007199254740993")}}); r.Code != http.StatusBadRequest {
		t.Errorf("rounded biginteger: %d %s", r.Code, r.Raw)
	}
	out := hgGQL(t, base, full, `mutation { createCounter(data: {ext: 9007199254740993}) { ext } }`)
	if raw, _ := json.Marshal(out); !strings.Contains(string(raw), `"9007199254740993"`) {
		t.Errorf("biginteger literal: %s", raw)
	}

	// Concurrent updates of different attributes both stay.
	doc := hgDocOf(t, hgCall(t, base, "POST", "/counters", full, map[string]any{"data": map[string]any{"a": 0, "b": 0}}))
	for round := 1; round <= 5; round++ {
		var wg sync.WaitGroup
		for _, k := range []string{"a", "b"} {
			wg.Add(1)
			go func(k string) {
				defer wg.Done()
				if r := hgCall(t, base, "PUT", "/counters/"+doc, full, map[string]any{"data": map[string]any{k: round}}); r.Code != 200 {
					t.Errorf("update %s: %d %s", k, r.Code, r.Raw)
				}
			}(k)
		}
		wg.Wait()
		d := hgCall(t, base, "GET", "/counters/"+doc, full, nil).data()
		if d["a"] != float64(round) || d["b"] != float64(round) {
			t.Fatalf("round %d lost an update: %v", round, d)
		}
	}

	// Writing an inverse relation needs update access to the owning type, and announces the
	// owning entries that changed.
	bob := hgDocOf(t, hgCall(t, base, "POST", "/writers", full, map[string]any{"data": map[string]any{"name": "Bob"}}))
	b1 := hgDocOf(t, hgCall(t, base, "POST", "/books?status=published", full, map[string]any{"data": map[string]any{"title": "One"}}))
	writerOnly := hgNewToken(t, srv, &hgToken{Name: "W", Kind: "custom", Permissions: map[string][]string{"writer": {"find", "findOne", "update"}}})
	if r := hgCall(t, base, "PUT", "/writers/"+bob, writerOnly, map[string]any{"data": map[string]any{"books": []any{b1}}}); r.Code != http.StatusBadRequest {
		t.Fatalf("inverse write without access to the owner: %d %s", r.Code, r.Raw)
	}
	if e, _ := srv.hgGetEntry(ctx, "book", b1, ""); e == nil || len(hgList(e.Published["writer"])) != 0 {
		t.Fatalf("the refused write changed the book: %+v", e)
	}
	both := hgNewToken(t, srv, &hgToken{Name: "WB", Kind: "custom", Permissions: map[string][]string{"writer": {"find", "findOne", "update"}, "book": {"update"}}})
	sub := srv.hgBroker().subscribe("test", true)
	defer srv.hgBroker().unsubscribe(sub)
	if r := hgCall(t, base, "PUT", "/writers/"+bob, both, map[string]any{"data": map[string]any{"books": []any{b1}}}); r.Code != 200 {
		t.Fatalf("inverse write: %d %s", r.Code, r.Raw)
	}
	var got []string
	for len(sub.ch) > 0 {
		ev := <-sub.ch
		got = append(got, ev.UID+" "+ev.Event)
	}
	if !containsStr(got, "book entry.update") || !containsStr(got, "book entry.publish") {
		t.Errorf("owning entry changes not announced: %v", got)
	}

	// A manual publication clears a pending schedule; a manual unpublication clears the
	// scheduled one, and a due unpublication of a draft announces nothing.
	m := hgDocOf(t, hgCall(t, base, "POST", "/books", full, map[string]any{"data": map[string]any{"title": "Later"}}))
	sc := srv.hgReg(ctx).Get("book")
	soon, later := time.Now().Add(time.Hour), time.Now().Add(2*time.Hour)
	if _, err := srv.hgSetSchedule(ctx, sc, m, "", &soon, &later); err != nil {
		t.Fatal(err)
	}
	if r := hgCall(t, base, "POST", "/books/"+m+"/publish", full, nil); r.Code != 200 {
		t.Fatalf("publish: %d %s", r.Code, r.Raw)
	}
	e, _ := srv.hgGetEntry(ctx, "book", m, "")
	if e.PublishAt != nil || e.UnpublishAt == nil {
		t.Fatalf("after a manual publish: publish_at %v unpublish_at %v", e.PublishAt, e.UnpublishAt)
	}
	if r := hgCall(t, base, "POST", "/books/"+m+"/unpublish", full, nil); r.Code != 200 {
		t.Fatalf("unpublish: %d %s", r.Code, r.Raw)
	}
	if e, _ = srv.hgGetEntry(ctx, "book", m, ""); e.UnpublishAt != nil {
		t.Fatal("a manual unpublish left the scheduled unpublication")
	}
	if _, err := srv.store.Pool.Exec(ctx, `UPDATE hg_entries SET unpublish_at = now() - interval '1 minute' WHERE id = $1`, e.ID); err != nil {
		t.Fatal(err)
	}
	for len(sub.ch) > 0 {
		<-sub.ch
	}
	if n := srv.hgRunSchedules(ctx); n != 0 || len(sub.ch) != 0 {
		t.Fatalf("a due unpublication of a draft ran: %d changes, %d events", n, len(sub.ch))
	}
	if e, _ = srv.hgGetEntry(ctx, "book", m, ""); e.UnpublishAt != nil {
		t.Fatal("the stale unpublication was not cleared")
	}
}

func TestHeadlessGoAuditPreviewSelfRelation(t *testing.T) {
	base, _, srv := hgSite(t)
	hgMustSchema(t, srv, &hgSchema{Kind: hgKindCollection, DisplayName: "Page", SingularName: "page", PluralName: "pages", DraftAndPublish: true, Fields: []hgField{
		{Name: "title", Type: "text"}, {Name: "related", Type: "relation", Relation: "manyToMany", Target: "page"}}})
	full := hgNewToken(t, srv, &hgToken{Name: "Full", Kind: "full-access"})
	other := hgDocOf(t, hgCall(t, base, "POST", "/pages?status=published", full, map[string]any{"data": map[string]any{"title": "Members only"}}))
	doc := hgDocOf(t, hgCall(t, base, "POST", "/pages", full, map[string]any{"data": map[string]any{"title": "Draft", "related": []any{other}}}))
	ptok := srv.hgPreviewToken(srv.hgLoad(), "page", doc, "", time.Hour)
	r := hgCall(t, base, "GET", "/pages/"+doc+"?status=draft&populate=related", ptok, nil)
	if r.Code != 200 || strings.Contains(r.Raw, "Members only") {
		t.Errorf("preview populate through a self-relation: %d %s", r.Code, r.Raw)
	}
	if r := hgCall(t, base, "GET", "/pages?status=draft&filters[related][title][$startsWith]=Mem", ptok, nil); r.Code != http.StatusBadRequest {
		t.Errorf("preview filter through a self-relation: %d %s", r.Code, r.Raw)
	}
}

func TestHeadlessGoAuditWebhookBatch(t *testing.T) {
	_, _, srv := hgSite(t)
	hgWebhookAllowPrivate.Store(true)
	defer hgWebhookAllowPrivate.Store(false)
	ctx := context.Background()
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(time.Second) // a slow receiver
	}))
	defer hook.Close()
	w := &hgWebhook{Name: "Slow", URL: hook.URL, Events: []string{"entry.create"}, Enabled: true}
	if err := srv.hgSaveWebhook(ctx, w); err != nil {
		t.Fatal(err)
	}
	var due []*hgQueued
	for i := 0; i < 8; i++ {
		q := &hgQueued{WebhookID: w.ID, DeliveryID: fmt.Sprintf("d%d", i), Event: "entry.create", Body: []byte(`{}`), MaxTries: 3}
		if err := srv.store.Pool.QueryRow(ctx, `INSERT INTO hg_webhook_queue(webhook_id, delivery_id, event, body, max_attempts, status, locked_at)
			VALUES ($1, $2, $3, $4, 3, 'running', now()) RETURNING id`, q.WebhookID, q.DeliveryID, q.Event, q.Body).Scan(&q.ID); err != nil {
			t.Fatal(err)
		}
		due = append(due, q)
	}
	start := time.Now()
	srv.hgAttemptBatch(ctx, due, 4, 500*time.Millisecond)
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("the batch took %v: deliveries are not bounded", d)
	}
	var attempted, released int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE attempts > 0), count(*) FILTER (WHERE attempts = 0 AND status = 'pending' AND locked_at IS NULL)
		FROM hg_webhook_queue WHERE webhook_id = $1`, w.ID).Scan(&attempted, &released)
	if attempted == 0 || released == 0 || attempted+released != 8 {
		t.Fatalf("attempted %d, released %d of 8", attempted, released)
	}
}
