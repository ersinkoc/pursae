package server

import (
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// gooThrottle counts failed attempts per key inside a sliding window (coupon guessing, order tracking).
type gooThrottle struct {
	mu    sync.Mutex
	max   int
	win   time.Duration
	fails map[string][]time.Time
}

func newGooThrottle(max int, win time.Duration) *gooThrottle {
	return &gooThrottle{max: max, win: win, fails: map[string][]time.Time{}}
}

func (t *gooThrottle) prune(key string, now time.Time) []time.Time {
	list := t.fails[key]
	i := 0
	for i < len(list) && now.Sub(list[i]) > t.win {
		i++
	}
	list = list[i:]
	if len(list) == 0 {
		delete(t.fails, key)
	} else {
		t.fails[key] = list
	}
	return list
}

// Blocked reports whether the key already used up its failed attempts.
func (t *gooThrottle) Blocked(key string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.prune(key, time.Now())) >= t.max
}

// Fail records a failed attempt.
func (t *gooThrottle) Fail(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	t.prune(key, now)
	t.fails[key] = append(t.fails[key], now)
	if len(t.fails) > 10000 { // bound memory: drop idle keys
		for k := range t.fails {
			t.prune(k, now)
		}
	}
}

var (
	gooCouponThrottle   = newGooThrottle(10, 10*time.Minute)
	gooTrackingThrottle = newGooThrottle(5, 10*time.Minute)
	gooOrderGetThrottle = newGooThrottle(20, 10*time.Minute)
)

const gooThrottledMsg = "Too many attempts. Please try again in a few minutes."

// gooApplyCouponLimited is cart.ApplyCoupon with a per-IP limit on failed attempts, so coupon codes
// cannot be guessed (classic form, AJAX and Store API all come through here).
func (s *Server) gooApplyCouponLimited(c *gin.Context, cart *gooCart, code string) error {
	ip := clientIP(c)
	if gooCouponThrottle.Blocked(ip) {
		return errGooThrottled
	}
	err := cart.ApplyCoupon(c.Request.Context(), code)
	if err != nil {
		gooCouponThrottle.Fail(ip)
	}
	return err
}

type gooThrottledError struct{}

func (gooThrottledError) Error() string { return gooThrottledMsg }

var errGooThrottled error = gooThrottledError{}

// gooDefaultCountry is the default customer country: the visitor's own (CDN geo header) for the
// "Geolocate" options, the shop base country otherwise.
func (s *Server) gooDefaultCountry(c *gin.Context) string {
	if c != nil && c.Request != nil {
		if def := s.gooOptString("default_customer_address"); def == "geolocation" || def == "geolocation_ajax" {
			if cc := gooikeCountry(c); cc != "" {
				return cc
			}
		}
	}
	return s.gooBaseCountry()
}
