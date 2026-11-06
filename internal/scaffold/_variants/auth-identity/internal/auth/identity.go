package auth

import "strings"

// Identity is who a sign-in provider says the user is.
type Identity struct {
	Provider string
	// Subject is the provider's stable ID for the user; accounts are matched on (Provider, Subject).
	Subject string
	// Email is lowercased. EmailVerified is the provider's word that the user controls it.
	Email         string
	EmailVerified bool
}

// normalize lowercases and trims an email address.
func normalize(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
