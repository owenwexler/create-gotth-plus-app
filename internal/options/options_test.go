package options

import (
	"strings"
	"testing"
)

func TestValidateName(t *testing.T) {
	for _, name := range []string{".", "app", "my-app", "my_app2", "a.b"} {
		if err := ValidateName(name); err != nil {
			t.Errorf("ValidateName(%q) = %v, want nil", name, err)
		}
	}
	for _, name := range []string{"", "My-App", "-app", "../app", "a/b", "app name", ".."} {
		if err := ValidateName(name); err == nil {
			t.Errorf("ValidateName(%q) = nil, want an error", name)
		}
	}
}

func TestValidateModulePath(t *testing.T) {
	for _, p := range []string{"app", "github.com/acme/app", "example.com/x/y/v2"} {
		if err := ValidateModulePath(p); err != nil {
			t.Errorf("ValidateModulePath(%q) = %v, want nil", p, err)
		}
	}
	for _, p := range []string{"", "github.com/acme/app/", "has space", "/abs"} {
		if err := ValidateModulePath(p); err == nil {
			t.Errorf("ValidateModulePath(%q) = nil, want an error", p)
		}
	}
}

func TestValidatePort(t *testing.T) {
	for _, s := range []string{"1", "3000", "65535"} {
		if err := ValidatePort(s); err != nil {
			t.Errorf("ValidatePort(%q) = %v, want nil", s, err)
		}
	}
	for _, s := range []string{"", "0", "65536", "abc", "7331", "-1"} {
		if err := ValidatePort(s); err == nil {
			t.Errorf("ValidatePort(%q) = nil, want an error", s)
		}
	}
}

func TestDatabaseName(t *testing.T) {
	for in, want := range map[string]string{
		"my-app":                "my_app",
		"app.v2":                "app_v2",
		"9lives":                "app_9lives",
		"Bob's 🙂":               "bob_s",
		"---":                   "app",
		strings.Repeat("a", 80): strings.Repeat("a", 63),
	} {
		if got := DatabaseName(in); got != want {
			t.Errorf("DatabaseName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseDatabase(t *testing.T) {
	for _, d := range Databases {
		if got, err := ParseDatabase(string(d)); err != nil || got != d {
			t.Errorf("ParseDatabase(%q) = %q, %v", d, got, err)
		}
	}
	if _, err := ParseDatabase("oracle"); err == nil {
		t.Error("ParseDatabase(oracle) = nil error")
	}
	if _, err := ParseSQLiteDriver("purego"); err != nil {
		t.Error(err)
	}
	if _, err := ParseSQLiteDriver("wasm"); err == nil {
		t.Error("ParseSQLiteDriver(wasm) = nil error")
	}
}

func TestNeedsCCompiler(t *testing.T) {
	o := Defaults()
	if o.NeedsCCompiler() {
		t.Error("default (pure Go SQLite) needs a C compiler")
	}
	o.SQLiteDriver = CGO
	if !o.NeedsCCompiler() {
		t.Error("CGO SQLite does not need a C compiler")
	}
	o.Database = Postgres
	if o.NeedsCCompiler() {
		t.Error("Postgres needs a C compiler")
	}
}

func TestDefaultModulePath(t *testing.T) {
	if got := DefaultModulePath("/home/me/my-app", "acme"); got != "github.com/acme/my-app" {
		t.Errorf("with user: got %q", got)
	}
	if got := DefaultModulePath("my-app", ""); got != "my-app" {
		t.Errorf("without user: got %q", got)
	}
}
