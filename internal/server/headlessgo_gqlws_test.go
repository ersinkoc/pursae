package server

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

// hgGQLErrs returns the errors of a GraphQL response as "message @line:column" strings.
func hgGQLErrs(r hgResp) []string {
	errs, _ := r.Body["errors"].([]any)
	var out []string
	for _, e := range errs {
		m, _ := e.(map[string]any)
		s, _ := m["message"].(string)
		if locs, _ := m["locations"].([]any); len(locs) > 0 {
			l, _ := locs[0].(map[string]any)
			s += " @" + hgCell(l["line"]) + ":" + hgCell(l["column"])
		}
		out = append(out, s)
	}
	return out
}

func TestHeadlessGoGraphQLValidationSpec(t *testing.T) {
	base, _, srv := hgSite(t)
	hgLibrary(t, srv)
	full := hgNewToken(t, srv, &hgToken{Name: "Full", Kind: "full-access"})
	ada := hgDocOf(t, hgCall(t, base, "POST", "/writers", full, map[string]any{"data": map[string]any{"name": "Ada"}}))
	hgCall(t, base, "POST", "/books?status=published", full, map[string]any{"data": map[string]any{"title": "One", "writer": ada}})
	run := func(q string, vars map[string]any) hgResp {
		return hgCall(t, base, "POST", "/graphql", full, map[string]any{"query": q, "variables": vars})
	}
	for _, c := range []struct {
		q    string
		vars map[string]any
		want string
	}{
		// Locations point at the offending field.
		{"{\n  books {\n    titel\n  }\n}", nil, `Cannot query field "titel" on type "Book". Did you mean "title"? @3:5`},
		// Unknown arguments on nested fields, and the hint.
		{`{ books { writer(first: 1) { name } } }`, nil, `Unknown argument "first" on field "Book.writer". @1:18`},
		{`{ books(filter: {}) { title } }`, nil, `Unknown argument "filter" on field "Query.books". Did you mean "filters"?`},
		// Unknown fields inside input objects: literals, nested, and variable values.
		{`{ books(filters: {titel: {eq: "x"}}) { title } }`, nil, `Field "titel" is not defined by type "BookFiltersInput". Did you mean "title"?`},
		{`{ books(filters: {writer: {nam: {eq: "Ada"}}}) { title } }`, nil, `Field "nam" is not defined by type "WriterFiltersInput". Did you mean "name"?`},
		{`{ books(filters: {title: {equals: "x"}}) { title } }`, nil, `Field "equals" is not defined by type "StringFilterInput".`},
		{`{ books(pagination: {size: 2}) { title } }`, nil, `Field "size" is not defined by type "PaginationArg".`},
		{`query Q($f: BookFiltersInput) { books(filters: $f) { title } }`, map[string]any{"f": map[string]any{"pages": map[string]any{"gt": 1}, "bogus": 1}},
			`Variable "$f" got invalid value: Field "bogus" is not defined by type "BookFiltersInput".`},
		{`mutation { createBook(data: {title: "x", colour: "red"}) { title } }`, nil, `Field "colour" is not defined by type "BookInput".`},
		{`{ writers { books(filters: {titel: {eq: "x"}}) { title } } }`, nil, `Field "titel" is not defined by type "BookFiltersInput".`},
		// Variables, required arguments, enums.
		{`{ books(filters: {title: {eq: $nope}}) { title } }`, nil, `Variable "$nope" is not defined.`},
		{`{ book { title } }`, nil, `Field "book" argument "documentId" of type "ID!" is required, but it was not provided.`},
		{`{ books(status: LIVE) { title } }`, nil, `Value "LIVE" does not exist in "PublicationStatus" enum.`},
		// Fragments and directives.
		{`{ books { ...W } } fragment W on Writer { name }`, nil, `Fragment "W" cannot be spread here as objects of type "Book" can never be of type "Writer".`},
		{`{ books { ... on Nope { title } } }`, nil, `Unknown type "Nope".`},
		{`{ books { ...Missing } }`, nil, `Unknown fragment "Missing".`},
		{`{ books { ...A } } fragment A on Book { ...A }`, nil, `Cannot spread fragment "A" within itself.`},
		{`{ books { title @deprecated } }`, nil, `Unknown directive "@deprecated".`},
		{`{ books { title @skip } }`, nil, `Directive "@skip" argument "if" of type "Boolean!" is required`},
		// Parts that @skip leaves out are validated too.
		{`{ books { title ghost @skip(if: true) } }`, nil, `Cannot query field "ghost" on type "Book".`},
	} {
		r := run(c.q, c.vars)
		got := strings.Join(hgGQLErrs(r), " | ")
		_, hasData := r.Body["data"]
		if r.Code != 400 || hasData || !strings.Contains(got, c.want) {
			t.Errorf("%s\n -> %d %s\n want %q", c.q, r.Code, r.Raw, c.want)
		}
	}
	// A syntax error carries its location and the parse code.
	r := run("{ books { title }", nil)
	if errs := hgGQLErrs(r); r.Code != 400 || len(errs) != 1 || !strings.Contains(errs[0], "@1:18") || !strings.Contains(r.Raw, "GRAPHQL_PARSE_FAILED") {
		t.Errorf("syntax error: %d %s", r.Code, r.Raw)
	}
	// What the REST API accepts still works: null tests, relation filters and shorthands, system
	// fields, enum spellings, lists of one.
	for _, q := range []string{
		`{ books(filters: {writer: {null: false}, title: {eq: "One"}}) { title } }`,
		`{ books(filters: {writer: {name: {eq: "Ada"}}, id: {gt: 0}}) { title } }`,
		`{ books(filters: {or: [{title: {eq: "One"}}, {pages: {null: true}}]}, sort: "title:asc", status: published) { title } }`,
		`{ writers(filters: {books: {title: {eq: "One"}}}) { name books(sort: "title") { title } } }`,
		`query Q($s: PublicationStatus = PUBLISHED, $d: Boolean = true) { books(status: $s) { title writer @include(if: $d) { name } } }`,
	} {
		if r := run(q, nil); r.Code != 200 || r.Body["errors"] != nil || !strings.Contains(r.Raw, `"One"`) {
			t.Errorf("valid query refused: %s\n -> %d %s", q, r.Code, r.Raw)
		}
	}
}

