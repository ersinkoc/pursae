package server

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// hgSecondUser creates a user with the role and returns a client logged in as them.
func hgSecondUser(t *testing.T, srv *Server, base, login, role string) *http.Client {
	t.Helper()
	asCreateUser(t, srv, login, role, asStrongPass)
	c := asNewClient()
	if code, _, _ := post(t, c, base+"/wp-login", url.Values{"log": {login}, "pwd": {asStrongPass}}); code != http.StatusFound {
		t.Fatalf("login %s: %d", login, code)
	}
	return c
}

// hgEntryForm posts an entry form action as the client; the nonce comes from the entry screen.
func hgEntryForm(t *testing.T, c *http.Client, base, typ, doc, do string, extra url.Values) (int, string, string) {
	t.Helper()
	_, body, _ := get(t, c, base+"/wp-admin/headlessgo?page=entries&type="+typ)
	form := url.Values{"_wpnonce": {formNonce(t, body)}, "page": {"entry"}, "do": {do}, "type": {typ}, "doc": {doc}}
	for k, v := range extra {
		form[k] = v
	}
	return post(t, c, base+"/wp-admin/headlessgo", form)
}

func hgMemo(t *testing.T, srv *Server, review bool) {
	t.Helper()
	hgMustSchema(t, srv, &hgSchema{Kind: hgKindCollection, DisplayName: "Memo", SingularName: "memo", PluralName: "memos", DraftAndPublish: true, ReviewWorkflow: review,
		Fields: []hgField{{Name: "title", Type: "text", Required: true}}})
}

