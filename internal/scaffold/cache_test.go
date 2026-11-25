package scaffold

import (
	"strings"
	"testing"

	"github.com/owenwexler/create-gotth-plus-app/internal/options"
)

// cacheProjects builds a project for each cache, on SQLite and Postgres, with and without built-in
// auth (whose app.go and tests take the cache too).
func cacheProjects(t *testing.T, c options.Cache) map[string]Project {
	t.Helper()
	projects := map[string]Project{}
	for _, db := range []options.Database{options.SQLite, options.Postgres} {
		for _, auth := range []options.Auth{options.NoAuth, options.BuiltinAuth, options.SupabaseAuth} {
			if auth == options.SupabaseAuth && db != options.Postgres {
				continue
			}
			o := testOptions()
			o.Cache, o.Database, o.Auth = c, db, auth
			if auth == options.BuiltinAuth {
				o.AuthMethods = options.AuthMethodList
			}
			projects[string(db)+"-"+string(auth)] = mustBuild(t, o, "/work/demo-app")
		}
	}
	return projects
}

func TestBuildWithoutCache(t *testing.T) {
	for name, p := range cacheProjects(t, options.NoCache) {
		t.Run(name, func(t *testing.T) {
			for _, f := range p.Paths() {
				if strings.HasPrefix(f, "internal/cache/") {
					t.Errorf("%s is still there", f)
				}
				if strings.Contains(string(p[f]), "/internal/cache\"") {
					t.Errorf("%s still imports internal/cache", f)
				}
			}
			for _, f := range []string{"go.mod", ".env.example", ".env", "README.md", "AGENTS.md", "SECURITY.md", "internal/config/config.go"} {
				if s := string(p[f]); strings.Contains(s, "CacheURL") || strings.Contains(s, "redis") {
					t.Errorf("%s still mentions the cache", f)
				}
			}
			mustContain(t, p, "cmd/server/main.go", "app.NewHandler(env, db)")
			mustContain(t, p, "cmd/server/main.go", "\t// only close the database once no handler can still be using it\n\tcloseDB(db)\n")
			mustContain(t, p, "cmd/server/testutil_test.go", "app.NewHandler(env, db)")
			mustContain(t, p, "README.md", "### Seed data")
			mustContain(t, p, "SECURITY.md", "### Authentication and sessions")
			if strings.Contains(string(p["internal/app/app.go"]), "cache") {
				t.Error("app.go still mentions the cache")
			}
		})
	}
}

func TestBuildRedisCache(t *testing.T) {
	for name, p := range cacheProjects(t, options.Redis) {
		t.Run(name, func(t *testing.T) {
			for _, f := range []string{"internal/cache/cache.go", "internal/cache/check.go", "internal/cache/redis.go", "internal/cache/redis_test.go", "internal/cache/cache_test.go"} {
				if _, ok := p[f]; !ok {
					t.Errorf("%s is missing", f)
				}
			}
			if _, ok := p["internal/cache/memcached.go"]; ok {
				t.Error("the Memcached client is in a Redis project")
			}
			mustContain(t, p, "go.mod", "github.com/redis/go-redis/v9 ")
			mustContain(t, p, "go.mod", "github.com/alicebob/miniredis/v2 ")
			mustContain(t, p, ".env", "\nCacheURL=\n")
			mustContain(t, p, "internal/cache/redis.go", `"github.com/redis/go-redis/v9"`)
			mustContain(t, p, "cmd/server/main.go", `"example.com/acme/demo/internal/cache"`)
			mustContain(t, p, "cmd/server/main.go", "app.NewHandler(env, db, appCache)")
			mustContain(t, p, "internal/app/app.go", "db *gorm.DB, c cache.Cache")
			mustContain(t, p, "cmd/server/testutil_test.go", "cache.Disabled()")
			mustContain(t, p, "SECURITY.md", "### Server-side cache")
		})
	}
}

func TestBuildMemcachedCache(t *testing.T) {
	for name, p := range cacheProjects(t, options.Memcached) {
		t.Run(name, func(t *testing.T) {
			for _, f := range []string{"internal/cache/cache.go", "internal/cache/check.go", "internal/cache/memcached.go", "internal/cache/memcached_test.go", "internal/cache/cache_test.go"} {
				if _, ok := p[f]; !ok {
					t.Errorf("%s is missing", f)
				}
			}
			for _, f := range []string{"internal/cache/redis.go", "internal/cache/redis_test.go"} {
				if _, ok := p[f]; ok {
					t.Errorf("%s is still there", f)
				}
			}
			mustContain(t, p, "go.mod", "github.com/bradfitz/gomemcache ")
			for _, mod := range []string{"go-redis", "miniredis"} {
				if strings.Contains(string(p["go.mod"]), mod) {
					t.Errorf("go.mod still requires %s", mod)
				}
			}
			for _, f := range p.Paths() {
				if strings.Contains(strings.ToLower(string(p[f])), "redis") && f != "go.sum" {
					t.Errorf("%s still mentions Redis", f)
				}
			}
			mustContain(t, p, ".env.example", "memcached://localhost:11211")
			mustContain(t, p, "README.md", "CacheURL=memcached://localhost:11211")
			mustContain(t, p, "AGENTS.md", "the Memcached client (`cache.Connect`, `memcached.go`")
			mustContain(t, p, "SECURITY.md", "The client sends no password and no TLS")
			mustContain(t, p, "internal/cache/memcached.go", `"github.com/bradfitz/gomemcache/memcache"`)
			mustContain(t, p, "cmd/server/main.go", "app.NewHandler(env, db, appCache)")
		})
	}
}

func TestDepsKeyCache(t *testing.T) {
	o := testOptions()
	for c, want := range map[options.Cache]string{options.NoCache: "sqlite-purego", options.Redis: "sqlite-purego-redis", options.Memcached: "sqlite-purego-memcached"} {
		o.Cache = c
		if got := DepsKey(o); got != want {
			t.Errorf("DepsKey with cache %s = %q, want %q", c, got, want)
		}
	}
	o.Cache, o.E2E, o.Auth, o.AuthMethods = options.Memcached, false, options.BuiltinAuth, []options.AuthMethod{options.Password}
	if got := DepsKey(o); got != "sqlite-purego-auth-password-memcached-noe2e" {
		t.Errorf("DepsKey = %q", got)
	}
}

func TestCacheLeftoversAreCaught(t *testing.T) {
	p, err := load(Template())
	if err != nil {
		t.Fatal(err)
	}
	// a new mention of the cache in a file the edits don't know about
	p["internal/handler/handler.go"] = append(p["internal/handler/handler.go"], "// see cache.CheckCache\n"...)
	o := testOptions()
	o.Cache = options.NoCache
	if err := applyCache(p, o, templateModule); err == nil || !strings.Contains(err.Error(), "handler.go") {
		t.Errorf("applyCache = %v, want an error naming handler.go", err)
	}
}
