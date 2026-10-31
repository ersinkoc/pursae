package server

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/net/websocket"
)

// GraphQL over WebSocket with the graphql-transport-ws protocol (the one of the "graphql-ws"
// library, used by Apollo Client, urql and GraphiQL), on the GraphQL endpoint itself:
// ws(s)://site/wp-json/headlessgo/v1/graphql with the sub-protocol "graphql-transport-ws".
//
//	client: {"type":"connection_init","payload":{"Authorization":"Bearer <token>"}}
//	server: {"type":"connection_ack"}
//	client: {"id":"1","type":"subscribe","payload":{"query":"subscription { articlePublished { title } }"}}
//	server: {"id":"1","type":"next","payload":{"data":{"articlePublished":{"title":"…"}}}}  (per event)
//	client: {"id":"1","type":"complete"}                                                   (stop it)
//
// Authentication is the REST API's: an API token in the connection_init payload ("Authorization":
// "Bearer …", or "token"), since browsers cannot set headers on a WebSocket, or the Authorization
// header of the upgrade request; without one the public permissions apply. One connection carries
// any number of subscriptions (at most hgWSMaxOps at a time); queries and mutations work too
// (one next, then complete). Validation and access errors answer the operation with an error
// message; protocol violations close the connection with the protocol's codes (4400 bad message,
// 4401 subscribe before the ack, 4403 bad token, 4408 no connection_init in time, 4409 duplicate
// id, 4429 a second connection_init). The server pings every hgSubPing and closes the connection
// with 1001 after hgSubMaxAge or when the module or GraphQL is switched off; clients reconnect.
// A browser's Origin must be the site itself or one of the CORS origins.

const (
	hgWSProtocol   = "graphql-transport-ws"
	hgWSMaxOps     = 50
	hgWSMaxMessage = 1 << 20
)

// hgWSInitTimeout is how long a new connection may take to send connection_init (a variable for
// the tests).
var hgWSInitTimeout = 10 * time.Second

// hgIsWebSocket tells whether a request asks to upgrade to a WebSocket.
func hgIsWebSocket(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket") && strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade")
}