func TestHeadlessGoEditLock(t *testing.T) {
	base, admin, srv := hgSite(t)
	ctx := context.Background()
	hgMemo(t, srv, false)
	editor := hgSecondUser(t, srv, base, "eddie", "editor")
	full := hgNewToken(t, srv, &hgToken{Name: "Full", Kind: "full-access"})
	doc := hgDocOf(t, hgCall(t, base, "POST", "/memos", full, map[string]any{"data": map[string]any{"title": "One"}}))
	page := base + "/wp-admin/headlessgo?page=entry&type=memo&doc=" + doc

	// The first editor takes the lock; nobody is told anything.
	_, body, _ := get(t, admin, page)
	if strings.Contains(body, "is currently editing this entry") || !strings.Contains(body, `id="hg-lock-data"`) || !strings.Contains(body, `name="base"`) {
		t.Fatal("the first editor sees a lock or has no heartbeat")
	}
	// The second sees a read-only form with a take-over button, and cannot save, publish or delete.
	_, body, _ = get(t, editor, page)
	if !strings.Contains(body, "is currently editing this entry") || !strings.Contains(body, `value="lock_takeover"`) || strings.Contains(body, `data-hg-publish="1"`) || !strings.Contains(body, `"locked":true`) {
		t.Fatal("the second editor does not see the lock")
	}
	for _, do := range []string{"save_entry", "publish_entry", "delete_entry", "unpublish_entry"} {
		code, _, loc := hgEntryForm(t, editor, base, "memo", doc, do, url.Values{"payload": {`{"title":"Hijacked"}`}})
		if code != http.StatusFound || !strings.Contains(loc, "hg-error") || !strings.Contains(loc, "editing+this+entry") {
			t.Fatalf("%s while locked: %d %s", do, code, loc)
		}
	}
	e, err := srv.hgGetEntry(ctx, "memo", doc, "")
	if err != nil || e.Data["title"] != "One" {
		t.Fatalf("the locked entry changed: %v %v", err, e)
	}
	// Heartbeat: the holder's is quiet, the other's says who holds the lock.
	beat := func(c *http.Client, op string) string {
		_, b, _ := get(t, c, base+"/wp-admin/headlessgo?page=entries&type=memo")
		_, out, _ := post(t, c, base+"/wp-admin/headlessgo/lock", url.Values{"_wpnonce": {formNonce(t, b)}, "type": {"memo"}, "doc": {doc}, "op": {op}})
		return out
	}
	if out := beat(admin, "heartbeat"); !strings.Contains(out, `"locked":false`) {
		t.Fatalf("holder heartbeat: %s", out)
	}
	if out := beat(editor, "heartbeat"); !strings.Contains(out, `"locked":true`) || !strings.Contains(out, `"by":"`) {
		t.Fatalf("other heartbeat: %s", out)
	}
	// Take over: the lock moves and the former holder is refused.
	if code, _, loc := hgEntryForm(t, editor, base, "memo", doc, "lock_takeover", nil); code != http.StatusFound || strings.Contains(loc, "hg-error") {
		t.Fatalf("take over: %d %s", code, loc)
	}
	if out := beat(admin, "heartbeat"); !strings.Contains(out, `"locked":true`) {
		t.Fatalf("former holder is not told: %s", out)
	}
	if _, _, l := hgEntryForm(t, admin, base, "memo", doc, "save_entry", url.Values{"payload": {`{"title":"Late"}`}}); !strings.Contains(l, "hg-error") {
		t.Fatalf("the former holder saved: %s", l)
	}
	if code, _, loc := hgEntryForm(t, editor, base, "memo", doc, "save_entry", url.Values{"payload": {`{"title":"Two"}`}}); code != http.StatusFound || strings.Contains(loc, "hg-error") {
		t.Fatalf("the new holder cannot save: %d %s", code, loc)
	}
	// A save on top of a newer version is refused even without a lock.
	if code, _, loc := hgEntryForm(t, editor, base, "memo", doc, "save_entry", url.Values{"payload": {`{"title":"Three"}`}, "base": {"1"}}); code != http.StatusFound || !strings.Contains(loc, "changed+by+someone+else") {
		t.Fatalf("stale save: %d %s", code, loc)
	}
	// Releasing frees the lock; a lock nobody renewed expires.
	beat(editor, "release")
	if _, body, _ := get(t, admin, page); strings.Contains(body, "is currently editing this entry") {
		t.Fatal("released lock still held")
	}
	_, _ = srv.store.Pool.Exec(ctx, `UPDATE hg_entry_locks SET touched_at = now() - interval '10 minutes'`)
	if _, body, _ := get(t, editor, page); strings.Contains(body, "is currently editing this entry") {
		t.Fatal("an expired lock still blocks")
	}
	// The lock goes with the entry; routes answer 404 while the module is off.
	hgToggle(t, admin, base, "deactivate")
	if code, _, _ := get(t, admin, page); code != http.StatusNotFound {
		t.Fatalf("admin screen while off: %d", code)
	}
	if code, _, _ := post(t, admin, base+"/wp-admin/headlessgo/lock", url.Values{"type": {"memo"}}); code != http.StatusNotFound {
		t.Fatalf("lock route while off: %d", code)
	}
}

