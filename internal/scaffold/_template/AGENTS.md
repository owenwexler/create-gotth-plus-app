# AGENTS.md — GOTTH Stack (Go · Tailwind · Templ · HTMX)

Guidance for AI coding agents and humans working in a GOTTH or GOTTH+ project. The first
sections are generic and reusable; the last section ("Project-specific notes")
is the only part that needs editing when you copy this file to a new project.

Stack: **Go** (`net/http`) · **Templ** (type-safe HTML) · **Tailwind CSS** · **HTMX**
(commonly paired with **Alpine.js** for small bits of client-only state).

---

## 1. Ground rules

1. The server owns state and renders HTML. HTMX endpoints return **HTML fragments**, not JSON.
2. Prefer the standard library (`net/http` with Go 1.22+ method/wildcard routing) over frameworks.
3. Keep JavaScript minimal. Reach for HTMX first, Alpine second, hand-written JS last.
4. Every change must leave `go build ./...`, `go vet ./...`, and `go test ./...` green.
5. Never hand-edit `*_templ.go` files. Edit `.templ`, then regenerate.
6. Make small, reviewable changes. Match the surrounding code's style and naming.

## 2. Commands

Adjust to the project, but the canonical workflow is (the `Makefile` wraps all of these, see `make help`):

```bash
templ generate                                   # .templ -> *_templ.go (run after EVERY .templ edit)
tailwindcss -i static/input.css -o static/output.css --minify   # build CSS (add --watch in dev)
go build ./... && go vet ./... && go test ./... -race
go test -race -count=1 -tags e2e ./...          # everything, browser tests included (see Project-specific notes)
air                                              # live reload (rebuilds on .go/.templ changes)
gofmt -l . ; templ fmt .                         # formatting
govulncheck ./...                                # known vulnerabilities in deps/stdlib
```

"My HTML change doesn't show up" is almost always a stale `_templ.go`. Run `templ generate`.
Tailwind class missing? Confirm the class lives in a file covered by the Tailwind `@source` /
`content` globs, and that classes are **complete literal strings** (never built by concatenation).

## 3. Architecture

Use a layered layout with dependencies pointing inward:

```
cmd/<app>/main.go     wiring only: config, DB, routes, server start, graceful shutdown
internal/config       env parsing + validation (fail fast at startup)
internal/database     connection + pool/pragma setup
internal/model        domain types
internal/dto          request/response shapes with validation tags
internal/repository   data access behind interfaces; takes context.Context
internal/service      business rules; no HTTP, no SQL
internal/handler      HTTP: parse -> validate -> call service -> render
internal/view         .templ components (pages, partials, layout)
static/               input.css, built CSS, fonts, icons
```

- Handlers are thin. No SQL in handlers, no HTML-building in services.
- Define repository/service **interfaces** at the consumer so handlers are testable with fakes.
- Pass `r.Context()` all the way to the database. Never use `context.Background()` inside a request.
- Return sentinel errors (`ErrNotFound`, `ErrConflict`, …) from repositories and map them to HTTP
  status codes in one place. Use `errors.Is`.
- Wrap errors with context: `fmt.Errorf("create task: %w", err)`. Don't `panic` in request paths.

## 4. Templ

- A component is a Go function returning `templ.Component`. Use typed arguments; pass structs, not 10 positional args.
- Build small composable components (button, input, row, toast, spinner) and reuse them.
- Split **full pages** (wrap in `Layout`) from **fragments** (what HTMX swaps in). Fragment endpoints must not render the layout.
- Templ escapes output by default. **Never** bypass it with `templ.Raw`, `templ.SafeURL`, or
  `@templ.Raw(...)` on user-controlled data. If you must, sanitize first and comment why.
- Do not interpolate user data into `<script>` or inline event handlers by string. Use
  `templ.JSONScript`, `data-*` attributes, or `templ.JSFuncCall`.
- For URLs built from input, use `templ.URL(...)` with an allow-listed scheme (`http`, `https`, relative).
- Commit generated `_templ.go` files only if the project does (check `.gitignore`); if committed, CI must
  run `templ generate && git diff --exit-code` so stale output fails the build.
- Give every list row and swap target a **stable, unique `id`** (e.g. `task-row-<uuid>`).

## 5. HTMX

