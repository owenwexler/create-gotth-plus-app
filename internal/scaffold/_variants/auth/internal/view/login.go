package view

import "net/url"

// LoginOptions say what the sign-in page offers.
type LoginOptions struct {
	// Next is where to go after signing in (already checked with auth.SafeNext).
	Next string
	// Password shows the email and password form, and the sign-up and reset links.
	Password bool
	// Providers are the "Continue with ..." buttons.
	Providers []LoginProvider
}

// LoginProvider is one "Continue with ..." button.
type LoginProvider struct {
	Name  string // in the URL: /auth/{Name}/login
	Label string
}

// providerLoginURL starts the provider's sign-in, keeping where to go afterwards.
func providerLoginURL(name, next string) string {
	u := "/auth/" + url.PathEscape(name) + "/login"
	if next != "" {
		u += "?next=" + url.QueryEscape(next)
	}
	return u
}
