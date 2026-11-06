# Dependencies per database choice

Each folder holds the `go.mod` (saved as `go.mod.tmpl`, since `go:embed` skips folders with a `go.mod`) and `go.sum` that a generated project gets for one database choice.
CGO SQLite has no folder: it uses the template's own files.

Don't edit these by hand. `make update-deps` generates a project for each choice, runs `go get` for
its driver and `go mod tidy`, and copies the results here. Run it after `make sync-template`, or to
update the drivers. The tests fail when a template dependency is newer than the one recorded here.
