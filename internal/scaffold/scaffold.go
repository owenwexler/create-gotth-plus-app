// Package scaffold turns the embedded gotth-plus template into a new project. The template is read
// into memory, adjusted to the user's options, and only then written to disk, so a failed step
// never leaves a half-written project behind.
package scaffold

import (
	"bytes"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"go/format"
	"html"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/mod/modfile"

	"github.com/owenwexler/create-gotth-plus-app/internal/options"
)

// _template is gotth-plus itself, _variants holds files that database choices add or replace, and
// _deps holds the go.mod/go.sum for each choice (see make update-deps).
//
//go:embed all:_template all:_variants all:_deps
var embedded embed.FS

const (
	versionFile    = ".gotth-plus-version"
	goModTemplate  = "go.mod.tmpl"
	gotthPlusRepo  = "https://github.com/owenwexler/gotth-plus"
	templateFolder = "_template"
)

// Template returns the embedded gotth-plus template.
func Template() fs.FS {
	sub, err := fs.Sub(embedded, templateFolder)
	if err != nil {
		panic(err) // the folder is embedded at build time, so this cannot fail
	}
	return sub
}

// TemplateVersion is the gotth-plus commit and date the template was synced from.
func TemplateVersion(fsys fs.FS) string {
	b, err := fs.ReadFile(fsys, versionFile)
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(b))
}

// Requirements reads the minimum Go version and the templ version from the template's go.mod, so
// the prerequisite check always matches the template.
func Requirements(fsys fs.FS) (goVersion, templVersion string, err error) {
	b, err := fs.ReadFile(fsys, goModTemplate)
	if err != nil {
		return "", "", err
	}
	mf, err := modfile.Parse(goModTemplate, b, nil)
	if err != nil {
		return "", "", err
	}
	if mf.Go == nil {
		return "", "", errors.New("template go.mod has no go line")
	}
	for _, r := range mf.Require {
		if r.Mod.Path == "github.com/a-h/templ" {
			return mf.Go.Version, r.Mod.Version, nil
		}
	}
	return "", "", errors.New("template go.mod does not require github.com/a-h/templ")
}

// Project is a generated project held in memory: slash-separated relative paths to file contents.
type Project map[string][]byte

// Paths returns the project's file paths, sorted.
func (p Project) Paths() []string {
	paths := make([]string, 0, len(p))
	for name := range p {
		paths = append(paths, name)
	}
	sort.Strings(paths)
	return paths
}

// Build reads the template and applies opts to it. dir is the absolute directory the project will be
// written to; it only supplies the display name when opts.Name is ".".
func Build(fsys fs.FS, opts options.Options, dir string) (Project, error) {
	return build(fsys, opts, dir, true)
}

// BuildWithoutDeps is Build, but keeps the template's go.mod and go.sum instead of the ones in
// _deps. Only the update-deps tool uses it, to produce those files.
func BuildWithoutDeps(fsys fs.FS, opts options.Options, dir string) (Project, error) {
	return build(fsys, opts, dir, false)
}

func build(fsys fs.FS, opts options.Options, dir string, withDeps bool) (Project, error) {
	p, err := load(fsys)
	if err != nil {
		return nil, err
	}
	templateModule, err := modulePath(p["go.mod"])
	if err != nil {
		return nil, err
	}
	original := Project{}
	for name, b := range p {
		original[name] = b
	}

	name := opts.Name
	if name == "." {
		name = filepath.Base(dir)
	}
	version := TemplateVersion(fsys)

	steps := []struct {
		what string
		run  func() error
	}{
		// the database step and the dependencies use the template's module path, so they come first
		{"database", func() error { return applyDatabase(p, opts, templateModule, options.DatabaseName(name)) }},
		{"dependencies", func() error {
			if !withDeps {
				return nil
			}
			return applyDeps(p, depsKey(opts))
		}},
		{"JS libraries", func() error {
			if !opts.CommitJS {
				return nil
			}
			return commitJS(p)
		}},
		{"module path", func() error { return rewriteModule(p, opts.ModulePath) }},
		{"project name", func() error { return setProjectName(p, name, version) }},
		{"environment", func() error { return setEnv(p, opts) }},
		{"gofmt", func() error { return formatChanged(p, original) }},
	}
	for _, s := range steps {
		if err := s.run(); err != nil {
			return nil, fmt.Errorf("%s: %w", s.what, err)
		}
	}
	return p, nil
}

func modulePath(gomod []byte) (string, error) {
	mf, err := modfile.Parse("go.mod", gomod, nil)
	if err != nil {
		return "", err
	}
	if mf.Module == nil {
		return "", errors.New("go.mod has no module line")
	}
	return mf.Module.Mod.Path, nil
}

// ErrMissingDeps means _deps has no go.mod/go.sum for a database choice yet.
var ErrMissingDeps = errors.New("no go.mod/go.sum in _deps for this choice; run make update-deps")

