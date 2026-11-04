// Command update-deps refreshes what generated projects pin: internal/scaffold/pins.go (Tailwind,
// GitHub Actions and images for the Dockerfile and CI), and internal/scaffold/_deps (for each set of
// choices that changes the dependencies, it generates a project, runs `go get` for the driver and
// `go mod tidy`, and saves the go.mod and go.sum). Run it with `make update-deps` from the
// repository root.
package main

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/mod/modfile"

	"github.com/owenwexler/create-gotth-plus-app/internal/options"
	"github.com/owenwexler/create-gotth-plus-app/internal/scaffold"
)

// modules are updated to their latest version for each database choice (the DepsKey without
// -noe2e). Everything else stays at the template's versions.
var modules = map[string][]string{
	"sqlite-cgo":    nil,
	"sqlite-purego": {"github.com/glebarez/sqlite@latest", "github.com/glebarez/go-sqlite@latest", "modernc.org/sqlite@latest"},
	"postgres":      {"gorm.io/driver/postgres@latest", "github.com/jackc/pgx/v5@latest"},
	"mysql":         {"gorm.io/driver/mysql@latest", "github.com/go-sql-driver/mysql@latest"},
}

// authModules are updated to their latest version when built-in auth is chosen.
var authModules = []string{"github.com/alexedwards/scs/v2@latest", "golang.org/x/crypto@latest"}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "update-deps:", err)
		os.Exit(1)
	}
}

func run() error {
	out := filepath.Join("internal", "scaffold", "_deps")
	if _, err := os.Stat(out); err != nil {
		return fmt.Errorf("run from the repository root: %w", err)
	}

	tmpl := scaffold.Template()
	templateModule, err := templateModulePath(tmpl)
	if err != nil {
		return err
	}

	goVersion, _, err := scaffold.Requirements(tmpl)
	if err != nil {
		return err
	}
	if err := updatePins(goVersion); err != nil {
		return err
	}

	for _, o := range scaffold.DepsChoices() {
		key := scaffold.DepsKey(o)
		fmt.Printf("== %s\n", key)
		dir, err := os.MkdirTemp("", "update-deps-"+key+"-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)

		o.Name = "app"
		// keep the template's module path, so the saved go.mod needs no renaming
		o.ModulePath = templateModule
		p, err := scaffold.BuildWithoutDeps(tmpl, o, dir)
		if err != nil {
			return err
		}
		if err := p.Write(dir); err != nil {
			return err
		}

		mods := modules[strings.TrimSuffix(strings.TrimSuffix(key, "-noe2e"), "-auth")]
		if o.Auth == options.BuiltinAuth {
			mods = append(mods, authModules...)
		}
		if len(mods) > 0 {
			if err := goCmd(dir, append([]string{"get"}, mods...)...); err != nil {
				return err
			}
		}
		if err := goCmd(dir, "mod", "tidy"); err != nil {
			return err
		}

		dest := filepath.Join(out, key)
		if err := os.MkdirAll(dest, 0o755); err != nil {
			return err
		}
		// go.mod is saved as go.mod.tmpl: go:embed skips directories that contain a go.mod
		for f, stored := range map[string]string{"go.mod": "go.mod.tmpl", "go.sum": "go.sum"} {
			b, err := os.ReadFile(filepath.Join(dir, f))
			if err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(dest, stored), b, 0o644); err != nil {
				return err
			}
		}
		fmt.Printf("   wrote %s\n", dest)
	}
	return nil
}

func goCmd(dir string, args ...string) error {
	fmt.Printf("   go %s\n", strings.Join(args, " "))
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// templateModulePath is the module path in the template's go.mod.
func templateModulePath(tmpl fs.FS) (string, error) {
	b, err := fs.ReadFile(tmpl, "go.mod.tmpl")
	if err != nil {
		return "", err
	}
	path := modfile.ModulePath(b)
	if path == "" {
		return "", fmt.Errorf("template go.mod has no module line")
	}
	return path, nil
}
