package scaffold

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"

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

func mustBuild(t *testing.T, o options.Options, dir string) Project {
	t.Helper()
	p, err := Build(Template(), o, dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return p
}

func TestBuildRenamesModule(t *testing.T) {
	p := mustBuild(t, testOptions(), "/work/demo-app")

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
	p := mustBuild(t, testOptions(), "/work/demo-app")

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
	p := mustBuild(t, o, "/work/demo-app")
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
	p = mustBuild(t, o, "/work/demo-app")
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

func TestRequirements(t *testing.T) {
	goVersion, templVersion, err := Requirements(Template())
	if err != nil {
		t.Fatal(err)
	}
	if goVersion == "" || !strings.HasPrefix(templVersion, "v") {
		t.Errorf("Requirements = %q, %q", goVersion, templVersion)
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

// choices are the database choices the generated-project tests cover. serverEnv names the variable
// that points the generated project's tests at a server; without it, those tests are not run (the
// project is still built and vetted).
var choices = []struct {
	name      string
	db        options.Database
	driver    options.SQLiteDriver
	serverEnv string
}{
	{"sqlite-purego", options.SQLite, options.PureGo, ""},
	{"sqlite-cgo", options.SQLite, options.CGO, ""},
	{"postgres", options.Postgres, options.PureGo, "CGPA_TEST_POSTGRES_URL"},
	{"mysql", options.MySQL, options.PureGo, "CGPA_TEST_MYSQL_URL"},
}

// TestGeneratedProjectBuilds writes a project for each database and runs the Go tools on it, the way
// a user would. It uses "." in a directory with an awkward name, so the title escaping is covered too.
func TestGeneratedProjectBuilds(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go build, vet and test on generated projects")
	}
	for _, c := range choices {
		for _, e2e := range []bool{true, false} {
			name := c.name
			if !e2e {
				name += "-noe2e"
			}
			t.Run(name, func(t *testing.T) { testGeneratedProject(t, c.db, c.driver, c.serverEnv, e2e, options.NoAuth) })
		}
		// built-in auth with every method on every database, and a few other sets below
		t.Run(c.name+"-auth-all", func(t *testing.T) {
			testGeneratedProject(t, c.db, c.driver, c.serverEnv, true, options.BuiltinAuth, options.AuthMethodList...)
		})
	}
	t.Run("sqlite-purego-auth-password-noe2e", func(t *testing.T) {
		testGeneratedProject(t, options.SQLite, options.PureGo, "", false, options.BuiltinAuth, options.Password)
	})
	for _, m := range []options.AuthMethod{options.Google, options.GitHub, options.Apple} {
		t.Run("sqlite-purego-auth-"+string(m), func(t *testing.T) {
			testGeneratedProject(t, options.SQLite, options.PureGo, "", true, options.BuiltinAuth, m)
		})
	}
}

// testGeneratedProject generates one project and checks it the way a user would build it.
func testGeneratedProject(t *testing.T, db options.Database, driver options.SQLiteDriver, serverEnv string, e2e bool, a options.Auth, methods ...options.AuthMethod) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), `Bob's "app" <1>`)
	o := testOptions()
	o.Name = "."
	o.Database, o.SQLiteDriver = db, driver
	o.E2E, o.Axe = e2e, e2e
	o.Auth = a
	if len(methods) > 0 {
		o.AuthMethods = methods
	}
	if err := mustBuild(t, o, dir).Write(dir); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(filepath.Join(dir, "download-js.sh"))
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Errorf("download-js.sh is not executable: %v %v", info, err)
	}

	env := os.Environ()
	if driver == options.PureGo || db.IsServer() {
		env = append(env, "CGO_ENABLED=0") // proves the pure-Go drivers need no C compiler
	}
	cmds := [][]string{
		{"mod", "tidy", "-diff"}, // _deps is up to date with the code
		{"build", "./..."},
		{"vet", "-tags", "e2e", "./..."},
	}
	switch {
	case serverEnv == "":
		cmds = append(cmds, []string{"test", "-count=1", "./..."})
	case os.Getenv(serverEnv) != "":
		env = append(env, "TestDatabaseURL="+os.Getenv(serverEnv))
		cmds = append(cmds, []string{"test", "-count=1", "./..."})
	default:
		t.Logf("set %s to run the generated project's tests against a server", serverEnv)
	}
	for _, args := range cmds {
		cmd := exec.Command("go", args...)
		cmd.Dir = dir
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	if out, err := exec.Command("gofmt", "-l", dir).Output(); err != nil || len(out) > 0 {
		t.Errorf("gofmt -l: %v\n%s", err, out)
	}
	if !e2e {
		// the Makefile still parses, and no target refers to the removed tests
		out, err := exec.Command("make", "-C", dir, "-n", "setup", "vet", "test", "help").CombinedOutput()
		text := string(out)
		if err != nil || strings.Contains(text, "-tags e2e") || strings.Contains(text, "e2e-install") || strings.Contains(text, "playwright") {
			t.Errorf("make -n: %v\n%s", err, out)
		}
	}
}

