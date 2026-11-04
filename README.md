# create-gotth-plus-app

Creates a new [GOTTH+](https://github.com/owenwexler/gotth-plus) web app: Go, Templ, Tailwind CSS v4,
htmx, hx-optimistic and the Alpine.js CSP build, with the security defaults, tests and agent guidance
of the GOTTH+ template.

```bash
go run github.com/owenwexler/create-gotth-plus-app@latest
# or install it
go install github.com/owenwexler/create-gotth-plus-app@latest
create-gotth-plus-app my-app
```

It asks for anything you don't pass as a flag, shows a summary, and writes the project. Nothing is
written if you cancel.

After writing the files it runs the steps you chose (`make setup`, the downloads, git). A step that
fails doesn't stop the others or remove the project: the CLI says what to run by hand and exits
with status 1.

Before you confirm, it checks the tools the project needs: Go (at least the version in the template's
`go.mod`), templ (the same version as the template's `go.mod`), the Tailwind CSS v4 standalone CLI,
`make`, `bash`, `git`, `curl`, `openssl`, and a C compiler for the CGO SQLite driver. A missing or
wrong version is a warning with install steps, not an error: the project is still created, but
`make setup` needs the tools.

## Flags

| Flag | Default | |
|---|---|---|
| `[name]` | asked | Project directory; `.` for the current directory |
| `--module` | `github.com/<git config github.user>/<name>`, or `<name>` | Go module path |
| `--port` | `3000` | Development port (`.env`) |
| `--seed-admin-key` | `true` | Write a random `SeedAdminKey` into `.env` |
| `--auth` | `none` | `none`, or `builtin`: email and password with argon2id, server-side sessions, confirmation and reset emails |
| `--db` | `sqlite` | `sqlite`, `postgres` or `mysql` |
| `--sqlite-driver` | `purego` | `purego` (glebarez/sqlite, no C compiler) or `cgo` (mattn/go-sqlite3) |
| `--e2e` | `true` | Include the Playwright end-to-end tests (`--e2e=false` removes them, their make targets and dependency) |
| `--live-reload` | `templ` | What `make dev` runs: `templ` (templ's proxy) or `air`; both reload the browser on port 7331 |
| `--docker` | `true` | Add a production `Dockerfile` (distroless, non-root, pinned Tailwind) and `.dockerignore` |
| `--ci` | `true` | Add a GitHub Actions workflow (verify, generated-code check, vet, test with a database service, govulncheck, Docker build) |
| `--js` | `true` | Download htmx, Alpine (CSP) and hx-optimistic, record versions and SHA-384 hashes in the README, and commit them |
| `--axe` | `true` | Download axe-core (sha512-verified, from the npm registry) for the accessibility test; off without `--e2e` |
| `--git` | `true` | `git init` and a first commit (skipped inside an existing repository or a non-empty directory) |
| `--setup` | `true` | Run `make setup` (Go modules, JS libraries, Chromium for the E2E tests, a first build) |
| `--yes`, `-y` | | Ask nothing: flags, then defaults (for CI and AI agents) |
| `--force` | | Write into a directory that is not empty |
| `--accessible` | `$ACCESSIBLE` | Plain prompts for screen readers |
| `--skip-checks` | | Don't check that the needed tools are installed |
| `--version` | | Print the CLI and template versions |

More sign-in options (Google, GitHub, Apple, OIDC, Supabase) are coming; see `SPEC.md`.

Every project gets a fixed `air.toml` (gotth-plus's runs no `templ generate`, and `make air` didn't
pass `-c air.toml`, so air ignored it) with air's browser-reloading proxy on port 7331.

### Databases

- **SQLite** is a file next to the app. The pure-Go driver needs no C compiler and builds with
  `CGO_ENABLED=0`; the CGO driver is the C SQLite library (and the template as gotth-plus ships it).
- **Postgres** and **MySQL** need a server, local or in the cloud. `.env` gets a `DatabaseURL` for
  a local server and a database named after the project; the CLI prints the command that creates
  it. The tests need a server too: each test creates its own throwaway database on
  `TestDatabaseURL` (or `DatabaseURL`) and drops it afterwards.

## How it works

The gotth-plus template is embedded in the binary (`internal/scaffold/_template`), so the CLI works
offline and each CLI version always produces the same project. The generator reads the template into
memory, applies the answers, and only then writes files.

Database choices add or replace files from `internal/scaffold/_variants`, and swap in the
`go.mod`/`go.sum` from `internal/scaffold/_deps` (made by `make update-deps`, so a new project is
tidy without network access). A variant file that replaces a template file records the template
file's hash; when gotth-plus changes that file, generation stops until the variant is reviewed.

Every edit to template text must match exactly once (`Project.replaceOnce`). When gotth-plus changes
in a way the generator doesn't expect, generation fails and the tests say which edit broke, instead
of quietly producing a wrong project.

## Development

```bash
make test            # includes generating a project and running go build, vet and test on it
make try             # generate ./tmp/demo-app to look at
make sync-template   # pull in the latest committed gotth-plus from ../gotth-plus
make gen-templ       # regenerate the *_templ.go files in _variants after editing a .templ file there
make update-deps     # refresh pins.go and _deps; after sync-template, or to update drivers, Tailwind and actions (needs network)
```

After `make sync-template`, run `make update-deps` and `make test`: that is how changes in
gotth-plus get noticed.

`make test` builds and vets a generated Postgres and MySQL project, but only runs their tests
against a server when you point it at one (the user needs to be allowed to create databases):

```bash
CGPA_TEST_POSTGRES_URL='postgres://localhost:5432/postgres?sslmode=disable' \
CGPA_TEST_MYSQL_URL='root:pass@tcp(localhost:3306)/?parseTime=true' make test
```
