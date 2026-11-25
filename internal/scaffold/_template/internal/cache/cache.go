// Package cache is the app's optional server-side cache: a small Cache interface, the client for the
// cache server (Connect), and CheckCache, which reads through the cache to the database.
//
// The cache is off when CacheURL is empty: Connect returns Disabled(), and CheckCache then always
// calls its fetch function. Code that uses the cache works the same either way, only slower.
package cache

import (
	"context"
	"errors"
	"time"
)

// ErrMiss is returned by Get when the key is not in the cache (never stored, expired, or evicted).
var ErrMiss = errors.New("cache: miss")

// Cache stores byte values under string keys, each with a time to live. Every method is safe for
// concurrent use.
type Cache interface {
	// Get returns the value stored under key, or ErrMiss.
	Get(ctx context.Context, key string) ([]byte, error)
	// Set stores value under key for ttl, which must be positive: nothing is cached forever.
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	// Delete removes the keys. Keys that are not in the cache are not an error.
	Delete(ctx context.Context, keys ...string) error
	// Ping checks that the cache server answers.
	Ping(ctx context.Context) error
	// Close releases the connections. The cache can't be used afterwards.
	Close() error
}

// errTTL is returned by Set for a ttl that is not positive.
var errTTL = errors.New("cache: ttl must be positive")

// Disabled returns a Cache that stores nothing: Get always misses, and every other method does
// nothing. Connect returns it when CacheURL is empty, and tests can pass it to app.NewHandler.
func Disabled() Cache { return disabled{} }

type disabled struct{}

func (disabled) Get(context.Context, string) ([]byte, error)              { return nil, ErrMiss }
func (disabled) Set(context.Context, string, []byte, time.Duration) error { return nil }
func (disabled) Delete(context.Context, ...string) error                  { return nil }
func (disabled) Ping(context.Context) error                               { return nil }
func (disabled) Close() error                                             { return nil }

// IsDisabled reports whether c is the Cache that stores nothing (CacheURL is empty).
func IsDisabled(c Cache) bool {
	_, ok := c.(disabled)
	return c == nil || ok
}

// WithPrefix returns a Cache that puts "<prefix>:" in front of every key, so several apps or
// environments can share one cache server without seeing each other's entries. An empty prefix
// returns c unchanged.
func WithPrefix(c Cache, prefix string) Cache {
	if prefix == "" {
		return c
	}
	return prefixed{c: c, prefix: prefix + ":"}
}

type prefixed struct {
	c      Cache
	prefix string
}

func (p prefixed) Get(ctx context.Context, key string) ([]byte, error) {
	return p.c.Get(ctx, p.prefix+key)
}

func (p prefixed) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return p.c.Set(ctx, p.prefix+key, value, ttl)
}

func (p prefixed) Delete(ctx context.Context, keys ...string) error {
	full := make([]string, len(keys))
	for i, k := range keys {
		full[i] = p.prefix + k
	}
	return p.c.Delete(ctx, full...)
}

func (p prefixed) Ping(ctx context.Context) error { return p.c.Ping(ctx) }
func (p prefixed) Close() error                   { return p.c.Close() }
