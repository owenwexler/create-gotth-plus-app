package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/owenwexler/gotth-plus/internal/app"
	"github.com/owenwexler/gotth-plus/internal/model"
)

const supabaseTestKey = "sb_publishable_test"

// fakeSupabase is Supabase Auth's REST API for tests: users with passwords, emailed links (which
// the test "opens"), PKCE codes and access tokens.
type fakeSupabase struct {
	t   *testing.T
	srv *httptest.Server

	mu     sync.Mutex
	users  map[string]*fakeSupabaseUser // by email
	codes  map[string]fakeSupabaseCode
	tokens map[string]string // access token -> email
	// mails are the links Supabase "sent", by email: the redirect_to and the PKCE challenge.
	mails map[string]fakeSupabaseMail
}

type fakeSupabaseUser struct {
	ID, Email, Password string
	Confirmed           bool
}

type fakeSupabaseCode struct{ email, challenge string }

type fakeSupabaseMail struct{ redirectTo, challenge string }

func newFakeSupabase(t *testing.T) *fakeSupabase {
	t.Helper()
	f := &fakeSupabase{t: t, users: map[string]*fakeSupabaseUser{}, codes: map[string]fakeSupabaseCode{}, tokens: map[string]string{}, mails: map[string]fakeSupabaseMail{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/v1/signup", f.signup)
	mux.HandleFunc("POST /auth/v1/recover", f.recover)
	mux.HandleFunc("POST /auth/v1/token", f.token)
	mux.HandleFunc("GET /auth/v1/user", f.getUser)
	mux.HandleFunc("PUT /auth/v1/user", f.putUser)
	f.srv = httptest.NewServer(f.requireKey(mux))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeSupabase) requireKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("apikey") != supabaseTestKey {
			http.Error(w, `{"msg":"no apikey"}`, http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func fakeID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func (f *fakeSupabase) body(r *http.Request) map[string]string {
	var m map[string]string
	_ = json.NewDecoder(r.Body).Decode(&m)
	return m
}

func supabaseFail(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error_code": code, "msg": msg})
}

func (f *fakeSupabase) userJSON(u *fakeSupabaseUser) map[string]any {
	m := map[string]any{"id": u.ID, "email": u.Email}
	if u.Confirmed {
		m["email_confirmed_at"] = time.Now().UTC().Format(time.RFC3339)
	}
	return m
}

func (f *fakeSupabase) signup(w http.ResponseWriter, r *http.Request) {
	b := f.body(r)
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, exists := f.users[b["email"]]; !exists {
		f.users[b["email"]] = &fakeSupabaseUser{ID: fakeID(), Email: b["email"], Password: b["password"]}
		f.mails[b["email"]] = fakeSupabaseMail{redirectTo: r.URL.Query().Get("redirect_to"), challenge: b["code_challenge"]}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"id": fakeID()}) // the same answer either way, like Supabase
}

func (f *fakeSupabase) recover(w http.ResponseWriter, r *http.Request) {
	b := f.body(r)
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, exists := f.users[b["email"]]; exists {
		f.mails[b["email"]] = fakeSupabaseMail{redirectTo: r.URL.Query().Get("redirect_to"), challenge: b["code_challenge"]}
	}
	_, _ = w.Write([]byte("{}"))
}

// openMail follows the last link emailed to the address: Supabase confirms the email (for a
// sign-up link) and returns the URL it redirects the browser to, with a code.
func (f *fakeSupabase) openMail(email string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.mails[email]
	if !ok {
		f.t.Fatalf("no email to %s", email)
	}
	f.users[email].Confirmed = true
	code := fakeID()
	f.codes[code] = fakeSupabaseCode{email: email, challenge: m.challenge}
	return m.redirectTo + "?code=" + code
}

// providerSignIn is a provider sign-in at Supabase: it returns the redirect back to the app.
func (f *fakeSupabase) providerSignIn(authorizeURL, email string) string {
	u, err := url.Parse(authorizeURL)
	if err != nil {
		f.t.Fatal(err)
	}
	q := u.Query()
	if q.Get("code_challenge_method") != "s256" || q.Get("code_challenge") == "" || q.Get("provider") == "" {
		f.t.Fatalf("authorize URL %s", u)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.users[email]; !ok {
		f.users[email] = &fakeSupabaseUser{ID: fakeID(), Email: email, Confirmed: true}
	}
	code := fakeID()
	f.codes[code] = fakeSupabaseCode{email: email, challenge: q.Get("code_challenge")}
	return q.Get("redirect_to") + "?code=" + code
}

func (f *fakeSupabase) token(w http.ResponseWriter, r *http.Request) {
	b := f.body(r)
	f.mu.Lock()
	defer f.mu.Unlock()
	var u *fakeSupabaseUser
	switch r.URL.Query().Get("grant_type") {
	case "password":
		u = f.users[b["email"]]
		if u == nil || u.Password != b["password"] {
			supabaseFail(w, http.StatusBadRequest, "invalid_credentials", "Invalid login credentials")
			return
		}
		if !u.Confirmed {
			supabaseFail(w, http.StatusBadRequest, "email_not_confirmed", "Email not confirmed")
			return
		}
	case "pkce":
		c, ok := f.codes[b["auth_code"]]
		delete(f.codes, b["auth_code"]) // single use
		sum := sha256.Sum256([]byte(b["code_verifier"]))
		if !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != c.challenge {
			supabaseFail(w, http.StatusBadRequest, "bad_code_verifier", "invalid code or verifier")
			return
		}
		u = f.users[c.email]
	default:
		supabaseFail(w, http.StatusBadRequest, "unsupported_grant_type", "unsupported")
		return
	}
	access := fakeID()
	f.tokens[access] = u.Email
	_ = json.NewEncoder(w).Encode(map[string]any{"access_token": access, "refresh_token": fakeID(), "user": f.userJSON(u)})
}

func (f *fakeSupabase) tokenUser(r *http.Request) *fakeSupabaseUser {
	email, ok := f.tokens[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
	if !ok {
		return nil
	}
	return f.users[email]
}

func (f *fakeSupabase) getUser(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u := f.tokenUser(r)
	if u == nil {
		supabaseFail(w, http.StatusUnauthorized, "bad_jwt", "invalid token")
		return
	}
	_ = json.NewEncoder(w).Encode(f.userJSON(u))
}

func (f *fakeSupabase) putUser(w http.ResponseWriter, r *http.Request) {
	b := f.body(r)
	f.mu.Lock()
	defer f.mu.Unlock()
	u := f.tokenUser(r)
	if u == nil {
		supabaseFail(w, http.StatusUnauthorized, "bad_jwt", "invalid token")
		return
	}
	u.Password = b["password"]
	_ = json.NewEncoder(w).Encode(f.userJSON(u))
}

func newSupabaseApp(t *testing.T) (http.Handler, *fakeSupabase, *gorm.DB) {
	t.Helper()
	f := newFakeSupabase(t)
	env := devEnv
	env.BaseURL = "http://app.example"
	env.SupabaseURL, env.SupabasePublishableKey, env.SupabaseProviders = f.srv.URL, supabaseTestKey, "google, github"
	_, db := newTestApp(t, env)
	return app.NewHandler(env, db), f, db
}

func countUsers(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var n int64
	if err := db.Model(&model.User{}).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

// path turns a URL on the app's BaseURL into a request path.
func path(t *testing.T, full string) string {
	t.Helper()
	u, err := url.Parse(full)
	if err != nil || u.Host != "app.example" {
		t.Fatalf("redirect to %q, want one back to the app", full)
	}
	return u.RequestURI()
}

const supabasePassword = "correct horse battery staple"

func (v *visitor) supabaseLogin(email, password string) *httptest.ResponseRecorder {
	return v.post("/login", url.Values{"email": {email}, "password": {password}})
}

func TestSupabaseSignUpConfirmInThisBrowser(t *testing.T) {
	handler, f, _ := newSupabaseApp(t)
	v := newVisitor(t, handler)
	if rec := v.post("/signup", url.Values{"email": {"Ann@Example.com"}, "password": {supabasePassword}}); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Check your email") {
		t.Fatalf("signup: %d %s", rec.Code, rec.Body)
	}
	// the confirmation link, opened in the same browser, signs the user in
	rec := v.get(path(t, f.openMail("ann@example.com")))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/account" {
		t.Fatalf("confirmation callback: %d %s", rec.Code, rec.Body)
	}
	if rec := v.get("/account"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "ann@example.com") {
		t.Errorf("account: %d", rec.Code)
	}
}

func TestSupabaseConfirmInAnotherBrowserThenSignIn(t *testing.T) {
	handler, f, db := newSupabaseApp(t)
	v := newVisitor(t, handler)
	v.post("/signup", url.Values{"email": {"ann@example.com"}, "password": {supabasePassword}})

	if rec := v.supabaseLogin("ann@example.com", supabasePassword); rec.Code != http.StatusForbidden {
		t.Errorf("sign-in before confirming: %d", rec.Code)
	}
	phone := newVisitor(t, handler)
	if rec := phone.get(path(t, f.openMail("ann@example.com"))); rec.Code != http.StatusOK || phone.cookie("session") != "" {
		t.Fatalf("link in another browser: %d, session %q", rec.Code, phone.cookie("session"))
	}
	if rec := v.supabaseLogin("ann@example.com", supabasePassword); rec.Code != http.StatusOK || rec.Header().Get("HX-Redirect") != "/account" {
		t.Fatalf("sign-in: %d %s", rec.Code, rec.Body)
	}
	if rec := v.supabaseLogin("ann@example.com", "the wrong password, at length"); rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong password: %d", rec.Code)
	}
	if n := countUsers(t, db); n != 1 {
		t.Errorf("%d users, want 1", n)
	}
}

func TestSupabaseProviderSignIn(t *testing.T) {
	handler, f, _ := newSupabaseApp(t)
	v := newVisitor(t, handler)
	if body := v.get("/login").Body.String(); !strings.Contains(body, `href="/auth/google/login"`) || !strings.Contains(body, `href="/auth/github/login"`) {
		t.Fatal("no provider buttons on the sign-in page")
	}
	rec := v.get("/auth/google/login?next=%2Faccount%3Ftab%3D1")
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), f.srv.URL+"/auth/v1/authorize?") {
		t.Fatalf("begin: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	back := f.providerSignIn(rec.Header().Get("Location"), "octo@example.com")
	if rec := v.get(path(t, back)); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/account?tab=1" {
		t.Fatalf("callback: %d %q %s", rec.Code, rec.Header().Get("Location"), rec.Body)
	}

	// a code from someone else's sign-in is useless without their PKCE verifier (login CSRF)
	attacker := newVisitor(t, handler)
	attacker.get("/auth/google/login")
	theirs := f.providerSignIn(newVisitor(t, handler).get("/auth/google/login").Header().Get("Location"), "mallory@example.com")
	if rec := attacker.get(path(t, theirs)); rec.Code != http.StatusBadRequest || attacker.cookie("session") != "" {
		t.Errorf("someone else's code: %d, session %q", rec.Code, attacker.cookie("session"))
	}
	if rec := v.get("/auth/not-enabled/login"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown provider: %d", rec.Code)
	}
}

func TestSupabasePasswordReset(t *testing.T) {
	handler, f, _ := newSupabaseApp(t)
	phone := newVisitor(t, handler)
	phone.post("/signup", url.Values{"email": {"ann@example.com"}, "password": {supabasePassword}})
	phone.get(path(t, f.openMail("ann@example.com"))) // confirmed and signed in on the phone

	laptop := newVisitor(t, handler)
	if rec := laptop.post("/forgot-password", url.Values{"email": {"ann@example.com"}}); rec.Code != http.StatusOK {
		t.Fatalf("forgot: %d", rec.Code)
	}
	rec := laptop.get(path(t, f.openMail("ann@example.com")))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `name="password"`) || rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("reset page: %d %s", rec.Code, rec.Body)
	}
	newPassword := "a brand new and longer password"
	if rec := laptop.post("/reset-password", url.Values{"password": {newPassword}}); rec.Code != http.StatusOK {
		t.Fatalf("reset: %d %s", rec.Code, rec.Body)
	}
	// the token was used up: it isn't kept around for another change
	if rec := laptop.post("/reset-password", url.Values{"password": {"yet another long password"}}); rec.Code != http.StatusBadRequest {
		t.Errorf("second reset with the same link: %d", rec.Code)
	}
	if rec := phone.get("/account"); rec.Code != http.StatusSeeOther {
		t.Errorf("the phone is still signed in after the reset: %d", rec.Code)
	}
	if rec := laptop.supabaseLogin("ann@example.com", newPassword); rec.Code != http.StatusOK {
		t.Errorf("new password: %d %s", rec.Code, rec.Body)
	}

	// a reset link opened in a browser that didn't ask for it doesn't work
	laptop.post("/forgot-password", url.Values{"email": {"ann@example.com"}})
	other := newVisitor(t, handler)
	if rec := other.get(path(t, f.openMail("ann@example.com"))); rec.Code != http.StatusBadRequest {
		t.Errorf("reset link in another browser: %d", rec.Code)
	}
}

func TestSupabaseShortPassword(t *testing.T) {
	handler, _, _ := newSupabaseApp(t)
	if rec := newVisitor(t, handler).post("/signup", url.Values{"email": {"ann@example.com"}, "password": {"fourteen chars"}}); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("short password: %d", rec.Code)
	}
}