func TestHeadlessGoReviewFlow(t *testing.T) {
	base, admin, srv := hgSite(t)
	ctx := context.Background()
	hgMemo(t, srv, true)
	contrib := hgSecondUser(t, srv, base, "carl", "contributor")
	full := hgNewToken(t, srv, &hgToken{Name: "Full", Kind: "full-access"})
	doc := hgDocOf(t, hgCall(t, base, "POST", "/memos", full, map[string]any{"data": map[string]any{"title": "Draft"}}))

	// Publishing needs an approval: every route refuses.
	if r := hgCall(t, base, "POST", "/memos/"+doc+"/publish", full, nil); r.Code < 400 || !strings.Contains(r.Raw, "review") {
		t.Fatalf("API publish without approval: %d %s", r.Code, r.Raw)
	}
	if r := hgCall(t, base, "PUT", "/memos/"+doc+"?status=published", full, map[string]any{"data": map[string]any{"title": "Sneak"}}); r.Code < 400 {
		t.Fatalf("API save+publish without approval: %d %s", r.Code, r.Raw)
	}
	if r := hgCall(t, base, "POST", "/memos?status=published", full, map[string]any{"data": map[string]any{"title": "Sneak"}}); r.Code < 400 {
		t.Fatalf("API create+publish without approval: %d %s", r.Code, r.Raw)
	}
	gql := hgCall(t, base, "POST", "/graphql", full, map[string]any{"query": `mutation { publishMemo(documentId: "` + doc + `") { documentId } }`})
	if !strings.Contains(gql.Raw, "review") {
		t.Fatalf("GraphQL publish without approval: %s", gql.Raw)
	}
	loc := func(code int, _ string, l string) string { return l }
	if l := loc(hgEntryForm(t, admin, base, "memo", doc, "publish_entry", nil)); !strings.Contains(l, "hg-error") {
		t.Fatalf("admin publish without approval: %s", l)
	}
	// Approving something that was never submitted is refused.
	if l := loc(hgEntryForm(t, admin, base, "memo", doc, "approve_review", nil)); !strings.Contains(l, "hg-error") {
		t.Fatalf("approve without submit: %s", l)
	}
	// Submit; a contributor cannot approve, an editor-level user can.
	if l := loc(hgEntryForm(t, admin, base, "memo", doc, "submit_review", nil)); strings.Contains(l, "hg-error") {
		t.Fatalf("submit: %s", l)
	}
	e, _ := srv.hgGetEntry(ctx, "memo", doc, "")
	if e.ReviewState() != hgReviewPending {
		t.Fatalf("state after submit: %q", e.ReviewState())
	}
	if l := loc(hgEntryForm(t, contrib, base, "memo", doc, "approve_review", nil)); !strings.Contains(l, "hg-error") {
		t.Fatalf("contributor approved: %s", l)
	}
	// A change after the submission voids it.
	hgCall(t, base, "PUT", "/memos/"+doc, full, map[string]any{"data": map[string]any{"title": "Changed"}})
	e, _ = srv.hgGetEntry(ctx, "memo", doc, "")
	if e.ReviewState() != "" {
		t.Fatalf("a changed draft is still %q", e.ReviewState())
	}
	if l := loc(hgEntryForm(t, admin, base, "memo", doc, "approve_review", nil)); !strings.Contains(l, "hg-error") {
		t.Fatalf("approved a changed draft: %s", l)
	}
	// Reject with a note, resubmit, approve.
	hgEntryForm(t, admin, base, "memo", doc, "submit_review", nil)
	if l := loc(hgEntryForm(t, admin, base, "memo", doc, "reject_review", url.Values{"note": {"Needs a better title"}})); strings.Contains(l, "hg-error") {
		t.Fatalf("reject: %s", l)
	}
	_, body, _ := get(t, admin, base+"/wp-admin/headlessgo?page=entry&type=memo&doc="+doc)
	if !strings.Contains(body, "Rejected") || !strings.Contains(body, "Needs a better title") || !strings.Contains(body, `value="submit_review"`) {
		t.Fatal("the rejection and its note are not shown")
	}
	hgEntryForm(t, admin, base, "memo", doc, "submit_review", nil)
	if l := loc(hgEntryForm(t, admin, base, "memo", doc, "approve_review", url.Values{"note": {"OK"}})); strings.Contains(l, "hg-error") {
		t.Fatalf("approve: %s", l)
	}
	if r := hgCall(t, base, "POST", "/memos/"+doc+"/publish", full, nil); r.Code != 200 {
		t.Fatalf("publish after approval: %d %s", r.Code, r.Raw)
	}
	// The history has the review steps with the notes.
	var actions []string
	notes := ""
	for _, v := range srv.hgVersions(ctx, e.ID, 50) {
		actions = append(actions, v.Action)
		notes += v.Note + "|"
	}
	got := strings.Join(actions, ",")
	for _, a := range []string{"submit", "approve", "reject"} {
		if !strings.Contains(got, a) {
			t.Fatalf("history lacks %q: %s", a, got)
		}
	}
	if !strings.Contains(notes, "Needs a better title") || !strings.Contains(notes, "OK") {
		t.Fatalf("history notes: %s", notes)
	}
	_, body, _ = get(t, admin, base+"/wp-admin/headlessgo?page=history&type=memo&doc="+doc)
	if !strings.Contains(body, "Submitted for review") || !strings.Contains(body, "Approved") {
		t.Fatal("history screen lacks the review steps")
	}
	// The form's Publish button saves the draft and publishes it when it is approved.
	hgEntryForm(t, admin, base, "memo", doc, "submit_review", nil) // already live and unchanged: pending again
	hgEntryForm(t, admin, base, "memo", doc, "approve_review", nil)
	// A draft changed in the form and published at once is saved but not published.
	l := loc(hgEntryForm(t, admin, base, "memo", doc, "save_entry", url.Values{"payload": {`{"title":"Edited in the form"}`}, "publish": {"1"}}))
	if !strings.Contains(l, "Saved+as+a+draft") {
		t.Fatalf("form publish of an unapproved change: %s", l)
	}
	if r := hgCall(t, base, "GET", "/memos/"+doc, full, nil); r.data()["title"] != "Changed" {
		t.Fatalf("the live version changed: %s", r.Raw)
	}
	// A scheduled publication of an unapproved draft fails with the reason.
	past := time.Now().Add(-time.Minute)
	if _, err := srv.hgSetSchedule(ctx, srv.hgReg(ctx).Get("memo"), doc, "", &past, nil); err != nil {
		t.Fatal(err)
	}
	srv.hgRunSchedules(ctx)
	e, _ = srv.hgGetEntry(ctx, "memo", doc, "")
	if !strings.Contains(e.ScheduleError, "review") {
		t.Fatalf("schedule error: %q", e.ScheduleError)
	}
}

