package scaffold

import (
	"fmt"
	"strings"

	"github.com/owenwexler/create-gotth-plus-app/internal/options"
)

// applyCache sets up the server-side cache. The template ships internal/cache with a Redis client;
// NoCache removes the package and everything that refers to it, and Memcached swaps the client for
// the one in _variants/cache-memcached. It runs after the auth step, whose app.go and tests take the
// cache too, and before the module is renamed, so mod is the template's module path.
func applyCache(p Project, o options.Options, mod string) error {
	var err error
	switch o.Cache {
	case options.Redis:
		return nil // the template as it is
	case options.Memcached:
		err = memcachedCache(p)
	default:
		err = removeCache(p, mod)
	}
	if err != nil {
		return err
	}
	return checkCacheLeftovers(p, o)
}

// cacheLeftovers is text that must not survive a cache step, by choice.
var cacheLeftovers = map[options.Cache][]string{
	options.NoCache:   {"CacheURL", "internal/cache", "cache.CheckCache", "go-redis", "miniredis"},
	options.Memcached: {"rediss://", "redis://", "go-redis", "miniredis", "Valkey", "redis.go"},
}

// checkCacheLeftovers fails when the template mentions the removed cache somewhere the edits above
// don't know about yet, so a gotth-plus change can't quietly leave a dangling reference behind.
func checkCacheLeftovers(p Project, o options.Options) error {
	for _, name := range p.Paths() {
		if name == "go.mod" || name == "go.sum" || strings.HasPrefix(name, "_") {
			continue // replaced from _deps
		}
		for _, s := range cacheLeftovers[o.Cache] {
			if strings.Contains(string(p[name]), s) {
				return fmt.Errorf("%s still mentions %q after removing it; add an edit for it", name, s)
			}
		}
	}
	return nil
}

