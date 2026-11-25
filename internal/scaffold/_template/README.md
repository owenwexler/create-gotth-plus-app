# GOTTH+

An opinionated, secure, production-ready starting point for the **GOTTH** stack: **Go**, **Templ**, **Tailwind CSS** and **HTMX**, plus a little **Alpine.js** for client-only state.

No Node, no npm, no bundler. Tailwind is the standalone CLI, and the three browser libraries are plain files downloaded by a shell script.

## What you get

- `net/http` server with timeouts, graceful shutdown and structured logs that carry a request ID
- Security middleware: strict Content-Security-Policy (no inline scripts, no `unsafe-eval`), HSTS in production, double-submit-cookie CSRF, cross-origin protection, per-IP rate limiting, request body limits
- Brotli/gzip compression
- Layered layout (`model` → `repository` → `service` → `factory`/`handler` → `view`) on SQLite and GORM
- Toasts and a spinner as Templ components, and htmx error handling that shows a toast instead of failing silently
- `hx-optimistic` wired up, with rollback on server errors
- htmx's `preload` extension loaded, ready to fetch pages and fragments on hover (see Preloading)
- A dev-only `/seed` route and a script that runs it with a valid CSRF token
- An optional server-side cache (Redis or Redis-compatible) and `cache.CheckCache`, which reads through it to the database
- Unit, integration and Playwright end-to-end tests
- `AGENTS.md` / `CLAUDE.md` guidance for AI coding agents

## Getting started

### Prerequisites

