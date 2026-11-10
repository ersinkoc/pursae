package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// MCP streamable HTTP sessions and server-sent events (mcp.go has the JSON-RPC part).
//
//   - initialize answers with an Mcp-Session-Id header; a client that sends it back gets its session,
//     one that does not is served statelessly as before.
//   - POST answers in application/json, or as a one-event text/event-stream when the client's Accept
//     header lists text/event-stream before application/json (or without it).
//   - GET with Accept: text/event-stream and a session opens the stream for server→client messages
//     (mcpPush); every event has an id, the last 64 are kept, and Last-Event-ID replays what a dropped
//     stream missed. Keep-alive comments flow every 25 seconds.
//   - DELETE ends a session. Sessions belong to the user that created them and expire after an hour
//     without use.
//   - A session has at most one GET stream: a new one replaces the previous. A stream ends when the
//     credential that opened it is revoked or expires (checked with every keep-alive), so a revoked
//     OAuth grant or Application Password cannot keep a connection open.

const (
	mcpSessionHeader   = "Mcp-Session-Id"
	mcpSessionIdle     = time.Hour
	mcpSessionsPerUser = 10
)

// mcpStreamKeepAlive is the interval of keep-alive comments and credential checks on a GET stream
// (a variable for tests).
var mcpStreamKeepAlive = 25 * time.Second

type mcpSession struct {
	id     string
	userID int64
	last   time.Time
	mu     sync.Mutex
	seq    int64        // id of the newest message
	buf    []mcpMessage // the last mcpReplay messages, for Last-Event-ID resumption
	wake   chan struct{}
	done   chan struct{}
	once   sync.Once
	stream chan struct{} // closed to end the current GET stream when a new one opens (guarded by mu)
}

type mcpMessage struct {
	id   int64
	data []byte
}

const mcpReplay = 64

func (m *mcpSession) push(data []byte) {
	m.mu.Lock()
	m.seq++
	m.buf = append(m.buf, mcpMessage{m.seq, data})
	if len(m.buf) > mcpReplay {
		m.buf = m.buf[len(m.buf)-mcpReplay:]
	}
	m.mu.Unlock()
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// after returns the messages newer than id and the newest id.
func (m *mcpSession) after(id int64) ([]mcpMessage, int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []mcpMessage
	for _, x := range m.buf {
		if x.id > id {
			out = append(out, x)
		}
	}
	return out, m.seq
}

func (m *mcpSession) close() { m.once.Do(func() { close(m.done) }) }

// takeStream makes the caller the session's only GET stream: the previous one is told to end.
func (m *mcpSession) takeStream() chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stream != nil {
		close(m.stream)
	}
	m.stream = make(chan struct{})
	return m.stream
}

type mcpSessions struct {
	mu   sync.Mutex
	list map[string]*mcpSession
}

var mcpSess = &mcpSessions{list: map[string]*mcpSession{}}

// create starts a session for the user, ending the user's oldest ones beyond the cap.
func (m *mcpSessions) create(userID int64) *mcpSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	var mine []*mcpSession
	for id, x := range m.list {
		if now.Sub(x.last) > mcpSessionIdle {
			x.close()
			delete(m.list, id)
		} else if x.userID == userID {
			mine = append(mine, x)
		}
	}
	for len(mine) >= mcpSessionsPerUser {
		oldest := 0
		for i, x := range mine {
			if x.last.Before(mine[oldest].last) {
				oldest = i
			}
		}
		mine[oldest].close()
		delete(m.list, mine[oldest].id)
		mine = append(mine[:oldest], mine[oldest+1:]...)
	}
	x := &mcpSession{id: newUUID(), userID: userID, last: now, wake: make(chan struct{}, 1), done: make(chan struct{})}
	m.list[x.id] = x
	return x
}

// get returns the live session of that user (and marks it used).
func (m *mcpSessions) get(id string, userID int64) *mcpSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	x := m.list[id]
	if x == nil || x.userID != userID || time.Since(x.last) > mcpSessionIdle {
		return nil
	}
	x.last = time.Now()
	return x
}

func (m *mcpSessions) end(id string, userID int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	x := m.list[id]
	if x == nil || x.userID != userID {
		return false
	}
	x.close()
	delete(m.list, id)
	return true
}

// mcpPush sends a JSON-RPC notification to every open stream of the user (for example
// notifications/tools/list_changed). Messages for a full or absent stream are dropped.
func mcpPush(userID int64, method string, params any) {
	msg, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	mcpSess.mu.Lock()
	defer mcpSess.mu.Unlock()
	for _, x := range mcpSess.list {
		if x.userID == userID {
			x.push(msg)
		}
	}
}

// wantsSSE reports whether a POST should be answered as an event stream.
func wantsSSE(accept string) bool {
	sse, js := strings.Index(accept, "text/event-stream"), strings.Index(accept, "application/json")
	return sse >= 0 && (js < 0 || sse < js)
}

func writeSSE(c *gin.Context, event string, data []byte) {
	_, _ = c.Writer.WriteString("event: " + event + "\ndata: " + string(data) + "\n\n")
	c.Writer.Flush()
}

func sseHeaders(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache, no-transform")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
}

// mcpStream is the GET stream of a session.
func (s *Server) mcpStream(c *gin.Context, sess *mcpSession) {
	replaced := sess.takeStream()
	sseHeaders(c)
	c.Writer.Flush()
	_, last := sess.after(0) // a new stream only sees new messages …
	if v, err := strconv.ParseInt(c.GetHeader("Last-Event-ID"), 10, 64); err == nil && v >= 0 && v <= last {
		last = v // … a resumed one also the ones it missed
	}
	flush := func() {
		msgs, _ := sess.after(last)
		for _, m := range msgs {
			_, _ = c.Writer.WriteString("id: " + strconv.FormatInt(m.id, 10) + "\nevent: message\ndata: " + string(m.data) + "\n\n")
			last = m.id
		}
		c.Writer.Flush()
	}
	flush()
	tick := time.NewTicker(mcpStreamKeepAlive)
	defer tick.Stop()
	for {
		select {
		case <-sess.wake:
			flush()
		case <-tick.C:
			if !s.restCredentialLive(c) {
				return // the grant or password was revoked or has expired: the client must sign in again
			}
			_, _ = c.Writer.WriteString(": keep-alive\n\n")
			c.Writer.Flush()
		case <-replaced:
			return
		case <-sess.done:
			return
		case <-c.Request.Context().Done():
			return
		}
	}
}

// restCredentialLive reports whether the OAuth access token or Application Password that authenticated
// the request still works. A long-lived response (the MCP GET stream) checks it now and then, because
// its authentication happened only once, when it opened.
func (s *Server) restCredentialLive(c *gin.Context) bool {
	ctx := c.Request.Context()
	if _, ok := c.Get(restOAuthKey); ok {
		h := c.GetHeader("Authorization")
		if len(h) <= 7 {
			return false
		}
		var one int // by hash: a refresh rotates the access token of the same grant row
		return s.store.Pool.QueryRow(ctx, `SELECT 1 FROM oauth_tokens WHERE access_hash = $1 AND access_expires_at > now()`,
			oauthHash(strings.TrimSpace(h[7:]))).Scan(&one) == nil
	}
	if id, ok := c.Get(restAppPasswordKey); ok {
		var one int
		return s.store.Pool.QueryRow(ctx, `SELECT 1 FROM app_passwords WHERE id = $1`, id).Scan(&one) == nil
	}
	return true // cookie sign-in: the session cookie is checked on the next request
}
