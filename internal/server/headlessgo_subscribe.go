package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// GraphQL subscriptions, on the same endpoint as queries (/wp-json/headlessgo/v1/graphql), over two
// transports:
//
//   - Server-sent events (the "distinct connections" mode of the graphql-sse protocol): send the
//     subscription as a GET (query in the URL, for EventSource) or a POST (JSON body, for fetch) and
//     read the stream. Plain HTTP: nothing new to host, proxies and CDNs pass it, and the same
//     Authorization header as every other call.
//   - WebSocket with the graphql-transport-ws protocol (the "graphql-ws" library, Apollo Client,
//     urql, GraphiQL): see headlessgo_ws.go.
//
// Root fields, per content type: <singular>Created, <singular>Updated, <singular>Published and
// <singular>Unpublished (the last two only with draft & publish) return the entry; <singular>Deleted
// returns an EntryReference {documentId locale} (the entry is gone). Arguments: documentId
// (collection types) and locale. Each event runs the subscription's selection against the entry, so
// aliases, populate, fragments and fields work as in a query.
//
// What a caller receives follows the REST rules: it needs find (single types) or findOne access to
// the type; a token that may read drafts gets Created and Updated with the draft values, Published
// with the published ones, and every Unpublished and Deleted; every other caller gets Published,
// Unpublished, and Deleted for documents that had a published version (for types without draft &
// publish also Created and Updated, published values). Events are fanned out inside the process
// that handled the write (single-instance deployments); streams are ended after hgSubMaxAge so a
// revoked token or a switched-off module does not keep one open, clients simply reconnect.

const (
	hgSubMaxAge  = 30 * time.Minute
	hgSubPing    = 20 * time.Second
	hgSubBuffer  = 256
	hgSubMaxConn = 500
)

// hgSubEvents maps the subscription suffixes to webhook events. dp: only for types with draft &
// publish; ref: the field returns an EntryReference instead of the entry.
var hgSubEvents = []struct {
	suffix, event string
	dp, ref       bool
}{
	{"Created", "entry.create", false, false}, {"Updated", "entry.update", false, false}, {"Published", "entry.publish", true, false},
	{"Unpublished", "entry.unpublish", true, true}, {"Deleted", "entry.delete", false, true},
}

func hgSubEventFor(field string, sc *hgSchema) (string, bool) {
	base := hgCamel(sc.SingularName)
	for _, ev := range hgSubEvents {
		if field == base+ev.suffix && (!ev.dp || sc.DraftAndPublish) {
			return ev.event, true
		}
	}
	return "", false
}

// hgSubEvent is one change, as the broker hands it to subscribers.
type hgSubEvent struct {
	Event, UID, Doc, Locale string
	// WasPublished: the entry had a published version (for deletions: before it was deleted).
	WasPublished bool
}

type hgSub struct {
	ch  chan hgSubEvent
	key string // the client it counts against (hgSubClient)
}

type hgBroker struct {
	mu      sync.Mutex
	subs    map[*hgSub]struct{}
	clients map[string]int // open subscriptions per client key
	conns   map[string]int // open WebSocket connections per client address
}

var hgBrokers sync.Map // *Server => *hgBroker

func (s *Server) hgBroker() *hgBroker {
	v, _ := hgBrokers.LoadOrStore(s, &hgBroker{subs: map[*hgSub]struct{}{}, clients: map[string]int{}, conns: map[string]int{}})
	return v.(*hgBroker)
}

// Admission: besides the global cap, one client (an API token, otherwise an address) holds at
// most hgSubPerClient subscriptions (hgSubPerToken for a token), callers without a token share
// hgSubMaxConn - hgSubReserved of the slots (the rest is kept for token callers), and an address
// opens at most hgWSPerAddr WebSocket connections.
const (
	hgSubPerClient = 20
	hgSubPerToken  = 100
	hgSubReserved  = 100
	hgWSPerAddr    = 10
)

// hgSubClient is the key a caller's subscriptions are counted under, and whether it is a token.
func hgSubClient(c *gin.Context, a *hgAuth) (string, bool) {
	if a != nil && a.Kind == "token" && a.Token != nil {
		return fmt.Sprintf("token:%d", a.Token.ID), true
	}
	return "ip:" + clientIP(c), false
}