- Go 1.27.3+ and a C compiler (the SQLite driver uses CGO)
- [`templ`](https://templ.guide/quick-start/installation): `go install github.com/a-h/templ/cmd/templ@latest`
- The [Tailwind CSS standalone CLI](https://tailwindcss.com/blog/standalone-cli) (v4) on your `PATH` as `tailwindcss`
- `git`, `curl` and `make`
- Optional: [`air`](https://github.com/air-verse/air)

### Setup

```bash
make setup    # creates .env, downloads Go modules and the JS libraries, generates templ + CSS
make dev      # live reload; open http://localhost:7331
```

`make run` starts the server without live reload on the port in `.env` (3000 by default). `make help` lists every target.

### The JavaScript libraries

htmx, its `preload` extension, the Alpine.js CSP build and `hx-optimistic` are **not** included in this template. `make js` (`./download-js.sh`) downloads the latest version of each into `static/js` and prints its SHA-384 hash. It resolves versions from each project's git tags (the preload extension, which has no release tags of its own, from jsDelivr); pin one with `HTMX_VERSION=2.0.11 ./download-js.sh` (likewise `PRELOAD_VERSION`, `ALPINE_VERSION`, `HX_OPTIMISTIC_VERSION`). htmx stays on its 2.x line unless you set `HTMX_MAJOR`.

`.gitignore` excludes `static/js/*.min.js` so the template itself stays free of them. In your own project, remove that line and commit the files, so production serves copies you have pinned and reviewed.

### Preloading

htmx's [preload extension](https://htmx.org/extensions/preload/) is loaded on every page (`hx-ext="optimistic, preload"` on `<body>`), but nothing uses it yet. Add `preload` to a link or an `hx-get` element to fetch its response on `mousedown`, or `preload="mouseover"` to fetch it after a short hover, so the click is answered from memory:

```html
<nav preload="mouseover">            <!-- applies to every link inside -->
  <a href="/projects">Projects</a>
  <a href="/settings">Settings</a>
</nav>
<button hx-get="/tabs/activity" hx-target="#panel" preload="mouseover">Activity</button>
```

Good candidates are tabs, pagination and "next page" links. Only preload GETs that are cheap and change nothing: a preload is a real request (sent with an `HX-Preloaded: true` header), whether or not the user clicks.

### Server-side cache

The app can cache database results in Redis, or anything that speaks its protocol (Valkey, KeyDB,
Dragonfly, a managed Redis). It is off until you set `CacheURL` in `.env`:

```bash
CacheURL=redis://localhost:6379/0      # rediss://user:password@host:6380/0 for TLS
CacheKeyPrefix=myapp                   # optional: keys become myapp:<key>
```

For a local server, install Redis or Valkey from your package manager (`redis-server` / `valkey-server`),
or run `docker run --rm -p 6379:6379 valkey/valkey`. The tests need neither: they run against an
in-memory Redis.

`cache.CheckCache` is the read-through helper. It returns the cached value for a key, or calls your
function (usually a repository query), caches the result as JSON for the given time, and returns it:

```go
cities, err := cache.CheckCache(ctx, c, "cities", 7*24*time.Hour,
	func(ctx context.Context) ([]model.City, error) { return repo.Cities(ctx) })
```

- An error from your function is returned and never cached.
- The cache failing never fails a request: a read error counts as a miss and a write error is only
  logged, so with the cache server down every call goes to the database. Short timeouts keep that
  fallback quick.
- With no `CacheURL`, the cache is `cache.Disabled()` and `CheckCache` always calls your function, so
  the same code runs with or without a cache server.
- Put everything the value depends on in the key (above all the user, for per-user data), and call
  `c.Delete(ctx, key)` when the data changes, or keep the time short enough that stale data doesn't matter.

`main.go` connects at startup (`cache.Connect`) and passes the cache to `app.NewHandler`; hand it from
`newMux` to the repositories or services that need it. A bad `CacheURL` stops the app at startup; a cache
server that doesn't answer is only a warning in the log. `/healthz` doesn't check the cache, because the
app works without it.

### Seed data

With the server running in development:

```bash
make seed     # POST /seed with a valid CSRF token; loads seedData.json
```

## Project structure

```
cmd/server/         entry point: config, database, cache, server start, graceful shutdown
internal/
  app/              routes (newMux) and middleware chain (buildHandler), shared by main and the tests
  cache/            optional server-side cache (Redis) and CheckCache
  config/           environment loading and validation
  csrf/             double-submit-cookie CSRF protection
  database/         SQLite connection (GORM)
  dto/              validated request shapes
  escaper/          SQL LIKE wildcard escaping
  factory/          handler constructors (closures over dependencies)
  handler/          struct-based handlers (health check)
  helper/           generic helpers (Ternary, Count)
  middleware/       security headers, rate limit, compression, body limit, admin key
  model/            domain types
  optimistic/       hx-optimistic config builders
  repository/       data access behind interfaces, sentinel errors, pagination
  requestid/        request IDs in headers and logs
  seed/             the /seed route's logic
  service/          business logic
  styles/           shared Tailwind class strings
  view/             .templ components (and generated *_templ.go)
static/
  input.css         Tailwind entry point
  js/app.js         Alpine store and components, htmx hooks
  font/  icon/      your self-hosted fonts and icons
download-js.sh      downloads htmx, its preload extension, Alpine (CSP) and hx-optimistic
seed.sh             runs POST /seed with a CSRF token
seedData.json       sample data
```

## Routes

| Method | Path | Purpose |
|---|---|---|
| GET | `/` | Splash page |
| POST | `/hello` | Example app: greeting fragment |
| GET | `/healthz` | Health check (pings the database) |
| POST | `/seed` | Load sample data (development only) |
| GET | `/static/*` | Static assets |

## Testing

```bash
make e2e-install   # once: downloads Chromium for Playwright-Go (`make setup` does this too)
make test          # everything: unit, integration (real routes and middleware, throwaway SQLite
                   # database) and end-to-end (a real browser)
make check         # generate, format, vet, test
```

The end-to-end tests are behind the `e2e` build tag: `make test` adds the tag, while a plain `go test ./...`
runs only the unit and integration tests and needs no browser.

- `cmd/server/e2e_helpers_test.go` is the harness: one shared browser, a fresh browser context per test, and
  helpers for routes, tabs, and console errors.
- `cmd/server/e2e_generic_test.go` holds checks that apply to any app (visible focus rings, accessible toasts,
  no third-party requests, no horizontal scroll, loading without JavaScript). Keep them when you replace the example.
- The axe accessibility scan needs `axe-core`, which is not vendored. Run it with
  `AXE_CORE_JS=/path/to/axe.min.js make test`, or copy `axe.min.js` to `cmd/server/testdata/`; otherwise it is skipped.

## Removing the example app

The example app (the hello form and the counter) is confined to:

- `internal/view/example.templ` (and its generated `example_templ.go`), and the `@ExampleApp()` line in `internal/view/pages.templ`
- `internal/factory/example.go`
- the `POST /hello` route and `greetingSvc` in `internal/app/app.go`
- `GreetingService` in `internal/service/service.go` (and `service_test.go`) and `HelloRequest` in `internal/dto/dto.go`
- the `counter` component in `static/js/app.js`
- the hello and counter tests in `cmd/server/integration_test.go` and `cmd/server/e2e_test.go` (keep `e2e_helpers_test.go` and `e2e_generic_test.go`)

`model.Example`, `repository/example.go` and `seedData.json` are placeholders for the seed route; rename them to your first real model.

## Production notes

- Set `GoEnv=production`. That enables HSTS, `Secure` cookies, JSON logs and strict rate limits, and unregisters `/seed`.
- Serve behind HTTPS. The rate limiter keys on the TCP peer address, so behind a reverse proxy put real-IP handling in front of it or rate-limit at the proxy.
- Commit the downloaded JS libraries and build `static/output.css` in your deploy step.
- If you use the cache, point `CacheURL` at a server only the app can reach, with a password and TLS (`rediss://`). See `SECURITY.md`.
- See `AGENTS.md` for the full conventions and security checklist.
