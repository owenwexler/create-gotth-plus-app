package options

import "testing"

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

func TestDefaultModulePath(t *testing.T) {
	if got := DefaultModulePath("/home/me/my-app", "acme"); got != "github.com/acme/my-app" {
		t.Errorf("with user: got %q", got)
	}
	if got := DefaultModulePath("my-app", ""); got != "my-app" {
		t.Errorf("without user: got %q", got)
	}
}
