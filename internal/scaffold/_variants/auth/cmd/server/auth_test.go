package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/owenwexler/gotth-plus/internal/app"
	"github.com/owenwexler/gotth-plus/internal/config"
	"github.com/owenwexler/gotth-plus/internal/csrf"
	"github.com/owenwexler/gotth-plus/internal/mail"
)

const testPassword = "correct horse battery staple"

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

func (b *visitor) login(email, password string) *httptest.ResponseRecorder {
	return b.post("/login", url.Values{"email": {email}, "password": {password}})
}

// newAuthApp is newTestApp with the app's emails recorded instead of logged.
func newAuthApp(t *testing.T, env config.Env) (http.Handler, *mail.Recorder) {
	t.Helper()
	_, db := newTestApp(t, env)
	mailer := &mail.Recorder{}
	return app.NewHandler(env, db, app.WithMailer(mailer)), mailer
}

var tokenLink = regexp.MustCompile(`(/[a-z-]+)\?token=([A-Za-z0-9_-]+)`)

// lastLink returns the path and token of the link in the newest email to the address.
func lastLink(t *testing.T, mailer *mail.Recorder, to string) (string, string) {
	t.Helper()
	sent := mailer.Sent()
	for i := len(sent) - 1; i >= 0; i-- {
		if sent[i].To == to {
			m := tokenLink.FindStringSubmatch(sent[i].Text)
			if m == nil {
				t.Fatalf("no link in the email to %s: %q", to, sent[i].Text)
			}
			return m[1], m[2]
		}
	}
	t.Fatalf("no email to %s", to)
	return "", ""
}

// signUp creates and confirms an account through the forms.
func signUp(t *testing.T, b *visitor, mailer *mail.Recorder, email string) {
	t.Helper()
	if rec := b.post("/signup", url.Values{"email": {email}, "password": {testPassword}}); rec.Code != http.StatusOK {
		t.Fatalf("signup: %d %s", rec.Code, rec.Body)
	}
	_, token := lastLink(t, mailer, email)
	if rec := b.post("/verify-email", url.Values{"token": {token}}); rec.Code != http.StatusOK {
		t.Fatalf("verify: %d %s", rec.Code, rec.Body)
	}
}

func TestSignUpConfirmSignInSignOut(t *testing.T) {
	handler, mailer := newAuthApp(t, devEnv)
	b := newVisitor(t, handler)

	// the email is trimmed and lowercased
	rec := b.post("/signup", url.Values{"email": {"  Ann@Example.com "}, "password": {testPassword}})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Check your email") {
		t.Fatalf("signup: %d %s", rec.Code, rec.Body)
	}

	// not confirmed yet: right password, but no session, and a new link is sent
	if rec := b.login("ann@example.com", testPassword); rec.Code != http.StatusForbidden || b.cookie("session") != "" {
		t.Fatalf("sign-in before confirming: %d, session %q", rec.Code, b.cookie("session"))
	}
	if n := len(mailer.Sent()); n != 2 {
		t.Fatalf("%d emails, want the sign-up link and a new one", n)
	}

	// the emailed link opens a page with a confirm button, without a Referer leak or a change on GET
	path, token := lastLink(t, mailer, "ann@example.com")
	rec = b.get(path + "?token=" + token)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `name="token"`) {
		t.Fatalf("GET %s: %d", path, rec.Code)
	}
	if got := rec.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("Referrer-Policy %q on a page with a token in its URL", got)
	}
	if rec := b.post("/verify-email", url.Values{"token": {token}}); rec.Code != http.StatusOK {
		t.Fatalf("confirm: %d %s", rec.Code, rec.Body)
	}
	if rec := b.post("/verify-email", url.Values{"token": {token}}); rec.Code != http.StatusBadRequest {
		t.Errorf("a used link worked again: %d", rec.Code)
	}

	csrfBefore := b.cookie(csrf.CookieName)
	rec = b.login("ann@example.com", testPassword)
	if rec.Code != http.StatusOK || rec.Header().Get("HX-Redirect") != "/account" {
		t.Fatalf("sign-in: %d, HX-Redirect %q, %s", rec.Code, rec.Header().Get("HX-Redirect"), rec.Body)
	}
	if b.cookie("session") == "" {
		t.Fatal("no session cookie after signing in")
	}
	if b.cookie(csrf.CookieName) == csrfBefore {
		t.Error("the CSRF token was not replaced at sign-in")
	}

	if rec := b.get("/account"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "ann@example.com") {
		t.Fatalf("account: %d", rec.Code)
	}
	if rec := b.get("/"); !strings.Contains(rec.Body.String(), `href="/account"`) {
		t.Error("the home page doesn't link to the account when signed in")
	}

	rec = b.post("/logout", nil)
	if rec.Header().Get("HX-Redirect") != "/" {
		t.Errorf("sign-out: HX-Redirect %q", rec.Header().Get("HX-Redirect"))
	}
	rec = b.get("/account")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login?next=%2Faccount" {
		t.Errorf("account after signing out: %d, Location %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestSignInFailuresLookTheSame(t *testing.T) {
	handler, mailer := newAuthApp(t, devEnv)
	b := newVisitor(t, handler)
	signUp(t, b, mailer, "ann@example.com")

	wrong := b.login("ann@example.com", "not the right password at all")
	unknown := b.login("nobody@example.com", testPassword)
	if wrong.Code != http.StatusUnauthorized || wrong.Code != unknown.Code || wrong.Body.String() != unknown.Body.String() {
		t.Errorf("wrong password: %d %q\nunknown email: %d %q", wrong.Code, wrong.Body, unknown.Code, unknown.Body)
	}
}

