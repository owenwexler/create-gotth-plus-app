package cache

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeCache is an in-memory Cache that records calls and can be told to fail.
type fakeCache struct {
	mu      sync.Mutex
	data    map[string][]byte
	ttls    map[string]time.Duration
	getErr  error
	setErr  error
	gets    int
	sets    int
	deleted []string
}

func newFakeCache() *fakeCache {
	return &fakeCache{data: map[string][]byte{}, ttls: map[string]time.Duration{}}
}

func (f *fakeCache) Get(_ context.Context, key string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gets++
	if f.getErr != nil {
		return nil, f.getErr
	}
	b, ok := f.data[key]
	if !ok {
		return nil, ErrMiss
	}
	return b, nil
}

func (f *fakeCache) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sets++
	if f.setErr != nil {
		return f.setErr
	}
	f.data[key], f.ttls[key] = value, ttl
	return nil
}

func (f *fakeCache) Delete(_ context.Context, keys ...string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, k := range keys {
		delete(f.data, k)
		f.deleted = append(f.deleted, k)
	}
	return nil
}

func (f *fakeCache) Ping(context.Context) error { return nil }
func (f *fakeCache) Close() error               { return nil }

type city struct {
	Name       string
	Population int
	Tags       []string
}

// counted returns a fetch function that returns v (or err) and counts its calls.
func counted[T any](v T, err error) (func(context.Context) (T, error), *int) {
	calls := 0
	return func(context.Context) (T, error) {
		calls++
		return v, err
	}, &calls
}

func TestCheckCacheMissThenHit(t *testing.T) {
	ctx := context.Background()
	c := newFakeCache()
	want := []city{{Name: "Lisbon", Population: 545_000, Tags: []string{"coast"}}}
	fetch, calls := counted(want, nil)

	for i := range 3 {
		got, err := CheckCache(ctx, c, "cities", time.Hour, fetch)
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if len(got) != 1 || got[0].Name != "Lisbon" || got[0].Population != 545_000 || got[0].Tags[0] != "coast" {
			t.Fatalf("call %d: got %+v", i, got)
		}
	}
	if *calls != 1 {
		t.Errorf("fetch ran %d times, want once (then cache hits)", *calls)
	}
	if c.ttls["cities"] != time.Hour {
		t.Errorf("ttl = %v, want 1h", c.ttls["cities"])
	}
	if string(c.data["cities"]) != `[{"Name":"Lisbon","Population":545000,"Tags":["coast"]}]` {
		t.Errorf("stored %s, want JSON", c.data["cities"])
	}
}

func TestCheckCacheNeverCachesFetchErrors(t *testing.T) {
	ctx := context.Background()
	c := newFakeCache()
	errDB := errors.New("db down")
	fetch, calls := counted(city{Name: "partial"}, errDB)

	for range 2 {
		got, err := CheckCache(ctx, c, "k", time.Hour, fetch)
		if !errors.Is(err, errDB) {
			t.Fatalf("err = %v, want the fetch error", err)
		}
		if got.Name != "" {
			t.Errorf("got %+v with an error, want the zero value", got)
		}
	}
	if *calls != 2 || c.sets != 0 || len(c.data) != 0 {
		t.Errorf("fetch calls %d, sets %d, data %v: a failed fetch must not be cached", *calls, c.sets, c.data)
	}
}

func TestCheckCacheSurvivesCacheFailures(t *testing.T) {
	ctx := context.Background()

	t.Run("read fails", func(t *testing.T) {
		c := newFakeCache()
		c.getErr = errors.New("connection refused")
		fetch, calls := counted(42, nil)
		got, err := CheckCache(ctx, c, "k", time.Minute, fetch)
		if err != nil || got != 42 || *calls != 1 {
			t.Errorf("got %v, %v after %d fetches; want the fetched value", got, err, *calls)
		}
	})

	t.Run("write fails", func(t *testing.T) {
		c := newFakeCache()
		c.setErr = errors.New("out of memory")
		fetch, _ := counted(42, nil)
		got, err := CheckCache(ctx, c, "k", time.Minute, fetch)
		if err != nil || got != 42 {
			t.Errorf("got %v, %v; want the fetched value", got, err)
		}
	})

	t.Run("unreadable entry", func(t *testing.T) {
		c := newFakeCache()
		c.data["k"] = []byte(`{"not":"an int"}`)
		fetch, calls := counted(42, nil)
		got, err := CheckCache(ctx, c, "k", time.Minute, fetch)
		if err != nil || got != 42 || *calls != 1 {
			t.Errorf("got %v, %v after %d fetches; want the fetched value", got, err, *calls)
		}
		if string(c.data["k"]) != "42" {
			t.Errorf("entry = %s, want it overwritten with 42", c.data["k"])
		}
	})

	t.Run("value not encodable", func(t *testing.T) {
		c := newFakeCache()
		fetch, _ := counted(func() {}, nil)
		if _, err := CheckCache(ctx, c, "k", time.Minute, fetch); err != nil {
			t.Errorf("err = %v; an unencodable value is returned, just not cached", err)
		}
		if c.sets != 0 {
			t.Error("an unencodable value was cached")
		}
	})
}