func TestHeadlessGoGraphQLValidation(t *testing.T) {
	base, _, srv := hgSite(t)
	hgMemo(t, srv, false)
	full := hgNewToken(t, srv, &hgToken{Name: "Full", Kind: "full-access"})
	hgCall(t, base, "POST", "/memos?status=published", full, map[string]any{"data": map[string]any{"title": "One"}})
	run := func(q string) hgResp {
		return hgCall(t, base, "POST", "/graphql", full, map[string]any{"query": q})
	}
	errMsg := func(r hgResp) string {
		errs, _ := r.Body["errors"].([]any)
		var out []string
		for _, e := range errs {
			m, _ := e.(map[string]any)
			out = append(out, m["message"].(string))
			if ext, _ := m["extensions"].(map[string]any); ext["code"] != "GRAPHQL_VALIDATION_FAILED" {
				t.Errorf("error code: %v", m)
			}
		}
		return strings.Join(out, " | ")
	}
	for _, c := range []struct{ q, want string }{
		{`{ memos { title bogus } }`, `Cannot query field "bogus" on type "Memo".`},
		{`{ memos { titel } }`, `Did you mean "title"?`},
		{`{ nothing { title } }`, `Cannot query field "nothing" on type "Query".`},
		{`{ memos { title { x } } }`, `must not have a selection`},
		{`{ memos }`, `must have a selection of subfields`},
		{`{ memos(nope: 1) { title } }`, `Unknown argument "nope" on field "Query.memos".`},
		{`{ memos_connection { nodes { title } nodez } }`, `Cannot query field "nodez"`},
		{`{ memos { ... on Memo { title ghost } } }`, `Cannot query field "ghost" on type "Memo".`},
		{`fragment M on Memo { nope } { memos { ...M } }`, `Cannot query field "nope" on type "Memo".`},
		{`mutation { createMemo(data: {title: "x"}) { bogus } }`, `Cannot query field "bogus" on type "Memo".`},
	} {
		r := run(c.q)
		if r.Code != 400 || r.Body["data"] != nil || !strings.Contains(errMsg(r), c.want) {
			t.Errorf("%s\n -> %d %s (want %q)", c.q, r.Code, r.Raw, c.want)
		}
	}
	// Several problems are all reported; nothing runs (the mutation created nothing).
	if r := run(`{ memos { a b } }`); !strings.Contains(errMsg(r), `"a"`) || !strings.Contains(errMsg(r), `"b"`) {
		t.Errorf("only some errors: %s", r.Raw)
	}
	if r := hgCall(t, base, "GET", "/memos", full, nil); len(r.list()) != 1 {
		t.Errorf("a rejected mutation ran: %s", r.Raw)
	}
	// Valid queries, introspection and a JSON scalar with a sub-selection keep working.
	if r := run(`{ __typename memos { __typename title createdAt } __schema { subscriptionType { name } } }`); r.Code != 200 || !strings.Contains(r.Raw, `"Subscription"`) || !strings.Contains(r.Raw, `"One"`) {
		t.Errorf("valid query: %d %s", r.Code, r.Raw)
	}
	if r := run(`{ __type(name: "Subscription") { fields { name } } }`); !strings.Contains(r.Raw, "memoCreated") || !strings.Contains(r.Raw, "memoPublished") {
		t.Errorf("subscription introspection: %s", r.Raw)
	}
}

