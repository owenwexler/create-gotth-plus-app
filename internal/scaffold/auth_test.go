package scaffold

import (
	"bytes"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/owenwexler/create-gotth-plus-app/internal/options"
)

func TestBuildAuth(t *testing.T) {
	for _, db := range []options.Database{options.SQLite, options.Postgres, options.MySQL} {
		t.Run(string(db), func(t *testing.T) {
			o := testOptions()
			o.Auth, o.Database = options.BuiltinAuth, db
			p := mustBuild(t, o, "/work/demo-app")

			for _, f := range []string{"internal/auth/session.go", "internal/auth/password.go", "internal/mail/mail.go", "internal/service/auth.go", "internal/factory/auth.go", "internal/view/auth_templ.go", "cmd/server/auth_test.go", "cmd/server/e2e_auth_test.go"} {
				if _, ok := p[f]; !ok {
					t.Errorf("%s is missing", f)
				}
			}
			user := string(p["internal/model/user.go"])
			if strings.Contains(user, "__UUID_TYPE__") || !strings.Contains(user, `gorm:"type:`+uuidColumnType(db)+`;primaryKey"`) {
				t.Errorf("UUID column type not set for %s", db)
			}
			mustContain(t, p, "internal/app/app.go", `"example.com/acme/demo/internal/auth"`)
			mustContain(t, p, "internal/view/pages.templ", "@AuthNav()")
			mustContain(t, p, "internal/view/pages.templ", `@Layout("demo-app")`)
			mustContain(t, p, "internal/view/pages_templ.go", `Layout("demo-app")`)
			mustContain(t, p, "internal/config/config.go", "BaseURL must be")
			mustContain(t, p, "internal/csrf/csrf.go", "func Rotate(")
			mustContain(t, p, "cmd/server/testutil_test.go", "db.AutoMigrate(model.All()...)")
			mustContain(t, p, "internal/seed/seed.go", "h.db.AutoMigrate(model.All()...)")
			mustContain(t, p, "go.mod", "github.com/alexedwards/scs/v2 ")
			mustContain(t, p, "SECURITY.md", "This project signs users in with an email and a password.")
			mustContain(t, p, "AGENTS.md", "- Auth: email and password")
			mustContain(t, p, "README.md", "| GET | `/account` |")
			if !regexp.MustCompile(`\nSeedUserPassword=[0-9a-f]{24}\n`).Match(p[".env"]) {
				t.Error(".env has no random SeedUserPassword")
			}
			mustContain(t, p, ".env.example", "\nSeedUserPassword=\n")
		})
	}

	o := testOptions()
	o.Auth, o.E2E, o.Axe = options.BuiltinAuth, false, false
	if _, ok := mustBuild(t, o, "/work/demo-app")["cmd/server/e2e_auth_test.go"]; ok {
		t.Error("the auth E2E test is there without the E2E harness")
	}

	o = testOptions()
	if _, ok := mustBuild(t, o, "/work/demo-app")["internal/auth/session.go"]; ok {
		t.Error("auth files added with --auth=none")
	}
}

// TestVariantTemplGenerated checks that the committed *_templ.go files in _variants are what the
// template's templ version generates from the .templ files (make gen-templ).
func TestVariantTemplGenerated(t *testing.T) {
	if testing.Short() {
		t.Skip("runs templ generate")
	}
	_, version, err := Requirements(Template())
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	var templs []string
	err = fs.WalkDir(embedded, "_variants", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".templ") {
			return err
		}
		b, err := fs.ReadFile(embedded, path)
		if err != nil {
			return err
		}
		// templ skips directories whose names start with "_", so the copy drops the underscore
		dest := filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(path, "_")))
		templs = append(templs, path)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dest, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "run", "github.com/a-h/templ/cmd/templ@"+version, "generate", "-path", ".")
	cmd.Dir = filepath.Join(dir, "variants")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("templ generate: %v\n%s", err, out)
	}
	for _, path := range templs {
		gen := strings.TrimSuffix(path, ".templ") + "_templ.go"
		want, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(gen, "_"))))
		if err != nil {
			t.Fatal(err)
		}
		got, err := fs.ReadFile(embedded, gen)
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s is not what templ %s generates: run make gen-templ", gen, version)
		}
	}
}
