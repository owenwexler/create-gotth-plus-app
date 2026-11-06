package scaffold

import (
	"bytes"
	"go/format"
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
			mustContain(t, p, "SECURITY.md", "**Built-in sign-in is experimental.**")
			mustContain(t, p, "AGENTS.md", "Built-in auth is experimental")
			mustContain(t, p, "AGENTS.md", "- Auth: sign-in with an email and a password")
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
	// one variant at a time, from its root, as in a project (templ writes paths relative to it)
	variants, err := os.ReadDir(filepath.Join(dir, "variants"))
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range variants {
		cmd := exec.Command("go", "run", "github.com/a-h/templ/cmd/templ@"+version, "generate", "-path", ".")
		cmd.Dir = filepath.Join(dir, "variants", v.Name())
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("templ generate in %s: %v\n%s", v.Name(), err, out)
		}
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

func TestStripBlocks(t *testing.T) {
	src := "a\n// cgpa:if google github\nb\n// cgpa:end\n\t// cgpa:if apple\nc\n\t// cgpa:end\nd\n"
	got, err := stripBlocks(src, map[string]bool{"google": false, "github": true, "apple": false})
	if err != nil || got != "a\nb\nd\n" {
		t.Errorf("stripBlocks = %q, %v", got, err)
	}
	for _, bad := range []string{
		"// cgpa:if nope\n// cgpa:end\n", // unknown condition
		"// cgpa:if apple\n",             // not closed
		"// cgpa:end\n",                  // not opened
		"// cgpa:if apple\n// cgpa:if apple\n// cgpa:end\n",
		"//cgpa:if apple\n//cgpa:end\n", // gofmt would move these
		"// cgpa:else\n",
	} {
		if _, err := stripBlocks(bad, map[string]bool{"apple": true}); err == nil {
			t.Errorf("stripBlocks(%q) = nil error", bad)
		}
	}
}

// TestVariantsAreGofmtClean: the variant sources are formatted as they are, so gofmt has never
// moved a marker (see stripBlocks), and generated files only differ from them where blocks went.
func TestVariantsAreGofmtClean(t *testing.T) {
	err := fs.WalkDir(embedded, "_variants", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		b, err := fs.ReadFile(embedded, path)
		if err != nil {
			return err
		}
		formatted, err := format.Source(b)
		if err != nil {
			t.Errorf("%s: %v", path, err)
		} else if !bytes.Equal(formatted, b) {
			t.Errorf("%s is not gofmt-formatted", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestNoStrayDocComments: with a provider left out, none of its doc comments stay behind.
func TestNoStrayDocComments(t *testing.T) {
	o := testOptions()
	o.Auth, o.AuthMethods = options.BuiltinAuth, []options.AuthMethod{options.Google}
	p := mustBuild(t, o, "/work/demo-app")
	for _, name := range []string{"internal/auth/providers.go", "internal/app/providers.go"} {
		for _, w := range []string{"GitHub", "Apple"} {
			if strings.Contains(string(p[name]), w) {
				t.Errorf("%s mentions %s in a Google-only project", name, w)
			}
		}
	}
	for _, name := range p.Paths() {
		if strings.Contains(string(p[name]), "cgpa:") {
			t.Errorf("%s still has a // cgpa: marker", name)
		}
	}
}

func TestBuildOAuth(t *testing.T) {
	o := testOptions()
	o.Auth, o.AuthMethods = options.BuiltinAuth, []options.AuthMethod{options.GitHub, options.Apple}
	p := mustBuild(t, o, "/work/demo-app")

	for _, f := range []string{"internal/auth/oauth.go", "internal/auth/providers.go", "internal/factory/oauth.go", "internal/service/identity.go", "cmd/server/oauth_test.go"} {
		if _, ok := p[f]; !ok {
			t.Errorf("%s is missing", f)
		}
	}
	// no password: none of its files, settings or routes
	for _, f := range []string{"internal/auth/password.go", "internal/mail/mail.go", "internal/factory/password.go", "cmd/server/auth_test.go"} {
		if _, ok := p[f]; ok {
			t.Errorf("%s is there without password sign-in", f)
		}
	}
	for _, w := range []string{"SMTPHost", "SeedUserPassword"} {
		if strings.Contains(string(p[".env.example"]), w) || strings.Contains(string(p["internal/config/config.go"]), w) {
			t.Errorf("%s set up without password sign-in", w)
		}
	}
	mustContain(t, p, ".env.example", "\nGitHubClientID=\nGitHubClientSecret=\n")
	mustContain(t, p, ".env.example", "\nApplePrivateKeyFile=\n")
	mustContain(t, p, "internal/config/config.go", "GitHubClientID, GitHubClientSecret must be set in production")
	mustContain(t, p, "internal/app/app.go", `mux.HandleFunc("POST /auth/{provider}/callback"`)
	mustContain(t, p, "internal/app/app.go", `crossOrigin.AddInsecureBypassPattern(appleCallback)`)
	mustContain(t, p, "SECURITY.md", "`POST /auth/apple/callback` alone is exempt")
	mustContain(t, p, "README.md", "### Sign-in providers")
	mustContain(t, p, "go.mod", "github.com/coreos/go-oidc/v3 ")
	if strings.Contains(string(p["internal/auth/providers.go"]), "GoogleEndpoints") {
		t.Error("Google's code is there without Google")
	}
	if strings.Contains(string(p[".env"]), "SeedUserPassword") {
		t.Error(".env has a SeedUserPassword without password sign-in")
	}

	// without Apple, no route skips the CSRF check
	o.AuthMethods = []options.AuthMethod{options.Google}
	p = mustBuild(t, o, "/work/demo-app")
	if strings.Contains(string(p["internal/app/app.go"]), "AddInsecureBypassPattern") || strings.Contains(string(p["internal/app/app.go"]), "POST /auth/{provider}/callback") {
		t.Error("the CSRF exemption is there without Apple")
	}
}

func TestBuildOIDC(t *testing.T) {
	o := testOptions()
	o.Auth, o.OIDCPreset = options.OIDCAuth, "logto"
	p := mustBuild(t, o, "/work/demo-app")
	mustContain(t, p, "internal/auth/providers.go", "func OIDC(")
	mustContain(t, p, "internal/app/providers.go", "auth.OIDC(env.OIDCLabel")
	mustContain(t, p, "internal/app/app.go", "authHandlers.AfterLogout")
	mustContain(t, p, ".env.example", "https://<tenant-id>.logto.app/oidc")
	mustContain(t, p, ".env.example", "\nOIDCLabel=Logto\n")
	mustContain(t, p, "internal/config/config.go", "OIDCIssuerURL, OIDCClientID, OIDCClientSecret must be set in production")
	mustContain(t, p, "SECURITY.md", "requires the ID token's issuer to be exactly `OIDCIssuerURL`")
	mustContain(t, p, "go.mod", "github.com/coreos/go-oidc/v3 ")
	for _, f := range []string{"internal/auth/password.go", "internal/mail/mail.go"} {
		if _, ok := p[f]; ok {
			t.Errorf("%s is there with OIDC sign-in", f)
		}
	}
	for _, w := range []string{"GoogleEndpoints", "GitHubEndpoints", "AppleEndpoints"} {
		if strings.Contains(string(p["internal/auth/providers.go"]), w) {
			t.Errorf("%s is there with OIDC sign-in", w)
		}
	}
	if DepsKey(o) != "sqlite-purego-auth-oauth" {
		t.Errorf("DepsKey = %q", DepsKey(o))
	}

	o.OIDCPreset = "other"
	mustContain(t, mustBuild(t, o, "/work/demo-app"), ".env.example", "\nOIDCLabel=single sign-on\n")
}

func TestBuildSupabase(t *testing.T) {
	o := testOptions()
	o.Auth, o.Database = options.SupabaseAuth, options.Postgres
	p := mustBuild(t, o, "/work/demo-app")
	for _, f := range []string{"internal/auth/supabase.go", "internal/factory/supabase.go", "internal/app/supabase.go", "internal/service/identity.go", "cmd/server/supabase_test.go"} {
		if _, ok := p[f]; !ok {
			t.Errorf("%s is missing", f)
		}
	}
	for _, f := range []string{"internal/auth/password.go", "internal/auth/oauth.go", "internal/mail/mail.go", "cmd/server/oauth_test.go"} {
		if _, ok := p[f]; ok {
			t.Errorf("%s is there with Supabase sign-in", f)
		}
	}
	mustContain(t, p, ".env.example", "\nSupabaseURL=\nSupabasePublishableKey=\n")
	mustContain(t, p, "internal/config/config.go", "SupabaseURL, SupabasePublishableKey must be set in production")
	mustContain(t, p, "SECURITY.md", "never the secret or\n      service_role key")
	if strings.Contains(string(p["SECURITY.md"]), "experimental") {
		t.Error("Supabase sign-in is labelled experimental")
	}
	mustContain(t, p, "README.md", "`/auth/supabase/callback`")
	if strings.Contains(string(p["go.mod"]), "go-oidc") || strings.Contains(string(p["go.mod"]), "x/oauth2") {
		t.Error("go.mod has OAuth libraries Supabase sign-in doesn't use")
	}
	if DepsKey(o) != "postgres-auth-supabase" {
		t.Errorf("DepsKey = %q", DepsKey(o))
	}

	o.Database = options.SQLite
	if err := o.Validate(); err == nil {
		t.Error("Supabase on SQLite validated")
	}
}