type hgWSMsg struct {
	ID      string          `json:"id,omitempty"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// hgWSConn is one graphql-transport-ws connection.
type hgWSConn struct {
	s      *Server
	c      *gin.Context
	st     hgSettings
	ws     *websocket.Conn
	wmu    sync.Mutex // guards writes and closed
	closed bool
	a      *hgAuth
	mu     sync.Mutex
	ops    map[string]chan struct{} // running operations by id; closed to stop one
	done   chan struct{}
	once   sync.Once
	// wg counts the goroutines that use c: the handler waits for them, gin reuses c afterwards.
	wg sync.WaitGroup
}

// hgGQLWebSocket upgrades the request and serves the connection until it closes.
func (s *Server) hgGQLWebSocket(c *gin.Context, a *hgAuth, st hgSettings) {
	if o := c.GetHeader("Origin"); o != "" {
		u, err := url.Parse(o)
		if err != nil || (!strings.EqualFold(u.Host, c.Request.Host) && !hgOriginAllowed(st.CORSOrigins, o)) {
			c.AbortWithStatusJSON(http.StatusForbidden, hgGQLErrorDoc("This origin may not open a WebSocket here (see the CORS origins in the settings).", "FORBIDDEN"))
			return
		}
	}
	offered := false
	for _, p := range strings.Split(c.GetHeader("Sec-WebSocket-Protocol"), ",") {
		if strings.TrimSpace(p) == hgWSProtocol {
			offered = true
		}
	}
	if !offered {
		c.AbortWithStatusJSON(http.StatusBadRequest, hgGQLErrorDoc("Use the WebSocket sub-protocol "+hgWSProtocol+".", "BAD_REQUEST"))
		return
	}
	srv := websocket.Server{
		Handshake: func(cfg *websocket.Config, r *http.Request) error {
			cfg.Protocol = []string{hgWSProtocol}
			return nil
		},
		Handler: func(ws *websocket.Conn) {
			ws.MaxPayloadBytes = hgWSMaxMessage
			wc := &hgWSConn{s: s, c: c, st: st, ws: ws, a: a, ops: map[string]chan struct{}{}, done: make(chan struct{})}
			wc.serve()
		},
	}
	srv.ServeHTTP(c.Writer, c.Request)
}

// send writes one message; it reports false when the connection is gone.
func (wc *hgWSConn) send(m hgWSMsg) bool {
	raw, err := json.Marshal(m)
	if err != nil {
		return false
	}
	wc.wmu.Lock()
	defer wc.wmu.Unlock()
	if wc.closed {
		return false
	}
	_ = wc.ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return websocket.Message.Send(wc.ws, string(raw)) == nil
}

func (wc *hgWSConn) sendPayload(id, typ string, payload any) bool {
	raw, err := json.Marshal(payload)
	if err != nil {
		return false
	}
	return wc.send(hgWSMsg{ID: id, Type: typ, Payload: raw})
}

// closeWith sends a close frame with a code and reason and ends the connection.
func (wc *hgWSConn) closeWith(code uint16, reason string) {
	wc.once.Do(func() {
		close(wc.done)
		buf := make([]byte, 2, 2+len(reason))
		binary.BigEndian.PutUint16(buf, code)
		buf = append(buf, reason...)
		wc.wmu.Lock()
		wc.closed = true
		_ = wc.ws.SetWriteDeadline(time.Now().Add(2 * time.Second))
		wc.ws.PayloadType = websocket.CloseFrame // x/net/websocket has no API for a close code
		_, _ = wc.ws.Write(buf)
		wc.wmu.Unlock()
		_ = wc.ws.SetReadDeadline(time.Now()) // unblocks the reader
	})
}

func (wc *hgWSConn) serve() {
	defer wc.wg.Wait()
	defer wc.closeWith(1000, "")
	defer wc.stopAll()
	wc.wg.Add(1)
	go func() {
		defer wc.wg.Done()
		wc.keepAlive()
	}()
	acked := false
	_ = wc.ws.SetReadDeadline(time.Now().Add(hgWSInitTimeout))
	for {
		var raw []byte
		if err := websocket.Message.Receive(wc.ws, &raw); err != nil {
			var ne net.Error
			if !acked && errors.As(err, &ne) && ne.Timeout() {
				wc.closeWith(4408, "Connection initialisation timeout")
			}
			return
		}
		var m hgWSMsg
		if json.Unmarshal(raw, &m) != nil || m.Type == "" {
			wc.closeWith(4400, "Invalid message received")
			return
		}
		switch m.Type {
		case "connection_init":
			if acked {
				wc.closeWith(4429, "Too many initialisation requests")
				return
			}
			if !wc.init(m.Payload) {
				wc.closeWith(4403, "Forbidden")
				return
			}
			acked = true
			_ = wc.ws.SetReadDeadline(time.Time{})
			if !wc.send(hgWSMsg{Type: "connection_ack"}) {
				return
			}
		case "ping":
			if !wc.send(hgWSMsg{Type: "pong", Payload: m.Payload}) {
				return
			}
		case "pong":
		case "subscribe":
			if !acked {
				wc.closeWith(4401, "Unauthorized")
				return
			}
			if m.ID == "" {
				wc.closeWith(4400, "Invalid message received")
				return
			}
			if !wc.subscribe(m.ID, m.Payload) {
				return
			}
		case "complete":
			wc.stop(m.ID)
		default:
			wc.closeWith(4400, "Invalid message received")
			return
		}
	}
}

// init reads the connection_init payload: an API token replaces the upgrade request's credentials.
func (wc *hgWSConn) init(payload json.RawMessage) bool {
	var p map[string]any
	_ = json.Unmarshal(payload, &p)
	tok := ""
	for _, k := range []string{"Authorization", "authorization"} {
		if h, ok := p[k].(string); ok && h != "" {
			scheme, val, _ := strings.Cut(h, " ")
			if !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(val) == "" {
				return false
			}
			tok = strings.TrimSpace(val)
		}
	}
	if t, ok := p["token"].(string); ok && tok == "" {
		tok = strings.TrimSpace(t)
	}
	if tok == "" {
		return true
	}
	if strings.HasPrefix(tok, hgPreviewPrefix) {
		return false // preview tokens read one entry; they cannot subscribe
	}
	t := wc.s.hgTokenByValue(wc.c, tok)
	if t == nil {
		return false
	}
	wc.a = &hgAuth{Kind: "token", Token: t, Public: wc.st.Public}
	return true
}

// subscribe starts an operation; it reports false when the connection must close.
func (wc *hgWSConn) subscribe(id string, payload json.RawMessage) bool {
	var req hgGQLRequest
	if json.Unmarshal(payload, &req) != nil {
		wc.closeWith(4400, "Invalid message received")
		return false
	}
	wc.mu.Lock()
	_, dup := wc.ops[id]
	n := len(wc.ops)
	wc.mu.Unlock()
	if dup {
		wc.closeWith(4409, "Subscriber for "+id+" already exists")
		return false
	}
	fail := func(doc map[string]any) bool {
		return wc.sendPayload(id, "error", doc["errors"])
	}
	if n >= hgWSMaxOps {
		return fail(hgGQLErrorDoc("Too many operations on this connection.", "SERVICE_UNAVAILABLE"))
	}
	doc, err := hgGQLParse(req.Query)
	if err != nil {
		return fail(hgGQLRequestError(err))
	}
	op, err := doc.operation(req.OperationName)
	if err != nil {
		return fail(hgGQLRequestError(err))
	}
	if op.kind != "subscription" {
		ex, sel, _, errDoc := wc.s.hgGQLPrepare(wc.c, wc.a, wc.st, req, doc, op)
		if errDoc != nil {
			return fail(errDoc)
		}
		return wc.sendPayload(id, "next", ex.run(op, sel)) && wc.send(hgWSMsg{ID: id, Type: "complete"})
	}
	sub, _, errDoc := wc.s.hgGQLSubscription(wc.c, wc.a, wc.st, req, doc, op)
	if errDoc != nil {
		return fail(errDoc)
	}
	stop := make(chan struct{})
	wc.mu.Lock()
	wc.ops[id] = stop
	wc.mu.Unlock()
	wc.wg.Add(1)
	go func() {
		defer wc.wg.Done()
		defer sub.close()
		for {
			select {
			case <-stop:
				return // completed by the client: no complete message is sent back
			case <-wc.done:
				return
			case ev, ok := <-sub.bs.ch:
				if !ok {
					wc.sendPayload(id, "next", hgSubFellBehind)
					if wc.finish(id) {
						wc.send(hgWSMsg{ID: id, Type: "complete"})
					}
					return
				}
				if p, ok := sub.deliver(ev); ok && !wc.sendPayload(id, "next", p) {
					return
				}
			}
		}
	}()
	return true
}

// finish forgets a running operation; it reports whether it was still running.
func (wc *hgWSConn) finish(id string) bool {
	wc.mu.Lock()
	defer wc.mu.Unlock()
	_, ok := wc.ops[id]
	delete(wc.ops, id)
	return ok
}

func (wc *hgWSConn) stop(id string) {
	wc.mu.Lock()
	defer wc.mu.Unlock()
	if ch, ok := wc.ops[id]; ok {
		close(ch)
		delete(wc.ops, id)
	}
}

func (wc *hgWSConn) stopAll() {
	wc.mu.Lock()
	defer wc.mu.Unlock()
	for id, ch := range wc.ops {
		close(ch)
		delete(wc.ops, id)
	}
}

// keepAlive pings the client and ends the connection when it is too old or GraphQL is switched off.
func (wc *hgWSConn) keepAlive() {
	ping := time.NewTicker(hgSubPing)
	defer ping.Stop()
	end := time.NewTimer(hgSubMaxAge)
	defer end.Stop()
	for {
		select {
		case <-wc.done:
			return
		case <-end.C:
			wc.closeWith(1001, "Session expired, reconnect")
			return
		case <-ping.C:
			if !wc.s.hgOn() || !wc.s.hgLoad().GraphQL {
				wc.closeWith(1001, "GraphQL is switched off")
				return
			}
			if !wc.send(hgWSMsg{Type: "ping"}) {
				return
			}
		}
	}
}
