package middleware

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/owenwexler/gotth-plus/internal/view"
)

// Limit is a token bucket: Rate requests per second with bursts up to Burst.
type Limit struct {
	Rate  rate.Limit
	Burst int
}

// RateLimits configures per-client limits. Static assets are never limited.
type RateLimits struct {
	All       Limit // every request that is not a static asset
	Mutations Limit // POST/PUT/PATCH/DELETE, applied in addition to All
}

const (
	limiterIdleTTL      = 10 * time.Minute
	limiterSweepEvery   = time.Minute
	rateLimitRetryAfter = "1"
)

type clientLimiters struct {
	mu        sync.Mutex
	limit     Limit
	clients   map[string]*clientEntry
	lastSweep time.Time
}

type clientEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

func newClientLimiters(l Limit) *clientLimiters {
	return &clientLimiters{limit: l, clients: map[string]*clientEntry{}, lastSweep: time.Now()}
}

func (c *clientLimiters) allow(key string) bool {
	now := time.Now()

	c.mu.Lock()
	defer c.mu.Unlock()

	// drop idle clients lazily so the map cannot grow without bound
	if now.Sub(c.lastSweep) > limiterSweepEvery {
		for k, e := range c.clients {
			if now.Sub(e.lastSeen) > limiterIdleTTL {
				delete(c.clients, k)
			}
		}
		c.lastSweep = now
	}

	e, ok := c.clients[key]
	if !ok {
		e = &clientEntry{limiter: rate.NewLimiter(c.limit.Rate, c.limit.Burst)}
		c.clients[key] = e
	}
	e.lastSeen = now

	return e.limiter.Allow()
}

// RateLimit limits requests per client IP. It keys on the TCP peer address and deliberately ignores
// X-Forwarded-For, which clients can spoof; behind a reverse proxy, put the proxy's real-IP handling in
// front of this (or rate-limit at the proxy).
func RateLimit(limits RateLimits) func(http.Handler) http.Handler {
	all := newClientLimiters(limits.All)
	mutations := newClientLimiters(limits.Mutations)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/static/") {
				next.ServeHTTP(w, r)
				return
			}

			ip := clientIP(r)
			ok := all.allow(ip)
			if ok && isMutation(r.Method) {
				ok = mutations.allow(ip)
			}

			if !ok {
				tooManyRequests(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func isMutation(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// tooManyRequests answers htmx requests with an out of band toast (HX-Reswap: none leaves the page as is,
// and the layout allows swapping that error status), and everyone else with plain text.
func tooManyRequests(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Retry-After", rateLimitRetryAfter)

	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Reswap", "none")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = view.Toast(view.ToastArgs{
			Kind:   view.ToastError,
			Header: "Slow down",
			Body:   "Too many requests, please try again in a moment",
		}).Render(r.Context(), w)
		return
	}

	http.Error(w, "too many requests", http.StatusTooManyRequests)
}