func removeCache(p Project, mod string) error {
	n := 0
	for _, name := range p.Paths() {
		if strings.HasPrefix(name, "internal/cache/") {
			delete(p, name)
			n++
		}
	}
	if n == 0 {
		return fmt.Errorf("internal/cache: not in template")
	}

	importLine := "\t\"" + mod + "/internal/cache\"\n"

	// tests build the app with cache.Disabled()
	for _, name := range p.Paths() {
		if !strings.HasPrefix(name, "cmd/server/") || !strings.HasSuffix(name, "_test.go") || !strings.Contains(string(p[name]), importLine) {
			continue
		}
		s := string(p[name])
		if !strings.Contains(s, ", cache.Disabled()") {
			return fmt.Errorf("%s: imports internal/cache but doesn't pass cache.Disabled()", name)
		}
		s = strings.ReplaceAll(s, ", cache.Disabled()", "")
		p[name] = []byte(strings.Replace(s, importLine, "", 1))
	}

	if err := p.replaceSpan("cmd/server/main.go",
		"\n\t// The server-side cache (cache.Disabled() when CacheURL is empty).", "\tcancelPing()\n", ""); err != nil {
		return err
	}
	if err := p.replaceCount("internal/app/app.go", "db *gorm.DB, c cache.Cache", "db *gorm.DB", 2); err != nil {
		return err
	}
	// NewHandler's doc comment; with built-in auth it sits in a cgpa:if oauth block, so it may be gone
	p["internal/app/app.go"] = []byte(strings.Replace(string(p["internal/app/app.go"]),
		"// c is the server-side cache: cache.Disabled() when CacheURL is empty.\n", "", 1))

	if err := p.edits([]edit{
		{"cmd/server/main.go", importLine, ""},
		{"cmd/server/main.go", "app.NewHandler(env, db, appCache)", "app.NewHandler(env, db)"},
		{"cmd/server/main.go",
			"\t// only close the database and the cache once no handler can still be using them\n\tcloseDB(db)\n" +
				"\tif err := appCache.Close(); err != nil {\n\t\tslog.Error(\"closing cache\", \"err\", err)\n\t}\n",
			"\t// only close the database once no handler can still be using it\n\tcloseDB(db)\n"},

		{"internal/app/app.go", importLine, ""},
		{"internal/app/app.go", "newMux(env, db, c", "newMux(env, db"},
		{"internal/app/app.go", " Pass c to the\n// repositories or services that read through the cache with cache.CheckCache.", ""},

		{"internal/config/config.go",
			"\t// CacheURL is the server-side cache: a Redis or Redis-compatible server. Empty: no cache.\n\tCacheURL string\n" +
				"\t// CacheKeyPrefix optionally goes in front of every cache key (\"<prefix>:<key>\").\n\tCacheKeyPrefix string\n", ""},
		{"internal/config/config.go", "\n\t\tCacheURL:       os.Getenv(\"CacheURL\"),\n\t\tCacheKeyPrefix: os.Getenv(\"CacheKeyPrefix\"),\n", ""},
	}); err != nil {
		return err
	}

	spans := []struct{ file, start, end, new string }{
		{".env.example", "\n# Optional. The server-side cache (internal/cache)", "CacheKeyPrefix=\n", ""},
		{"README.md", "### Server-side cache\n", "### Seed data\n", "### Seed data\n"},
		{"AGENTS.md", "- Cache: optional, off while `CacheURL` is empty.", "what every client must pass.\n", ""},
		{"SECURITY.md", "| Cache that fails safe |", "|\n", ""},
		{"SECURITY.md", "- [ ] If `CacheURL` is set:", "from the secret store.\n", ""},
		{"SECURITY.md", "### Server-side cache\n", "### Authentication and sessions", "### Authentication and sessions"},
	}
	for _, s := range spans {
		if err := p.replaceSpan(s.file, s.start, s.end, s.new); err != nil {
			return err
		}
	}

	return p.edits([]edit{
		{"README.md", "- An optional server-side cache (Redis or Redis-compatible) and `cache.CheckCache`, which reads through it to the database\n", ""},
		{"README.md", "entry point: config, database, cache, server start", "entry point: config, database, server start"},
		{"README.md", "  cache/            optional server-side cache (Redis) and CheckCache\n", ""},
		{"README.md", "- If you use the cache, point `CacheURL` at a server only the app can reach, with a password and TLS (`rediss://`). See `SECURITY.md`.\n", ""},
		{"AGENTS.md", ", `CacheURL`, `CacheKeyPrefix`", ""},
		{"SECURITY.md", "pass them at runtime (`CacheURL` included, since it\n  carries the cache's password).\n", "pass them at runtime.\n"},
	})
}

