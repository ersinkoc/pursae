package server

import (
	"encoding/json"
	"net/http"
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
//     (mcpPush); keep-alive comments flow every 25 seconds. Resuming with Last-Event-ID is not supported.
//   - DELETE ends a session. Sessions belong to the user that created them and expire after an hour
//     without use.

const (
	mcpSessionHeader   = "Mcp-Session-Id"
	mcpSessionIdle     = time.Hour
	mcpSessionsPerUser = 10
	mcpStreamKeepAlive = 25 * time.Second
)

type mcpSession struct {
	id     string
	userID int64
	last   time.Time
	out    chan []byte // messages for the GET stream
	done   chan struct{}
	once   sync.Once
}

func (m *mcpSession) close() { m.once.Do(func() { close(m.done) }) }

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
	x := &mcpSession{id: newUUID(), userID: userID, last: now, out: make(chan []byte, 32), done: make(chan struct{})}
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
			select {
			case x.out <- msg:
			default:
			}
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
	sseHeaders(c)
	c.Writer.Flush()
	tick := time.NewTicker(mcpStreamKeepAlive)
	defer tick.Stop()
	for {
		select {
		case msg := <-sess.out:
			writeSSE(c, "message", msg)
		case <-tick.C:
			_, _ = c.Writer.WriteString(": keep-alive\n\n")
			c.Writer.Flush()
		case <-sess.done:
			return
		case <-c.Request.Context().Done():
			return
		}
	}
}
