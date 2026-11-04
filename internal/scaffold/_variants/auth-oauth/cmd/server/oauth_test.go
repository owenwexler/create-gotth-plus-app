package main

import (
	// cgpa:if apple
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	// cgpa:end
	"context"
	"hash/fnv"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/owenwexler/gotth-plus/internal/app"
	"github.com/owenwexler/gotth-plus/internal/model"
	"github.com/owenwexler/gotth-plus/internal/repository"
)

// oauthProviders are the sign-in providers this project has; the generic tests run on the first.
var oauthProviders = []string{
	// cgpa:if google
	"google",
	// cgpa:end
	// cgpa:if github
	"github",
	// cgpa:end
	// cgpa:if apple
	"apple",
	// cgpa:end
}

const testClientID = "test-client-id"

// oauthApp is the app with every provider pointed at its own fake.
type oauthApp struct {
	handler http.Handler
	db      *gorm.DB
	fakes   map[string]*fakeProvider
}

func newOAuthApp(t *testing.T) *oauthApp {
	t.Helper()
	env := devEnv
	env.BaseURL = "http://app.example"
	a := &oauthApp{fakes: map[string]*fakeProvider{}}
	var opts []app.Option
	for _, name := range oauthProviders {
		f := newFakeProvider(t, testClientID)
		a.fakes[name] = f
		opts = append(opts, app.WithEndpoints(name, f.endpoints()))
	}
	// cgpa:if google
	env.GoogleClientID, env.GoogleClientSecret = testClientID, "test-secret"
	// cgpa:end
	// cgpa:if github
	env.GitHubClientID, env.GitHubClientSecret = testClientID, "test-secret"
	// cgpa:end
	// cgpa:if apple
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(t.TempDir(), "AuthKey.p8")
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	a.fakes["apple"].appleKey = &key.PublicKey
	env.AppleClientID, env.AppleTeamID, env.AppleKeyID, env.ApplePrivateKeyFile = testClientID, "TEAM123", "KEY123", keyFile
	// cgpa:end

	_, a.db = newTestApp(t, env)
	a.handler = app.NewHandler(env, a.db, opts...)
	return a
}

// started is what the app sent the browser to the provider with.
type started struct {
	state, nonce, challenge string
	authURL                 *url.URL
}

// begin follows a provider's sign-in button.
func begin(t *testing.T, v *visitor, provider, next string) started {
	t.Helper()
	path := "/auth/" + provider + "/login"
	if next != "" {
		path += "?next=" + url.QueryEscape(next)
	}
	rec := v.get(path)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("GET %s: %d", path, rec.Code)
	}
	u, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("state") == "" || q.Get("client_id") != testClientID || q.Get("redirect_uri") != "http://app.example/auth/"+provider+"/callback" {
		t.Fatalf("authorization URL %s", u)
	}
	if c := q.Get("code_challenge"); c != "" && q.Get("code_challenge_method") != "S256" {
		t.Errorf("PKCE challenge without S256: %s", u)
	}
	return started{state: q.Get("state"), nonce: q.Get("nonce"), challenge: q.Get("code_challenge"), authURL: u}
}

// grant is what the fake provider knows about the user who approved the sign-in.
func grant(provider string, s started, subject, email string, verified bool) fakeGrant {
	g := fakeGrant{challenge: s.challenge}
	if provider == "github" {
		// GitHub's subject is a number: a stable one per test subject
		h := fnv.New32a()
		_, _ = h.Write([]byte(subject))
		g.githubID = int64(h.Sum32())
		g.githubEmails = []map[string]any{{"email": email, "primary": true, "verified": verified}}
		return g
	}
	g.claims = map[string]any{"sub": subject, "email": email, "email_verified": verified, "nonce": s.nonce}
	if provider == "apple" {
		// Apple sends email_verified as a string
		g.claims["email_verified"] = map[bool]string{true: "true", false: "false"}[verified]
	}
	return g
}

