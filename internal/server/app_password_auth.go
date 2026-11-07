package server

import (
	"crypto/sha256"
	"encoding/binary"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/auth"
	"wpgo/internal/store"
)

// Basic-auth hardening for Application Passwords. Every Basic-auth REST request verifies a password
// with argon2id (64 MiB). Without care a client could make the server hash every stored password of a
// user on every request, from any number of requests at once. Here:
//
//   - the core login throttle (auth.Throttled / RecordAttempt) applies to Basic auth, unless Pursae
//     Security handles lockouts itself;
//   - auth bounds how many argon2 computations run at once (auth.argonSem);
//   - the most recently used password is tried first, and a successful match is remembered for a few
//     minutes, keyed by a SHA-256 of user and password: a working client then costs no argon2 at all
//     until the entry expires. The entry only counts while the stored row still has the same hash, so a
//     revoked password stops working at once.

// appPassMatch is the stored Application Password a request's password matched.
type appPassMatch struct {
	id     int64
	hash   string
	scopes []string
}

const (
	appPassCacheTTL = 5 * time.Minute
	appPassCacheMax = 10000 // entries; the cache is emptied when it grows past this
)

type appPassHit struct {
	id   int64
	hash string
	exp  time.Time
}

var appPassCache = struct {
	sync.Mutex
	m map[[32]byte]appPassHit
}{m: map[[32]byte]appPassHit{}}

func appPassCacheKey(userID int64, pass string) [32]byte {
	h := sha256.New()
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(userID))
	h.Write(b[:])
	h.Write([]byte(pass))
	var k [32]byte
	copy(k[:], h.Sum(nil))
	return k
}

func appPassCached(k [32]byte) (appPassHit, bool) {
	appPassCache.Lock()
	defer appPassCache.Unlock()
	hit, ok := appPassCache.m[k]
	if ok && time.Now().After(hit.exp) {
		delete(appPassCache.m, k)
		return appPassHit{}, false
	}
	return hit, ok
}

func appPassRemember(k [32]byte, id int64, hash string) {
	appPassCache.Lock()
	defer appPassCache.Unlock()
	if len(appPassCache.m) >= appPassCacheMax {
		appPassCache.m = map[[32]byte]appPassHit{}
	}
	appPassCache.m[k] = appPassHit{id: id, hash: hash, exp: time.Now().Add(appPassCacheTTL)}
}

// matchAppPassword finds the user's Application Password that pass matches.
func (s *Server) matchAppPassword(c *gin.Context, userID int64, pass string) (appPassMatch, bool) {
	rows, err := s.store.Pool.Query(c, `SELECT id, password_hash, scopes FROM app_passwords WHERE user_id = $1
		ORDER BY last_used_at DESC NULLS LAST, id DESC`, userID)
	if err != nil {
		return appPassMatch{}, false
	}
	var list []appPassMatch
	for rows.Next() {
		var m appPassMatch
		if rows.Scan(&m.id, &m.hash, &m.scopes) == nil {
			list = append(list, m)
		}
	}
	rows.Close() // no connection is held while hashing
	key := appPassCacheKey(userID, pass)
	if hit, ok := appPassCached(key); ok {
		for _, m := range list {
			if m.id == hit.id && m.hash == hit.hash {
				return m, true
			}
		}
	}
	ctx := c.Request.Context()
	if len(list) == 0 {
		auth.CheckDummyPassword(ctx, pass) // a user without passwords answers as slowly as one with
		return appPassMatch{}, false
	}
	for _, m := range list {
		ok, err := auth.CheckPasswordCtx(ctx, pass, m.hash)
		if ctx.Err() != nil {
			return appPassMatch{}, false // the client went away while waiting for an argon2 slot
		}
		if err == nil && ok {
			appPassRemember(key, m.id, m.hash)
			return m, true
		}
	}
	return appPassMatch{}, false
}

// restLoginThrottled is the core login throttle for Basic auth: too many recent failures from the
// address refuse the request before any password is hashed. Pursae Security, when active, replaces it
// with its own lockouts (adminsecRESTLocked).
func (s *Server) restLoginThrottled(c *gin.Context) bool {
	if s.adminsecOn() {
		return false
	}
	throttled, _ := s.auth.Throttled(c, clientIP(c))
	return throttled
}

// restLoginFailed records a failed Basic-auth sign-in for the throttle (or Pursae Security's log).
func (s *Server) restLoginFailed(c *gin.Context, login string, u *store.User, reason string) {
	if s.adminsecOn() {
		s.adminsecLoginFailed(c, login, u, reason)
		return
	}
	s.auth.RecordAttempt(c, clientIP(c), login, false)
}