func TestBuildDatabases(t *testing.T) {
	for _, c := range choices {
		t.Run(c.name, func(t *testing.T) {
			o := testOptions()
			o.Database = c.db
			o.SQLiteDriver = c.driver
			p := mustBuild(t, o, "/work/demo-app")
			gomod := string(p["go.mod"])

			switch {
			case c.db == options.Postgres:
				mustContain(t, p, "go.mod", "gorm.io/driver/postgres ")
				mustContain(t, p, ".env", "\nDatabaseURL=postgres://localhost:5432/demo_app?sslmode=disable\n")
				mustContain(t, p, "internal/model/model.go", "`gorm:\"type:uuid\"`")
				mustContain(t, p, "README.md", "createdb demo_app")
			case c.db == options.MySQL:
				mustContain(t, p, "go.mod", "gorm.io/driver/mysql ")
				mustContain(t, p, ".env", "\nDatabaseURL=root@tcp(localhost:3306)/demo_app?parseTime=true&charset=utf8mb4&loc=UTC\n")
				mustContain(t, p, "internal/model/model.go", "`gorm:\"type:char(36)\"`")
				mustContain(t, p, "internal/escaper/escaper.go", "no ESCAPE clause")
			case c.driver == options.PureGo:
				mustContain(t, p, "internal/database/database.go", `"github.com/glebarez/sqlite"`)
				mustContain(t, p, "go.mod", "github.com/glebarez/sqlite ")
			default:
				mustContain(t, p, "go.mod", "gorm.io/driver/sqlite ")
				mustContain(t, p, ".env", "\nDatabasePath=app.db\n")
			}

			if c.db.IsServer() || c.driver == options.PureGo {
				if strings.Contains(gomod, "mattn/go-sqlite3") {
					t.Error("go.mod still requires the CGO SQLite driver")
				}
			}
			if c.db.IsServer() {
				for _, name := range p.Paths() {
					if strings.HasSuffix(name, ".go") && strings.Contains(string(p[name]), "DatabasePath") {
						t.Errorf("%s still uses DatabasePath", name)
					}
				}
				mustContain(t, p, "internal/database/dbtest/dbtest.go", `"example.com/acme/demo/internal/database"`)
				mustContain(t, p, "cmd/server/testutil_test.go", "dbtest.Open(t)")
				mustContain(t, p, "SECURITY.md", "### Data: "+c.db.Label()+" and GORM")
				if strings.Contains(string(p[".gitignore"]), "*.db") {
					t.Error(".gitignore still ignores SQLite files")
				}
			}
		})
	}
}

func TestBuildWithoutE2E(t *testing.T) {
	o := testOptions()
	o.E2E, o.Axe = false, false
	p := mustBuild(t, o, "/work/demo-app")

	for _, f := range e2eFiles {
		if _, ok := p[f]; ok {
			t.Errorf("%s is still there", f)
		}
	}
	for _, f := range []string{"go.mod", "Makefile"} {
		if strings.Contains(strings.ToLower(string(p[f])), "playwright") || strings.Contains(string(p[f]), "e2e") {
			t.Errorf("%s still mentions the E2E tests", f)
		}
	}
	mustContain(t, p, "README.md", "created without the Playwright end-to-end tests")
	mustContain(t, p, "SECURITY.md", "this project has no browser tests")
	mustContain(t, p, "AGENTS.md", "There are no browser (E2E) tests.")
	if strings.Contains(string(p["AGENTS.md"]), "e2e_helpers_test.go") {
		t.Error("AGENTS.md still describes the E2E harness")
	}
}