// subscribe registers a subscription for client key; nil when a cap is reached.
func (b *hgBroker) subscribe(key string, token bool) *hgSub {
	b.mu.Lock()
	defer b.mu.Unlock()
	limit, global := hgSubPerClient, hgSubMaxConn-hgSubReserved
	if token {
		limit, global = hgSubPerToken, hgSubMaxConn
	}
	if len(b.subs) >= global || b.clients[key] >= limit {
		return nil
	}
	sub := &hgSub{ch: make(chan hgSubEvent, hgSubBuffer), key: key}
	b.subs[sub] = struct{}{}
	b.clients[key]++
	return sub
}

// drop removes a subscription (the caller holds mu).
func (b *hgBroker) drop(sub *hgSub) {
	delete(b.subs, sub)
	close(sub.ch)
	if b.clients[sub.key]--; b.clients[sub.key] <= 0 {
		delete(b.clients, sub.key)
	}
}

func (b *hgBroker) unsubscribe(sub *hgSub) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.subs[sub]; ok {
		b.drop(sub)
	}
}

// openConn counts a WebSocket connection of addr; false when it has too many open.
func (b *hgBroker) openConn(addr string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.conns[addr] >= hgWSPerAddr {
		return false
	}
	b.conns[addr]++
	return true
}

func (b *hgBroker) closeConn(addr string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.conns[addr]--; b.conns[addr] <= 0 {
		delete(b.conns, addr)
	}
}

// publish hands an event to every subscriber without ever blocking the writer; a subscriber that
// does not keep up is dropped (its stream ends with an error, the client reconnects).
func (b *hgBroker) publish(ev hgSubEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for sub := range b.subs {
		select {
		case sub.ch <- ev:
		default:
			b.drop(sub)
		}
	}
}

// hgSubscription is a validated and authorized subscription, registered with the broker. The
// caller releases it with close.
type hgSubscription struct {
	s       *Server
	ex      *hgGQLExec
	field   *hgGQLField
	sc      *hgSchema
	event   string
	drafts  bool // the caller may read drafts
	wantDoc string
	wantLoc string
	bs      *hgSub
}

func (sub *hgSubscription) close() { sub.s.hgBroker().unsubscribe(sub.bs) }

// hgGQLErrorDoc is a response document with one error and an extensions code.
func hgGQLErrorDoc(msg, code string) map[string]any {
	return map[string]any{"errors": []any{map[string]any{"message": msg, "extensions": map[string]any{"code": code}}}}
}

// hgGQLSubscription validates a subscription operation, checks access and registers it with the
// broker. On failure it returns the HTTP status and the response document.
func (s *Server) hgGQLSubscription(c *gin.Context, a *hgAuth, st hgSettings, req hgGQLRequest, doc *hgGQLDoc, op *hgGQLOp) (*hgSubscription, int, map[string]any) {
	ex, roots, status, errDoc := s.hgGQLPrepare(c, a, st, req, doc, op)
	if errDoc != nil {
		return nil, status, errDoc
	}
	if len(roots) != 1 {
		return nil, http.StatusBadRequest, hgGQLErrorDoc("A subscription must select exactly one root field.", hgGQLValidationCode)
	}
	f := roots[0]
	if strings.HasPrefix(f.name, "__") {
		return nil, http.StatusBadRequest, hgGQLErrorDoc("A subscription cannot select "+f.name+".", hgGQLValidationCode)
	}
	sub := &hgSubscription{s: s, ex: ex, field: f}
	for _, t := range s.hgReg(c).Types() {
		if ev, ok := hgSubEventFor(f.name, t); ok {
			sub.sc, sub.event = t, ev
			break
		}
	}
	if sub.sc == nil {
		return nil, http.StatusBadRequest, hgGQLErrorDoc(fmt.Sprintf("Cannot subscribe to field %q.", f.name), hgGQLValidationCode)
	}
	need := "findOne"
	if sub.sc.IsSingle() {
		need = "find"
	}
	if a.Kind == "preview" || !a.can(sub.sc.UID, need) {
		return nil, http.StatusForbidden, hgGQLErrorDoc("Forbidden access", "FORBIDDEN")
	}
	sub.drafts = a.drafts(sub.sc.UID)
	sub.wantDoc = hgGQLString(ex.resolve(f.args["documentId"]))
	sub.wantLoc = hgGQLString(ex.resolve(f.args["locale"]))
	if sub.wantLoc != "" {
		if _, err := s.hgEntryLocale(c, sub.sc, sub.wantLoc); err != nil {
			return nil, http.StatusBadRequest, hgGQLErrorDoc(err.Error(), "BAD_USER_INPUT")
		}
	}
	key, token := hgSubClient(c, a)
	if sub.bs = s.hgBroker().subscribe(key, token); sub.bs == nil {
		return nil, http.StatusServiceUnavailable, hgGQLErrorDoc("Too many open subscriptions; try again later.", "SERVICE_UNAVAILABLE")
	}
	return sub, 0, nil
}