// applyDeps swaps in the go.mod and go.sum for a database choice. They are produced by
// `make update-deps`, which runs go mod tidy on a generated project, so a new project needs no
// network access to be consistent.
func applyDeps(p Project, key string) error {
	if key == "" {
		return nil
	}
	// go.mod is stored as go.mod.tmpl: go:embed skips directories that contain a go.mod
	for f, stored := range map[string]string{"go.mod": goModTemplate, "go.sum": "go.sum"} {
		b, err := fs.ReadFile(embedded, "_deps/"+key+"/"+stored)
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%s: %w", key, ErrMissingDeps)
		}
		if err != nil {
			return err
		}
		p[f] = b
	}
	return nil
}

// formatChanged gofmts every .go file the steps changed, so edits never leave misaligned code.
func formatChanged(p, original Project) error {
	for name, b := range p {
		if path.Ext(name) != ".go" || bytes.Equal(b, original[name]) {
			continue
		}
		formatted, err := format.Source(b)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		p[name] = formatted
	}
	return nil
}

// load reads every template file into memory, restoring go.mod and dropping the version marker.
func load(fsys fs.FS) (Project, error) {
	p := Project{}
	err := fs.WalkDir(fsys, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if name == versionFile {
			return nil
		}
		b, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		if name == goModTemplate {
			name = "go.mod"
		}
		p[name] = b
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read template: %w", err)
	}
	if _, ok := p["go.mod"]; !ok {
		return nil, errors.New("template has no go.mod.tmpl")
	}
	return p, nil
}

// replaceOnce replaces old with new in file, and fails unless old occurs exactly once. Edits are tied
// to exact template text, so when gotth-plus changes, the generator fails loudly (and its tests
// catch it) instead of silently skipping an edit.
func (p Project) replaceOnce(file, old, new string) error {
	b, ok := p[file]
	if !ok {
		return fmt.Errorf("%s: not in template", file)
	}
	if n := strings.Count(string(b), old); n != 1 {
		return fmt.Errorf("%s: expected %q once, found it %d times", file, old, n)
	}
	p[file] = []byte(strings.Replace(string(b), old, new, 1))
	return nil
}

func setProjectName(p Project, name, version string) error {
	title := strconv.Quote(name)

	// The browser tab title. pages_templ.go is generated from pages.templ, so both get the same edit
	// and no `templ generate` is needed to keep them in step.
	if err := p.replaceOnce("internal/view/pages.templ", `@Layout("GOTTH+")`, "@Layout("+title+")"); err != nil {
		return err
	}
	if err := p.replaceOnce("internal/view/pages_templ.go", `Layout("GOTTH+")`, "Layout("+title+")"); err != nil {
		return err
	}

	// The tests check the title too. The splash heading still reads GOTTH+, so its check stays.
	if err := p.replaceOnce("cmd/server/integration_test.go", "\t\t\"GOTTH+\",\n",
		"\t\t"+strconv.Quote("<title>"+html.EscapeString(name)+"</title>")+",\n"); err != nil {
		return err
	}
	if err := p.replaceOnce("cmd/server/e2e_test.go", `ToHaveTitle("GOTTH+")`, "ToHaveTitle("+title+")"); err != nil {
		return err
	}

	readmeTitle := fmt.Sprintf("# %s\n\nGenerated with [create-gotth-plus-app](https://github.com/owenwexler/create-gotth-plus-app) from [GOTTH+](%s) (%s).\n", name, gotthPlusRepo, version)
	return p.replaceOnce("README.md", "# GOTTH+\n", readmeTitle)
}

// setEnv sets the port in .env.example and writes a ready-to-use .env (git-ignored) next to it.
func setEnv(p Project, opts options.Options) error {
	port := strconv.Itoa(opts.Port)
	if err := p.replaceOnce(".env.example", "\nPort=3000\n", "\nPort="+port+"\n"); err != nil {
		return err
	}

	env := string(p[".env.example"])
	if opts.SeedAdminKey {
		key, err := randomHex(32)
		if err != nil {
			return err
		}
		env = strings.Replace(env, "\nSeedAdminKey=\n", "\nSeedAdminKey="+key+"\n", 1)
	}
	p[".env"] = []byte(env)
	return nil
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate random key: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// CheckDest reports whether dir can receive a new project: it must be missing, an empty directory,
// or (with force) any directory.
func CheckDest(dir string, force bool) error {
	info, err := os.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s exists and is not a directory", dir)
	}
	if force {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		return fmt.Errorf("%s is not empty; pick another name, or pass --force to write into it anyway", dir)
	}
	return nil
}

// Write writes the project into dir, creating it if needed. Existing files with the same paths are
// overwritten (CheckDest decides whether that is allowed). Shell scripts are made executable.
func (p Project) Write(dir string) error {
	for _, name := range p.Paths() {
		target := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if path.Ext(name) == ".sh" {
			mode = 0o755
		}
		if err := os.WriteFile(target, p[name], mode); err != nil {
			return err
		}
		// WriteFile keeps the mode of a file that already exists (--force)
		if err := os.Chmod(target, mode); err != nil {
			return err
		}
	}
	return nil
}