func TestLiveReload(t *testing.T) {
	o := testOptions() // port 4000
	p := mustBuild(t, o, "/work/demo-app")
	// the air.toml fix applies either way, since make air exists in both
	mustContain(t, p, "air.toml", `cmd = "templ generate && go build -o ./tmp/main ./cmd/server"`)
	mustContain(t, p, "air.toml", "app_port = 4000\n")
	mustContain(t, p, "Makefile", "\t$(MAKE) -j2 dev-css dev-templ\n")
	mustContain(t, p, "Makefile", "\tair -c air.toml\n")

	o.LiveReload = options.Air
	p = mustBuild(t, o, "/work/demo-app")
	mustContain(t, p, "Makefile", "\t$(MAKE) -j2 dev-css air\n")
	mustContain(t, p, "README.md", "go install github.com/air-verse/air@latest")
}

func TestReplaceSpan(t *testing.T) {
	p := Project{"f": []byte("a\ncodegen:\n\tcmd\nz")}
	if err := p.replaceSpan("f", "\ncodegen:\n", "\n", ""); err != nil {
		t.Fatal(err)
	}
	if string(p["f"]) != "az" {
		t.Errorf("got %q", p["f"])
	}
}

func mustContain(t *testing.T, p Project, file, want string) {
	t.Helper()
	if !strings.Contains(string(p[file]), want) {
		t.Errorf("%s does not contain %q", file, want)
	}
}

// TestDepsNotOlderThanTemplate catches a gotth-plus update (make sync-template) that raised a
// dependency without make update-deps: the generated project would silently get the older version.
func TestDepsNotOlderThanTemplate(t *testing.T) {
	tmpl, err := fs.ReadFile(Template(), goModTemplate)
	if err != nil {
		t.Fatal(err)
	}
	want := requires(t, tmpl)
	for _, o := range DepsChoices() {
		key := DepsKey(o)
		b, err := fs.ReadFile(embedded, "_deps/"+key+"/"+goModTemplate)
		if err != nil {
			t.Fatalf("%s: %v (run make update-deps)", key, err)
		}
		got := requires(t, b)
		for mod, v := range want {
			if have, ok := got[mod]; ok && semver.Compare(have, v) < 0 {
				t.Errorf("_deps/%s has %s %s, the template has %s: run make update-deps", key, mod, have, v)
			}
		}
		if modfile.ModulePath(b) != modfile.ModulePath(tmpl) {
			t.Errorf("_deps/%s/go.mod has module %q, want the template's", key, modfile.ModulePath(b))
		}
	}

	// every folder in _deps belongs to a current choice, so renamed keys don't leave stale sets
	keys := map[string]bool{}
	for _, o := range DepsChoices() {
		keys[DepsKey(o)] = true
	}
	entries, err := fs.ReadDir(embedded, "_deps")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() && !keys[e.Name()] {
			t.Errorf("_deps/%s is not used by any choice: delete it", e.Name())
		}
	}
}

func requires(t *testing.T, gomod []byte) map[string]string {
	t.Helper()
	mf, err := modfile.Parse("go.mod", gomod, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]string{}
	for _, r := range mf.Require {
		m[r.Mod.Path] = r.Mod.Version
	}
	return m
}

func TestOverlayRefusesChangedTemplateFile(t *testing.T) {
	p, err := load(Template())
	if err != nil {
		t.Fatal(err)
	}
	p["internal/database/database.go"] = append(p["internal/database/database.go"], "// changed upstream\n"...)
	if _, err := p.overlay("postgres"); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Errorf("overlay = %v, want an error about the changed template file", err)
	}
}
