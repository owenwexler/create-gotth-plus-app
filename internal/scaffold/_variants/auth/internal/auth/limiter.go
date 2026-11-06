package auth

import (
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// KeyLimiter rate-limits by a key such as an email address. The per-IP limits in the middleware
// don't stop an attacker who spreads guesses for one account over many IPs; this does.
// Limits are in memory, per process (see SECURITY.md, "Rate limiting behind a proxy").
type KeyLimiter struct {
	mu        sync.Mutex
	every     time.Duration
	burst     int
	keys      map[string]*keyEntry
	lastSweep time.Time
}

type keyEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// NewKeyLimiter allows burst attempts per key, refilling one every interval.
func NewKeyLimiter(every time.Duration, burst int) *KeyLimiter {
	return &KeyLimiter{every: every, burst: burst, keys: map[string]*keyEntry{}, lastSweep: time.Now()}
}

// Allow reports whether one more attempt for key is allowed now. Keys are compared case-insensitively.
func (l *KeyLimiter) Allow(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	now := time.Now()

	l.mu.Lock()
	defer l.mu.Unlock()

	// forget keys that are full again, so the map can't grow without bound
	idle := l.every * time.Duration(l.burst)
	if now.Sub(l.lastSweep) > time.Minute {
		for k, e := range l.keys {
			if now.Sub(e.lastSeen) > idle {
				delete(l.keys, k)
			}
		}
		l.lastSweep = now
	}

	e, ok := l.keys[key]
	if !ok {
		e = &keyEntry{limiter: rate.NewLimiter(rate.Every(l.every), l.burst)}
		l.keys[key] = e
	}
	e.lastSeen = now
	return e.limiter.Allow()
}
