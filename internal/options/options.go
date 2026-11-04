// Package options holds the user's answers and checks them. Prompts and flags both fill an Options;
// the generator only reads it.
package options

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/mod/module"
)

// Options are the answers that shape the generated project.
type Options struct {
	// Name is the project directory to create, relative to the working directory, or "." for the
	// working directory itself.
	Name string
	// ModulePath is the Go module path written to go.mod and every import.
	ModulePath string
	// Port is the port the server listens on in development (.env).
	Port int
	// SeedAdminKey writes a random SeedAdminKey into .env, so POST /seed needs it.
	SeedAdminKey bool
	// Database is the database the app uses.
	Database Database
	// SQLiteDriver picks the SQLite driver; only used when Database is SQLite.
	SQLiteDriver SQLiteDriver

	// E2E keeps the Playwright end-to-end tests (cmd/server/e2e_*_test.go).
	E2E bool
	// LiveReload is what make dev runs.
	LiveReload LiveReload

	// What happens after the files are written.

	// CommitJS downloads htmx, Alpine (CSP) and hx-optimistic, records their versions and hashes,
	// and stops git-ignoring them, so they are committed.
	CommitJS bool
	// Axe downloads axe-core for the accessibility test.
	Axe bool
	// Git runs git init and makes a first commit.
	Git bool
	// Setup runs make setup (Go modules, JS libraries, the test browser, a first build).
	Setup bool
}

// Database is a supported database.
type Database string

const (
	SQLite   Database = "sqlite"
	Postgres Database = "postgres"
	MySQL    Database = "mysql"
)

// Databases lists the supported databases in the order they are offered.
var Databases = []Database{SQLite, Postgres, MySQL}

// Label is the database's display name.
func (d Database) Label() string {
	switch d {
	case SQLite:
		return "SQLite"
	case Postgres:
		return "Postgres"
	case MySQL:
		return "MySQL"
	}
	return string(d)
}

// IsServer reports whether the database runs as a separate server (as opposed to a file).
func (d Database) IsServer() bool { return d == Postgres || d == MySQL }

// LiveReload is a live-reload tool for make dev.
type LiveReload string

const (
	// TemplProxy is templ generate --watch with its browser-reloading proxy.
	TemplProxy LiveReload = "templ"
	// Air is github.com/air-verse/air with its browser-reloading proxy.
	Air LiveReload = "air"
)

// ParseLiveReload reads a --live-reload value.
func ParseLiveReload(s string) (LiveReload, error) {
	switch LiveReload(s) {
	case TemplProxy, Air:
		return LiveReload(s), nil
	}
	return "", fmt.Errorf("unknown live reload %q; use templ or air", s)
}

// SQLiteDriver is a GORM SQLite driver.
type SQLiteDriver string

const (
	// PureGo is github.com/glebarez/sqlite: no C compiler, builds with CGO_ENABLED=0.
	PureGo SQLiteDriver = "purego"
	// CGO is gorm.io/driver/sqlite on mattn/go-sqlite3: the C SQLite library, needs a C compiler.
	CGO SQLiteDriver = "cgo"
)

// SQLiteDrivers lists the SQLite drivers in the order they are offered.
var SQLiteDrivers = []SQLiteDriver{PureGo, CGO}

// NeedsCCompiler reports whether the generated project needs a C compiler to build.
func (o Options) NeedsCCompiler() bool {
	return o.Database == SQLite && o.SQLiteDriver == CGO
}

// DatabaseLabel describes the database choice, including the SQLite driver.
func (o Options) DatabaseLabel() string {
	if o.Database != SQLite {
		return o.Database.Label()
	}
	if o.SQLiteDriver == CGO {
		return "SQLite (CGO driver, mattn/go-sqlite3)"
	}
	return "SQLite (pure Go driver, glebarez/sqlite)"
}

