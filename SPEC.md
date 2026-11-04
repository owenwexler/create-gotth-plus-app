# What is this?
This is to be a command-line utility to scaffold a new app using the opinionated [GOTTH+](https://github.com/owenwexler/gotth-plus) Stack of Go, Tailwind, Templ, HTMX, HX-Optimistic, and Alpine.JS-CSP for web apps.  It is to be a question prompt CLI scaffold tool similar to [Create-T3-App](https://create.t3.gg) that asks the user some questions and sets up a GOTTH+ starter according to the answers.  Thank you!

# UI Library 
[charmbracelet/huh](https://github.com/charmbracelet/huh)

# The Opinions: What Parts Of The Stack Do We Want To Keep No Matter What 
* Go 
** Go is the backbone of this stack and is a must.  1.27.3 earliest supported version for security reasons
* Templ 
** Templ is another critical part of the stack and must be supported 
* Tailwind
** The use of Tailwind v4 for styling and CSS is another foundation of this stack.  Support only for version 4 for simplicity
* HTMX 
** Version 2 as the one stable version.  Support for version 4 can come down the line later.
* Alpine 
** CSP version is non-negotiable, must be CSP version for security reasons
* Brotli/GZIP compression
* HX-Optimistic
** HX-Optimistic is essential for app performance, so its inclusion should be mandatory
* Directory structure 
* Almost all evergreen boilerplate code
* All architectural and security decisions, adapted to auth while being kept as secure as possible
* Generic helpers (Ternary, Count)
* Generic non-Alpine components written by myself or AI agents (toast, button)
* GORM - perhaps let us keep GORM as a mandatory piece for simplicity for now and only change this if enough users want other options
* AGENTS.md, CLAUDE.md, and SECURITY.md, as written, except for changes for database and auth
* **Tooling:** `Makefile` (all targets and `make help`), `download-js.sh` (git-tag version lookup, temp-file downloads, sha384 output), `seed.sh` (gets the CSRF cookie first), `air.toml`, `tailwind.config.js` (imports nothing so the standalone CLI can read it), `static/input.css`, `.env.example` comments, `.gitignore`, `static/font/.gitkeep`, `static/icon/.gitkeep`.
* **No Node / no npm / no bundler.** README says this first, and the auth choices above can break it. Worth making it an explicit opinion.
* **Strict CSP with only self-hosted scripts**, and the `TestE2ENoThirdPartyRequests` test that enforces it.
* **Middleware packages and order:** request ID, security headers, compression, rate limit, cross-origin protection, body limit, CSRF (`internal/app/app.go` `buildHandler`), plus the dev-only trusted origins for the 7331 proxy.
* **Packages:** `csrf`, `requestid` (and its slog handler), `escaper`, `optimistic`, `styles`, `middleware/adminkey`, `dto` + `go-playground/validator`, sentinel errors in `repository`, the `factory` handler pattern, `respondWithToast` / `respondWithErrorPage`.
* **Server setup in `main.go`:** timeouts, graceful shutdown, closing the DB after shutdown, JSON logs in production.
* **`GoEnv` behaviour:** fail-fast config, production removes `/seed`, enables HSTS, `Secure` cookies, strict rate limits.
* **`/healthz`** that pings the database.
* **`app.js` behaviour:** `globalState` store, htmx error/toast hooks, `data-reset-on-success` and `data-close-on-success`, network-error toast, hx-optimistic rollback.
* **Templ components:** spinner and text components, plus toast and buttons (already listed).
* **Test setup:** unit tests beside packages, `integration_test.go` using the real `app.NewHandler`, `testutil_test.go`, the e2e harness and generic tests, the `e2e` build tag.
* **Committed `*_templ.go` files.** The repo commits them, so the generator should run `templ generate` after any `.templ` change it makes.

# What Do We Want The User To Be Able To Choose?
* Name of project
* Project module path (different from project name)
* Auth - see the Auth section (asked before the database, because Supabase fixes the database to Postgres)
* Which database to use (skipped when Supabase is chosen)
** Choices: Postgres, MySQL, SQLite
* SQLite driver: CGO or pure Go
* Include Playwright E2E tests?
* Download axe-core for the accessibility test?
* Download and commit the JavaScript libraries now?
* Live reload: templ proxy or air
* Initialize git and make a first commit?
* Run `make setup` now?
* Dockerfile / deploy target
* CI (GitHub Actions)
* Which PORT to use?
* Generate a random SeedAdminKey?

# Also Worth Having
* **Non-interactive flags** for every prompt (`--db=postgres --yes` etc), so CI and AI agents can run it without a terminal. create-t3-app has `--CI` for this.
* **Prerequisite check** at start: Go ≥ 1.27.3, `templ`, `tailwindcss` v4, `git`, `curl`, `openssl` (used by `download-js.sh`), `make`, a C compiler only when CGO SQLite is chosen, Docker only when Postgres/MySQL + compose is chosen. Warn with install links instead of failing late.
* **Refuse to write into a non-empty directory** unless `--force`.
* **Summary screen** of all answers before generating.

# What We Do Not Want To Include 
* [Alpine components](https://www.alpinejs.dev) (dropdowns etc.)
** These are paywalled, so it needs to be up to the user to add them.

# Auth
The considerations brought up are very important.  Auth is a must-have for the vast majority of web apps, and support for frequently used auth libraries needs to happen alongside security considerations. 
You're correct that BetterAuth-Go is mostly supported via sidecar and is an edge case that GOTTH+ does not need to support at this time. 

## Decisions for version 1

### Prompts
```
Auth?  None | Built-in | OIDC (WorkOS/Logto/Kinde) | Supabase
  Built-in → which methods? (pick one or more)
             [x] Email + password  [ ] Google  [ ] GitHub  [ ] Apple
  OIDC     → which provider preset? WorkOS | Logto | Kinde | Auth0 | Zitadel | Keycloak | Other (enter issuer URL)
Database?  Postgres | MySQL | SQLite   (skipped for Supabase: Postgres)
```
* Default: **None** (the repo as it is today).
* Non-interactive flags: `--auth=none|builtin|oidc|supabase`, `--auth-methods=password,google,github,apple`, `--oidc-preset=<name>`.

### Not in version 1
* **Clerk:** the sign-in UI and its 60-second session refresh run in ClerkJS, loaded from Clerk's servers. Supporting it means loosening the strict security policy and the no-third-party-requests test. Can be reconsidered later as a clearly labelled lower-security option.
* **PocketBase:** embedding it replaces the router, the database and GORM. If added later, run it as a separate service only.
* **BetterAuth-Go:** needs a Node sidecar.

### Shared core (every option except None)
Each provider only answers "who is this user?". Everything after that is the same code:
* `internal/auth`: an `Identity{Provider, Subject, Email, EmailVerified, Name}` type, plus one small interface per sign-in method.
* `model.User`, and `model.Identity` keyed by (provider, subject), not by email. One user can have several identities.
* **Sessions:** `alexedwards/scs` with its GORM store. Sessions are stored on the server and can be revoked. Cookie: `__Host-session` in production (plain name in development, because `__Host-` needs HTTPS), `HttpOnly`, `Secure`, `SameSite=Lax`, idle and absolute timeouts.
* **Login:** `RenewToken()` rotates the session, and the CSRF token is rotated too. Tie the CSRF token to the session.
* **Logout:** POST only. Send it with htmx and answer with `HX-Redirect`, not a plain form redirect (see "Security policy and redirects").
* **Middleware:** `LoadSession` (global, placed after CSRF) and `RequireUser` (per route). Everything else in the chain stays the same.
* **Ownership:** the example app gets `owner_id`, and the repository fetches by (id, owner_id), returning 404 for rows the user doesn't own.
* **Rate limits:** login, signup, password reset and OAuth callbacks are limited per account as well as per IP.
* **Seed:** creates a dev user (password from `.env`, or a fixed identity for the fake OIDC server).
* **Views:** login page, account page, and a signed-in/signed-out header built from the existing button, text and toast components.
* **Account linking by verified email:** when a new identity's email matches an existing user, link them only if the provider says the email is verified (`email_verified`, or GitHub's verified primary email) *and* the existing user's email is verified too. Otherwise create no link and show a generic message.
* **Provider tokens:** not stored unless the app needs to call the provider's API. If stored, encrypted with a key from `.env`.

### Built-in: email + password
* Hash with argon2id (`golang.org/x/crypto/argon2`), with the parameters stored next to each hash so they can be raised later.
* When the email isn't found, hash a dummy password anyway, so response time doesn't reveal which emails have accounts.
* Signup, login and password reset give the same message whether or not the account exists.
* Password rules follow NIST SP 800-63B-4: no character-type rules and a long maximum length. Check the current minimum before release (believed to be 15 characters when the password is the only factor).
* Email verification and password reset links: single-use, short-lived random tokens, stored hashed.
* `Mailer` interface: an SMTP version for production, and a development version that logs the link instead of sending it. `.env` gets the SMTP settings only when this method is chosen.

### Built-in: OAuth (Google, GitHub, Apple)
* `golang.org/x/oauth2` with PKCE, plus `state` and `nonce` checks. The state is kept in a short-lived cookie.
* **Google:** OIDC, so check the ID token with `coreos/go-oidc`.
* **GitHub:** not OIDC (no ID token). Call `/user` and `/user/emails`, and use only the verified primary email.
* **Apple:**
  * When name and email are requested, Apple returns the user with a cross-site POST (`response_mode=form_post`). The callback route needs a narrow exception from the CSRF middleware and `CrossOriginProtection`, and the state cookie must be `SameSite=None; Secure`.
  * The client secret is a signed token (ES256) that expires after at most 6 months, so the app generates it from the `.p8` key file at startup.
  * Apple sends the user's name only on the first login, so save it then.
* Each provider's client ID and secret goes into `.env`, and only for the providers chosen.

### OIDC (WorkOS/Logto/Kinde)
* One generic path: `coreos/go-oidc` discovery + `x/oauth2` with PKCE. The presets only fill in the issuer URL format and the docs links.
* `.env`: `OIDCIssuerURL`, `OIDCClientID`, `OIDCClientSecret`, `OIDCRedirectURL`.
* Logout clears the local session and then sends the user to the provider's `end_session_endpoint` when there is one.
* **Verify before release:** does WorkOS AuthKit support standard OIDC discovery? If not, its preset needs a small adapter using `workos-go`, behind the same interface.
* The user signs in on the provider's page, with no third-party JS on our pages, so the strict security policy stays unchanged.

### Supabase
* Fixes the database to Postgres (the Supabase project's Postgres, via GORM).
* The Go SDK is in alpha, so call the Supabase Auth REST API directly.
* Check Supabase's tokens against its published keys (JWKS), then create our own server-side session. Logout and revoking then work the same as the other options.

### Security policy and redirects (applies to every option)
* Chrome applies `form-action 'self'` to redirects that follow a form submission. So a POST form that redirects to Google or the OIDC provider gets blocked.
* Start login with a plain GET link (`/auth/{provider}/login`). It changes nothing on the server except the short-lived state cookie.
* Do logout as an htmx POST answered with `HX-Redirect`, which is a script navigation, not a form submission.
* That way the security policy stays exactly as it is today for every v1 option.

### Tests
* A small fake OIDC server built on `httptest` (discovery, JWKS, authorize, token endpoints), used by integration and E2E tests. The full login flow runs offline for Built-in OAuth, OIDC and Supabase-style token checks.
* New tests for:
  * CSRF and session rotation on login
  * logout revoking the session
  * user A unable to see user B's rows (404)
  * account linking refused when either email is unverified
  * the Apple callback exception, which accepts only that one route
* `TestE2ENoThirdPartyRequests` stays unchanged.

### Docs to update per choice
* **SECURITY.md:**
  * Replace "Authentication and sessions (when you add users)" with a description of what the chosen option does.
  * Update the "Known limitations" lines about no auth and the CSRF token not being tied to a session.
* **AGENTS.md** "Project-specific notes": auth packages, middleware, and how to protect a new route.
* **README.md:** routes table (login, callback, logout, account) and the `.env` settings.

### Test combinations
Database (3) × auth (None, Built-in, OIDC; Supabase only with Postgres). For Built-in, test each method alone and all methods together.

---

# Suggestions (review of SPEC.md against the gotth-plus repo, 2026-11-02)

Everything below is a suggestion. Nothing above this line was changed. Items marked **Decide** need your call.

## 1. Notes on the choices already listed

### Database (Postgres, MySQL, SQLite)
#### Answer: Config, AGENTS.md, SECURITY.md, Drivers should change based on user database choice.  .env etc changes based on database choice.
#### Testing for Postgres & MySQL: Require Postgres/MySQL to be installed locally, create a throwaway local Postgres or MySQL database locally in testing.  Users have option to use a cloud DB, just point settings to cloud URL, or use a local DB, just point settings to local DB.
The repo is SQLite-only today, so each choice touches more than `internal/database`:
* **Config:** `DatabasePath` (a file) becomes a `DatabaseURL` DSN for Postgres/MySQL. Changes `internal/config/config.go`, `.env.example`, and the fail-fast checks.
* **Pool and pragmas:** AGENTS.md §7 asks for pool settings (all DBs) and WAL, `busy_timeout`, `foreign_keys=ON` (SQLite). `database.ConnectDb` sets none of these yet. The generator could add the right block per DB.
* **Drivers:** Postgres → `pgx`; MySQL → `go-sql-driver/mysql` (needs `parseTime=true`); SQLite → see the CGO question in §2.
* **Local dev:** Postgres/MySQL need a running server. Suggest generating a `compose.yaml` for them and adding `make db-up` / `make db-down`.
* **Tests:** `cmd/server/integration_test.go` uses a throwaway SQLite file. For Postgres/MySQL, pick one: testcontainers-go, a `TEST_DATABASE_URL` that tests skip without, or keep SQLite for tests only (not recommended, since the SQL can behave differently).
* **SQL differences to check:** UUID column types (`uuid` in Postgres, `CHAR(36)`/`BINARY(16)` in MySQL); `escaper.Like` with `ESCAPE '\'` (in MySQL a backslash inside a string literal needs to be written `'\\'`); `ON CONFLICT DO NOTHING` in `CreateMultiple` (MySQL uses `INSERT IGNORE`; GORM handles it, sqlx does not).
* **Text to update:** `.gitignore` (`*.db`, only for SQLite), README prerequisites ("a C compiler"), SECURITY.md "Data: SQLite and GORM", the `DatabasePath` checklist item, and the "Containers and hosting" notes.

### ORM / no ORM (GORM vs sqlx)
#### Answer: Let's make GORM a mandatory part of the stack for simplicity.
* With sqlx, these GORM-only pieces need rewriting: `repository.Paginate` (a GORM scope), `repository/example.go`, `seed.go` (`AutoMigrate`, `clause.OnConflict`), `handler.HealthHandler` (pings through `*gorm.DB`), `closeDB` in `main.go`, and the `*gorm.DB` arguments in `app.NewHandler`/`newMux`.
* sqlx has no `AutoMigrate`, so it needs a migration tool. Suggest `pressly/goose` (works as a library and a CLI) with a `migrations/` folder and `make migrate`. Worth offering goose for GORM too, since SECURITY.md already says not to use `AutoMigrate` in production.
* **Decide:** add **sqlc** as a third option? It generates type-safe Go from SQL, and fits a stack that already generates code with templ.
* For sqlx, SECURITY.md's GORM-specific notes (`Order`, `Select`, `Raw`...) need an sqlx version (`sqlx.In`, `Rebind`, named queries).

### Auth library (Clerk, BetterAuth-Go, Goth, PocketBase)
#### Answered in new Auth section
Each one conflicts with something you listed as non-negotiable, so each needs a decision:
* **Clerk:** server-side verification in Go is fine (`clerk-sdk-go`), but the sign-in UI and the short-lived `__session` token refresh depend on ClerkJS, loaded from Clerk's domain. That breaks `script-src 'self'` / `connect-src 'self'` in `middleware/security.go`, and `TestE2ENoThirdPartyRequests` would fail. **Decide:** allow a CSP exception for Clerk only (and change that test to allow-list Clerk's host), or drop Clerk.
* **BetterAuth-Go:** Better Auth is a TypeScript library. Please confirm which Go package you mean and that it is maintained. If it needs a Node sidecar, it goes against "no Node, no npm".
* **Goth:** handles OAuth sign-in only (Google, GitHub, ...). It has no sessions or users table, so the generator would also need to create sessions (server-side, `__Host-session` cookie, rotation on login, as SECURITY.md describes), a `users` table, and the login/logout/callback routes.
* **PocketBase:** a whole backend with its own router, SQLite database, migrations, and admin UI. It doesn't combine with the database choice, the ORM choice, or the `net/http` mux. **Decide:** drop it, or offer it as a separate "PocketBase mode" that skips the DB and ORM questions.
* **Suggest adding "None"** (the default; the repo ships with no auth today), and possibly a **"built-in email + password"** option (argon2id, server-side sessions) that follows AGENTS.md §8 and SECURITY.md exactly, with no third parties.
* For any auth choice: auth middleware goes after CSRF (see SECURITY.md "Middleware order"), the CSRF token should be tied to the session and rotated on login, and SECURITY.md "Known limitations" ("No authentication...") needs updating.

### Example app vs. blank page
#### Answer Let's keep the example app for simplicity for now.
* The "Removing the example app" list in README.md is the exact list of edits for "blank". The generator should also remove that README section once it has done them.
* **Decide:** with "blank", keep `model.Example`, `repository/example.go`, `seedData.json` and `/seed` as placeholders, or remove them as well? The README calls them placeholders, separate from the example app.
* Keep `e2e_helpers_test.go` and `e2e_generic_test.go` either way. Check that the generic tests still pass on the bare splash page (e.g. the toast live region and focus-ring checks need something to look at).

### HTMX version
* `download-js.sh` defaults to htmx 2 (`HTMX_MAJOR`), and `app.js` plus the `layout.templ` config use htmx 2 event and config names. htmx 4 would need a ported `app.js`, `layout.templ`, the `hx-disable` note in SECURITY.md, and whatever e2e tests depend on event names.
* **Check** that `hx-optimistic` works with htmx 4 before offering it. Since hx-optimistic is mandatory, it decides whether htmx 4 can be offered at all.
* Suggest a prompt: "htmx 2 (stable) / htmx 4", and keep two `app.js` versions in the template.

## 2. Additional prompts to consider
### Implement all of these except HTMX version which we are pinning to 2 for now
| Prompt | Why | Suggested default |
|---|---|---|
| **Go module path** (separate from project name) | `go.mod` says `github.com/owenwexler/gotth-plus`, and every import uses it. Project `my-app` might live at `github.com/acme/my-app`. | `github.com/<git user>/<project>` |
| **SQLite driver: CGO or pure Go** (SQLite only) | `mattn/go-sqlite3` needs a C compiler and blocks `CGO_ENABLED=0` / distroless images. SECURITY.md already mentions `modernc.org/sqlite` (GORM driver: `glebarez/sqlite`). | pure Go |
| **htmx 2 or 4** | See above. | 2 |
| **Include Playwright E2E tests?** | `make setup` downloads Chromium (large). Some people won't want it. | yes |
| **Download axe-core for the accessibility test?** | It is skipped without `cmd/server/testdata/axe.min.js`. The CLI could fetch it. | yes |
| **Download and commit the JS libraries now?** | README and SECURITY.md both say to delete the `static/js/*.min.js` line from `.gitignore` in your own app. The CLI can do this, pin versions, and record the sha384 hashes. | yes |
| **Live reload: templ proxy or air** | Both are in the Makefile. Could keep both and skip this prompt. | templ proxy |
| **Initialize git and make a first commit?** | Like create-t3-app. | yes |
| **Run `make setup` now?** | Needs network, templ, and tailwindcss on PATH. | yes |
| **Dockerfile / deploy target** (none, Docker, Fly.io, Railway...) | SECURITY.md describes multi-stage, non-root, distroless images, so a generated Dockerfile should follow that. | Docker |
| **CI (GitHub Actions)** | Run `make check`, `templ generate && git diff --exit-code` (AGENTS.md §4), `go mod verify`, `govulncheck`. | yes |
| **Port** | `.env.example` has 3000; dev proxy uses 7331. | 3000 |
| **Generate a random `SeedAdminKey`?** | Small security win in `.env`. | yes |

Also worth having:
* **Non-interactive flags** for every prompt (`--db=postgres --orm=sqlx --auth=none --blank --yes`), so CI and AI agents can run it without a terminal. create-t3-app has `--CI` for this.
* **Prerequisite check** at start: Go ≥ 1.27.3, `templ`, `tailwindcss` v4, `git`, `curl`, `openssl` (used by `download-js.sh`), `make`, a C compiler only when CGO SQLite is chosen, Docker only when Postgres/MySQL + compose is chosen. Warn with install links instead of failing late.
* **Refuse to write into a non-empty directory** unless `--force`.
* **Summary screen** of all answers before generating.

## 3. Things in the repo to preserve that aren't in the list yet
### Added to The Opinions
Add these to "The Opinions" so they are not lost:
* **Tooling:** `Makefile` (all targets and `make help`), `download-js.sh` (git-tag version lookup, temp-file downloads, sha384 output), `seed.sh` (gets the CSRF cookie first), `air.toml`, `tailwind.config.js` (imports nothing so the standalone CLI can read it), `static/input.css`, `.env.example` comments, `.gitignore`, `static/font/.gitkeep`, `static/icon/.gitkeep`.
* **No Node / no npm / no bundler.** README says this first, and the auth choices above can break it. Worth making it an explicit opinion.
* **Strict CSP with only self-hosted scripts**, and the `TestE2ENoThirdPartyRequests` test that enforces it.
* **Middleware packages and order:** request ID, security headers, compression, rate limit, cross-origin protection, body limit, CSRF (`internal/app/app.go` `buildHandler`), plus the dev-only trusted origins for the 7331 proxy.
* **Packages:** `csrf`, `requestid` (and its slog handler), `escaper`, `optimistic`, `styles`, `middleware/adminkey`, `dto` + `go-playground/validator`, sentinel errors in `repository`, the `factory` handler pattern, `respondWithToast` / `respondWithErrorPage`.
* **Server setup in `main.go`:** timeouts, graceful shutdown, closing the DB after shutdown, JSON logs in production.
* **`GoEnv` behaviour:** fail-fast config, production removes `/seed`, enables HSTS, `Secure` cookies, strict rate limits.
* **`/healthz`** that pings the database.
* **`app.js` behaviour:** `globalState` store, htmx error/toast hooks, `data-reset-on-success` and `data-close-on-success`, network-error toast, hx-optimistic rollback.
* **Templ components:** spinner and text components, plus toast and buttons (already listed).
* **Test setup:** unit tests beside packages, `integration_test.go` using the real `app.NewHandler`, `testutil_test.go`, the e2e harness and generic tests, the `e2e` build tag.
* **Committed `*_templ.go` files.** The repo commits them, so the generator should run `templ generate` after any `.templ` change it makes.

## 4. "AGENTS.md, CLAUDE.md, SECURITY.md, as written" needs one exception
### Keeping these as templates with per-setting changes is a good idea
These files describe SQLite + GORM, the example app, and "no auth". After the user's choices they would be wrong in places:
* **AGENTS.md:** the "Project-specific notes" section (data layer, example app, tests). AGENTS.md itself says this is the only section meant to change per project, so the generator should rewrite just that section.
* **SECURITY.md:** "Data: SQLite and GORM", the `DatabasePath` checklist item, CGO notes, "Known limitations" (when auth is chosen), and the reporting section, which points to the template's GitHub Security tab rather than the new app's.
* **README.md:** title, prerequisites, routes table, project structure, "Removing the example app".

Suggest keeping these as templates with small marked blocks per choice, so the rest stays word-for-word.

## 5. Implementation notes
### Implmentation notes implemented.  Let's embed with go:embed, and install with go install/go run as suggested
* **PromptUI** (`manifoldco/promptui`) hasn't had a release in years. **Decide:** keep it, or use `charmbracelet/huh`, which is maintained, supports multi-question forms, and has an accessible mode for screen readers.
* **Where the template comes from:** embed it in the CLI with `go:embed` (works offline, CLI version = template version) or clone gotth-plus at a tag (always fresh, needs network). Embedding is simpler to test. Either way, print which gotth-plus version was used.
* **Rewrite imports with Go tools,** not plain find-and-replace: `go mod edit -module`, then rewrite import paths, then `gofmt`, `templ generate`, `go mod tidy`.
* **Test every combination in CI:** generate each DB × ORM × auth × example/blank combination and run `go build`, `go vet`, and `go test` on the result. That's 3 × 2 × 5 × 2 = 60 combinations; a matrix job can handle that, or test a chosen subset.
* **Install with** `go install github.com/owenwexler/create-gotth-plus-app@latest` or `go run ...@latest`. Leave out an npx wrapper, to keep "no npm".
