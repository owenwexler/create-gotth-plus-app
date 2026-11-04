package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/owenwexler/gotth-plus/internal/csrf"
)

// Test helpers shared by the sign-in tests.

// visitor keeps cookies like a real one and sends the CSRF token the way htmx does.
type visitor struct {
	t       *testing.T
	handler http.Handler
	cookies map[string]*http.Cookie
}

func newVisitor(t *testing.T, handler http.Handler) *visitor {
	t.Helper()
	b := &visitor{t: t, handler: handler, cookies: map[string]*http.Cookie{}}
	b.get("/")
	if b.cookie(csrf.CookieName) == "" {
		t.Fatal("GET / did not set the CSRF cookie")
	}
	return b
}

func (b *visitor) cookie(name string) string {
	if c, ok := b.cookies[name]; ok {
		return c.Value
	}
	return ""
}

func (b *visitor) do(method, path string, form url.Values, htmx bool) *httptest.ResponseRecorder {
	b.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for _, c := range b.cookies {
		req.AddCookie(&http.Cookie{Name: c.Name, Value: c.Value})
	}
	if token := b.cookie(csrf.CookieName); token != "" {
		req.Header.Set(csrf.HeaderName, token)
	}
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	rec := httptest.NewRecorder()
	b.handler.ServeHTTP(rec, req)
	for _, c := range rec.Result().Cookies() {
		if c.MaxAge < 0 || (!c.Expires.IsZero() && c.Expires.Before(time.Now())) {
			delete(b.cookies, c.Name)
			continue
		}
		b.cookies[c.Name] = c
	}
	return rec
}

func (b *visitor) get(path string) *httptest.ResponseRecorder {
	return b.do("GET", path, nil, false)
}

// post submits a form the way the htmx forms do.
func (b *visitor) post(path string, form url.Values) *httptest.ResponseRecorder {
	return b.do("POST", path, form, true)
}