func memcachedCache(p Project) error {
	for _, name := range []string{"internal/cache/redis.go", "internal/cache/redis_test.go"} {
		if _, ok := p[name]; !ok {
			return fmt.Errorf("%s: not in template", name)
		}
		delete(p, name)
	}
	if _, err := p.overlay("cache-memcached"); err != nil {
		return err
	}

	if err := p.replaceSpan(".env.example",
		"# Optional. The server-side cache (internal/cache): a Redis server", "from the database.\n",
		"# Optional. The server-side cache (internal/cache): one or more Memcached servers, or anything that\n"+
			"# speaks its text protocol, e.g. memcached://localhost:11211, or memcached://host1:11211,host2:11211\n"+
			"# to spread keys over several. The client has no passwords or TLS, so keep the servers on a private\n"+
			"# network. Empty: no cache, so cache.CheckCache always reads from the database.\n"); err != nil {
		return err
	}
	if err := p.replaceSpan("README.md", "The app can cache database results in Redis, or anything", "in-memory Redis.\n", memcachedReadme); err != nil {
		return err
	}

	return p.edits([]edit{
		{"internal/config/config.go", "a Redis or Redis-compatible server. Empty: no cache.", "one or more Memcached (or Memcached-compatible) servers. Empty: no cache."},

		{"README.md", "- An optional server-side cache (Redis or Redis-compatible)", "- An optional server-side cache (Memcached or Memcached-compatible)"},
		{"README.md", "optional server-side cache (Redis) and CheckCache", "optional server-side cache (Memcached) and CheckCache"},
		{"README.md",
			"- If you use the cache, point `CacheURL` at a server only the app can reach, with a password and TLS (`rediss://`). See `SECURITY.md`.\n",
			"- If you use the cache, keep the Memcached servers on a private network only the app can reach: the client sends no password and no TLS. See `SECURITY.md`.\n"},

		{"AGENTS.md",
			"the Redis client (`cache.Connect`, `redis.go`; any Redis-compatible server works)",
			"the Memcached client (`cache.Connect`, `memcached.go`; any Memcached-compatible server works, and `memcached://a:11211,b:11211` spreads keys over several)"},
		{"AGENTS.md",
			"Cache tests run against an in-memory Redis (miniredis), so no server is needed;",
			"Cache tests run against a fake Memcached server (`memcached_test.go`), so no server is needed; keys must be at most 250 bytes with no spaces or control characters (prefix included), and values at most 1 MB by default;"},

		{"SECURITY.md",
			"- [ ] If `CacheURL` is set: the cache server is reachable only from the app (private network or\n" +
				"      firewall), requires a password (or ACL user), and is connected to over TLS (`rediss://`)\n" +
				"      unless it is on the same host. `CacheURL` comes from the secret store.\n",
			"- [ ] If `CacheURL` is set: the Memcached servers are reachable only from the app (localhost, a\n" +
				"      private network, or a firewall). The client sends no password and no TLS, so the network\n" +
				"      is the only protection. UDP is off on the servers (`-U 0`).\n"},
		{"SECURITY.md",
			"- **Keep the cache server private.** Never expose Redis (or Memcached) to the internet: an open\n" +
				"  cache server can be read, filled with poisoned entries, or flushed by anyone. Bind it to a private\n" +
				"  network, firewall it, and set a password or an ACL user. Use TLS (`rediss://`) for anything that\n" +
				"  isn't localhost.\n",
			"- **Keep the cache servers private.** Never expose Memcached to the internet: an open server can be\n" +
				"  read, filled with poisoned entries, flushed, or used for UDP amplification attacks. The client\n" +
				"  (`gomemcache`) has no passwords and no TLS, so bind the servers to localhost or a private\n" +
				"  network, firewall them, and keep UDP off (`-U 0`, the default since Memcached 1.5.6). For TLS,\n" +
				"  set the client's `DialContext` to a `tls.Dialer`'s (see `memcached.go`).\n"},
		{"SECURITY.md", "anyone with the password sees every key.", "anyone who can reach the servers sees every key."},
		{"SECURITY.md", "pass them at runtime (`CacheURL` included, since it\n  carries the cache's password).\n", "pass them at runtime.\n"},
	})
}

// memcachedReadme replaces the Redis-specific start of README.md's "Server-side cache" section.
const memcachedReadme = "The app can cache database results in Memcached, or anything that speaks its text protocol. It is\n" +
	"off until you set `CacheURL` in `.env`:\n\n" +
	"```bash\n" +
	"CacheURL=memcached://localhost:11211   # memcached://host1:11211,host2:11211 spreads keys over both\n" +
	"CacheKeyPrefix=myapp                   # optional: keys become myapp:<key>\n" +
	"```\n\n" +
	"For a local server, install Memcached from your package manager (`memcached -l 127.0.0.1`), or run\n" +
	"`docker run --rm -p 127.0.0.1:11211:11211 memcached`. The tests need neither: they run against a fake\n" +
	"Memcached server. Keys must be at most 250 bytes, with no spaces or control characters (the prefix\n" +
	"included), and values at most 1 MB by default.\n"
