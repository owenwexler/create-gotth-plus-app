package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/owenwexler/create-gotth-plus-app/internal/options"
	"github.com/owenwexler/create-gotth-plus-app/internal/post"
	"github.com/owenwexler/create-gotth-plus-app/internal/prereq"
)

// noTools is a PATH with nothing on it, so tests don't depend on the machine's tools.
var noTools = prereq.Runner{
	LookPath: func(string) (string, error) { return "", errors.New("not found") },
	Output: func(context.Context, string, ...string) ([]byte, error) {
		return nil, errors.New("not found")
	},
}

// noPost records the options the after-creation steps were given, instead of running them.
type noPost struct{ got *options.Options }

func (n noPost) RunAll(_ context.Context, _ string, o options.Options) []post.Outcome {
	if n.got != nil {
		*n.got = o
	}
	return nil
}

// runCLI runs the CLI in dir with no terminal attached, no tools installed and no after-creation steps.
func runCLI(t *testing.T, dir string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = Run(context.Background(), args, IO{
		In:    strings.NewReader(""),
		Out:   &out,
		Err:   &errOut,
		Getwd: func() (string, error) { return dir, nil },
		Tools: &noTools,
		Post:  noPost{},
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
		{"axe without e2e", []string{"--yes", "app", "--e2e=false", "--axe"}, exitError, "only applies with the E2E tests"},
		{"bad live reload", []string{"--yes", "app", "--live-reload=fresh"}, exitError, "unknown live reload"},
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

func runWithPost(t *testing.T, dir string, p PostSteps, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := Run(context.Background(), args, IO{
		In: strings.NewReader(""), Out: &out, Err: &errOut,
		Getwd: func() (string, error) { return dir, nil },
		Tools: &noTools,
		Post:  p,
	})
	return code, out.String(), errOut.String()
}

func TestAfterCreationFlags(t *testing.T) {
	var got options.Options
	code, out, errOut := runWithPost(t, t.TempDir(), noPost{&got}, "--yes", "--js=false", "--setup=false", "app")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if got.CommitJS || got.Setup || !got.Axe || !got.Git {
		t.Errorf("post steps got %+v", got)
	}
	if !strings.Contains(out, "make setup    #") {
		t.Errorf("next steps should say make setup when it didn't run:\n%s", out)
	}

	_, out, _ = runWithPost(t, t.TempDir(), noPost{&got}, "--yes", "app")
	if !got.CommitJS || !got.Setup {
		t.Errorf("defaults: post steps got %+v", got)
	}
	if strings.Contains(out, "make setup    #") {
		t.Errorf("next steps repeat make setup after it ran:\n%s", out)
	}
}

func TestNoE2ETurnsOffAxe(t *testing.T) {
	var got options.Options
	code, _, errOut := runWithPost(t, t.TempDir(), noPost{&got}, "--yes", "--e2e=false", "app")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if got.E2E || got.Axe {
		t.Errorf("post steps got E2E %v, Axe %v", got.E2E, got.Axe)
	}
}

func TestAirIsChecked(t *testing.T) {
	_, out, _ := runCLI(t, t.TempDir(), "--yes", "--live-reload=air", "app")
	if !strings.Contains(out, "problem  air: not found") || !strings.Contains(out, "go install github.com/air-verse/air@latest") {
		t.Errorf("air not checked:\n%s", out)
	}
	_, out, _ = runCLI(t, t.TempDir(), "--yes", "app")
	if strings.Contains(out, "air:") {
		t.Errorf("air checked without --live-reload=air:\n%s", out)
	}
}

func TestNoGitInNonEmptyDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mine.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	var got options.Options
	code, out, _ := runWithPost(t, dir, noPost{&got}, "--yes", "--force", ".")
	if code != exitOK || got.Git {
		t.Errorf("exit %d, git %v", code, got.Git)
	}
	if !strings.Contains(out, "Not running git init") {
		t.Errorf("no explanation:\n%s", out)
	}
}

type failingPost struct{}

func (failingPost) RunAll(context.Context, string, options.Options) []post.Outcome {
	return []post.Outcome{{Step: "make setup", Err: errors.New("exit status 2"), Fix: "make setup"}}
}

func TestFailedStepExitsNonZero(t *testing.T) {
	dir := t.TempDir()
	code, out, errOut := runWithPost(t, dir, failingPost{}, "--yes", "app")
	if code != exitError || !strings.Contains(errOut, "the project was created") {
		t.Errorf("exit %d, stderr %q", code, errOut)
	}
	if !strings.Contains(out, "Run by hand: make setup") || !strings.Contains(out, "make setup    #") {
		t.Errorf("output:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "app", "go.mod")); err != nil {
		t.Error("the project should stay")
	}
}

func TestDockerAndCIFlags(t *testing.T) {
	dir := t.TempDir()
	code, out, errOut := runCLI(t, dir, "--yes", "app")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	for _, f := range []string{"Dockerfile", ".dockerignore", ".github/workflows/ci.yml"} {
		if _, err := os.Stat(filepath.Join(dir, "app", f)); err != nil {
			t.Errorf("%s not written by default", f)
		}
	}
	if !strings.Contains(out, "Dockerfile           yes") || !strings.Contains(out, "GitHub Actions CI    yes") {
		t.Errorf("summary:\n%s", out)
	}

	dir = t.TempDir()
	if code, _, errOut := runCLI(t, dir, "--yes", "--docker=false", "--ci=false", "app"); code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	for _, f := range []string{"Dockerfile", ".github"} {
		if _, err := os.Stat(filepath.Join(dir, "app", f)); err == nil {
			t.Errorf("%s written with --docker=false --ci=false", f)
		}
	}
}

func TestAuthFlag(t *testing.T) {
	dir := t.TempDir()
	code, out, errOut := runCLI(t, dir, "--yes", "--auth=builtin", "app")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(out, "Auth                 Built-in: Email and password") || !strings.Contains(out, "dev@example.com") {
		t.Errorf("output:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "app", "internal", "auth", "session.go")); err != nil {
		t.Error("no auth package")
	}
	if code, _, errOut := runCLI(t, t.TempDir(), "--yes", "--auth=clerk", "app"); code != exitError || !strings.Contains(errOut, "unknown auth") {
		t.Errorf("--auth=clerk: exit %d, %q", code, errOut)
	}
}

func TestAuthMethodsFlag(t *testing.T) {
	dir := t.TempDir()
	code, out, errOut := runCLI(t, dir, "--yes", "--auth=builtin", "--auth-methods=github,apple", "app")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(out, "Built-in: GitHub, Apple") || !strings.Contains(out, "sign-in providers: register an app") || strings.Contains(out, "make seed") {
		t.Errorf("output:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "app", "internal", "auth", "oauth.go")); err != nil {
		t.Error("no OAuth code")
	}
	for _, args := range [][]string{
		{"--yes", "--auth=none", "--auth-methods=google", "app"},
		{"--yes", "--auth=builtin", "--auth-methods=facebook", "app"},
		{"--yes", "--auth=builtin", "--auth-methods=", "app"},
	} {
		if code, _, _ := runCLI(t, t.TempDir(), args...); code != exitError {
			t.Errorf("%v: exit %d, want an error", args, code)
		}
	}
}

func TestOIDCFlag(t *testing.T) {
	dir := t.TempDir()
	code, out, errOut := runCLI(t, dir, "--yes", "--auth=oidc", "--oidc-preset=workos", "app")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(out, "Auth                 OIDC: WorkOS") || !strings.Contains(out, "create the client in the WorkOS dashboard") {
		t.Errorf("output:\n%s", out)
	}
	for _, args := range [][]string{
		{"--yes", "--auth=builtin", "--oidc-preset=logto", "app"},
		{"--yes", "--auth=oidc", "--oidc-preset=okta-ish", "app"},
	} {
		if code, _, _ := runCLI(t, t.TempDir(), args...); code != exitError {
			t.Errorf("%v: exit %d, want an error", args, code)
		}
	}
}