// hgSSEReader reads the events of a server-sent event stream.
type hgSSEReader struct {
	t    *testing.T
	body io.ReadCloser
	ch   chan string
}

func hgOpenSSE(t *testing.T, base, token, query string) (*hgSSEReader, *http.Response) {
	t.Helper()
	req, _ := http.NewRequest("GET", base+"/wp-json/headlessgo/v1/graphql?query="+url.QueryEscape(query), nil)
	req.Header.Set("Accept", "text/event-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r := &hgSSEReader{t: t, body: resp.Body, ch: make(chan string, 64)}
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		return r, resp
	}
	go func() {
		defer close(r.ch)
		sc := bufio.NewScanner(resp.Body)
		var block []string
		for sc.Scan() {
			if line := sc.Text(); line != "" {
				block = append(block, line)
				continue
			}
			if len(block) > 0 {
				r.ch <- strings.Join(block, "\n")
			}
			block = nil
		}
	}()
	return r, resp
}

// next returns the next event block (comments included), failing after a timeout.
func (r *hgSSEReader) next() string {
	r.t.Helper()
	select {
	case b, ok := <-r.ch:
		if !ok {
			r.t.Fatal("the stream ended")
		}
		return b
	case <-time.After(5 * time.Second):
		r.t.Fatal("no event within 5 s")
	}
	return ""
}

func (r *hgSSEReader) quiet() bool {
	select {
	case <-r.ch:
		return false
	case <-time.After(300 * time.Millisecond):
		return true
	}
}

