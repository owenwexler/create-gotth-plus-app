package cache

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

// newRedis starts an in-memory Redis (miniredis) for one test, and connects to it.
func newRedis(t *testing.T, prefix string) (Cache, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	c, err := Connect("redis://"+mr.Addr(), prefix)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c, mr
}

func TestRedisContract(t *testing.T) {
	c, mr := newRedis(t, "")
	testContract(t, c, mr.FastForward)
}

func TestRedisPrefix(t *testing.T) {
	c, mr := newRedis(t, "myapp")
	if err := c.Set(context.Background(), "cities", []byte("x"), time.Minute); err != nil {
		t.Fatal(err)
	}
	if !mr.Exists("myapp:cities") {
		t.Errorf("keys on the server: %v, want myapp:cities", mr.Keys())
	}
	if ttl := mr.TTL("myapp:cities"); ttl != time.Minute {
		t.Errorf("server-side TTL = %v, want 1m", ttl)
	}
}

func TestConnectWithoutURL(t *testing.T) {
	c, err := Connect("", "ignored")
	if err != nil || !IsDisabled(c) {
		t.Errorf("Connect(\"\") = %v, %v; want the disabled cache", c, err)
	}
}

func TestConnectRejectsBadURLs(t *testing.T) {
	for _, u := range []string{
		"http://localhost:6379",
		"localhost:6379",
		"redis://localhost:6379/not-a-db",
		"redis://user:s3cret@[::1",
	} {
		_, err := Connect(u, "")
		if err == nil {
			t.Errorf("Connect(%q) succeeded, want an error", u)
			continue
		}
		if strings.Contains(err.Error(), "s3cret") {
			t.Errorf("Connect(%q) error leaks the password: %v", u, err)
		}
	}
}

func TestRedisDownFallsBackToFetch(t *testing.T) {
	c, mr := newRedis(t, "")
	mr.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	got, err := CheckCache(ctx, c, "k", time.Minute, func(context.Context) (string, error) { return "from the database", nil })
	if err != nil || got != "from the database" {
		t.Errorf("CheckCache with the cache down = %q, %v; want the fetched value", got, err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("a cache that is down took %v; the timeouts should keep it short", elapsed)
	}
	if err := c.Ping(ctx); err == nil {
		t.Error("Ping succeeded with the server down")
	}
}
