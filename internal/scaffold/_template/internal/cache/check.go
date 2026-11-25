package cache

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"
)

// CheckCache returns the value cached under key. On a miss it calls fetch (usually a database
// query), caches what fetch returns for ttl, and returns it.
//
//   - A fetch error is returned as it is (so errors.Is still works) and is never cached.
//   - The cache failing never fails the request: a read error counts as a miss, and a write error
//     only logs a warning. When the cache server is down, every call goes to fetch.
//   - Values are stored as JSON, so T must survive encoding/json: exported fields only, and no
//     channels or functions.
//   - A nil or disabled cache (CacheURL is empty) always calls fetch.
//
// Include everything the value depends on in the key, above all the user or owner for per-user data
// ("tasks:owner:<id>:page:2"), and delete the key (c.Delete) when the data changes, or keep ttl short
// enough that stale data doesn't matter. For example:
//
//	cities, err := cache.CheckCache(r.Context(), c, "cities", 7*24*time.Hour,
//		func(ctx context.Context) ([]model.City, error) { return repo.Cities(ctx) })
func CheckCache[T any](ctx context.Context, c Cache, key string, ttl time.Duration, fetch func(ctx context.Context) (T, error)) (T, error) {
	if IsDisabled(c) {
		return fetch(ctx)
	}

	b, err := c.Get(ctx, key)
	switch {
	case err == nil:
		var v T
		if err := json.Unmarshal(b, &v); err == nil {
			slog.DebugContext(ctx, "cache hit", "key", key)
			return v, nil
		} else {
			// e.g. the type changed since the entry was written; it is overwritten below
			slog.WarnContext(ctx, "cache entry unreadable, fetching instead", "key", key, "err", err)
		}
	case errors.Is(err, ErrMiss):
		slog.DebugContext(ctx, "cache miss", "key", key)
	default:
		slog.WarnContext(ctx, "cache read failed, fetching instead", "key", key, "err", err)
	}

	v, err := fetch(ctx)
	if err != nil {
		// returned before anything is written, so a failed fetch is never cached
		var zero T
		return zero, err
	}

	b, err = json.Marshal(v)
	if err != nil {
		slog.WarnContext(ctx, "cache value not encodable, not cached", "key", key, "err", err)
		return v, nil
	}
	if err := c.Set(ctx, key, b, ttl); err != nil {
		slog.WarnContext(ctx, "cache write failed", "key", key, "err", err)
	}
	return v, nil
}