// finish comes back from the provider with code and state: a GET, or Apple's cross-site POST
// (without a CSRF token, like the real one).
func finish(t *testing.T, a *oauthApp, v *visitor, provider, code, state string) *httptest.ResponseRecorder {
	t.Helper()
	path := "/auth/" + provider + "/callback"
	params := url.Values{"code": {code}, "state": {state}}
	if provider != "apple" {
		return v.get(path + "?" + params.Encode())
	}
	req := httptest.NewRequest("POST", path, strings.NewReader(params.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://appleid.apple.com")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	for _, c := range v.cookies {
		req.AddCookie(&http.Cookie{Name: c.Name, Value: c.Value})
	}
	rec := httptest.NewRecorder()
	a.handler.ServeHTTP(rec, req)
	for _, c := range rec.Result().Cookies() {
		if c.MaxAge < 0 {
			delete(v.cookies, c.Name)
		} else {
			v.cookies[c.Name] = c
		}
	}
	return rec
}

// signInWith runs a whole provider sign-in and returns the callback's response.
func signInWith(t *testing.T, a *oauthApp, v *visitor, provider, subject, email string, verified bool) *httptest.ResponseRecorder {
	t.Helper()
	s := begin(t, v, provider, "")
	return finish(t, a, v, provider, a.fakes[provider].code(grant(provider, s, subject, email, verified)), s.state)
}

func countRows(t *testing.T, db *gorm.DB, m any) int64 {
	t.Helper()
	var n int64
	if err := db.Model(m).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

func TestOAuthCreatesUserAndSignsIn(t *testing.T) {
	for _, provider := range oauthProviders {
		t.Run(provider, func(t *testing.T) {
			a := newOAuthApp(t)
			v := newVisitor(t, a.handler)
			csrfBefore := v.cookie("csrf_token")

			rec := signInWith(t, a, v, provider, "subject-1", "Ann@Example.com", true)
			if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/account" {
				t.Fatalf("callback: %d, Location %q, %s", rec.Code, rec.Header().Get("Location"), rec.Body)
			}
			if v.cookie("csrf_token") == csrfBefore {
				t.Error("the CSRF token was not replaced at sign-in")
			}
			if rec := v.get("/account"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "ann@example.com") {
				t.Fatalf("account: %d", rec.Code)
			}

			// signing in again finds the same user, even if the email changed at the provider
			v2 := newVisitor(t, a.handler)
			signInWith(t, a, v2, provider, "subject-1", "ann.new@example.com", true)
			if n := countRows(t, a.db, &model.User{}); n != 1 {
				t.Errorf("%d users after signing in twice, want 1", n)
			}
			if rec := v2.get("/account"); !strings.Contains(rec.Body.String(), "ann@example.com") {
				t.Error("the second sign-in landed in another account")
			}
		})
	}
}

func TestOAuthFlowCookie(t *testing.T) {
	a := newOAuthApp(t)
	provider := oauthProviders[0]
	v := newVisitor(t, a.handler)
	begin(t, v, provider, "")
	c := v.cookies["oauth"]
	if c == nil || !c.HttpOnly || c.MaxAge <= 0 || c.MaxAge > 600 {
		t.Fatalf("flow cookie %+v", c)
	}
	want := http.SameSiteLaxMode
	if provider == "apple" {
		want = http.SameSiteNoneMode
	}
	if c.SameSite != want {
		t.Errorf("flow cookie SameSite %v, want %v", c.SameSite, want)
	}
}

func TestOAuthRejectsBadState(t *testing.T) {
	a := newOAuthApp(t)
	provider := oauthProviders[0]

	// a state that isn't the one this browser started with (login CSRF)
	v := newVisitor(t, a.handler)
	s := begin(t, v, provider, "")
	code := a.fakes[provider].code(grant(provider, s, "subject-1", "ann@example.com", true))
	if rec := finish(t, a, v, provider, code, "forged-state"); rec.Code != http.StatusBadRequest || v.cookie("session") != "" {
		t.Errorf("forged state: %d, session %q", rec.Code, v.cookie("session"))
	}

	// no flow cookie at all: a callback this browser never started
	other := newVisitor(t, a.handler)
	if rec := finish(t, a, other, provider, code, s.state); rec.Code != http.StatusBadRequest {
		t.Errorf("callback without a flow cookie: %d", rec.Code)
	}
	if n := countRows(t, a.db, &model.User{}); n != 0 {
		t.Errorf("%d users after rejected callbacks", n)
	}
}

func TestOAuthCallbackCantBeReplayed(t *testing.T) {
	a := newOAuthApp(t)
	provider := oauthProviders[0]
	v := newVisitor(t, a.handler)
	s := begin(t, v, provider, "")
	code := a.fakes[provider].code(grant(provider, s, "subject-1", "ann@example.com", true))
	flow := v.cookies["oauth"]
	if rec := finish(t, a, v, provider, code, s.state); rec.Code != http.StatusSeeOther {
		t.Fatalf("first callback: %d %s", rec.Code, rec.Body)
	}
	// the flow cookie is gone after the first callback; putting it back doesn't help either,
	// since the provider's code is single-use
	attacker := newVisitor(t, a.handler)
	attacker.cookies["oauth"] = flow
	if rec := finish(t, a, attacker, provider, code, s.state); rec.Code != http.StatusBadRequest {
		t.Errorf("replayed callback: %d", rec.Code)
	}
}

func TestOAuthCancelled(t *testing.T) {
	a := newOAuthApp(t)
	provider := oauthProviders[0]
	v := newVisitor(t, a.handler)
	s := begin(t, v, provider, "")
	var rec *httptest.ResponseRecorder
	if provider == "apple" {
		rec = finish(t, a, v, provider, "", s.state) // no code: treated as failed
	} else {
		rec = v.get("/auth/" + provider + "/callback?error=access_denied&state=" + url.QueryEscape(s.state))
	}
	if v.cookie("session") != "" || rec.Code == http.StatusSeeOther {
		t.Errorf("cancelled sign-in: %d, session %q", rec.Code, v.cookie("session"))
	}
}

func TestOAuthNextStaysOnSite(t *testing.T) {
	a := newOAuthApp(t)
	provider := oauthProviders[0]
	for next, want := range map[string]string{"/account?tab=1": "/account?tab=1", "//evil.example/": "/account", "https://evil.example": "/account"} {
		v := newVisitor(t, a.handler)
		s := begin(t, v, provider, next)
		rec := finish(t, a, v, provider, a.fakes[provider].code(grant(provider, s, "subject-1", "ann@example.com", true)), s.state)
		if got := rec.Header().Get("Location"); got != want {
			t.Errorf("next=%q: redirected to %q, want %q", next, got, want)
		}
	}
}

// addConfirmedUser creates an account directly, as if it had signed up another way.
func addConfirmedUser(t *testing.T, db *gorm.DB, email string, confirmed bool) model.User {
	t.Helper()
	now := time.Now()
	u := model.User{ID: uuid.New(), Email: email, SessionVersion: 1, CreatedAt: now, UpdatedAt: now}
	if confirmed {
		u.EmailVerifiedAt = &now
	}
	if err := repository.NewUserRepository(db).Create(context.Background(), &u); err != nil {
		t.Fatal(err)
	}
	return u
}

func TestOAuthLinksOnlyVerifiedEmails(t *testing.T) {
	provider := oauthProviders[0]

	t.Run("both verified: linked", func(t *testing.T) {
		a := newOAuthApp(t)
		existing := addConfirmedUser(t, a.db, "ann@example.com", true)
		v := newVisitor(t, a.handler)
		if rec := signInWith(t, a, v, provider, "subject-1", "ann@example.com", true); rec.Code != http.StatusSeeOther {
			t.Fatalf("callback: %d %s", rec.Code, rec.Body)
		}
		var id model.Identity
		if err := a.db.First(&id).Error; err != nil || id.UserID != existing.ID {
			t.Errorf("identity %+v, %v; want it linked to %s", id, err, existing.ID)
		}
	})

	t.Run("provider email not verified: refused", func(t *testing.T) {
		a := newOAuthApp(t)
		addConfirmedUser(t, a.db, "ann@example.com", true)
		v := newVisitor(t, a.handler)
		if rec := signInWith(t, a, v, provider, "subject-1", "ann@example.com", false); rec.Code != http.StatusForbidden || v.cookie("session") != "" {
			t.Errorf("callback: %d, session %q", rec.Code, v.cookie("session"))
		}
		if n := countRows(t, a.db, &model.Identity{}); n != 0 {
			t.Errorf("%d identities, want none", n)
		}
	})

	t.Run("account email not confirmed: refused", func(t *testing.T) {
		a := newOAuthApp(t)
		addConfirmedUser(t, a.db, "ann@example.com", false)
		v := newVisitor(t, a.handler)
		if rec := signInWith(t, a, v, provider, "subject-1", "ann@example.com", true); rec.Code != http.StatusConflict || v.cookie("session") != "" {
			t.Errorf("callback: %d, session %q", rec.Code, v.cookie("session"))
		}
	})

	t.Run("new user without a verified email: refused", func(t *testing.T) {
		a := newOAuthApp(t)
		v := newVisitor(t, a.handler)
		if rec := signInWith(t, a, v, provider, "subject-1", "ann@example.com", false); rec.Code != http.StatusForbidden {
			t.Errorf("callback: %d", rec.Code)
		}
		if n := countRows(t, a.db, &model.User{}); n != 0 {
			t.Errorf("%d users, want none", n)
		}
	})
}

func TestOAuthLoginPageShowsProviders(t *testing.T) {
	a := newOAuthApp(t)
	body := newVisitor(t, a.handler).get("/login?next=%2Faccount").Body.String()
	for _, provider := range oauthProviders {
		if !strings.Contains(body, `href="/auth/`+provider+`/login?next=%2Faccount"`) {
			t.Errorf("no %s button keeping ?next= on the sign-in page", provider)
		}
	}
}

// cgpa:if google
func TestGoogleChecksNonceAndPKCE(t *testing.T) {
	a := newOAuthApp(t)
	f := a.fakes["google"]

	v := newVisitor(t, a.handler)
	s := begin(t, v, "google", "")
	if s.nonce == "" || s.challenge == "" {
		t.Fatalf("no nonce or PKCE challenge: %s", s.authURL)
	}
	// an ID token minted for another sign-in (another nonce) is rejected
	g := grant("google", s, "subject-1", "ann@example.com", true)
	g.claims["nonce"] = "another-sign-in"
	if rec := finish(t, a, v, "google", f.code(g), s.state); rec.Code != http.StatusBadRequest {
		t.Errorf("wrong nonce: %d", rec.Code)
	}

	// the app proves PKCE: the fake refuses an exchange whose verifier doesn't match
	v = newVisitor(t, a.handler)
	s = begin(t, v, "google", "")
	g = grant("google", s, "subject-1", "ann@example.com", true)
	g.challenge = "a-challenge-the-app-never-made"
	if rec := finish(t, a, v, "google", f.code(g), s.state); rec.Code != http.StatusBadRequest {
		t.Errorf("mismatched PKCE: %d", rec.Code)
	}

	// an ID token from another issuer is rejected
	v = newVisitor(t, a.handler)
	s = begin(t, v, "google", "")
	g = grant("google", s, "subject-1", "ann@example.com", true)
	g.claims["iss"] = "https://evil.example"
	if rec := finish(t, a, v, "google", f.code(g), s.state); rec.Code != http.StatusBadRequest {
		t.Errorf("wrong issuer: %d", rec.Code)
	}
}

// cgpa:end
// cgpa:if github
func TestGitHubUsesVerifiedPrimaryEmail(t *testing.T) {
	a := newOAuthApp(t)
	f := a.fakes["github"]
	v := newVisitor(t, a.handler)
	s := begin(t, v, "github", "")
	g := fakeGrant{challenge: s.challenge, githubID: 42, githubEmails: []map[string]any{
		{"email": "old@example.com", "primary": false, "verified": true},
		{"email": "Octo@Example.com", "primary": true, "verified": true},
	}}
	if rec := finish(t, a, v, "github", f.code(g), s.state); rec.Code != http.StatusSeeOther {
		t.Fatalf("callback: %d %s", rec.Code, rec.Body)
	}
	var u model.User
	if err := a.db.First(&u).Error; err != nil || u.Email != "octo@example.com" {
		t.Errorf("user %+v, %v", u, err)
	}
	var id model.Identity
	if err := a.db.First(&id).Error; err != nil || id.Provider != "github" || id.Subject != "42" {
		t.Errorf("identity %+v, %v", id, err)
	}

	// an unverified primary email is not enough
	v = newVisitor(t, a.handler)
	s = begin(t, v, "github", "")
	g = fakeGrant{challenge: s.challenge, githubID: 43, githubEmails: []map[string]any{{"email": "new@example.com", "primary": true, "verified": false}}}
	if rec := finish(t, a, v, "github", f.code(g), s.state); rec.Code != http.StatusForbidden {
		t.Errorf("unverified primary email: %d", rec.Code)
	}
}

// cgpa:end
// cgpa:if apple
func TestAppleFormPostCallback(t *testing.T) {
	a := newOAuthApp(t)
	v := newVisitor(t, a.handler)
	s := begin(t, v, "apple", "")
	if s.authURL.Query().Get("response_mode") != "form_post" || s.nonce == "" {
		t.Fatalf("authorization URL %s", s.authURL)
	}
	// the cross-site POST has no CSRF token and fails the cross-origin check by design; this one
	// route is exempt. The fake checks the client secret is an ES256 JWT signed with the app's key.
	rec := finish(t, a, v, "apple", a.fakes["apple"].code(grant("apple", s, "apple-subject", "x1@privaterelay.appleid.com", true)), s.state)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/account" {
		t.Fatalf("callback: %d, %s", rec.Code, rec.Body)
	}
	if v.cookie("session") == "" {
		t.Error("no session after the Apple callback")
	}
}

func TestOnlyTheAppleCallbackSkipsCSRF(t *testing.T) {
	a := newOAuthApp(t)
	for _, path := range []string{"/logout", "/auth/google/callback", "/auth/apple/callback/extra"} {
		req := httptest.NewRequest("POST", path, nil)
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		rec := httptest.NewRecorder()
		a.handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("cross-site POST %s without a CSRF token: %d, want 403", path, rec.Code)
		}
	}
}

// cgpa:end
