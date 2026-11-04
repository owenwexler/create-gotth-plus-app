// Package options holds the user's answers and checks them. Prompts and flags both fill an Options;
// the generator only reads it.
package options

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"

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
}

// Defaults returns the options used for anything the user does not answer.
func Defaults() Options {
	return Options{
		Port:         3000,
		SeedAdminKey: true,
	}
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