// deliver turns a broker event into the subscription's payload ({"data": …} or {"errors": …}); ok
// is false when the event is not for this subscription or this caller.
func (sub *hgSubscription) deliver(ev hgSubEvent) (map[string]any, bool) {
	sc, f := sub.sc, sub.field
	if ev.Event != sub.event || ev.UID != sc.UID || (sub.wantDoc != "" && ev.Doc != sub.wantDoc) || (sub.wantLoc != "" && ev.Locale != sub.wantLoc) {
		return nil, false
	}
	status := "published"
	switch ev.Event {
	case "entry.create", "entry.update":
		if sc.DraftAndPublish {
			if !sub.drafts {
				return nil, false // draft changes are not for this caller: only publications reach them
			}
			status = "draft"
		}
	case "entry.delete":
		if sc.DraftAndPublish && !sub.drafts && !ev.WasPublished {
			return nil, false // the caller never could see this document
		}
		fallthrough
	case "entry.unpublish":
		ref := map[string]any{}
		for _, x := range hgGQLFlat(f.sel) {
			switch x.name {
			case "documentId":
				ref[x.key()] = ev.Doc
			case "locale":
				if ev.Locale != "" {
					ref[x.key()] = ev.Locale
				} else {
					ref[x.key()] = nil // a type without translations
				}
			case "__typename":
				ref[x.key()] = "EntryReference"
			}
		}
		return map[string]any{"data": map[string]any{f.key(): ref}}, true
	}
	one := &hgGQLField{name: hgCamel(sc.SingularName), sel: f.sel, args: map[string]any{
		"documentId": ev.Doc, "locale": ev.Locale, "status": hgGQLEnum(status)}}
	sub.ex.budget = newHGReadBudget() // every event is its own request
	v, err := sub.ex.one(sc, one)
	if err != nil {
		return map[string]any{"data": nil, "errors": []any{map[string]any{"message": hgGQLErrorMessage(err), "path": []string{f.key()}}}}, true
	}
	if v == nil {
		return nil, false // gone again, or not visible to this caller
	}
	return map[string]any{"data": map[string]any{f.key(): v}}, true
}

// hgSubFellBehind is the payload sent before a subscription dropped by the broker ends.
var hgSubFellBehind = map[string]any{"errors": []any{map[string]any{"message": "The stream fell behind and was closed; reconnect."}}}

func hgSSE(c *gin.Context, event string, payload any) bool {
	raw, err := json.Marshal(payload)
	if err != nil {
		return false
	}
	data := string(raw)
	if event == "complete" {
		data = ""
	}
	if _, err := fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event, data); err != nil {
		return false
	}
	c.Writer.Flush()
	return true
}

// hgGQLSubscribe serves one subscription as an event stream.
func (s *Server) hgGQLSubscribe(c *gin.Context, a *hgAuth, st hgSettings, req hgGQLRequest, doc *hgGQLDoc, op *hgGQLOp) {
	sub, status, errDoc := s.hgGQLSubscription(c, a, st, req, doc, op)
	if errDoc != nil {
		c.Header("Cache-Control", "no-store")
		c.JSON(status, errDoc)
		return
	}
	defer sub.close()
	h := c.Writer.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache, no-store, no-transform")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
	if _, err := c.Writer.WriteString(": connected\n\n"); err != nil {
		return
	}
	c.Writer.Flush()

	ping := time.NewTicker(hgSubPing)
	defer ping.Stop()
	end := time.NewTimer(hgSubMaxAge)
	defer end.Stop()
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case <-end.C:
			hgSSE(c, "complete", nil)
			return
		case <-ping.C:
			if !s.hgOn() || !s.hgLoad().GraphQL {
				hgSSE(c, "complete", nil)
				return
			}
			if _, err := c.Writer.WriteString(": ping\n\n"); err != nil {
				return
			}
			c.Writer.Flush()
		case ev, ok := <-sub.bs.ch:
			if !ok {
				hgSSE(c, "next", hgSubFellBehind)
				hgSSE(c, "complete", nil)
				return
			}
			if payload, ok := sub.deliver(ev); ok && !hgSSE(c, "next", payload) {
				return
			}
		}
	}
}
