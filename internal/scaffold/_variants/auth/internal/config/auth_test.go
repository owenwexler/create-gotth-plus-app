package config

import (
	"strings"
	"testing"
)

// TestProductionNeedsSignInSettings: production refuses to start without each sign-in setting,
// rather than failing at the first sign-in.
func TestProductionNeedsSignInSettings(t *testing.T) {
	complete := map[string]string{
		"GoEnv":       "production",
		"Port":        "8080",
		"DatabaseURL": "postgres://db.example/app", // only read when the database is a server
		"BaseURL":     "https://example.com",
		// cgpa:if password
		"SMTPHost": "smtp.example.com",
		"MailFrom": "app@example.com",
		// cgpa:end
		// cgpa:if google
		"GoogleClientID":     "id",
		"GoogleClientSecret": "secret",
		// cgpa:end
		// cgpa:if github
		"GitHubClientID":     "id",
		"GitHubClientSecret": "secret",
		// cgpa:end
		// cgpa:if apple
		"AppleClientID":       "com.example.web",
		"AppleTeamID":         "TEAM123",
		"AppleKeyID":          "KEY123",
		"ApplePrivateKeyFile": "/run/secrets/AuthKey.p8",
		// cgpa:end
		// cgpa:if supabase
		"SupabaseURL":            "https://abc.supabase.co",
		"SupabasePublishableKey": "sb_publishable_test",
		// cgpa:end
		// cgpa:if oidc
		"OIDCIssuerURL":    "https://example.authkit.app",
		"OIDCClientID":     "id",
		"OIDCClientSecret": "secret",
		// cgpa:end
	}
	set := func(t *testing.T, env map[string]string) {
		for k, v := range env {
			t.Setenv(k, v)
		}
	}
	panics := func(t *testing.T) (msg string) {
		t.Helper()
		defer func() {
			if r := recover(); r != nil {
				msg, _ = r.(string)
				if msg == "" {
					msg = "panic"
				}
			}
		}()
		GetEnv()
		return ""
	}

	t.Run("complete", func(t *testing.T) {
		set(t, complete)
		if msg := panics(t); msg != "" {
			t.Fatalf("GetEnv panicked: %s", msg)
		}
	})
	for key := range complete {
		if key == "GoEnv" || key == "Port" || key == "DatabaseURL" {
			continue
		}
		t.Run("without "+key, func(t *testing.T) {
			set(t, complete)
			t.Setenv(key, "")
			if msg := panics(t); !strings.Contains(msg, key) {
				t.Errorf("GetEnv without %s: panic %q, want one naming it", key, msg)
			}
		})
	}
	t.Run("http BaseURL", func(t *testing.T) {
		set(t, complete)
		t.Setenv("BaseURL", "http://example.com")
		if msg := panics(t); !strings.Contains(msg, "BaseURL") {
			t.Errorf("panic %q", msg)
		}
	})
}