func TestHeadlessGoSubscriptionsUnpublishDelete(t *testing.T) {
	base, _, srv := hgSite(t)
	hgMemo(t, srv, false)
	full := hgNewToken(t, srv, &hgToken{Name: "Full", Kind: "full-access"})
	readOnly := hgNewToken(t, srv, &hgToken{Name: "RO", Kind: "read-only"})
	r := hgCall(t, base, "POST", "/graphql", full, map[string]any{"query": `{ __type(name: "Subscription") { fields { name type { kind ofType { name } } } } }`})
	if !strings.Contains(r.Raw, `"memoUnpublished"`) || !strings.Contains(r.Raw, `"memoDeleted"`) || !strings.Contains(r.Raw, `"EntryReference"`) {
		t.Fatalf("introspection: %s", r.Raw)
	}
	drafts, resp := hgOpenSSE(t, base, full, `subscription { memoDeleted { documentId __typename } }`)
	defer resp.Body.Close()
	public, resp2 := hgOpenSSE(t, base, readOnly, `subscription { gone: memoDeleted { documentId } }`)
	defer resp2.Body.Close()
	unpub, resp3 := hgOpenSSE(t, base, readOnly, `subscription { memoUnpublished { documentId locale } }`)
	defer resp3.Body.Close()
	for _, s := range []*hgSSEReader{drafts, public, unpub} {
		s.next()
	}
	// A draft that never was published: only a caller that may read drafts hears of its deletion.
	d1 := hgDocOf(t, hgCall(t, base, "POST", "/memos", full, map[string]any{"data": map[string]any{"title": "Draft"}}))
	hgCall(t, base, "DELETE", "/memos/"+d1, full, nil)
	if got := drafts.next(); !strings.Contains(got, `"memoDeleted":{"__typename":"EntryReference","documentId":"`+d1+`"}`) {
		t.Fatalf("deleted event: %q", got)
	}
	if !public.quiet() {
		t.Fatal("the deletion of a never published draft reached a read-only token")
	}
	// A published one: unpublish and delete reach everyone.
	d2 := hgDocOf(t, hgCall(t, base, "POST", "/memos?status=published", full, map[string]any{"data": map[string]any{"title": "Live"}}))
	hgCall(t, base, "POST", "/memos/"+d2+"/unpublish", full, nil)
	if got := unpub.next(); !strings.Contains(got, `"memoUnpublished":{"documentId":"`+d2+`","locale":null}`) {
		t.Fatalf("unpublished event: %q", got)
	}
	hgCall(t, base, "POST", "/memos/"+d2+"/publish", full, nil)
	hgCall(t, base, "DELETE", "/memos/"+d2, full, nil)
	if got := public.next(); !strings.Contains(got, `"gone":{"documentId":"`+d2+`"}`) {
		t.Fatalf("deleted event for a read-only token: %q", got)
	}
}