func TestHeadlessGoSubscriptions(t *testing.T) {
	base, _, srv := hgSite(t)
	hgMemo(t, srv, false)
	hgMustSchema(t, srv, &hgSchema{Kind: hgKindCollection, DisplayName: "Note", SingularName: "note", PluralName: "notes", Fields: []hgField{{Name: "body", Type: "text"}}})
	full := hgNewToken(t, srv, &hgToken{Name: "Full", Kind: "full-access"})
	readOnly := hgNewToken(t, srv, &hgToken{Name: "RO", Kind: "read-only"})

	// Validation and access are answered as plain JSON before any stream starts.
	for _, c := range []struct {
		tok, q string
		code   int
		want   string
	}{
		{full, `subscription { memoNope { title } }`, 400, "Cannot query field"},
		{full, `subscription { memoCreated { title } noteCreated { body } }`, 400, "exactly one root field"},
		{full, `subscription { memoCreated { nothing } }`, 400, `Cannot query field \"nothing\" on type \"Memo\".`},
		{"", `subscription { memoCreated { title } }`, 403, ""},
	} {
		r, resp := hgOpenSSE(t, base, c.tok, c.q)
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		_ = r
		if resp.StatusCode != c.code || !strings.Contains(string(b), c.want) {
			t.Errorf("%s: %d %s", c.q, resp.StatusCode, b)
		}
	}

	// A full-access token sees drafts: created, updated (draft values), published.
	created, resp := hgOpenSSE(t, base, full, `subscription { memoCreated { documentId title publishedAt } }`)
	defer resp.Body.Close()
	updated, resp2 := hgOpenSSE(t, base, full, `subscription Watch($d: ID) { u: memoUpdated(documentId: $d) { title } }`)
	defer resp2.Body.Close()
	published, resp3 := hgOpenSSE(t, base, full, `subscription { memoPublished { title publishedAt } }`)
	defer resp3.Body.Close()
	other, resp4 := hgOpenSSE(t, base, readOnly, `subscription { memoCreated { title } }`)
	defer resp4.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" || resp.Header.Get("Cache-Control") == "" {
		t.Fatalf("stream headers: %d %v", resp.StatusCode, resp.Header)
	}
	for _, r := range []*hgSSEReader{created, updated, published, other} {
		if got := r.next(); got != ": connected" {
			t.Fatalf("first block: %q", got)
		}
	}
	doc := hgDocOf(t, hgCall(t, base, "POST", "/memos", full, map[string]any{"data": map[string]any{"title": "Draft one"}}))
	got := created.next()
	if !strings.HasPrefix(got, "event: next\ndata: ") || !strings.Contains(got, `"memoCreated":{"documentId":"`+doc+`"`) || !strings.Contains(got, `"title":"Draft one"`) || !strings.Contains(got, `"publishedAt":null`) {
		t.Fatalf("created event: %q", got)
	}
	// The draft is not for a read-only token; nothing else was created, so the other streams are quiet.
	if !other.quiet() || !published.quiet() {
		t.Fatal("a draft change reached a caller that may not see drafts or a publish subscriber")
	}
	hgCall(t, base, "PUT", "/memos/"+doc, full, map[string]any{"data": map[string]any{"title": "Draft two"}})
	if got := updated.next(); !strings.Contains(got, `"u":{"title":"Draft two"}`) {
		t.Fatalf("updated event (alias, draft values): %q", got)
	}
	if hgCall(t, base, "POST", "/memos/"+doc+"/publish", full, nil).Code != 200 {
		t.Fatal("publish")
	}
	if got := published.next(); !strings.Contains(got, `"memoPublished":{"publishedAt":"`) {
		t.Fatalf("published event: %q", got)
	}
	// A type without draft & publish, and the documentId / other-type filters.
	note, respN := hgOpenSSE(t, base, readOnly, `subscription { noteCreated { body } }`)
	defer respN.Body.Close()
	note.next()
	hgCall(t, base, "POST", "/notes", full, map[string]any{"data": map[string]any{"body": "hello"}})
	if got := note.next(); !strings.Contains(got, `"noteCreated":{"body":"hello"}`) {
		t.Fatalf("note event for a read-only token: %q", got)
	}
	if !created.quiet() {
		t.Fatal("another type's event reached a memo subscriber")
	}
	// Closing the stream frees the subscriber.
	resp.Body.Close()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		srv.hgBroker().mu.Lock()
		n := len(srv.hgBroker().subs)
		srv.hgBroker().mu.Unlock()
		if n == 4 { // updated, published, other, note
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("a closed stream was not released")
}

