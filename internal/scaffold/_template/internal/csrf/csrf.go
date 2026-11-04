// Package csrf implements double-submit-cookie CSRF protection for htmx apps.
//
// Every visitor gets a random token in an HttpOnly cookie. The same token is rendered into the page
// (the layout puts it in hx-headers) so htmx echoes it back in the X-CSRF-Token header on every request.
// Mutating requests are rejected unless the header matches the cookie. A cross-site attacker can make the
// browser send the cookie but can neither read the token nor set the custom header.
package csrf

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
)

const (
	CookieName = "csrf_token"
	HeaderName = "X-CSRF-Token"

	tokenBytes = 32
	// base64url(32 bytes) without padding
	tokenLen = 43
)

type ctxKey struct{}

// Token returns the CSRF token for the current request, for rendering into pages.
func Token(ctx context.Context) string {
	t, _ := ctx.Value(ctxKey{}).(string)
	return t
}

// Middleware issues the token cookie when missing and verifies it on unsafe methods.
// Set secure to true when serving over HTTPS (production).
func Middleware(secure bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := ""
			if c, err := r.Cookie(CookieName); err == nil && len(c.Value) == tokenLen {
				token = c.Value
			}

			if token == "" {
				var err error
				if token, err = newToken(); err != nil {
					http.Error(w, "internal error", http.StatusInternalServerError)
					return
				}
				http.SetCookie(w, &http.Cookie{
					Name:     CookieName,
					Value:    token,
					Path:     "/",
					HttpOnly: true,
					Secure:   secure,
					SameSite: http.SameSiteLaxMode,
				})
			}

			if !isSafe(r.Method) && !valid(token, r.Header.Get(HeaderName), r.Cookie) {
				http.Error(w, "invalid CSRF token", http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, token)))
		})
	}
}

// valid requires a cookie to have been sent with the request (a token we just minted proves nothing)
// and the header to match it.
func valid(token, header string, cookie func(string) (*http.Cookie, error)) bool {
	c, err := cookie(CookieName)
	if err != nil || len(c.Value) != tokenLen {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(c.Value), []byte(header)) == 1 &&
		subtle.ConstantTimeCompare([]byte(token), []byte(header)) == 1
}

func isSafe(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	}
	return false
}

func newToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
