package cache

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"strings"
	"time"

	"github.com/bradfitz/gomemcache/memcache"
)

// Connect opens the cache at cacheURL (CacheURL in .env): one or more Memcached servers, or anything
// that speaks its text protocol. Every key gets "<keyPrefix>:" in front of it when keyPrefix is set.
// An empty cacheURL turns the cache off: Connect returns Disabled().
//
// The URL looks like memcached://host:11211, or memcached://host1:11211,host2:11211 for several
// servers (keys are spread over them). Connect looks the host names up but doesn't contact the
// servers: call Ping to check them. A cache that is down slows requests by at most the 500ms
// timeout, since CheckCache falls back to the database.
//
// The client has no passwords and no TLS, so the servers must be on a private network or localhost.
// For TLS, set the memcache.Client's DialContext to a tls.Dialer's.
func Connect(cacheURL, keyPrefix string) (Cache, error) {
	if cacheURL == "" {
		return Disabled(), nil
	}
	servers, err := parseMemcachedURL(cacheURL)
	if err != nil {
		return nil, err
	}
	var list memcache.ServerList
	if err := list.SetServers(servers...); err != nil {
		return nil, fmt.Errorf("CacheURL: %w", err)
	}
	client := memcache.NewFromSelector(&list)
	// a cache is an optimisation: give up quickly and let CheckCache go to the database instead
	client.Timeout = 500 * time.Millisecond
	client.MaxIdleConns = 10

	return WithPrefix(&memcachedCache{client: client}, keyPrefix), nil
}

// parseMemcachedURL returns the host:port of each server in a memcached://host:port[,host:port] URL.
func parseMemcachedURL(cacheURL string) ([]string, error) {
	const format = "CacheURL must look like memcached://host:11211 (or memcached://host1:11211,host2:11211)"
	rest, ok := strings.CutPrefix(cacheURL, "memcached://")
	if !ok {
		return nil, errors.New(format)
	}
	if strings.Contains(rest, "@") {
		// don't repeat the URL: it may hold a password
		return nil, errors.New("CacheURL: the Memcached client has no passwords; keep the servers on a private network")
	}
	rest = strings.TrimSuffix(rest, "/")
	var servers []string
	for _, s := range strings.Split(rest, ",") {
		s = strings.TrimSpace(s)
		host, port, err := net.SplitHostPort(s)
		if err != nil || host == "" || port == "" {
			return nil, errors.New(format)
		}
		servers = append(servers, s)
	}
	return servers, nil
}

type memcachedCache struct {
	client *memcache.Client
}

// maxRelativeTTL is the longest time to live Memcached takes as a number of seconds; anything larger
// is read as a Unix time.
const maxRelativeTTL = 30 * 24 * time.Hour

// expiration turns ttl into Memcached's expiration: whole seconds, rounded up so an entry never
// lives shorter than asked, and a Unix time beyond 30 days.
func expiration(ttl time.Duration, now time.Time) int32 {
	secs := int64((ttl + time.Second - 1) / time.Second)
	if ttl > maxRelativeTTL {
		secs += now.Unix()
	}
	return int32(min(secs, math.MaxInt32))
}

func (c *memcachedCache) Get(ctx context.Context, key string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	item, err := c.client.Get(key)
	if errors.Is(err, memcache.ErrCacheMiss) {
		return nil, ErrMiss
	}
	if err != nil {
		return nil, err
	}
	return item.Value, nil
}

func (c *memcachedCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if ttl <= 0 {
		return errTTL // Memcached would keep it with no expiry at all
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.client.Set(&memcache.Item{Key: key, Value: value, Expiration: expiration(ttl, time.Now())})
}

func (c *memcachedCache) Delete(ctx context.Context, keys ...string) error {
	var errs []error
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := c.client.Delete(key); err != nil && !errors.Is(err, memcache.ErrCacheMiss) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (c *memcachedCache) Ping(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.client.Ping()
}

func (c *memcachedCache) Close() error { return c.client.Close() }
