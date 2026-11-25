package cache

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeMemcached is a Memcached server for tests: the text-protocol commands the client uses (gets,
// set, delete, version), in memory, with a clock the test moves forward.
type fakeMemcached struct {
	ln    net.Listener
	mu    sync.Mutex
	items map[string]fakeItem
	now   time.Time
	// exptimes records the expiration sent with each set, by key
	exptimes map[string]int64
}

type fakeItem struct {
	value   []byte
	flags   string
	expires time.Time // zero: never
}

func newFakeMemcached(t *testing.T) *fakeMemcached {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeMemcached{ln: ln, items: map[string]fakeItem{}, exptimes: map[string]int64{}, now: time.Now()}
	go f.serve()
	t.Cleanup(f.Close)
	return f
}

func (f *fakeMemcached) Addr() string { return f.ln.Addr().String() }

func (f *fakeMemcached) Close() { _ = f.ln.Close() }

// advance moves the server's clock forward, so entries expire without the test sleeping.
func (f *fakeMemcached) advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

func (f *fakeMemcached) serve() {
	for {
		conn, err := f.ln.Accept()
		if err != nil {
			return
		}
		go f.handle(conn)
	}
}

func (f *fakeMemcached) handle(conn net.Conn) {
	defer conn.Close()
	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
	for {
		line, err := rw.ReadString('\n')
		if err != nil {
			return
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			return
		}
		f.mu.Lock()
		switch fields[0] {
		case "version":
			fmt.Fprint(rw, "VERSION 1.6.0-fake\r\n")
		case "gets", "get":
			for _, key := range fields[1:] {
				if it, ok := f.live(key); ok {
					fmt.Fprintf(rw, "VALUE %s %s %d 1\r\n%s\r\n", key, it.flags, len(it.value), it.value)
				}
			}
			fmt.Fprint(rw, "END\r\n")
		case "set": // set <key> <flags> <exptime> <bytes>
			n, _ := strconv.Atoi(fields[4])
			data := make([]byte, n+2) // the value and its \r\n
			if _, err := io.ReadFull(rw, data); err != nil {
				f.mu.Unlock()
				return
			}
			exptime, _ := strconv.ParseInt(fields[3], 10, 64)
			f.exptimes[fields[1]] = exptime
			it := fakeItem{value: data[:n], flags: fields[2]}
			switch {
			case exptime > int64(maxRelativeTTL/time.Second):
				it.expires = time.Unix(exptime, 0)
			case exptime > 0:
				it.expires = f.now.Add(time.Duration(exptime) * time.Second)
			}
			f.items[fields[1]] = it
			fmt.Fprint(rw, "STORED\r\n")
		case "delete":
			if _, ok := f.live(fields[1]); ok {
				delete(f.items, fields[1])
				fmt.Fprint(rw, "DELETED\r\n")
			} else {
				fmt.Fprint(rw, "NOT_FOUND\r\n")
			}
		default:
			fmt.Fprint(rw, "ERROR\r\n")
		}
		f.mu.Unlock()
		if err := rw.Flush(); err != nil {
			return
		}
	}
}

// live returns the item under key if it hasn't expired. The caller holds f.mu.
func (f *fakeMemcached) live(key string) (fakeItem, bool) {
	it, ok := f.items[key]
	if !ok || (!it.expires.IsZero() && !f.now.Before(it.expires)) {
		return fakeItem{}, false
	}
	return it, true
}

func newMemcached(t *testing.T, prefix string) (Cache, *fakeMemcached) {
	t.Helper()
	f := newFakeMemcached(t)
	c, err := Connect("memcached://"+f.Addr(), prefix)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c, f
}

func TestMemcachedContract(t *testing.T) {
	c, f := newMemcached(t, "")
	testContract(t, c, f.advance)
}

func TestMemcachedPrefix(t *testing.T) {
	c, f := newMemcached(t, "myapp")
	if err := c.Set(context.Background(), "cities", []byte("x"), time.Minute); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.items["myapp:cities"]; !ok {
		t.Errorf("keys on the server: %v, want myapp:cities", f.items)
	}
	if f.exptimes["myapp:cities"] != 60 {
		t.Errorf("expiration sent = %d, want 60 seconds", f.exptimes["myapp:cities"])
	}
}

func TestExpiration(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	for _, tc := range []struct {
		ttl  time.Duration
		want int32
	}{
		{time.Millisecond, 1}, // rounded up, never 0 (which would mean "forever")
		{time.Second, 1},
		{1500 * time.Millisecond, 2},
		{time.Hour, 3600},
		{30 * 24 * time.Hour, 30 * 24 * 3600}, // the longest relative time
		{31 * 24 * time.Hour, 1_800_000_000 + 31*24*3600},
		{200 * 365 * 24 * time.Hour, 1<<31 - 1}, // clamped instead of overflowing
	} {
		if got := expiration(tc.ttl, now); got != tc.want {
			t.Errorf("expiration(%v) = %d, want %d", tc.ttl, got, tc.want)
		}
	}
}

func TestMemcachedLongTTLIsAbsolute(t *testing.T) {
	c, f := newMemcached(t, "")
	ctx := context.Background()
	if err := c.Set(ctx, "k", []byte("x"), 60*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	// read as seconds, 60 days would be a Unix time in 1970: already expired
	if _, err := c.Get(ctx, "k"); err != nil {
		t.Errorf("Get after a 60-day Set = %v, want the value", err)
	}
	f.mu.Lock()
	sent := f.exptimes["k"]
	f.mu.Unlock()
	if sent < time.Now().Unix() {
		t.Errorf("expiration sent = %d, want a Unix time in the future", sent)
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
		"localhost:11211",
		"http://localhost:11211",
		"memcached://",
		"memcached://localhost",
		"memcached://localhost:11211,",
		"memcached://user:s3cret@localhost:11211",
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

func TestConnectSeveralServers(t *testing.T) {
	a, b := newFakeMemcached(t), newFakeMemcached(t)
	c, err := Connect("memcached://"+a.Addr()+","+b.Addr()+"/", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	testContract(t, c, func(d time.Duration) { a.advance(d); b.advance(d) })
}

func TestMemcachedDownFallsBackToFetch(t *testing.T) {
	c, f := newMemcached(t, "")
	f.Close()

	start := time.Now()
	got, err := CheckCache(context.Background(), c, "k", time.Minute, func(context.Context) (string, error) { return "from the database", nil })
	if err != nil || got != "from the database" {
		t.Errorf("CheckCache with the cache down = %q, %v; want the fetched value", got, err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("a cache that is down took %v; the timeout should keep it short", elapsed)
	}
	if err := c.Ping(context.Background()); err == nil {
		t.Error("Ping succeeded with the server down")
	}
}
