# Security

This document covers how to report a vulnerability in the GOTTH+ template, what the template
protects against out of the box, and what you must do yourself when you build an app on it.

GOTTH+ is a starting point, not a finished app. It ships safe defaults for the parts every app has
(headers, CSRF, rate limits, escaping, error handling). It ships **no authentication, no
authorisation and no user data**. Those are yours to add, and most of the guidance below is about
adding them without undoing the defaults.

For the day-to-day coding rules, see sections 4, 5 and 8 of [`AGENTS.md`](AGENTS.md). This file
explains the reasoning behind them and adds the deployment side.

---

## Reporting a vulnerability

Please **do not open a public issue** for a security problem.

Report it privately through GitHub: go to the repository's **Security** tab and choose
**Report a vulnerability**. Include:

- what is affected (file, route, middleware, or a dependency),
- steps to reproduce, or a minimal proof of concept,
- the impact you expect (for example "CSRF bypass on POST routes").

You should get an acknowledgement within a week. Fixes land on `main`; there are no other
supported branches or releases.

If you find a problem in **your own app** built from this template, report it to that app's
maintainers. If the root cause is in the template's defaults, a report here is welcome too.

---

## What the template does for you

| Protection | Where | Notes |
| --- | --- | --- |
| Strict Content-Security-Policy | `internal/middleware/security.go` | `script-src 'self'`, no `unsafe-inline`, no `unsafe-eval`, `frame-ancestors 'none'`, `form-action 'self'`, `object-src 'none'`. |
| Other security headers | `internal/middleware/security.go` | `X-Content-Type-Options: nosniff`, `Referrer-Policy`, `Permissions-Policy`, `Cross-Origin-Opener-Policy`. HSTS only when `GoEnv=production`. |
| CSRF (double-submit cookie) | `internal/csrf/csrf.go`, `internal/view/layout.templ` | 32-byte random token, `HttpOnly` + `SameSite=Lax` cookie (`Secure` in production), echoed by htmx in `X-CSRF-Token` via `hx-headers`, compared in constant time on every non-safe method. |
| Cross-origin protection | `internal/app/app.go` | Go's `http.CrossOriginProtection` rejects cross-site browser requests using `Sec-Fetch-Site` / `Origin`. Defence in depth next to the CSRF token. |
| Per-IP rate limiting | `internal/middleware/ratelimit.go` | Separate buckets for all requests and for mutations. Strict in production, loose in development. Static assets are exempt. |
| Request body cap | `internal/middleware/bodylimit.go` | 1 MiB on every request (`app.MaxBodyBytes`). |
| Server timeouts | `cmd/server/main.go` | `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`, `IdleTimeout`, `MaxHeaderBytes` are all set, which blunts slowloris-style attacks. |
| Output escaping | Templ | Templ escapes text, attributes and URLs by context. No `templ.Raw` in the template. |
| htmx hardening | `internal/view/layout.templ` | `allowEval:false`, `selfRequestsOnly:true`, `includeIndicatorStyles:false` (so the CSP needs no inline styles). |
| Alpine without eval | `static/js/app.js` | The Alpine CSP build is used; components are registered with `Alpine.data` and referenced by name. |
| Self-hosted scripts | `download-js.sh`, `static/js` | No third-party script hosts at runtime. The download script prints a `sha384` hash for each file. |
| Generic errors | `internal/factory/factory.go` | Clients see a generic toast or error page. Details go to the server log only. |
| Request IDs in logs | `internal/requestid` | Every log line carries `request_id`. Incoming IDs are kept only if short and alphanumeric, so they can't inject into logs. |
| Fail-fast config | `internal/config/config.go` | An unknown `GoEnv` refuses to boot instead of silently acting like development. |
| Dev-only routes removed in production | `internal/app/app.go` | `POST /seed` is not registered at all when `GoEnv=production`, and can be gated by `SeedAdminKey` in development. |
| No directory listings | `internal/app/app.go` (`staticFiles`) | Only files under `static/` are served, never a directory index. |
| Input validation | `internal/dto`, `internal/factory` | DTOs with `go-playground/validator` tags, length caps, trimmed input. |
| LIKE escaping | `internal/escaper` | Escapes `%`, `_` and `\` for literal search matches. |
| Tests | `internal/csrf`, `internal/middleware/protect_test.go`, `cmd/server/*_test.go` | Unit tests for CSRF and the middleware; E2E checks that no third-party requests are made. |

---

## Production checklist

Go through this before the first deploy and again after any large change.

**Configuration**

- [ ] `GoEnv=production`. This turns on HSTS, `Secure` cookies, JSON logs and strict rate limits,
      and removes `POST /seed`. Confirm `POST /seed` returns 404 on the live site.
- [ ] Config comes from the platform's environment or secret store, not from a `.env` file on the
      server. `.env` stays git-ignored.
- [ ] `DatabasePath` points outside `static/` and outside any directory the web server serves.
      The database file and its `-wal`/`-shm` files are readable only by the app's user (`chmod 600`).

**Network and TLS**

- [ ] The app is served **only over HTTPS**. HSTS is sent with `includeSubDomains`; make sure every
      subdomain can serve HTTPS before you ship it, or remove `includeSubDomains`.
- [ ] The server listens on `:Port` (all interfaces). In production, bind it to localhost or a
      private network and put a TLS-terminating reverse proxy in front, or firewall the port.
- [ ] If you run behind a proxy or load balancer, read
      [Rate limiting behind a proxy](#rate-limiting-behind-a-proxy). Without changes every user
      shares the proxy's IP and one rate-limit bucket.

**Frontend assets**

- [ ] `static/js/*.min.js` is **committed**, pinned to known versions, and reviewed. The template
      ignores those files only because it doesn't ship the libraries; delete that line from
      `.gitignore` in your app. Production must never run `download-js.sh` during a build, because it
      fetches the **latest** tag by default.
- [ ] Record the `sha384` hashes the download script prints, and check them when you upgrade.

**Code**

- [ ] `make check` passes. `govulncheck ./...` reports nothing that affects you.
- [ ] Every mutating route uses POST/PUT/PATCH/DELETE, never GET.
- [ ] Every route that touches user data checks authorisation (see below).
- [ ] No `templ.Raw`, `templ.SafeURL` or inline `<script>` carries user data.
- [ ] No dev-only or debug endpoint (pprof, seed, reset) is reachable in production.

---

## Best practices by layer

### Go and `net/http`

- **Keep Go current.** Many fixes for `net/http`, `crypto/tls` and `html/template` ship in patch
  releases. Bump `go.mod` and rebuild when a security release comes out.
- **Keep the timeouts.** Never replace the configured `http.Server` with bare
  `http.ListenAndServe`. Long-running endpoints (SSE, large exports) should get their own deadline
  with `http.NewResponseController(w).SetWriteDeadline`, not a bigger global `WriteTimeout`.
- **Keep the body limit.** If one route needs uploads, raise the limit **for that route only** and
  check the file's type and size yourself. Don't trust the client's `Content-Type` or filename.
- **Pass `r.Context()` down** to the database so cancelled or timed-out requests stop doing work.
- **Parse strictly.** Use `uuid.Parse` and `strconv.Atoi` and reject bad input with a 400. Never
  fall back to a default ID or silently coerce.
- **Don't `panic` in request paths.** Return errors and map them to status codes in one place.

### Middleware order

The chain in `buildHandler` runs outermost first:

```
request ID -> security headers -> compression -> rate limit -> cross-origin -> body limit -> CSRF -> routes
```

Cheap rejections run before any handler work, and every response (errors included) gets the
security headers. When you add middleware:

- **Authentication** goes inside CSRF (after it in the list), so a forged request is rejected
  before any session lookup.
- Anything that **sets cookies or headers** must run before the handler writes the body.
- Don't put anything that reads the body before the body limit.

### Templ (output)

- Templ escapes by context. The main way to create an XSS hole is to opt out:
  `templ.Raw`, `@templ.Raw(...)`, `templ.SafeURL`, `templ.SafeCSS`. Don't use them on anything a
  user can influence. If you must render user HTML (for example Markdown), sanitize it with an
  allow-list sanitizer such as `bluemonday`, and leave a comment saying why it is safe.
- **URLs:** build them with `templ.URL(...)`, which blocks `javascript:` URLs. Still check that a
  user-supplied link is `http`, `https` or relative before storing it.
- **Data for scripts:** use `templ.JSONScript` or `data-*` attributes read from `app.js`. Never build
  JavaScript strings from user data, and never write inline event handlers (`onclick=`); the CSP
  blocks them anyway.
- Test escaping: render a component with `<script>alert(1)</script>` and `"><img src=x onerror=…>`
  and assert the output is escaped.

### htmx

- **Keep the config** in `layout.templ`: `allowEval:false` and `selfRequestsOnly:true`. Don't turn
  on `hx-on` handlers or `js:` expressions that need eval.
- **Neutralise htmx attributes in user content.** Escaping stops `<script>`, but if user HTML is
  rendered on purpose (sanitized Markdown, rich text), an attacker could inject `hx-get`,
  `hx-post` or `hx-trigger`. Put `hx-disable` on the container that holds user content, and make
  sure your sanitizer strips `hx-*` and `data-hx-*` attributes. (If you move to htmx 4, check the
  name of this attribute in its migration guide.)
- **Never mutate on GET.** htmx makes it easy to put `hx-get` on anything; GET requests skip the
  CSRF check by design.
- **Fragment endpoints are still endpoints.** Anyone can call them directly without htmx. Each one
  needs the same validation and authorisation as a full-page route. `HX-Request` is a client
  header and proves nothing.
- **Response headers are trusted instructions.** `HX-Redirect`, `HX-Location` and `HX-Push-Url`
  must never contain an unchecked, user-supplied URL (open redirect). Use paths you build yourself.
- Set `Vary: HX-Request` on responses that differ by that header so caches don't serve a fragment
  as a page, or the reverse.

### Alpine.js

- Stay on the **CSP build**. The standard build needs `unsafe-eval`, which would weaken the CSP for
  the whole site.
- Register components and stores in `static/js/app.js`. `x-data` attributes should only name a
  component, never contain expressions built from server data.
- Avoid `x-html`. It writes raw HTML into the page. Use `x-text`.
- Alpine state lives in the browser and can be changed by the user. Never use it for decisions
  the server must enforce.

### Tailwind and static files

- Tailwind runs at build time only and adds no runtime risk. Keep it that way: no CDN play script.
- `static/` is public. Never put the database, `.env`, backups, source maps you don't want public,
  or uploaded user files you haven't checked into it.
- Serve fonts and icons from `static/`. Third-party font or icon CDNs break the CSP and leak
  visitors' IPs to that host.

### Data: SQLite and GORM

- **Use bound parameters.** `db.Where("name = ?", name)` is safe. `db.Where("name = '" + name + "'")`
  and `fmt.Sprintf` into SQL are not.
- **GORM methods that take raw SQL fragments** (`Order`, `Select`, `Group`, `Having`, `Joins`,
  `Raw`, `Exec`, and `Where` with a single string) are injection points if you pass user input
  into the SQL text. For user-chosen sorting or columns, map the input through an allow-list:
  ```go
  var sortColumns = map[string]string{"name": "name", "newest": "created_at DESC"}
  col, ok := sortColumns[r.FormValue("sort")]
  if !ok { col = "created_at DESC" }
  db.Order(col)
  ```
- **No mass assignment.** Never decode a form or JSON body straight into a `model` struct. Use a
  DTO from `internal/dto`, validate it, then copy allowed fields. Set `ID`, `CreatedAt`,
  `DeletedAt`, ownership and role fields on the server.
- **Escape LIKE input** with `escaper.Like` and `ESCAPE '\'`, and cap search length.
- **Paginate and clamp.** `repository.Paginate` caps page size at 100; keep a cap on every list so
  one request can't dump a whole table.
- **Soft delete is not deletion.** `model.Example` uses `DeletedAt`, so "deleted" rows stay on disk.
  If users can ask for their data to be erased, hard-delete it (`Unscoped().Delete`) and think about
  backups.
- `AutoMigrate` runs only in the dev-only seed route. In production, run reviewed migrations
  explicitly.
- `mattn/go-sqlite3` is a C library. Keep it updated and watch `govulncheck` for it.

### Authentication and sessions (when you add users)

The template has none of this yet. When you add it:

- **Passwords:** hash with argon2id or bcrypt from `golang.org/x/crypto`. Never store or log the
  plain password. Compare hashes with the library's verify function (it is constant-time).
- **Session cookie:** random ID (at least 32 bytes from `crypto/rand`), `HttpOnly`, `Secure`,
  `SameSite=Lax` or `Strict`, `Path=/`. Use the `__Host-` name prefix (for example
  `__Host-session`) so subdomains can't overwrite it. Store sessions server-side so you can revoke
  them.
- **Rotate on login and privilege change.** Issue a new session ID, and a new CSRF token, when a
  user logs in, logs out, or changes password. This prevents session fixation.
- **Rate-limit logins per account as well as per IP**, and return the same message for "no such
  user" and "wrong password".
- **Authorise every object access**, not just "is logged in". Fetch with the owner in the query:
  ```go
  db.Where("id = ? AND owner_id = ?", id, userID).First(&task)
  ```
  Return 404 (not 403) for objects the user doesn't own, so IDs can't be probed.
- Don't put session IDs or tokens in URLs; they leak through history, logs and `Referer`.

### CSRF

- The token is sent automatically by htmx through `hx-headers` on `<body>`. **Plain HTML forms**
  (without htmx, or with `hx-boost` off) don't send that header; add a hidden input and read it in
  the middleware, or keep every mutating form on htmx.
- The CSRF cookie is named `csrf_token`. For a deployed app, consider renaming it to
  `__Host-csrf_token` (requires `Secure`, `Path=/`, no `Domain`). This stops a compromised or
  untrusted subdomain from planting its own token cookie, the main weakness of double-submit
  cookies. Note that `__Host-` cookies are rejected over plain HTTP, so keep the plain name in
  development.
- If you add sessions, consider tying the token to the session (store it server-side, or HMAC it
  with the session ID) and rotating it on login.
- Requests with no `Origin`/`Sec-Fetch-Site` headers (curl, scripts) pass the cross-origin check
  but still need the token. Keep both checks.

### Rate limiting behind a proxy

`middleware.RateLimit` keys on the TCP peer address and ignores `X-Forwarded-For` on purpose,
because clients can forge it. Behind a reverse proxy every request comes from the proxy's address,
so all users share one bucket and one busy user can lock everyone out. Choose one fix:

1. **Rate-limit at the proxy** (nginx `limit_req`, Caddy, your CDN or load balancer), and loosen or
   remove the in-app limits, or
2. **Trust the forwarded IP only from your proxy:** add middleware in front of `RateLimit` that
   replaces `r.RemoteAddr` with the client IP from `X-Forwarded-For` **only when** the peer address
   is your proxy. Take the right-most address your proxy appended, not the left-most one the
   client sent.

Limits are kept in memory per process. With several instances, each one has its own counters;
use the proxy or a shared store if you need a global limit.

### Errors, logging and secrets

- Never send `err.Error()` to the client. Use `respondWithToast` / `respondWithErrorPage`, which log
  the details and show a generic message.
- Never log passwords, session IDs, CSRF tokens, `X-Admin-Key`, full request bodies, or personal
  data. Be careful logging whole headers or form values.
- Logs are written to stderr. Ship them somewhere with access control and a retention limit.
- `SeedAdminKey` is a development convenience, not real auth. It only matters in development,
  because the route doesn't exist in production.
- If a secret is ever committed, **rotate it**. Removing it from git history is not enough.

### Dev-only and debug endpoints

- Register them inside `if !env.IsProduction()` in `newMux`, like `POST /seed`. A route that isn't
  registered can't be reached, which is safer than a runtime check inside the handler.
- If you need `pprof` in production, serve it on a separate listener bound to localhost, never on
  the public mux.
- `/healthz` is public. Keep it to a simple status; don't add version numbers, config or stack
  details.

### Dependencies and supply chain

- Commit `go.sum`. Run `go mod verify` and `govulncheck ./...` in CI and before releases. `gosec`
  is a useful extra.
- Review new Go dependencies before adding them; fewer is better. The template avoids Node and npm
  entirely to keep the JavaScript supply chain small. Keep it that way.
- **Browser libraries:** pin versions when you run `download-js.sh`
  (`HTMX_VERSION=… ALPINE_VERSION=… HX_OPTIMISTIC_VERSION=… ./download-js.sh`), commit the
  results, and compare the printed `sha384` against the upstream release before upgrading. Read
  release notes for security fixes.
- `playwright-go` is a test-only dependency (behind the `e2e` build tag) and is not compiled into
  the server binary.

### Containers and hosting

- Multi-stage build; final image distroless or minimal, running as a **non-root** user.
- `mattn/go-sqlite3` needs CGO, so the binary is not fully static. Use a base image with a
  compatible libc, or switch to `modernc.org/sqlite` (pure Go) if you want `CGO_ENABLED=0`.
- Read-only root filesystem, with a single writable volume for the SQLite file.
- No secrets baked into the image or build args; pass them at runtime.
- Back up the database, and protect the backups like the database itself.

---

## Testing security

The template's tests are a good base. When you add features, add tests for:

- **CSRF:** a mutating request without the header, with a wrong header, and with no cookie returns 403.
- **Authorisation:** user A cannot read, update or delete user B's objects (expect 404).
- **Escaping:** hostile strings render escaped in every component that shows user data.
- **Validation:** too-long, empty, and wrong-type input is rejected with a 4xx and a generic message.
- **Headers:** responses carry the CSP and other security headers, including error responses.
- **Production mode:** with `GoEnv=production`, dev-only routes return 404 and cookies are `Secure`.
- **No third-party requests:** the existing E2E check in `e2e_generic_test.go` catches a CDN script
  or font sneaking in.

---

## Known limitations of the template

- No authentication, authorisation or sessions. Every route is public.
- The CSRF token is not tied to a session (there are no sessions) and uses a plain cookie name.
- Rate limits are in-memory, per process, and keyed on the TCP peer address (see above).
- HSTS is sent but the app does not terminate TLS itself; that is the proxy's or platform's job.
- `download-js.sh` fetches the latest library versions unless you pin them.
