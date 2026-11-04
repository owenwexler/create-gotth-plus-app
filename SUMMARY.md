# Work summary: 2026-11-03 to 2026-11-04

Where `create-gotth-plus-app` stands, for picking the work up again. The plan and the decisions
behind it are in `SPEC.md`; how to use and develop the CLI is in `README.md`.

## Status

All work is on the branch **`build-cli`** (not merged, not pushed). `make test` passes, including
generated Postgres projects when `CGPA_TEST_POSTGRES_URL` points at a server.

| Step | Commit | What |
|---|---|---|
| 1 | `b2bc45a` | CLI skeleton: prompts (charm.land/huh/v2), flags, `--yes`, summary, embedded template, module rename |
| 2 | `00bdedb` | Prerequisite check (Go and templ versions from the template's `go.mod`, Tailwind v4, make, bash, git, curl, openssl, C compiler) |
| 3 | `505b13d` | Database: SQLite (pure Go default, or CGO), Postgres, MySQL |
| 4 | `cd938d0` | After-creation steps: `make setup`, JS download and commit (versions and hashes in the README), axe-core (sha512-verified), git init |
| 4b | `2bcecf9` | E2E tests on/off, live reload (templ or air), fixed `air.toml` |
| 5 | `08aaf7a` | Dockerfile (distroless, non-root) and GitHub Actions workflow |
| 6a | `83d0aad` | Built-in auth core and email/password |
| 6b | `23487c5` | Google, GitHub, Apple (`--auth-methods`) |
| 6c | `1d85492` | Generic OIDC (`--auth=oidc`, `--oidc-preset`: WorkOS, Logto, Kinde, Auth0, Zitadel, Keycloak, other) |
| 6d | `ec13919` | Supabase Auth (`--auth=supabase`) |

**Next: step 7**, a CI workflow for this repository that runs `make test` (with Postgres and
MySQL service containers, so the generated server-database projects' tests run too), then merging
`build-cli`.

## How the generator works

- `internal/scaffold/_template` is gotth-plus's committed files (`make sync-template`), embedded in
  the binary. `go.mod` is stored as `go.mod.tmpl`, because `go:embed` skips folders with a `go.mod`.
- `scaffold.Build` reads everything into memory, applies the steps in order (database, E2E, live
  reload, auth, dependencies, module rename, project name, `.env`, Docker/CI, gofmt), and only then
  writes. The database and auth steps run before the module rename, so variant files use
  gotth-plus's import paths.
- **Text edits** use `replaceOnce` (or `replaceSpan`): the text must occur exactly once, so a change
  in gotth-plus fails the build and the tests instead of being skipped silently.
- **Whole-file replacements** (`database.go`, `air.toml`, `app.go`, `pages.templ`,
  `pages_templ.go`) record the sha256 of the template file they replace (`replacedFiles`); a
  changed template file stops generation until the replacement is reviewed.
- **Variants** (`internal/scaffold/_variants`) add or replace files: `postgres`, `mysql`, `auth`
  (core), `auth-password`, `auth-identity`, `auth-oauth`, `auth-supabase`.
- **Conditional blocks** in variant Go files: `// cgpa:if a b` … `// cgpa:end` keeps the lines
  when any condition is true (`password`, `oauth`, `google`, `github`, `apple`, `oidc`, `supabase`,
  `e2e`). The space after `//` matters: gofmt moves `//word:` comments like directives. Tests check
  the variants are gofmt-clean and no marker survives into a project.
- **Pre-generated templ**: `_templ.go` files in the variants are generated with the template's
  templ version (`make gen-templ`), so the CLI never needs templ; a test checks they're current.
- **Dependencies**: `_deps/<key>/` holds `go.mod.tmpl` and `go.sum` for every combination that
  changes dependencies (database × auth profile × E2E, 34 sets), made by `make update-deps`,
  which also refreshes `pins.go` (Tailwind version and checksums, GitHub Actions commit SHAs, the
  Go image). New projects are tidy without network access.

## Decisions made along the way

These go beyond, or differ from, what `SPEC.md` says:

- **SQLite pure-Go driver** is `glebarez/sqlite`, with `glebarez/go-sqlite` and
  `modernc.org/sqlite` bumped to their latest versions (tested).
- **Postgres/MySQL tests** create one throwaway database per test (`internal/database/dbtest`) on
  `TestDatabaseURL` or `DatabaseURL`, and fail with a clear message without a server.
- **No docker-compose**: per the spec answer, a local or cloud database server is expected.
- **Sessions**: our own small GORM store for `alexedwards/scs`, instead of `scs/gormstore`, which
  has no releases and pulls in every GORM driver. 7-day lifetime, 24-hour idle,
  `__Host-session` in production. `User.SessionVersion` ends all of a user's sessions.
- **Passwords**: argon2id, 15 to 256 characters (NIST SP 800-63B-4); Supabase: 15 to 72 (bcrypt).
- **Emailed links** open a page whose button POSTs (no change on GET, scanners can't use the
  token), and are built from `BaseURL`, never the Host header.
- **OAuth**: state and nonce in a `__Host-oauth` cookie, PKCE for Google, GitHub and generic OIDC
  (not documented for Apple, so off there). Accounts are created or linked only with verified
  emails. Apple's form-post callback is the only route that skips the CSRF and cross-origin checks.
- **OIDC**: endpoints from discovery at the first sign-in (the app starts while the provider is
  down); the redirect URL is derived from `BaseURL` rather than a separate `OIDCRedirectURL`.
  Sign-out also ends the provider session when it has an `end_session_endpoint`.
- **Supabase**: providers *and* email/password (chosen by the user). Supabase's OAuth 2.1/OIDC
  server was not used: it is beta and needs a consent page built with supabase-js. Tokens are
  checked with `GET /auth/v1/user` instead of JWKS (works with legacy and new signing keys, and
  catches revocation). All flows use PKCE.
- **Sign-in without JavaScript** isn't supported: the forms rely on htmx sending the CSRF header
  (they fail safely, never putting a password in a URL).

## Not tested against the real thing

- **MySQL**: generated MySQL projects are built and vetted, but their tests never ran against a
  server (no MySQL locally; this user can't use Docker). Run
  `CGPA_TEST_MYSQL_URL='root:pass@tcp(localhost:3306)/?parseTime=true' make test`.
- **`docker build`**: the Dockerfile was checked by running its build steps on the host and the
  binary as the image would. The generated CI's Docker job will be the first real build.
- **GitHub Actions**: generated workflows pass `actionlint` (without shellcheck), but haven't run.
- **Real providers**: Google, GitHub, Apple, OIDC and Supabase were tested only against fakes in
  the generated tests. Apple needs an https tunnel to try locally.
- **Unverified facts**: Kinde's issuer format (`https://<business>.kinde.com`), the Auth0, Zitadel
  and Keycloak formats (from memory), and whether GitHub uses the PKCE parameters (harmless if
  ignored).

## Problems found in gotth-plus (worth fixing upstream)

1. `air.toml` never runs `templ generate`, so `.templ` edits rebuild from stale code.
2. `make air` runs plain `air`, which only reads `.air.toml`, so `air.toml` is ignored entirely.
3. `internal/csrf/csrf_test.go`, "wrong header" case: `"x" + token[1:]` equals the token when it
   already starts with `x`, so the test fails about 1 run in 64.
4. SECURITY.md says production `POST /seed` returns 404; without a CSRF token it returns 403
   (CSRF runs before routing). The route is gone; only the wording is off.
5. Your local templ CLI is v0.3.1020; gotth-plus's `go.mod` uses v0.3.1070. A `tool` directive
   (`go tool templ`) would keep them in step.

The CLI works around 1 and 2 in every generated project; 3 to 5 are left as they are.

## Open ideas (not started)

- Migrations (e.g. goose): every new table is only created by the dev seed route's `AutoMigrate`.
- The account page could list and unlink connected providers.
- Second factor (TOTP, passkeys); the spec's `owner_id` ownership example.
- A browser (E2E) test for provider sign-in through a fake provider page.
