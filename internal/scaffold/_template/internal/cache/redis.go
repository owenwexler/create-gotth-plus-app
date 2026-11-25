package cache

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/redis/go-redis/v9"
)

// Connect opens the cache at cacheURL (CacheURL in .env): a Redis server, or anything that speaks
// its protocol (Valkey, KeyDB, Dragonfly, a managed Redis). Every key gets "<keyPrefix>:" in front
// of it when keyPrefix is set. An empty cacheURL turns the cache off: Connect returns Disabled().
//
// The URL looks like redis://[[user]:password@]host:6379[/db] (rediss:// for TLS). Connect doesn't
// contact the server: call Ping to check it. A cache that is down slows requests by at most the
// timeouts below, since CheckCache falls back to the database; override them in the URL with
// dial_timeout, read_timeout and write_timeout (e.g. ?read_timeout=200ms).
func Connect(cacheURL, keyPrefix string) (Cache, error) {
	if cacheURL == "" {
		return Disabled(), nil
	}
	// url.Parse errors repeat the URL, password included, so they are not passed on
	if _, err := url.Parse(cacheURL); err != nil {
		return nil, errors.New("CacheURL is not a valid URL")
	}
	opts, err := redis.ParseURL(cacheURL)
	if err != nil {
		return nil, fmt.Errorf("CacheURL: %w", err)
	}
	// a cache is an optimisation: give up quickly and let CheckCache go to the database instead
	if opts.DialTimeout == 0 {
		opts.DialTimeout = time.Second
	}
	if opts.DialerRetries == 0 {
		opts.DialerRetries = 1
	}
	if opts.ReadTimeout == 0 {
		opts.ReadTimeout = 500 * time.Millisecond
	}
	if opts.WriteTimeout == 0 {
		opts.WriteTimeout = 500 * time.Millisecond
	}
	// stop waiting when the request's context is cancelled or times out
	opts.ContextTimeoutEnabled = true

	return WithPrefix(&redisCache{client: redis.NewClient(opts)}, keyPrefix), nil
}

type redisCache struct {
	client *redis.Client
}

func (c *redisCache) Get(ctx context.Context, key string) ([]byte, error) {
	b, err := c.client.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, ErrMiss
	}
	return b, err
}

func (c *redisCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if ttl <= 0 {
		return errTTL // go-redis would store it with no expiry at all
	}
	return c.client.Set(ctx, key, value, ttl).Err()
}

func (c *redisCache) Delete(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	return c.client.Del(ctx, keys...).Err()
}

func (c *redisCache) Ping(ctx context.Context) error { return c.client.Ping(ctx).Err() }

func (c *redisCache) Close() error { return c.client.Close() }
