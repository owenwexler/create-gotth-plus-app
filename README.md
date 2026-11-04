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
| `--yes`, `-y` | | Ask nothing: flags, then defaults (for CI and AI agents) |
| `--force` | | Write into a directory that is not empty |
| `--accessible` | `$ACCESSIBLE` | Plain prompts for screen readers |
| `--skip-checks` | | Don't check that the needed tools are installed |
| `--version` | | Print the CLI and template versions |

More choices (database, auth, tests, Docker, CI) are coming; see `SPEC.md`.

## How it works

The gotth-plus template is embedded in the binary (`internal/scaffold/_template`), so the CLI works
offline and each CLI version always produces the same project. The generator reads the template into
memory, applies the answers, and only then writes files.

Every edit to template text must match exactly once (`Project.replaceOnce`). When gotth-plus changes
in a way the generator doesn't expect, generation fails and the tests say which edit broke, instead
of quietly producing a wrong project.

## Development

```bash
make test            # includes generating a project and running go build, vet and test on it
make try             # generate ./tmp/demo-app to look at
make sync-template   # pull in the latest committed gotth-plus from ../gotth-plus
```

After `make sync-template`, run `make test`: it is how changes in gotth-plus get noticed.