// hgWS is a graphql-transport-ws test client.
type hgWS struct {
	t  *testing.T
	ws *websocket.Conn
}

func hgDialWS(t *testing.T, base, origin string, hdr http.Header) (*hgWS, error) {
	t.Helper()
	cfg, err := websocket.NewConfig(strings.Replace(base, "http", "ws", 1)+hgAPIPrefix+"/graphql", origin)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Protocol = []string{hgWSProtocol}
	cfg.Header = hdr
	ws, err := websocket.DialConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &hgWS{t: t, ws: ws}, nil
}

func (c *hgWS) send(m map[string]any) {
	c.t.Helper()
	if err := websocket.JSON.Send(c.ws, m); err != nil {
		c.t.Fatal(err)
	}
}

// recv returns the next message other than a server ping, or nil when the connection closed.
func (c *hgWS) recv() map[string]any {
	c.t.Helper()
	for {
		_ = c.ws.SetReadDeadline(time.Now().Add(5 * time.Second))
		var m map[string]any
		if err := websocket.JSON.Receive(c.ws, &m); err != nil {
			if ne, ok := err.(interface{ Timeout() bool }); ok && ne.Timeout() {
				c.t.Fatal("no message within 5 s")
			}
			return nil
		}
		if m["type"] != "ping" {
			return m
		}
	}
}

func (c *hgWS) quiet() bool {
	_ = c.ws.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	var m map[string]any
	err := websocket.JSON.Receive(c.ws, &m)
	return err != nil
}