func TestSignUpWithTakenEmailLooksTheSame(t *testing.T) {
	handler, mailer := newAuthApp(t, devEnv)
	b := newVisitor(t, handler)
	first := b.post("/signup", url.Values{"email": {"ann@example.com"}, "password": {testPassword}})
	again := b.post("/signup", url.Values{"email": {"ann@example.com"}, "password": {"another long password here"}})
	if first.Code != again.Code || first.Body.String() != again.Body.String() {
		t.Errorf("first: %d %q\nagain: %d %q", first.Code, first.Body, again.Code, again.Body)
	}
	sent := mailer.Sent()
	if len(sent) != 2 || sent[1].Subject != "Sign-up attempt" || tokenLink.MatchString(sent[1].Text) {
		t.Errorf("the account owner should get a note without a token: %+v", sent)
	}
}

func TestSignUpRejectsShortPassword(t *testing.T) {
	handler, mailer := newAuthApp(t, devEnv)
	b := newVisitor(t, handler)
	rec := b.post("/signup", url.Values{"email": {"ann@example.com"}, "password": {"fourteen chars"}})
	if rec.Code != http.StatusUnprocessableEntity || len(mailer.Sent()) != 0 {
		t.Errorf("%d, %d emails", rec.Code, len(mailer.Sent()))
	}
}

func TestSessionFixation(t *testing.T) {
	handler, mailer := newAuthApp(t, devEnv)
	b := newVisitor(t, handler)
	signUp(t, b, mailer, "ann@example.com")

	// an attacker plants a session token before the victim signs in
	planted := "attacker-chosen-session-token-0000000000000"
	b.cookies["session"] = &http.Cookie{Name: "session", Value: planted}
	b.login("ann@example.com", testPassword)
	if b.cookie("session") == planted || b.cookie("session") == "" {
		t.Fatalf("session token after sign-in: %q", b.cookie("session"))
	}

	attacker := newVisitor(t, handler)
	attacker.cookies["session"] = &http.Cookie{Name: "session", Value: planted}
	if rec := attacker.get("/account"); rec.Code != http.StatusSeeOther {
		t.Errorf("the planted token is signed in: %d", rec.Code)
	}
}

func TestPasswordResetSignsOutEverywhere(t *testing.T) {
	handler, mailer := newAuthApp(t, devEnv)
	phone := newVisitor(t, handler)
	signUp(t, phone, mailer, "ann@example.com")
	phone.login("ann@example.com", testPassword)

	laptop := newVisitor(t, handler)
	if rec := laptop.post("/forgot-password", url.Values{"email": {"ann@example.com"}}); rec.Code != http.StatusOK {
		t.Fatalf("forgot: %d", rec.Code)
	}
	// unknown emails get the same answer and no email
	before := len(mailer.Sent())
	if rec := laptop.post("/forgot-password", url.Values{"email": {"nobody@example.com"}}); rec.Code != http.StatusOK || len(mailer.Sent()) != before {
		t.Errorf("forgot for an unknown email: %d, %d emails", rec.Code, len(mailer.Sent())-before)
	}

	path, token := lastLink(t, mailer, "ann@example.com")
	if rec := laptop.get(path + "?token=" + token); rec.Code != http.StatusOK || rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("GET %s: %d", path, rec.Code)
	}
	newPassword := "a brand new and longer password"
	if rec := laptop.post("/reset-password", url.Values{"token": {token}, "password": {newPassword}}); rec.Code != http.StatusOK {
		t.Fatalf("reset: %d %s", rec.Code, rec.Body)
	}
	if rec := laptop.post("/reset-password", url.Values{"token": {token}, "password": {newPassword}}); rec.Code != http.StatusBadRequest {
		t.Errorf("a used reset link worked again: %d", rec.Code)
	}

	if rec := phone.get("/account"); rec.Code != http.StatusSeeOther {
		t.Errorf("the other session survived the reset: %d", rec.Code)
	}
	if rec := laptop.login("ann@example.com", testPassword); rec.Code != http.StatusUnauthorized {
		t.Errorf("the old password still works: %d", rec.Code)
	}
	if rec := laptop.login("ann@example.com", newPassword); rec.Code != http.StatusOK {
		t.Errorf("the new password doesn't work: %d %s", rec.Code, rec.Body)
	}
}