func TestCheckCacheDisabled(t *testing.T) {
	ctx := context.Background()
	for name, c := range map[string]Cache{"Disabled": Disabled(), "nil": nil} {
		t.Run(name, func(t *testing.T) {
			fetch, calls := counted("fresh", nil)
			for range 2 {
				if got, err := CheckCache(ctx, c, "k", time.Minute, fetch); err != nil || got != "fresh" {
					t.Fatalf("got %q, %v", got, err)
				}
			}
			if *calls != 2 {
				t.Errorf("fetch ran %d times, want every time", *calls)
			}
		})
	}
}

func TestDisabled(t *testing.T) {
	ctx := context.Background()
	c := Disabled()
	if err := c.Set(ctx, "k", []byte("v"), time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(ctx, "k"); !errors.Is(err, ErrMiss) {
		t.Errorf("Get = %v, want ErrMiss", err)
	}
	if err := errors.Join(c.Delete(ctx, "k"), c.Ping(ctx), c.Close()); err != nil {
		t.Error(err)
	}
	if !IsDisabled(c) || !IsDisabled(nil) || IsDisabled(newFakeCache()) {
		t.Error("IsDisabled is wrong")
	}
}

func TestWithPrefix(t *testing.T) {
	ctx := context.Background()
	inner := newFakeCache()
	if WithPrefix(inner, "") != Cache(inner) {
		t.Error("an empty prefix should return the cache unchanged")
	}

	c := WithPrefix(inner, "staging")
	if err := c.Set(ctx, "cities", []byte("x"), time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, ok := inner.data["staging:cities"]; !ok {
		t.Errorf("stored under %v, want staging:cities", inner.data)
	}
	if b, err := c.Get(ctx, "cities"); err != nil || string(b) != "x" {
		t.Errorf("Get = %q, %v", b, err)
	}
	if err := c.Delete(ctx, "cities", "towns"); err != nil {
		t.Fatal(err)
	}
	if len(inner.deleted) != 2 || inner.deleted[0] != "staging:cities" || inner.deleted[1] != "staging:towns" {
		t.Errorf("deleted %v, want both keys prefixed", inner.deleted)
	}
}

// testContract checks the behaviour every Cache client must have. advance moves the server's clock
// forward, so expiry can be tested without sleeping.
func testContract(t *testing.T, c Cache, advance func(time.Duration)) {
	t.Helper()
	ctx := context.Background()

	if err := c.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if _, err := c.Get(ctx, "missing"); !errors.Is(err, ErrMiss) {
		t.Errorf("Get(missing) = %v, want ErrMiss", err)
	}

	value := []byte("bin\x00ary \r\n value")
	if err := c.Set(ctx, "k1", value, time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got, err := c.Get(ctx, "k1"); err != nil || !bytes.Equal(got, value) {
		t.Errorf("Get(k1) = %q, %v; want %q", got, err, value)
	}

	// overwrite
	if err := c.Set(ctx, "k1", []byte("new"), time.Minute); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.Get(ctx, "k1"); string(got) != "new" {
		t.Errorf("after overwrite Get(k1) = %q", got)
	}

	// a ttl of 0 would mean "forever" to the server: refused
	for _, ttl := range []time.Duration{0, -time.Second} {
		if err := c.Set(ctx, "forever", []byte("x"), ttl); err == nil {
			t.Errorf("Set with ttl %v succeeded, want an error", ttl)
		}
	}
	if _, err := c.Get(ctx, "forever"); !errors.Is(err, ErrMiss) {
		t.Errorf("a refused Set stored a value: %v", err)
	}

	// expiry
	if err := c.Set(ctx, "short", []byte("x"), 2*time.Second); err != nil {
		t.Fatal(err)
	}
	advance(3 * time.Second)
	if _, err := c.Get(ctx, "short"); !errors.Is(err, ErrMiss) {
		t.Errorf("Get(short) after its ttl = %v, want ErrMiss", err)
	}

	// delete, including keys that are not there
	if err := c.Set(ctx, "k2", []byte("x"), time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(ctx, "k1", "k2", "never-set"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := c.Delete(ctx); err != nil {
		t.Errorf("Delete with no keys: %v", err)
	}
	for _, k := range []string{"k1", "k2"} {
		if _, err := c.Get(ctx, k); !errors.Is(err, ErrMiss) {
			t.Errorf("Get(%s) after Delete = %v, want ErrMiss", k, err)
		}
	}

	// CheckCache on the real client
	fetch, calls := counted(city{Name: "Porto"}, nil)
	for range 2 {
		got, err := CheckCache(ctx, c, "city", time.Minute, fetch)
		if err != nil || got.Name != "Porto" {
			t.Fatalf("CheckCache = %+v, %v", got, err)
		}
	}
	if *calls != 1 {
		t.Errorf("CheckCache fetched %d times, want once", *calls)
	}
}