func TestHeadlessGoSubscriptionBrokerOverflow(t *testing.T) {
	b := &hgBroker{subs: map[*hgSub]struct{}{}, clients: map[string]int{}, conns: map[string]int{}}
	sub := b.subscribe("ip:192.0.2.1", false)
	for i := 0; i < hgSubBuffer+5; i++ {
		b.publish(hgSubEvent{Event: "entry.create"})
	}
	n := 0
	for range sub.ch { // closed by the overflow: the buffered events still drain
		n++
	}
	if n != hgSubBuffer || len(b.subs) != 0 || len(b.clients) != 0 {
		t.Fatalf("overflow: drained %d, subscribers %d", n, len(b.subs))
	}
	b.unsubscribe(sub) // a second release must not panic
}

// hgNotes lists "action: note" of an entry's history, newest first.
func hgNotes(t *testing.T, srv *Server, uid, doc string) []string {
	t.Helper()
	e, err := srv.hgGetEntry(context.Background(), uid, doc, "")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, v := range srv.hgVersions(context.Background(), e.ID, 50) {
		out = append(out, v.Action+": "+v.Note)
	}
	return out
}

func TestHeadlessGoHistoryOtherSideOfRelation(t *testing.T) {
	base, _, srv := hgSite(t)
	hgLibrary(t, srv)
	full := hgNewToken(t, srv, &hgToken{Name: "Full", Kind: "full-access"})
	ada := hgDocOf(t, hgCall(t, base, "POST", "/writers", full, map[string]any{"data": map[string]any{"name": "Ada"}}))
	bob := hgDocOf(t, hgCall(t, base, "POST", "/writers", full, map[string]any{"data": map[string]any{"name": "Bob"}}))
	b1 := hgDocOf(t, hgCall(t, base, "POST", "/books", full, map[string]any{"data": map[string]any{"title": "One", "writer": ada}}))
	has := func(list []string, want string) bool {
		for _, x := range list {
			if x == want {
				return true
			}
		}
		return false
	}
	// Linking from the owning side is recorded on the entry whose inverse field changed.
	if n := hgNotes(t, srv, "writer", ada); !has(n, `link: Linked by Book "One" via books`) {
		t.Fatalf("ada's history after the owner linked her: %v", n)
	}
	// Moving the link by writing the inverse side is recorded on the owning entry (its data changed).
	if r := hgCall(t, base, "PUT", "/writers/"+bob, full, map[string]any{"data": map[string]any{"books": []any{b1}}}); r.Code != 200 {
		t.Fatalf("inverse write: %d %s", r.Code, r.Raw)
	}
	n := hgNotes(t, srv, "book", b1)
	if !has(n, `link: Linked Writer "Bob" via writer`) {
		t.Fatalf("the book's history after Bob took it: %v", n)
	}
	// Unlinking from the owning side.
	if r := hgCall(t, base, "PUT", "/books/"+b1, full, map[string]any{"data": map[string]any{"writer": nil}}); r.Code != 200 {
		t.Fatalf("unlink: %s", r.Raw)
	}
	if n := hgNotes(t, srv, "writer", bob); !has(n, `link: Unlinked by Book "One" via books`) {
		t.Fatalf("bob's history after the owner unlinked him: %v", n)
	}
	// Nothing changed: no new history on the other side.
	before := len(hgNotes(t, srv, "writer", bob))
	hgCall(t, base, "PUT", "/books/"+b1, full, map[string]any{"data": map[string]any{"title": "One!"}})
	if after := len(hgNotes(t, srv, "writer", bob)); after != before {
		t.Fatalf("an unrelated save wrote to the other side: %d -> %d", before, after)
	}
}