func TestSignOutEverywhere(t *testing.T) {
	handler, mailer := newAuthApp(t, devEnv)
	phone := newVisitor(t, handler)
	signUp(t, phone, mailer, "ann@example.com")
	phone.login("ann@example.com", testPassword)
	laptop := newVisitor(t, handler)
	laptop.login("ann@example.com", testPassword)

	if rec := laptop.post("/logout-everywhere", nil); rec.Header().Get("HX-Redirect") != "/" {
		t.Fatalf("sign out everywhere: %d", rec.Code)
	}
	for name, b := range map[string]*visitor{"phone": phone, "laptop": laptop} {
		if rec := b.get("/account"); rec.Code != http.StatusSeeOther {
			t.Errorf("%s is still signed in: %d", name, rec.Code)
		}
	}
}

func TestSignInRateLimitPerAccount(t *testing.T) {
	handler, mailer := newAuthApp(t, devEnv)
	b := newVisitor(t, handler)
	signUp(t, b, mailer, "ann@example.com")

	for i := 0; i < 5; i++ {
		if rec := b.login("ann@example.com", "wrong password number "+string(rune('a'+i))); rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d", i+1, rec.Code)
		}
	}
	// a different IP doesn't help: the limit is on the account (and is case-insensitive)
	other := newVisitor(t, handler)
	if rec := other.login("ANN@example.com", testPassword); rec.Code != http.StatusTooManyRequests {
		t.Errorf("sixth attempt: %d, want 429", rec.Code)
	}
}

func TestRequireUser(t *testing.T) {
	handler, _ := newAuthApp(t, devEnv)
	b := newVisitor(t, handler)
	rec := b.do("GET", "/account", nil, true)
	if rec.Code != http.StatusUnauthorized || rec.Header().Get("HX-Redirect") != "/login?next=%2Faccount" {
		t.Errorf("htmx request: %d, HX-Redirect %q", rec.Code, rec.Header().Get("HX-Redirect"))
	}
	if rec := b.post("/logout-everywhere", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("POST without a session: %d", rec.Code)
	}
}

func TestSignInRedirectStaysOnSite(t *testing.T) {
	handler, mailer := newAuthApp(t, devEnv)
	b := newVisitor(t, handler)
	signUp(t, b, mailer, "ann@example.com")

	for next, want := range map[string]string{
		"/account?tab=1":        "/account?tab=1",
		"//evil.example/":       "/account",
		"https://evil.example/": "/account",
		`/\evil.example`:        "/account",
		"javascript:alert(1)":   "/account",
	} {
		b.post("/logout", nil)
		rec := b.post("/login", url.Values{"email": {"ann@example.com"}, "password": {testPassword}, "next": {next}})
		if got := rec.Header().Get("HX-Redirect"); got != want {
			t.Errorf("next=%q: redirected to %q, want %q", next, got, want)
		}
	}
}

func TestProductionSessionCookie(t *testing.T) {
	prod := config.Env{GoEnv: "production", Port: "0", BaseURL: "https://example.com"}
	handler, mailer := newAuthApp(t, prod)
	b := newVisitor(t, handler)
	signUp(t, b, mailer, "ann@example.com")
	if !strings.Contains(mailer.Sent()[0].Text, "https://example.com/verify-email?token=") {
		t.Errorf("links don't use BaseURL: %q", mailer.Sent()[0].Text)
	}

	b.login("ann@example.com", testPassword)
	c, ok := b.cookies["__Host-session"]
	if !ok {
		t.Fatalf("no __Host-session cookie in production: %v", b.cookies)
	}
	if !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.Domain != "" {
		t.Errorf("session cookie %+v", c)
	}
}