// Defaults returns the options used for anything the user does not answer.
func Defaults() Options {
	return Options{
		Port:         3000,
		SeedAdminKey: true,
		Database:     SQLite,
		SQLiteDriver: PureGo,
		E2E:          true,
		LiveReload:   TemplProxy,
		CommitJS:     true,
		Axe:          true,
		Git:          true,
		Setup:        true,
	}
}

// ParseDatabase reads a --db value.
func ParseDatabase(s string) (Database, error) {
	for _, d := range Databases {
		if string(d) == s {
			return d, nil
		}
	}
	return "", fmt.Errorf("unknown database %q; use sqlite, postgres or mysql", s)
}

// ParseSQLiteDriver reads a --sqlite-driver value.
func ParseSQLiteDriver(s string) (SQLiteDriver, error) {
	for _, d := range SQLiteDrivers {
		if string(d) == s {
			return d, nil
		}
	}
	return "", fmt.Errorf("unknown SQLite driver %q; use purego or cgo", s)
}

// DatabaseName is the database name used in the generated DatabaseURL for a project: the project
// name with anything other than letters, digits and underscores turned into underscores.
func DatabaseName(projectName string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(projectName) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	name := strings.Trim(b.String(), "_")
	if name == "" || (name[0] >= '0' && name[0] <= '9') {
		name = "app_" + name
	}
	if len(name) > 63 { // Postgres's identifier limit (MySQL's is 64)
		name = name[:63]
	}
	return strings.TrimRight(name, "_")
}

// devProxyPort is used by templ's live-reload proxy (make dev), so the app cannot listen on it.
const devProxyPort = 7331

var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// ValidateName checks a project name. "." means the working directory.
func ValidateName(name string) error {
	if name == "" {
		return errors.New("project name is required")
	}
	if name == "." {
		return nil
	}
	if len(name) > 100 {
		return errors.New("project name must be at most 100 characters")
	}
	if !namePattern.MatchString(name) {
		return errors.New("project name may only use lowercase letters, digits, '.', '-' and '_', and must start with a letter or digit")
	}
	return nil
}

// ValidateModulePath checks a Go module path the way the go command does.
func ValidateModulePath(path string) error {
	if path == "" {
		return errors.New("module path is required")
	}
	if err := module.CheckImportPath(path); err != nil {
		return fmt.Errorf("invalid module path: %w", err)
	}
	return nil
}

// ValidatePort checks a port given as text (from a prompt or flag).
func ValidatePort(s string) error {
	port, err := strconv.Atoi(s)
	if err != nil {
		return errors.New("port must be a number")
	}
	return validatePort(port)
}

func validatePort(port int) error {
	if port < 1 || port > 65535 {
		return errors.New("port must be between 1 and 65535")
	}
	if port == devProxyPort {
		return fmt.Errorf("port %d is used by the live-reload proxy (make dev); pick another", devProxyPort)
	}
	return nil
}

// Validate checks every answer. Call it after prompts and flags are merged.
func (o Options) Validate() error {
	if err := ValidateName(o.Name); err != nil {
		return err
	}
	if err := ValidateModulePath(o.ModulePath); err != nil {
		return err
	}
	if _, err := ParseDatabase(string(o.Database)); err != nil {
		return err
	}
	if _, err := ParseSQLiteDriver(string(o.SQLiteDriver)); err != nil {
		return err
	}
	if _, err := ParseLiveReload(string(o.LiveReload)); err != nil {
		return err
	}
	if o.Axe && !o.E2E {
		return errors.New("axe-core is only used by the E2E tests; it can't be downloaded without them")
	}
	return validatePort(o.Port)
}

// DefaultModulePath suggests a module path for a project. With a GitHub user name it is
// github.com/<user>/<project>; without one it is the bare project name, which works for local
// modules and can be changed later with `go mod edit -module`.
func DefaultModulePath(projectDir, githubUser string) string {
	base := filepath.Base(projectDir)
	if githubUser != "" {
		return "github.com/" + githubUser + "/" + base
	}
	return base
}