func hgJSON(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

func TestHeadlessGoGraphQLWebSocket(t *testing.T) {
	base, _, srv := hgSite(t)
	hgMemo(t, srv, false)
	full := hgNewToken(t, srv, &hgToken{Name: "Full", Kind: "full-access"})
	readOnly := hgNewToken(t, srv, &hgToken{Name: "RO", Kind: "read-only"})

	// Another site's page may not open a socket; the sub-protocol is required.
	if _, err := hgDialWS(t, base, "https://evil.example", nil); err == nil {
		t.Fatal("a foreign origin opened a WebSocket")
	}
	req, _ := http.NewRequest("GET", base+hgAPIPrefix+"/graphql", nil)
	for k, v := range map[string]string{"Connection": "Upgrade", "Upgrade": "websocket", "Sec-WebSocket-Version": "13", "Sec-WebSocket-Key": "dGhlIHNhbXBsZSBub25jZQ=="} {
		req.Header.Set(k, v)
	}
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("upgrade without the sub-protocol: %v %v", resp, err)
	}

	c, err := hgDialWS(t, base, base, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.ws.Close()
	if p := c.ws.Config().Protocol; len(p) != 1 || p[0] != hgWSProtocol {
		t.Fatalf("negotiated protocol: %v", p)
	}
	c.send(map[string]any{"type": "connection_init", "payload": map[string]any{"Authorization": "Bearer " + full}})
	if m := c.recv(); m["type"] != "connection_ack" {
		t.Fatalf("ack: %v", m)
	}
	c.send(map[string]any{"type": "ping", "payload": map[string]any{"n": 1}})
	if m := c.recv(); m["type"] != "pong" {
		t.Fatalf("pong: %v", m)
	}
	// A validation error answers the operation; the connection stays open.
	c.send(map[string]any{"id": "bad", "type": "subscribe", "payload": map[string]any{"query": `subscription { memoCreated { nothing } }`}})
	if m := c.recv(); m["type"] != "error" || m["id"] != "bad" || !strings.Contains(hgJSON(m["payload"]), `Cannot query field \"nothing\" on type \"Memo\".`) {
		t.Fatalf("validation error: %v", m)
	}
	// Two subscriptions on one connection, a draft change, and a query.
	c.send(map[string]any{"id": "1", "type": "subscribe", "payload": map[string]any{"query": `subscription { memoCreated { title } }`}})
	c.send(map[string]any{"id": "2", "type": "subscribe", "payload": map[string]any{"query": `subscription S($d: ID) { memoUpdated(documentId: $d) { title } }`, "variables": map[string]any{"d": "nope"}}})
	time.Sleep(100 * time.Millisecond)
	doc := hgDocOf(t, hgCall(t, base, "POST", "/memos", full, map[string]any{"data": map[string]any{"title": "Draft one"}}))
	if m := c.recv(); m["type"] != "next" || m["id"] != "1" || hgJSON(m["payload"]) != `{"data":{"memoCreated":{"title":"Draft one"}}}` {
		t.Fatalf("next: %v", m)
	}
	hgCall(t, base, "PUT", "/memos/"+doc, full, map[string]any{"data": map[string]any{"title": "Draft two"}})
	if !c.quiet() {
		t.Fatal("subscription 2 heard of another document")
	}
	c.send(map[string]any{"id": "q", "type": "subscribe", "payload": map[string]any{"query": `{ memos(status: DRAFT) { title } }`}})
	if m := c.recv(); m["type"] != "next" || m["id"] != "q" || !strings.Contains(hgJSON(m["payload"]), `"Draft two"`) {
		t.Fatalf("query over the socket: %v", m)
	}
	if m := c.recv(); m["type"] != "complete" || m["id"] != "q" {
		t.Fatalf("query complete: %v", m)
	}
	// The client completes subscription 1: no more events for it.
	c.send(map[string]any{"id": "1", "type": "complete"})
	time.Sleep(100 * time.Millisecond)
	hgCall(t, base, "POST", "/memos", full, map[string]any{"data": map[string]any{"title": "Another"}})
	if !c.quiet() {
		t.Fatal("a completed subscription kept delivering")
	}
	// A duplicate id closes the connection (4409).
	c.send(map[string]any{"id": "2", "type": "subscribe", "payload": map[string]any{"query": `subscription { memoCreated { title } }`}})
	if m := c.recv(); m != nil {
		t.Fatalf("duplicate id: %v", m)
	}

	// Without connection_init, a subscribe closes the connection (4401); a bad token is refused (4403);
	// without a token the public permissions apply and a read-only token sees no drafts.
	c2, _ := hgDialWS(t, base, base, nil)
	defer c2.ws.Close()
	c2.send(map[string]any{"id": "1", "type": "subscribe", "payload": map[string]any{"query": `subscription { memoCreated { title } }`}})
	if m := c2.recv(); m != nil {
		t.Fatalf("subscribe before init: %v", m)
	}
	c3, _ := hgDialWS(t, base, base, nil)
	defer c3.ws.Close()
	c3.send(map[string]any{"type": "connection_init", "payload": map[string]any{"token": "nope"}})
	if m := c3.recv(); m != nil {
		t.Fatalf("bad token: %v", m)
	}
	c4, _ := hgDialWS(t, base, base, nil)
	defer c4.ws.Close()
	c4.send(map[string]any{"type": "connection_init"})
	c4.recv()
	c4.send(map[string]any{"id": "1", "type": "subscribe", "payload": map[string]any{"query": `subscription { memoCreated { title } }`}})
	if m := c4.recv(); m["type"] != "error" || !strings.Contains(hgJSON(m["payload"]), "Forbidden") {
		t.Fatalf("public subscription without permission: %v", m)
	}
	c5, _ := hgDialWS(t, base, base, http.Header{"Authorization": {"Bearer " + readOnly}})
	defer c5.ws.Close()
	c5.send(map[string]any{"type": "connection_init", "payload": map[string]any{}})
	c5.recv()
	c5.send(map[string]any{"id": "c", "type": "subscribe", "payload": map[string]any{"query": `subscription { memoCreated { title } }`}})
	c5.send(map[string]any{"id": "p", "type": "subscribe", "payload": map[string]any{"query": `subscription { memoPublished { title } }`}})
	time.Sleep(100 * time.Millisecond)
	d2 := hgDocOf(t, hgCall(t, base, "POST", "/memos", full, map[string]any{"data": map[string]any{"title": "Secret draft"}}))
	hgCall(t, base, "POST", "/memos/"+d2+"/publish", full, nil)
	if m := c5.recv(); m["id"] != "p" || hgJSON(m["payload"]) != `{"data":{"memoPublished":{"title":"Secret draft"}}}` {
		t.Fatalf("read-only token over the socket: %v", m)
	}
	// Closed sockets release their subscribers.
	c5.ws.Close()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		srv.hgBroker().mu.Lock()
		n := len(srv.hgBroker().subs)
		srv.hgBroker().mu.Unlock()
		if n == 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("closed sockets kept their subscribers")
}

// hgWSCloseCode opens a raw graphql-transport-ws connection, sends the text messages and returns the
// code of the close frame the server answers with.
func hgWSCloseCode(t *testing.T, base string, msgs ...string) uint16 {
	t.Helper()
	u, _ := url.Parse(base)
	conn, err := net.Dial("tcp", u.Host)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprintf(conn, "GET %s/graphql HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Protocol: %s\r\n\r\n", hgAPIPrefix, u.Host, hgWSProtocol)
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil || resp.StatusCode != http.StatusSwitchingProtocols || resp.Header.Get("Sec-WebSocket-Protocol") != hgWSProtocol {
		t.Fatalf("handshake: %v %v", resp, err)
	}
	for _, m := range msgs {
		frame := []byte{0x81, 0x80 | byte(len(m)), 1, 2, 3, 4} // masked text frame, short payload
		for i := 0; i < len(m); i++ {
			frame = append(frame, m[i]^frame[2+i%4])
		}
		if _, err := conn.Write(frame); err != nil {
			t.Fatal(err)
		}
	}
	for {
		hdr := make([]byte, 2)
		if _, err := io.ReadFull(br, hdr); err != nil {
			t.Fatalf("no close frame: %v", err)
		}
		n := int(hdr[1] & 0x7f)
		if n == 126 {
			ext := make([]byte, 2)
			io.ReadFull(br, ext)
			n = int(binary.BigEndian.Uint16(ext))
		}
		payload := make([]byte, n)
		if _, err := io.ReadFull(br, payload); err != nil {
			t.Fatal(err)
		}
		if hdr[0]&0x0f == 0x8 {
			if len(payload) < 2 {
				return 0
			}
			return binary.BigEndian.Uint16(payload)
		}
	}
}

func TestHeadlessGoGraphQLWebSocketCloseCodes(t *testing.T) {
	base, _, srv := hgSite(t)
	hgMemo(t, srv, false)
	full := hgNewToken(t, srv, &hgToken{Name: "Full", Kind: "full-access"})
	initMsg := `{"type":"connection_init","payload":{"Authorization":"Bearer ` + full + `"}}`
	sub := `{"id":"1","type":"subscribe","payload":{"query":"subscription { memoCreated { title } }"}}`
	for _, c := range []struct {
		name string
		msgs []string
		want uint16
	}{
		{"invalid message", []string{`{"nope"`}, 4400},
		{"unknown type", []string{`{"type":"start"}`}, 4400},
		{"subscribe before init", []string{sub}, 4401},
		{"bad token", []string{`{"type":"connection_init","payload":{"token":"nope"}}`}, 4403},
		{"two inits", []string{initMsg, initMsg}, 4429},
		{"duplicate id", []string{initMsg, sub, sub}, 4409},
	} {
		if got := hgWSCloseCode(t, base, c.msgs...); got != c.want {
			t.Errorf("%s: close code %d, want %d", c.name, got, c.want)
		}
	}
	old := hgWSInitTimeout
	hgWSInitTimeout = 200 * time.Millisecond
	defer func() { hgWSInitTimeout = old }()
	if got := hgWSCloseCode(t, base); got != 4408 {
		t.Errorf("no connection_init: close code %d, want 4408", got)
	}
}