**Patterns**
- Be explicit: set `hx-target`, `hx-swap`, and `hx-trigger` rather than relying on inheritance surprises.
- Use the right verb: GET for reads (idempotent, cacheable), POST/PUT/PATCH/DELETE for mutations. Never mutate on GET.
- Return the **smallest fragment** that updates the UI. Use out-of-band swaps (`hx-swap-oob`) for secondary updates
  (counters, toasts) instead of re-rendering whole pages.
- Use `HX-Request` to detect partial vs. full requests (and full-page-render when it's absent so deep links/refresh work).
  Set `Vary: HX-Request` on responses that differ by it so caches don't mix them up.
- Use response headers for flow control: `HX-Redirect`, `HX-Trigger`, `HX-Retarget`, `HX-Reswap`, `HX-Push-Url`.
- Keep URLs meaningful: `hx-push-url` for navigations/filters so back button, refresh, and sharing work.
- Debounce search: `hx-trigger="input changed delay:300ms, search"`. Cancel stale requests with `hx-sync="this:replace"`.
- Infinite scroll: `hx-trigger="revealed"` (or `intersect once`) on a sentinel row that swaps itself via `outerHTML`.
- Loading UX: `hx-indicator`, `hx-disabled-elt`, and the `htmx-request` class. Disable submit buttons while in flight to prevent double submits.
- Optimistic UI: update immediately, and have a defined rollback path on error (HTMX does not swap 4xx/5xx by default).

**Errors**
- By default HTMX **ignores 4xx/5xx responses**. Either configure `htmx.config.responseHandling` / handle
  `htmx:responseError` to show a toast, or return 200 with an error fragment for validation errors (or 422 plus
  explicit handling). Decide once and be consistent.
- Failures must never be silent: handle `htmx:sendError` (the request never reached the server) and
  `htmx:responseError` (an error status with nothing to swap) with a generic toast.
- If `htmx:beforeSwap` forces an error response to swap (`shouldSwap = true; isError = false`), htmx then
  reports that request as **successful** in `htmx:afterRequest`. Check `xhr.status < 400` before resetting a form
  or closing a dialog "on success", or a failed submit throws away what the user typed.

**Security**
- **Pin and self-host** htmx/Alpine (vendor under `static/`) or load from a CDN with a version pin **and**
  Subresource Integrity (`integrity="sha384-…" crossorigin="anonymous"`). Never use floating tags like `@2` / `@latest` in production.
- Set `htmx.config.allowEval=false` and `htmx.config.selfRequestsOnly=true` (or the equivalent in your htmx version) when you don't need them.
- Don't render untrusted HTML with `hx-disable` absent: for user-generated content areas add `hx-disable` on the container to neutralise htmx attributes injected through content.
- Send the CSRF token on every mutating request, e.g. `<body hx-headers='{"X-CSRF-Token": "…"}'>` (see §8).

## 6. Tailwind

- Use **complete class names** in source; dynamic string-building (`"bg-" + color`) is purged and silently breaks.
- Make sure Tailwind scans `.templ` files (`content: ["**/*.templ"]` in v3, `@source "../internal/view/*.templ"` in v4).
- Production CSS must be **minified** and served with long-lived cache headers + a content hash/version in the filename or query string.
- Extract repeated utility clusters into **Templ components**, not `@apply` sprawl.
- Self-host fonts (`woff2`), `font-display: swap`, preload the critical one. Avoid third-party font CDNs (privacy + latency).
- Honour accessibility: visible focus rings, sufficient contrast, `prefers-reduced-motion`, `sr-only` labels for icon buttons.
- v4 trap: don't pair `focus:outline-none` with a `focus-visible:outline-*` ring. `outline-none` sets
  `--tw-outline-style: none`, which `outline-2` reads, so the ring never shows. Use the `focus-visible:` classes alone.
- A spinner inside a button keeps its accessible label (e.g. "Loading") in the button's name; put `aria-hidden="true"`
  on the indicator wrapper.

## 7. Performance

**Server**
- Always set `http.Server` timeouts (never bare `http.ListenAndServe` in production):
  ```go
  srv := &http.Server{
      Addr: addr, Handler: handler,
      ReadHeaderTimeout: 5 * time.Second,
      ReadTimeout:       10 * time.Second,
      WriteTimeout:      30 * time.Second,
      IdleTimeout:       60 * time.Second,
      MaxHeaderBytes:    1 << 20,
  }
  ```
- Graceful shutdown: `signal.NotifyContext` + `srv.Shutdown(ctx)`.
- Compress responses (gzip/brotli middleware) for HTML/CSS/JS; skip already-compressed assets.
- Stream large pages with Templ's `Render(ctx, w)` directly to the `ResponseWriter`; don't build giant strings.
- Render to a `bytes.Buffer` first when you need to set status/headers conditionally on render success (avoids half-written responses).
- Use `sync.Pool`/buffer reuse only after profiling shows a need. Use `pprof` (behind auth/localhost) to find real hotspots.
- Set `GOMAXPROCS`/memory limits appropriately in containers (`automaxprocs` or Go 1.25+ container-aware defaults, `GOMEMLIMIT`).

**Database**
- Configure the pool (`SetMaxOpenConns`, `SetMaxIdleConns`, `SetConnMaxLifetime`). For SQLite: WAL mode,
  `busy_timeout`, `foreign_keys=ON`, and typically a small write-connection limit.
- **Always paginate** list queries and clamp page size server-side. Prefer **keyset (cursor) pagination** over `OFFSET` for large/infinite lists.
- Add indexes for every column used in `WHERE`/`ORDER BY`/filters; verify with `EXPLAIN`.
- Avoid N+1 queries (preload/join deliberately). Select only needed columns for list views.
- Escape `LIKE` wildcards (`%`, `_`) in user search terms, and set a max search length.
- Use transactions for multi-step writes; keep them short.
- Run migrations explicitly; don't rely on `AutoMigrate` in production without review.

**Frontend**
- Static assets: `Cache-Control: public, max-age=31536000, immutable` for hashed filenames; `no-cache` (ETag) for HTML.
- Load scripts with `defer`. Keep total JS tiny (htmx ≈ 14 kB gz, Alpine ≈ 15 kB gz). Don't add a bundler unless you must.
- Use `hx-boost` for progressive enhancement of links/forms where full navigations would otherwise occur.
- Prefetch with htmx's `preload` extension (loaded on `<body>` in this stack): `preload` or `preload="mouseover"` on links
  and `hx-get` elements whose GET is cheap and side-effect free (tabs, pagination, next pages). Use it sparingly: a
  preload is a real request (it carries `HX-Preloaded: true`) even if the user never clicks.
- Set explicit `width`/`height` on images, `loading="lazy"` below the fold, prefer SVG/AVIF/WebP.

## 8. Security

**Input & output**
- Treat all input (query, form, headers, path values, cookies) as hostile. Validate with explicit rules
  (e.g. `go-playground/validator` tags, length caps, enum checks like `Filter.IsValid()`), server-side, every time.
- Limit request bodies: `r.Body = http.MaxBytesReader(w, r.Body, 1<<20)`. Parse UUIDs/ints with strict parsers; reject, don't coerce.
- Do **not** bind client JSON/forms straight into DB models (mass assignment). Use DTOs and copy only allowed fields.
  Never trust client-supplied `ID`, `CreatedAt`, `DeletedAt`, `Role`, ownership fields — set them server-side.
- Use parameterised queries / ORM bindings only. Never build SQL with `fmt.Sprintf` or string concatenation.
- Rely on Templ's contextual auto-escaping (see §4). Set `Content-Type` explicitly; add `X-Content-Type-Options: nosniff`.

**Authentication & sessions** (when the app has users)
- Hash passwords with **argon2id** or **bcrypt** (`golang.org/x/crypto`). Compare with constant-time functions.
- Session cookies: `HttpOnly`, `Secure`, `SameSite=Lax` (or `Strict`), `Path=/`, short TTL, rotate on login, server-side revocable.
- Authorise **every** handler and every object access (check ownership, not just "is logged in"). Fetch by `(id, owner_id)`.
- Rate-limit login/search/mutations (per IP + per account), e.g. `golang.org/x/time/rate` middleware.

**CSRF**
- Cookie-authenticated mutations (POST/PUT/PATCH/DELETE) need CSRF protection: a per-session token sent via
  `hx-headers` / hidden input and verified server-side, plus `SameSite` cookies, plus Go 1.25+ `http.CrossOriginProtection`
  (Fetch-metadata/Origin check) as defence in depth. Never perform state changes on GET.

**Headers** (apply via middleware)
```
Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'; form-action 'self'
Strict-Transport-Security: max-age=63072000; includeSubDomains   (HTTPS only)
X-Content-Type-Options: nosniff
Referrer-Policy: strict-origin-when-cross-origin
Permissions-Policy: camera=(), microphone=(), geolocation=()
Cross-Origin-Opener-Policy: same-origin
```
- A strict CSP means **self-hosting htmx/Alpine and avoiding inline `<script>`**. If inline scripts are unavoidable,
  use per-request **nonces** (pass via context into the Layout) rather than `'unsafe-inline'`. Alpine needs its
  CSP build (`@alpinejs/csp`) when `'unsafe-eval'` is forbidden; htmx needs `allowEval=false`.
- Mind that htmx injects an indicator `<style>`; set `htmx.config.includeIndicatorStyles=false` (and ship your own CSS) or add a style nonce.

**Errors, logging, secrets**
- Never return `err.Error()` to clients (leaks internals, SQL, paths). Log details server-side with a request ID
  (`log/slog`, structured, JSON in prod); return a generic message/fragment to the user.
- Never log secrets, tokens, passwords, or full PII. Redact.
- Config comes from environment variables; `.env` is for local dev only and **must be git-ignored**. Provide `.env.example`.
  Validate config at startup and refuse to boot if required values are missing/invalid. Don't `panic` on a missing
  `.env` file in production (env may come from the platform).
- **Dev-only endpoints (seed, debug, pprof, reset) must be disabled in production** or sit behind authentication. Gate on config, not on obscurity.
- Serve static files from a dedicated directory only; disable directory listings (wrap `http.FileServer` or use `fs.Sub` + embed), never serve the project root or `.env`/`.db` files.

**Dependencies & supply chain**
- Commit `go.sum`; run `go mod tidy` and `go mod verify`. Run `govulncheck ./...` and (optionally) `gosec ./...` in CI.
- Keep Go itself current (stdlib security fixes ship in patch releases).
- Direct imports belong in the `require` block as direct (no `// indirect` for modules you import).
- Pin third-party browser assets (version + SRI) and review before upgrading.
- Container: multi-stage build, static binary (`CGO_ENABLED=0` where possible — note `mattn/go-sqlite3` needs CGO; `modernc.org/sqlite` does not),
  distroless/non-root final image, read-only filesystem, no secrets baked in.

## 9. Testing

- Table-driven unit tests for services, validators, helpers. Run with `-race`.
- Handler tests with `net/http/httptest` + fake repositories; assert status, headers (`HX-*`), and fragment contents.
- Repository tests against a real DB (in-memory SQLite or testcontainers) rather than mocks.
- Templ components: render to a buffer and assert on output (or use `goquery`) — verify escaping of hostile strings like `<script>alert(1)</script>`.
- E2E with Playwright-Go behind the `e2e` build tag (`go test -tags e2e`), so the fast suite stays fast and needs no browser.
- CI: `templ generate && git diff --exit-code`, `gofmt`, `go vet`, `go test -race ./...`, `govulncheck`, Tailwind build.

## 10. Accessibility & UX checklist

- Semantic HTML first (`<button>`, `<form>`, `<label>`, `<nav>`); every control keyboard-reachable with visible focus.
- Announce async updates: `aria-live="polite"` on toast/status regions; `aria-busy` on swap targets while loading.
- Swapping content should preserve focus intent (`hx-swap="... focus-scroll:false"` / manage focus explicitly after edits).
- Provide `<noscript>` fallbacks or progressive enhancement for critical flows when feasible (`hx-boost`, real `<form action>`).
- Every async action has: a loading state, a success signal, and a failure signal with a retry path.

## 11. Agent workflow checklist

Before finishing a task, verify:

- [ ] `templ generate` run; `_templ.go` files up to date
- [ ] `gofmt`/`templ fmt` clean; `go vet ./...` and `go test ./... -race` pass
- [ ] New input is validated, length-capped, and enum-checked server-side
- [ ] No user data reaches `templ.Raw`, SQL string concatenation, or inline script
- [ ] New mutating routes use the right verb, CSRF protection, and authorisation
- [ ] Errors are logged server-side and generic to the client
- [ ] Lists are paginated; new filter/sort columns are indexed
- [ ] Tailwind classes are literal strings and appear in scanned files
- [ ] No secrets committed; `.env.example` updated for new config
- [ ] Dev-only routes are not reachable in production

---

## Project-specific notes (edit per project)

**GOTTH+ template** — a splash page plus a small example app (a hello form driven by htmx and a counter driven by Alpine). Replace this paragraph with a description of your app.

- Module: `github.com/owenwexler/gotth-plus` (rename it in `go.mod` and the imports). Entry: `cmd/server/main.go`. Config via `.env` (`GoEnv`, `Port`, `DatabasePath`, `SeedAdminKey`); see `.env.example`.
- No Node and no npm anywhere: Tailwind is the standalone CLI, and htmx, its `preload` extension, Alpine (CSP build) and `hx-optimistic` are downloaded into `static/js` by `download-js.sh` (`make js`), which needs only git and curl.
- Dev workflow: `make setup` once, then `make dev` (Tailwind `--watch` plus `templ generate --watch` with its live-reload proxy on port 7331; templ starts the server for you). `make air` is the alternative (config in `air.toml`; excludes `*_templ.go`).
- Before finishing: `make check` (generate, format, vet, and `make test`: unit, integration and end-to-end tests). The browser tests need `make e2e-install` once (`make setup` does it).
- Data: SQLite (`app.db`) via GORM (`gorm.io/driver/sqlite`, CGO). `model.Example` is a placeholder model with a UUID primary key and soft-delete (`DeletedAt`).
- Wiring: routes are registered in `newMux` and the global middleware chain is built in `buildHandler`, both in `internal/app/app.go` (`app.NewHandler`, which `cmd/server/main.go` and the integration tests both call). Handlers are built in `internal/factory` (closures over repo/service/validator); `internal/handler` holds struct-based handlers (`HealthHandler`).
- Middleware order (outermost first): request ID, security headers, compression, rate limit, cross-origin protection, body limit, CSRF.
- Errors: htmx requests get an out of band toast with `HX-Reswap: none` (`respondWithToast` in `internal/factory`), or the error page with `HX-Reswap: innerHTML` (`respondWithErrorPage`), swapped into the request's target. `app.js` swaps any 4xx/5xx that sets `HX-Reswap`, and shows a generic error toast for network failures and other error responses. Full page loads get `view.ErrorPage`.
- Forms: `data-reset-on-success` clears a form, and `data-close-on-success` closes a `<dialog>` (refocusing its input on an error), only after a real 2xx/3xx answer. Disable submit buttons while in flight with `hx-disabled-elt`.
- Pagination: `repository.ConstantPageSize = 10`; `Paginate` clamps page size to 100.
- Frontend: htmx (with its `preload` extension; `hx-ext="optimistic, preload"` on `<body>`, opt in per element with `preload`) + Alpine + `hx-optimistic` loaded from `/static/js` in `internal/view/layout.templ`; Tailwind v4 (`static/input.css` → `static/output.css`, both `internal/view/*.templ` and `internal/styles/*.go` are scanned).
- Alpine uses the CSP build, which cannot evaluate inline objects or statements: register components with `Alpine.data` in `static/js/app.js` and reference them by name (`x-data="counter"`). Shared client state lives in the `globalState` Alpine store; its example fields (`pendingRequests`, `loading`, `online`) are kept current by the htmx and browser event hooks in `app.js`.
- Seeding: `POST /seed` (development only, optionally behind `SeedAdminKey`) loads `seedData.json`; run it with `make seed` / `./seed.sh`, which handles the CSRF token.
- Tests: unit tests sit next to their packages; integration tests (`cmd/server/integration_test.go`) drive the real mux and middleware with `httptest` on a throwaway SQLite file; E2E tests (build tag `e2e`) drive Chromium with Playwright-Go: the harness is `cmd/server/e2e_helpers_test.go` (one shared browser, a fresh context per test, `newE2EApp`/`newPage`, request and console-error recording, `delayRoute`/`failRoute`, `newTab`/`inContext`, `eventually`), app-independent checks are in `e2e_generic_test.go` (focus rings, live toast region, no third-party requests, no horizontal scroll, no-JS load, axe), and the example app's tests are in `e2e_test.go`. The axe scan runs when `AXE_CORE_JS` points at an `axe.min.js` (or one is at `cmd/server/testdata/axe.min.js`), and skips otherwise.
- The example app is confined to the places listed under "Removing the example app" in `README.md`.
