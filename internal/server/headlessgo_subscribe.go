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

// GraphQL subscriptions over server-sent events (the "distinct connections" mode of the graphql-sse
// protocol), on the same endpoint as queries: send the subscription as a GET (query in the URL, for
// EventSource) or a POST (JSON body, for fetch) to /wp-json/headlessgo/v1/graphql and read the
// stream. Plain HTTP means nothing new to host (no WebSocket upgrade, proxies and CDNs pass it) and
// the same Authorization header as every other call.
//
// Root fields, per content type: <singular>Created, <singular>Updated, <singular>Published (only
// with draft & publish), arguments documentId (collection types) and locale. Each event runs the
// subscription's selection against the entry, so aliases, populate, fragments and fields work as in
// a query; the stream sends `event: next` with `{"data":{...}}` per change, `: ping` comments as
// keep-alive, and `event: complete` when the server ends the stream.
//
// What a caller receives follows the REST rules: it needs find (single types) or findOne access to
// the type; a token that may read drafts gets Created and Updated with the draft values and
// Published with the published ones; every other caller gets Published (and, for types without
// draft & publish, Created and Updated) with the published values only. Events are fanned out
// inside the process that handled the write (single-instance deployments); streams are ended after
// hgSubMaxAge so a revoked token or a switched-off module does not keep one open, clients simply
// reconnect.

const (
	hgSubMaxAge  = 30 * time.Minute
	hgSubPing    = 20 * time.Second
	hgSubBuffer  = 256
	hgSubMaxConn = 500
)

// hgSubEvents maps the subscription suffixes to webhook events.
var hgSubEvents = []struct{ suffix, event string }{
	{"Created", "entry.create"}, {"Updated", "entry.update"}, {"Published", "entry.publish"},
}

func hgSubEventFor(field string, sc *hgSchema) (string, bool) {
	base := hgCamel(sc.SingularName)
	for _, ev := range hgSubEvents {
		if field == base+ev.suffix && (ev.suffix != "Published" || sc.DraftAndPublish) {
			return ev.event, true
		}
	}
	return "", false
}

// hgSubEvent is one change, as the broker hands it to subscribers.
type hgSubEvent struct {
	Event, UID, Doc, Locale string
}

type hgSub struct{ ch chan hgSubEvent }

type hgBroker struct {
	mu   sync.Mutex
	subs map[*hgSub]struct{}
}

var hgBrokers sync.Map // *Server => *hgBroker

func (s *Server) hgBroker() *hgBroker {
	v, _ := hgBrokers.LoadOrStore(s, &hgBroker{subs: map[*hgSub]struct{}{}})
	return v.(*hgBroker)
}

func (b *hgBroker) subscribe() *hgSub {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.subs) >= hgSubMaxConn {
		return nil
	}
	sub := &hgSub{ch: make(chan hgSubEvent, hgSubBuffer)}
	b.subs[sub] = struct{}{}
	return sub
}

func (b *hgBroker) unsubscribe(sub *hgSub) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.subs[sub]; ok {
		delete(b.subs, sub)
		close(sub.ch)
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
			delete(b.subs, sub)
			close(sub.ch)
		}
	}
}

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
	bad := func(status int, errs ...string) {
		c.Header("Cache-Control", "no-store")
		c.JSON(status, map[string]any{"errors": hgGQLValidationErrors(errs)})
	}
	vars := map[string]any{}
	for k, v := range op.defaults {
		vars[k] = v
	}
	for k, v := range req.Variables {
		vars[k] = v
	}
	ex := &hgGQLExec{s: s, c: c, a: a, st: st, vars: vars}
	sel, err := doc.expand(op.sel, ex, 0, map[string]bool{})
	if err != nil {
		bad(http.StatusBadRequest, err.Error())
		return
	}
	roots := hgGQLRootFlat(sel, "Subscription")
	if errs := ex.validate(roots, "subscription"); len(errs) > 0 {
		bad(http.StatusBadRequest, errs...)
		return
	}
	if len(roots) != 1 {
		bad(http.StatusBadRequest, "A subscription must select exactly one root field.")
		return
	}
	f := roots[0]
	if strings.HasPrefix(f.name, "__") {
		bad(http.StatusBadRequest, "A subscription cannot select "+f.name+".")
		return
	}
	var sc *hgSchema
	var event string
	for _, t := range s.hgReg(c).Types() {
		if ev, ok := hgSubEventFor(f.name, t); ok {
			sc, event = t, ev
			break
		}
	}
	if sc == nil {
		bad(http.StatusBadRequest, fmt.Sprintf("Cannot subscribe to field %q.", f.name))
		return
	}
	need := "findOne"
	if sc.IsSingle() {
		need = "find"
	}
	if a.Kind == "preview" || !a.can(sc.UID, need) {
		hgForbidden(c, a)
		return
	}
	wantDoc := hgGQLString(ex.resolve(f.args["documentId"]))
	wantLoc := hgGQLString(ex.resolve(f.args["locale"]))
	if wantLoc != "" {
		if _, err := s.hgEntryLocale(c, sc, wantLoc); err != nil {
			bad(http.StatusBadRequest, err.Error())
			return
		}
	}
	status := "published"
	if event != "entry.publish" && sc.DraftAndPublish {
		if !a.drafts(sc.UID) {
			// Draft changes are not for this caller: only publications reach them.
			status = ""
		} else {
			status = "draft"
		}
	}

	sub := s.hgBroker().subscribe()
	if sub == nil {
		bad(http.StatusServiceUnavailable, "Too many open subscriptions; try again later.")
		return
	}
	defer s.hgBroker().unsubscribe(sub)
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
		case ev, ok := <-sub.ch:
			if !ok {
				hgSSE(c, "next", map[string]any{"errors": []any{map[string]any{"message": "The stream fell behind and was closed; reconnect."}}})
				hgSSE(c, "complete", nil)
				return
			}
			if ev.Event != event || ev.UID != sc.UID || status == "" || (wantDoc != "" && ev.Doc != wantDoc) || (wantLoc != "" && ev.Locale != wantLoc) {
				continue
			}
			one := &hgGQLField{name: hgCamel(sc.SingularName), sel: f.sel, args: map[string]any{
				"documentId": ev.Doc, "locale": ev.Locale, "status": hgGQLEnum(status)}}
			v, err := ex.one(sc, one)
			if err != nil {
				if !hgSSE(c, "next", map[string]any{"data": nil, "errors": []any{map[string]any{"message": err.Error(), "path": []string{f.key()}}}}) {
					return
				}
				continue
			}
			if v == nil {
				continue // gone again, or not visible to this caller
			}
			if !hgSSE(c, "next", map[string]any{"data": map[string]any{f.key(): v}}) {
				return
			}
		}
	}
}
