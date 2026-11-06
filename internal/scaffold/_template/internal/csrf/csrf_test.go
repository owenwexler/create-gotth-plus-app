package csrf

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func newHandler() http.Handler {
	return Middleware(false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Token-In-Ctx", Token(r.Context()))
		w.WriteHeader(http.StatusNoContent)
	}))
}

// issue makes a GET and returns the token cookie value.
func issue(t *testing.T, h http.Handler) string {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	for _, c := range rec.Result().Cookies() {
		if c.Name == CookieName {
			if !c.HttpOnly || c.SameSite != http.SameSiteLaxMode {
				t.Fatalf("cookie must be HttpOnly and SameSite=Lax: %+v", c)
			}
			if rec.Header().Get("X-Token-In-Ctx") != c.Value {
				t.Fatal("token in context must match the cookie")
			}
			return c.Value
		}
	}
	t.Fatal("no CSRF cookie issued")
	return ""
}

func TestMutations(t *testing.T) {
	h := newHandler()
	token := issue(t, h)

	tests := []struct {
		name   string
		method string
		cookie string
		header string
		want   int
	}{
		{"GET needs no token", "GET", "", "", 204},
		{"valid token", "POST", token, token, 204},
		{"valid token on DELETE", "DELETE", token, token, 204},
		{"missing header", "POST", token, "", 403},
		{"wrong header", "PUT", token, "x" + token[1:], 403},
		{"missing cookie", "POST", "", token, 403},
		{"header without matching cookie", "POST", "short", "short", 403},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/", nil)
			if tt.cookie != "" {
				req.AddCookie(&http.Cookie{Name: CookieName, Value: tt.cookie})
			}
			if tt.header != "" {
				req.Header.Set(HeaderName, tt.header)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d", rec.Code, tt.want)
			}
		})
	}
}
