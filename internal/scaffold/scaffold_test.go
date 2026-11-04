package scaffold

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/owenwexler/create-gotth-plus-app/internal/options"
)

const templateModule = "github.com/owenwexler/gotth-plus"

func testOptions() options.Options {
	o := options.Defaults()
	o.Name = "demo-app"
	o.ModulePath = "example.com/acme/demo"
	o.Port = 4000
	return o
}

func build(t *testing.T, o options.Options, dir string) Project {
	t.Helper()
	p, err := Build(Template(), o, dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return p
}

func TestBuildRenamesModule(t *testing.T) {
	p := build(t, testOptions(), "/work/demo-app")

	if !strings.HasPrefix(string(p["go.mod"]), "module example.com/acme/demo\n") {
		t.Errorf("go.mod starts with %q", strings.SplitN(string(p["go.mod"]), "\n", 2)[0])
	}
	for _, name := range p.Paths() {
		if strings.Contains(string(p[name]), `"`+templateModule) {
			t.Errorf("%s still refers to %s", name, templateModule)
		}
	}
	if !strings.Contains(string(p["internal/app/app.go"]), `"example.com/acme/demo/internal/csrf"`) {
		t.Error("app.go imports were not renamed")
	}
	if !strings.Contains(string(p["internal/view/layout.templ"]), `import "example.com/acme/demo/internal/csrf"`) {
		t.Error("layout.templ import was not renamed")
	}
	// the splash page's link to gotth-plus is a URL, not an import, and stays
	if !strings.Contains(string(p["internal/view/text.templ"]), "https://github.com/owenwexler/gotth-plus") {
		t.Error("the GOTTH+ link on the splash page was changed")
	}
}

func TestBuildFiles(t *testing.T) {
	p := build(t, testOptions(), "/work/demo-app")

	for _, name := range []string{versionFile, goModTemplate} {
		if _, ok := p[name]; ok {
			t.Errorf("%s should not be in the project", name)
		}
	}
	for _, name := range []string{"go.mod", "go.sum", ".gitignore", ".env.example", ".env", "AGENTS.md", "CLAUDE.md", "SECURITY.md", "static/font/.gitkeep", "download-js.sh"} {
		if _, ok := p[name]; !ok {
			t.Errorf("%s is missing", name)
		}
	}
	if !strings.HasPrefix(string(p["README.md"]), "# demo-app\n") {
		t.Error("README title not set")
	}
	if !strings.Contains(string(p["internal/view/pages.templ"]), `@Layout("demo-app")`) {
		t.Error("page title not set")
	}
}

func TestBuildEnv(t *testing.T) {
	o := testOptions()
	p := build(t, o, "/work/demo-app")
	if !strings.Contains(string(p[".env.example"]), "\nPort=4000\n") || !strings.Contains(string(p[".env"]), "\nPort=4000\n") {
		t.Error("port not set in .env.example and .env")
	}
	if !regexp.MustCompile(`\nSeedAdminKey=[0-9a-f]{64}\n`).Match(p[".env"]) {
		t.Error(".env has no random SeedAdminKey")
	}
	if !strings.Contains(string(p[".env.example"]), "\nSeedAdminKey=\n") {
		t.Error(".env.example must not get the key")
	}

	o.SeedAdminKey = false
	p = build(t, o, "/work/demo-app")
	if !strings.Contains(string(p[".env"]), "\nSeedAdminKey=\n") {
		t.Error("SeedAdminKey set although it was turned off")
	}
}

func TestRewriteGoImports(t *testing.T) {
	src := `package x

import (
	"fmt"

	"github.com/owenwexler/gotth-plus/internal/b"
	"github.com/owenwexler/gotth-plus-other/a"
)

var _ = fmt.Sprint("github.com/owenwexler/gotth-plus/internal/b")
`
	got, err := rewriteGoImports("x.go", []byte(src), templateModule, "zz.example/app")
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if !strings.Contains(s, `"zz.example/app/internal/b"`) {
		t.Error("import not renamed")
	}
	if !strings.Contains(s, `"github.com/owenwexler/gotth-plus-other/a"`) {
		t.Error("a module that only shares the prefix was renamed")
	}
	if !strings.Contains(s, `fmt.Sprint("github.com/owenwexler/gotth-plus/internal/b")`) {
		t.Error("a string literal that is not an import was changed")
	}
}

func TestCheckDest(t *testing.T) {
	dir := t.TempDir()
	if err := CheckDest(filepath.Join(dir, "missing"), false); err != nil {
		t.Errorf("missing dir: %v", err)
	}
	if err := CheckDest(dir, false); err != nil {
		t.Errorf("empty dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "f"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CheckDest(dir, false); err == nil {
		t.Error("non-empty dir accepted without force")
	}
	if err := CheckDest(dir, true); err != nil {
		t.Errorf("non-empty dir with force: %v", err)
	}
	if err := CheckDest(filepath.Join(dir, "f"), true); err == nil {
		t.Error("a file accepted as the project directory")
	}
}

// TestGeneratedProjectBuilds writes a project and runs the Go tools on it, the way a user would.
// It uses "." in a directory with an awkward name, so the title escaping is covered too.
func TestGeneratedProjectBuilds(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go build, vet and test on a generated project")
	}
	dir := filepath.Join(t.TempDir(), `Bob's "app" <1>`)
	o := testOptions()
	o.Name = "."
	if err := build(t, o, dir).Write(dir); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(filepath.Join(dir, "download-js.sh"))
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Errorf("download-js.sh is not executable: %v %v", info.Mode(), err)
	}

	for _, args := range [][]string{
		{"build", "./..."},
		{"vet", "-tags", "e2e", "./..."},
		{"test", "-count=1", "./..."},
	} {
		cmd := exec.Command("go", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	if out, err := exec.Command("gofmt", "-l", dir).Output(); err != nil || len(out) > 0 {
		t.Errorf("gofmt -l: %v\n%s", err, out)
	}
}
