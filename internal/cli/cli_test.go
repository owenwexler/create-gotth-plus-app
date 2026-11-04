package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/owenwexler/create-gotth-plus-app/internal/prereq"
)

// noTools is a PATH with nothing on it, so tests don't depend on the machine's tools.
var noTools = prereq.Runner{
	LookPath: func(string) (string, error) { return "", errors.New("not found") },
	Output: func(context.Context, string, ...string) ([]byte, error) {
		return nil, errors.New("not found")
	},
}

// runCLI runs the CLI in dir with no terminal attached and no tools installed.
func runCLI(t *testing.T, dir string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = Run(context.Background(), args, IO{
		In:    strings.NewReader(""),
		Out:   &out,
		Err:   &errOut,
		Getwd: func() (string, error) { return dir, nil },
		Tools: &noTools,
	})
	return code, out.String(), errOut.String()
}

func TestMissingToolsWarnButStillGenerate(t *testing.T) {
	dir := t.TempDir()
	code, out, errOut := runCLI(t, dir, "--yes", "--sqlite-driver=cgo", "app")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	for _, want := range []string{"problem  Go: not found", "Install: https://go.dev/dl/", "problem  C compiler", "can still be created"} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "app", "go.mod")); err != nil {
		t.Error("project not written")
	}

	// the pure-Go SQLite driver (the default) needs no C compiler
	_, out, _ = runCLI(t, t.TempDir(), "--yes", "app")
	if strings.Contains(out, "C compiler") {
		t.Errorf("C compiler checked for pure-Go SQLite:\n%s", out)
	}

	_, out, _ = runCLI(t, t.TempDir(), "--yes", "--skip-checks", "app")
	if strings.Contains(out, "Tools:") {
		t.Errorf("--skip-checks still checked tools:\n%s", out)
	}
}

func TestYesGeneratesProject(t *testing.T) {
	dir := t.TempDir()
	code, out, errOut := runCLI(t, dir, "my-app", "--yes", "--module", "example.com/me/my-app", "--port=8080")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	gomod, err := os.ReadFile(filepath.Join(dir, "my-app", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(gomod), "module example.com/me/my-app\n") {
		t.Errorf("go.mod: %s", gomod)
	}
	env, _ := os.ReadFile(filepath.Join(dir, "my-app", ".env"))
	if !strings.Contains(string(env), "\nPort=8080\n") {
		t.Errorf(".env has no Port=8080")
	}
	if !strings.Contains(out, "cd my-app") {
		t.Errorf("no next steps in output: %s", out)
	}
}

func TestFlagsBeforeAndAfterName(t *testing.T) {
	f, err := parseFlags([]string{"-y", "app", "--port", "5000", "--seed-admin-key=false"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if !f.yes || f.name != "app" || f.port != 5000 || f.seedAdminKey {
		t.Errorf("parsed %+v", f)
	}
	if !f.set["yes"] || !f.set["port"] || !f.set["seed-admin-key"] || f.set["module"] {
		t.Errorf("set = %v", f.set)
	}
}

func TestErrors(t *testing.T) {
	notEmpty := t.TempDir()
	if err := os.MkdirAll(filepath.Join(notEmpty, "taken", "x"), 0o755); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		args []string
		code int
		want string
	}{
		{"no terminal", []string{"app"}, exitError, "no terminal"},
		{"no name with --yes", []string{"--yes"}, exitError, "project name is required"},
		{"two names", []string{"a", "b"}, exitUsage, "expected one project name"},
		{"unknown flag", []string{"--nope"}, exitUsage, "nope"},
		{"bad name", []string{"--yes", "Bad Name"}, exitError, "lowercase"},
		{"bad module", []string{"--yes", "app", "--module", "a b"}, exitError, "module path"},
		{"bad port", []string{"--yes", "app", "--port", "7331"}, exitError, "live-reload"},
		{"not empty", []string{"--yes", "taken"}, exitError, "not empty"},
		{"bad db", []string{"--yes", "app", "--db", "oracle"}, exitError, "unknown database"},
		{"bad sqlite driver", []string{"--yes", "app", "--sqlite-driver", "wasm"}, exitError, "unknown SQLite driver"},
		{"sqlite driver with postgres", []string{"--yes", "app", "--db=postgres", "--sqlite-driver=cgo"}, exitError, "only applies to --db=sqlite"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, _, errOut := runCLI(t, notEmpty, tt.args...)
			if code != tt.code || !strings.Contains(errOut, tt.want) {
				t.Errorf("exit %d, stderr %q; want exit %d containing %q", code, errOut, tt.code, tt.want)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(notEmpty, "taken", "go.mod")); err == nil {
		t.Error("wrote into a non-empty directory")
	}
}

func TestForceWritesIntoNonEmptyDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := runCLI(t, dir, "--yes", "--force", "--module", "app", ".")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "notes.txt")); string(b) != "keep" {
		t.Error("an unrelated file was changed")
	}
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		t.Error("no go.mod written")
	}
}

func TestDatabaseFlag(t *testing.T) {
	dir := t.TempDir()
	code, out, errOut := runCLI(t, dir, "--yes", "--db=postgres", "shop-app")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(out, "Database             Postgres") {
		t.Errorf("summary has no database row:\n%s", out)
	}
	if !strings.Contains(out, "createdb shop_app") {
		t.Errorf("next steps don't say how to create the database:\n%s", out)
	}
	env, _ := os.ReadFile(filepath.Join(dir, "shop-app", ".env"))
	if !strings.Contains(string(env), "DatabaseURL=postgres://localhost:5432/shop_app") {
		t.Errorf(".env has no Postgres DatabaseURL:\n%s", env)
	}
}

func TestVersion(t *testing.T) {
	code, out, _ := runCLI(t, t.TempDir(), "--version")
	if code != exitOK || !strings.Contains(out, "gotth-plus ") {
		t.Errorf("exit %d, output %q", code, out)
	}
}
