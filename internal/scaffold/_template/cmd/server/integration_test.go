package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/owenwexler/gotth-plus/internal/app"
	"github.com/owenwexler/gotth-plus/internal/config"
	"github.com/owenwexler/gotth-plus/internal/csrf"
	"github.com/owenwexler/gotth-plus/internal/repository"
)

// client talks to the app the way a browser would: it keeps the CSRF cookie from its first GET and
// echoes it in the X-CSRF-Token header, like the hx-headers attribute on <body> does.
type client struct {
	t       *testing.T
	handler http.Handler
	token   string
}

func newClient(t *testing.T, handler http.Handler) *client {
	t.Helper()
	c := &client{t: t, handler: handler}

	rec := c.do("GET", "/", nil, nil)
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == csrf.CookieName {
			c.token = cookie.Value
		}
	}
	if c.token == "" {
		t.Fatal("GET / did not set the CSRF cookie")
	}
	return c
}

func (c *client) do(method, path string, form url.Values, headers map[string]string) *httptest.ResponseRecorder {
	c.t.Helper()

	req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if c.token != "" {
		req.AddCookie(&http.Cookie{Name: csrf.CookieName, Value: c.token})
		req.Header.Set(csrf.HeaderName, c.token)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	rec := httptest.NewRecorder()
	c.handler.ServeHTTP(rec, req)
	return rec
}

// hello submits the example form like htmx does.
func (c *client) hello(name string) *httptest.ResponseRecorder {
	c.t.Helper()
	return c.do("POST", "/hello", url.Values{"name": {name}}, map[string]string{"HX-Request": "true"})
}

func TestHome(t *testing.T) {
	handler, _ := newTestApp(t, devEnv)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Errorf("Content-Type = %q", got)
	}

	body := rec.Body.String()
	for _, want := range []string{
		"<!doctype html>",
		"GOTTH+",
		`id="greeting"`,
		"Hello!",
		`hx-post="/hello"`,
		`x-data="counter"`,
		`id="toasts"`,
		csrf.HeaderName,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("home page is missing %q", want)
		}
	}

	// the security middleware wraps every response
	for _, h := range []string{"Content-Security-Policy", "X-Content-Type-Options", "Referrer-Policy", "X-Request-ID"} {
		if rec.Header().Get(h) == "" {
			t.Errorf("missing %s header", h)
		}
	}
	if rec.Header().Get("Strict-Transport-Security") != "" {
		t.Error("HSTS must not be sent in development")
	}

	// the CSRF token rendered into the page is the one in the cookie
	var token string
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == csrf.CookieName {
			token = cookie.Value
			if !cookie.HttpOnly {
				t.Error("CSRF cookie must be HttpOnly")
			}
		}
	}
	if token == "" || !strings.Contains(body, token) {
		t.Error("the CSRF token in the cookie must be rendered into hx-headers")
	}
}

func TestUnknownPathIs404(t *testing.T) {
	handler, _ := newTestApp(t, devEnv)
	c := newClient(t, handler)

	for _, path := range []string{"/nope", "/hello", "/.env", "/app.db", "/go.mod"} {
		if got := c.do("GET", path, nil, nil).Code; got != http.StatusNotFound && got != http.StatusMethodNotAllowed {
			t.Errorf("GET %s: status %d, want 404 or 405", path, got)
		}
	}
}

func TestStaticFiles(t *testing.T) {
	handler, _ := newTestApp(t, devEnv)
	c := newClient(t, handler)

	rec := c.do("GET", "/static/js/app.js", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /static/js/app.js: status %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "javascript") {
		t.Errorf("app.js Content-Type = %q", got)
	}

	// no directory listings, and nothing outside static/
	for _, path := range []string{"/static/", "/static/js/", "/static/../go.mod", "/static/%2e%2e/go.mod"} {
		if got := c.do("GET", path, nil, nil).Code; got == http.StatusOK {
			t.Errorf("GET %s: status 200, want it refused", path)
		}
	}
}

func TestHealth(t *testing.T) {
	handler, _ := newTestApp(t, devEnv)
	c := newClient(t, handler)

	rec := c.do("GET", "/healthz", nil, nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Errorf("GET /healthz: status %d, body %q", rec.Code, rec.Body.String())
	}
}

func TestHello(t *testing.T) {
	handler, _ := newTestApp(t, devEnv)
	c := newClient(t, handler)

	t.Run("greets by name", func(t *testing.T) {
		rec := c.hello("Gopher")

		if rec.Code != http.StatusOK {
			t.Fatalf("status %d, want 200", rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, `id="greeting"`) || !strings.Contains(body, "Hello Gopher!") {
			t.Errorf("unexpected fragment: %s", body)
		}
		if strings.Contains(strings.ToLower(body), "<html") {
			t.Error("a fragment must not render the layout")
		}
	})

	t.Run("trims whitespace", func(t *testing.T) {
		if body := c.hello("  Ada  ").Body.String(); !strings.Contains(body, "Hello Ada!") {
			t.Errorf("unexpected fragment: %s", body)
		}
	})

	t.Run("escapes hostile input", func(t *testing.T) {
		body := c.hello("<script>alert(1)</script>").Body.String()

		if strings.Contains(body, "<script>") {
			t.Errorf("name was not escaped: %s", body)
		}
		if !strings.Contains(body, "&lt;script&gt;") {
			t.Errorf("expected the escaped name in: %s", body)
		}
	})

	// validation errors come back as an out of band toast that leaves the page untouched
	for name, tt := range map[string]struct{ input, message string }{
		"empty name":      {"", "Please enter your name"},
		"whitespace only": {"   ", "Please enter your name"},
		"too long":        {strings.Repeat("a", 51), "50 characters or fewer"},
	} {
		t.Run(name, func(t *testing.T) {
			rec := c.hello(tt.input)

			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status %d, want 422", rec.Code)
			}
			if rec.Header().Get("HX-Reswap") != "none" {
				t.Error("an error toast must set HX-Reswap: none")
			}
			body := rec.Body.String()
			if !strings.Contains(body, `hx-swap-oob="beforeend:#toasts"`) || !strings.Contains(body, tt.message) {
				t.Errorf("unexpected toast: %s", body)
			}
			if strings.Contains(body, `id="greeting"`) {
				t.Error("a rejected name must not return a greeting")
			}
		})
	}

	t.Run("longest allowed name", func(t *testing.T) {
		if got := c.hello(strings.Repeat("a", 50)).Code; got != http.StatusOK {
			t.Errorf("status %d, want 200", got)
		}
	})
}

