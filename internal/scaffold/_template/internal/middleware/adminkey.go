package middleware

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
)

// RequireAdminKey rejects requests whose X-Admin-Key header doesn't match key.
// An empty key disables the check (intended for local development only).
func RequireAdminKey(key string, next http.Handler) http.Handler {
	if key == "" {
		return next
	}
	want := sha256.Sum256([]byte(key))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := sha256.Sum256([]byte(r.Header.Get("X-Admin-Key")))

		// hashing first makes the comparison constant-time regardless of length
		if subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
