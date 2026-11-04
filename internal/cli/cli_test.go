package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runCLI runs the CLI in dir with no terminal attached.
func runCLI(t *testing.T, dir string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = Run(context.Background(), args, IO{
		In:    strings.NewReader(""),
		Out:   &out,
		Err:   &errOut,
		Getwd: func() (string, error) { return dir, nil },
	})
	return code, out.String(), errOut.String()
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

func TestVersion(t *testing.T) {
	code, out, _ := runCLI(t, t.TempDir(), "--version")
	if code != exitOK || !strings.Contains(out, "gotth-plus ") {
		t.Errorf("exit %d, output %q", code, out)
	}
}