func TestHelloRequiresCSRFToken(t *testing.T) {
	handler, _ := newTestApp(t, devEnv)
	c := newClient(t, handler)
	form := url.Values{"name": {"Gopher"}}

	// no cookie and no header at all
	anonymous := &client{t: t, handler: handler}
	if got := anonymous.do("POST", "/hello", form, nil).Code; got != http.StatusForbidden {
		t.Errorf("no token: status %d, want 403", got)
	}

	// the cookie is sent (as a browser would on a forged request) but the header is wrong
	if got := c.do("POST", "/hello", form, map[string]string{csrf.HeaderName: "forged"}).Code; got != http.StatusForbidden {
		t.Errorf("wrong token: status %d, want 403", got)
	}

	// a cross-site browser request is refused even with a valid token
	if got := c.do("POST", "/hello", form, map[string]string{"Sec-Fetch-Site": "cross-site"}).Code; got != http.StatusForbidden {
		t.Errorf("cross-site: status %d, want 403", got)
	}
}

func TestHelloRejectsOversizedBody(t *testing.T) {
	handler, _ := newTestApp(t, devEnv)
	c := newClient(t, handler)

	if got := c.hello(strings.Repeat("a", app.MaxBodyBytes+1)).Code; got != http.StatusRequestEntityTooLarge {
		t.Errorf("status %d, want 413", got)
	}
}

func TestSeed(t *testing.T) {
	handler, db := newTestApp(t, devEnv)
	c := newClient(t, handler)
	repo := repository.NewExampleRepository(db)

	// seeding twice must not duplicate rows
	for i := 0; i < 2; i++ {
		if rec := c.do("POST", "/seed", nil, nil); rec.Code != http.StatusNoContent {
			t.Fatalf("POST /seed: status %d, body %q", rec.Code, rec.Body.String())
		}
	}

	count, err := repo.Count(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if count != 4 {
		t.Errorf("%d examples after seeding, want the 4 in seedData.json", count)
	}

	examples, err := repo.GetAll(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(examples) == 0 || examples[0].Name != "Go" {
		t.Errorf("expected the first example in the file first, got %+v", examples)
	}

	// seeding is a mutation, so it needs the CSRF token too (seed.sh fetches one)
	anonymous := &client{t: t, handler: handler}
	if got := anonymous.do("POST", "/seed", nil, nil).Code; got != http.StatusForbidden {
		t.Errorf("seed without CSRF token: status %d, want 403", got)
	}
}

func TestSeedAdminKey(t *testing.T) {
	handler, _ := newTestApp(t, config.Env{GoEnv: "development", Port: "0", SeedAdminKey: "s3cret"})
	c := newClient(t, handler)

	if got := c.do("POST", "/seed", nil, nil).Code; got != http.StatusForbidden {
		t.Errorf("no admin key: status %d, want 403", got)
	}
	if got := c.do("POST", "/seed", nil, map[string]string{"X-Admin-Key": "wrong"}).Code; got != http.StatusForbidden {
		t.Errorf("wrong admin key: status %d, want 403", got)
	}
	if got := c.do("POST", "/seed", nil, map[string]string{"X-Admin-Key": "s3cret"}).Code; got != http.StatusNoContent {
		t.Errorf("right admin key: status %d, want 204", got)
	}
}

func TestProduction(t *testing.T) {
	handler, _ := newTestApp(t, config.Env{GoEnv: "production", Port: "0"})
	c := newClient(t, handler)

	// dev-only routes are not registered at all
	if got := c.do("POST", "/seed", nil, nil).Code; got != http.StatusNotFound && got != http.StatusMethodNotAllowed {
		t.Errorf("POST /seed in production: status %d, want 404 or 405", got)
	}

	rec := c.do("GET", "/", nil, nil)
	if rec.Header().Get("Strict-Transport-Security") == "" {
		t.Error("HSTS must be sent in production")
	}

	// a fresh visitor's CSRF cookie is Secure in production
	fresh := httptest.NewRecorder()
	handler.ServeHTTP(fresh, httptest.NewRequest("GET", "/", nil))
	for _, cookie := range fresh.Result().Cookies() {
		if cookie.Name == csrf.CookieName && !cookie.Secure {
			t.Error("CSRF cookie must be Secure in production")
		}
	}
}