func TestHeadlessGoRestoreValidatesAgainstSchema(t *testing.T) {
	base, admin, srv := hgSite(t)
	ctx := context.Background()
	hgMustSchema(t, srv, &hgSchema{Kind: hgKindCollection, DisplayName: "Gadget", SingularName: "gadget", PluralName: "gadgets", Fields: []hgField{
		{Name: "name", Type: "text"}, {Name: "count", Type: "number", Format: "integer"}, {Name: "secret", Type: "password"}}})
	full := hgNewToken(t, srv, &hgToken{Name: "Full", Kind: "full-access"})
	doc := hgDocOf(t, hgCall(t, base, "POST", "/gadgets", full, map[string]any{"data": map[string]any{"name": "A", "count": 5, "secret": "pa55word!"}}))
	hgCall(t, base, "PUT", "/gadgets/"+doc, full, map[string]any{"data": map[string]any{"name": "B", "count": 2}})
	e, _ := srv.hgGetEntry(ctx, "gadget", doc, "")
	var v1 int64
	for _, v := range srv.hgVersions(ctx, e.ID, 50) {
		if v.Action == "create" {
			v1 = v.ID
		}
	}
	// The schema tightens: count may be 3 at most.
	g := hgCopySchema(srv.hgReg(ctx).Get("gadget"))
	g.Fields[1].Max = hgFloat(3)
	hgMustSchema(t, srv, g)

	// The history screen reports the mismatch before anything is restored.
	_, body, _ := get(t, admin, base+"/wp-admin/headlessgo?page=history&type=gadget&doc="+doc+"&v="+itoa64(v1))
	if !strings.Contains(body, "no longer fit the content type") || !strings.Contains(body, `name="partial"`) {
		t.Fatal("the history screen does not report the mismatch")
	}
	form := url.Values{"page": {"entry"}, "do": {"restore_version"}, "type": {"gadget"}, "doc": {doc}, "version": {itoa64(v1)}}
	code, _, loc := hgAdminSubmit(t, admin, base, form)
	if code != http.StatusFound || !strings.Contains(loc, "hg-error") || !strings.Contains(loc, "nothing+was+restored") || !strings.Contains(loc, "page=history") {
		t.Fatalf("restore of a version that does not fit: %d %s", code, loc)
	}
	if e, _ := srv.hgGetEntry(ctx, "gadget", doc, ""); e.Data["name"] != "B" {
		t.Fatalf("a refused restore changed the draft: %v", e.Data)
	}
	// Restoring only what fits: name comes back, count keeps its value, the password hash survives.
	form.Set("partial", "1")
	code, _, loc = hgAdminSubmit(t, admin, base, form)
	if code != http.StatusFound || strings.Contains(loc, "hg-error") || !strings.Contains(loc, "except+for+count") {
		t.Fatalf("partial restore: %d %s", code, loc)
	}
	e, _ = srv.hgGetEntry(ctx, "gadget", doc, "")
	if e.Data["name"] != "A" || e.Data["count"] != float64(2) || e.Data["secret"] == nil || e.Data["secret"] == "pa55word!" {
		t.Fatalf("after the partial restore: %v", e.Data)
	}
	// A version that fits restores as before.
	if _, _, err := srv.hgRestoreVersion(ctx, srv.hgReg(ctx).Get("gadget"), v1, 1, false); err == nil {
		t.Fatal("the full restore went through although count is out of range")
	}
	hgCall(t, base, "PUT", "/gadgets/"+doc, full, map[string]any{"data": map[string]any{"name": "C", "count": 3}})
	e, _ = srv.hgGetEntry(ctx, "gadget", doc, "")
	var v3 int64
	for _, v := range srv.hgVersions(ctx, e.ID, 50) {
		if v.Action == "save" {
			v3 = v.ID
			break
		}
	}
	hgCall(t, base, "PUT", "/gadgets/"+doc, full, map[string]any{"data": map[string]any{"name": "D"}})
	if _, skipped, err := srv.hgRestoreVersion(ctx, srv.hgReg(ctx).Get("gadget"), v3, 1, false); err != nil || len(skipped) != 0 {
		t.Fatalf("restore of a valid version: %v %v", err, skipped)
	}
	if e, _ := srv.hgGetEntry(ctx, "gadget", doc, ""); e.Data["name"] != "C" || e.Data["count"] != float64(3) {
		t.Fatalf("restored values: %v", e.Data)
	}
}
