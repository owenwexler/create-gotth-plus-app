package main

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/owenwexler/gotth-plus/internal/auth"
)

// fakeProvider is an OAuth provider for tests: the token, key and API endpoints of Google, GitHub
// or Apple, without a network. Codes are issued by the test (standing in for the user approving
// at the provider) and can be exchanged once.
type fakeProvider struct {
	t        *testing.T
	srv      *httptest.Server
	key      *rsa.PrivateKey
	clientID string
	// appleKey, when set, is the public key the client secret (an ES256 JWT) must be signed with.
	appleKey *ecdsa.PublicKey

	mu     sync.Mutex
	codes  map[string]fakeGrant
	tokens map[string]fakeGrant
	// exchanges records the token requests, for checking what the app sent.
	exchanges []map[string]string
}

// fakeGrant is what the provider knows about a user who approved the sign-in.
type fakeGrant struct {
	// claims go into the ID token (OpenID Connect providers); nil means no ID token (GitHub).
	claims map[string]any
	// challenge is the PKCE code challenge from the authorization request, if any.
	challenge string
	// GitHub's API answers.
	githubID     int64
	githubEmails []map[string]any
}

func newFakeProvider(t *testing.T, clientID string) *fakeProvider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeProvider{t: t, key: key, clientID: clientID, codes: map[string]fakeGrant{}, tokens: map[string]fakeGrant{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", f.token)
	mux.HandleFunc("GET /jwks", f.jwks)
	mux.HandleFunc("GET /user", f.githubUser)
	mux.HandleFunc("GET /user/emails", f.githubUserEmails)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// endpoints are the fake's URLs, for app.WithEndpoints.
func (f *fakeProvider) endpoints() auth.Endpoints {
	return auth.Endpoints{
		AuthURL:  f.srv.URL + "/authorize",
		TokenURL: f.srv.URL + "/token",
		Issuer:   f.srv.URL,
		JWKSURL:  f.srv.URL + "/jwks",
		APIURL:   f.srv.URL,
	}
}

// code returns an authorization code for g.
func (f *fakeProvider) code(g fakeGrant) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := randomToken()
	f.codes[c] = g
	return c
}

func randomToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func (f *fakeProvider) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	got := map[string]string{}
	for k := range r.PostForm {
		got[k] = r.PostForm.Get(k)
	}
	f.mu.Lock()
	f.exchanges = append(f.exchanges, got)
	g, ok := f.codes[got["code"]]
	delete(f.codes, got["code"]) // single use
	f.mu.Unlock()

	fail := func(msg string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant", "error_description": msg})
	}
	switch {
	case !ok:
		fail("unknown or used code")
		return
	case got["client_id"] != f.clientID:
		fail("wrong client_id")
		return
	case g.challenge != "" && s256(got["code_verifier"]) != g.challenge:
		fail("PKCE verifier does not match the challenge")
		return
	case f.appleKey != nil && !f.validAppleSecret(got["client_secret"]):
		fail("bad Apple client secret")
		return
	}

	access := randomToken()
	f.mu.Lock()
	f.tokens[access] = g
	f.mu.Unlock()
	resp := map[string]any{"access_token": access, "token_type": "Bearer", "expires_in": 3600}
	if g.claims != nil {
		resp["id_token"] = f.idToken(g.claims)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func s256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// idToken signs claims (with iss, aud, iat and exp filled in unless set) as an RS256 JWT.
func (f *fakeProvider) idToken(claims map[string]any) string {
	full := map[string]any{"iss": f.srv.URL, "aud": f.clientID, "iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix()}
	for k, v := range claims {
		full[k] = v
	}
	enc := base64.RawURLEncoding
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "test", "typ": "JWT"})
	body, _ := json.Marshal(full)
	input := enc.EncodeToString(header) + "." + enc.EncodeToString(body)
	digest := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, digest[:])
	if err != nil {
		f.t.Fatal(err)
	}
	return input + "." + enc.EncodeToString(sig)
}

func (f *fakeProvider) jwks(w http.ResponseWriter, _ *http.Request) {
	enc := base64.RawURLEncoding
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
		"kty": "RSA", "kid": "test", "use": "sig", "alg": "RS256",
		"n": enc.EncodeToString(f.key.N.Bytes()),
		"e": enc.EncodeToString(big.NewInt(int64(f.key.E)).Bytes()),
	}}})
}

func (f *fakeProvider) grantFor(r *http.Request) (fakeGrant, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	g, ok := f.tokens[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
	return g, ok
}

func (f *fakeProvider) githubUser(w http.ResponseWriter, r *http.Request) {
	g, ok := f.grantFor(r)
	if !ok {
		http.Error(w, "bad token", http.StatusUnauthorized)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"id": g.githubID, "login": "octocat"})
}

func (f *fakeProvider) githubUserEmails(w http.ResponseWriter, r *http.Request) {
	g, ok := f.grantFor(r)
	if !ok {
		http.Error(w, "bad token", http.StatusUnauthorized)
		return
	}
	_ = json.NewEncoder(w).Encode(g.githubEmails)
}

// validAppleSecret checks the client secret the way Apple does: an ES256 JWT signed with the
// registered key, for this client, that hasn't expired.
func (f *fakeProvider) validAppleSecret(secret string) bool {
	parts := strings.Split(secret, ".")
	if len(parts) != 3 {
		return false
	}
	enc := base64.RawURLEncoding
	sig, err := enc.DecodeString(parts[2])
	if err != nil || len(sig) != 64 {
		return false
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !ecdsa.Verify(f.appleKey, digest[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		return false
	}
	var header struct{ Alg, Kid string }
	var claims struct {
		Iss, Sub, Aud string
		Exp           int64
	}
	h, _ := enc.DecodeString(parts[0])
	c, _ := enc.DecodeString(parts[1])
	if json.Unmarshal(h, &header) != nil || json.Unmarshal(c, &claims) != nil {
		return false
	}
	return header.Alg == "ES256" && header.Kid == "KEY123" && claims.Iss == "TEAM123" &&
		claims.Sub == f.clientID && claims.Aud == f.srv.URL && claims.Exp > time.Now().Unix()
}
