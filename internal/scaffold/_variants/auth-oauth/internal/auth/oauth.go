package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// Identity is who a sign-in provider says the user is.
type Identity struct {
	Provider string
	// Subject is the provider's stable ID for the user; accounts are matched on (Provider, Subject).
	Subject string
	// Email is lowercased. EmailVerified is the provider's word that the user controls it.
	Email         string
	EmailVerified bool
}

// Endpoints are a provider's URLs. Tests point them at a fake provider.
type Endpoints struct {
	AuthURL  string
	TokenURL string
	// Issuer and JWKSURL verify ID tokens (OpenID Connect providers).
	Issuer  string
	JWKSURL string
	// APIURL is the provider's API, for providers without ID tokens (GitHub).
	APIURL string
}

// Provider is one "Continue with ..." sign-in option.
type Provider struct {
	Name  string // in URLs: /auth/{Name}/login
	Label string // on the button
	// FormPost providers come back with a cross-site POST (Apple), so the flow cookie has to be
	// SameSite=None, and the callback is exempt from the CSRF check (state and nonce protect it).
	FormPost bool
	// PKCE adds a code verifier, so a stolen authorization code is useless on its own.
	PKCE bool

	config *oauth2.Config
	// clientSecret, when set, makes a fresh client secret for each code exchange (Apple).
	clientSecret func() (string, error)
	// verifier checks ID tokens (OpenID Connect providers); issuers are the accepted "iss" values.
	verifier *oidc.IDTokenVerifier
	issuers  []string
	identify func(ctx context.Context, p *Provider, token *oauth2.Token, nonce string) (Identity, error)
	apiURL   string
	client   *http.Client
}

// Providers are the configured sign-in providers, in the order the sign-in page shows them.
type Providers struct {
	list   []*Provider
	byName map[string]*Provider
}

// Add appends p.
func (ps *Providers) Add(p *Provider) {
	if ps.byName == nil {
		ps.byName = map[string]*Provider{}
	}
	ps.list = append(ps.list, p)
	ps.byName[p.Name] = p
}

// Get returns the provider called name, or nil.
func (ps *Providers) Get(name string) *Provider { return ps.byName[name] }

// All returns the providers in order.
func (ps *Providers) All() []*Provider { return ps.list }

var (
	// ErrSignInCancelled means the user said no at the provider.
	ErrSignInCancelled = errors.New("sign-in cancelled at the provider")
	// ErrSignInFailed covers everything else: a missing or wrong state, a bad code or ID token.
	ErrSignInFailed = errors.New("sign-in with the provider failed")
)

// flowMaxAge bounds how long someone can take at the provider.
const flowMaxAge = 10 * time.Minute

// flow is what the browser keeps between leaving for the provider and coming back. Its values are
// random and checked against what the provider sends back; nothing in it grants access by itself.
type flow struct {
	Provider string `json:"p"`
	State    string `json:"s"`
	Nonce    string `json:"n"`
	Verifier string `json:"v,omitempty"`
	Next     string `json:"x,omitempty"`
}

// flowCookieName is __Host- prefixed in production, so a subdomain can't plant one (login CSRF).
func flowCookieName(secure bool) string {
	if secure {
		return "__Host-oauth"
	}
	return "oauth"
}

func (p *Provider) flowCookie(secure bool, value string, maxAge int) *http.Cookie {
	c := &http.Cookie{
		Name:     flowCookieName(secure),
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	}
	if p.FormPost {
		// sent with the provider's cross-site POST; browsers require Secure with SameSite=None
		// (they accept it on http://localhost too)
		c.SameSite = http.SameSiteNoneMode
		c.Secure = true
	}
	return c
}

func randomString() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Begin sends the browser to the provider. next is where to go after signing in (checked again with
// SafeNext when it is used).
func (p *Provider) Begin(w http.ResponseWriter, r *http.Request, secure bool, next string) error {
	f := flow{Provider: p.Name, Next: SafeNext(next, "")}
	var err error
	if f.State, err = randomString(); err != nil {
		return err
	}
	if f.Nonce, err = randomString(); err != nil {
		return err
	}

	var opts []oauth2.AuthCodeOption
	if p.verifier != nil {
		// the ID token must carry this nonce, so a token from another sign-in can't be replayed
		opts = append(opts, oauth2.SetAuthURLParam("nonce", f.Nonce))
	}
	if p.PKCE {
		f.Verifier = oauth2.GenerateVerifier()
		opts = append(opts, oauth2.S256ChallengeOption(f.Verifier))
	}
	if p.FormPost {
		opts = append(opts, oauth2.SetAuthURLParam("response_mode", "form_post"))
	}

	b, err := json.Marshal(f)
	if err != nil {
		return err
	}
	http.SetCookie(w, p.flowCookie(secure, base64.RawURLEncoding.EncodeToString(b), int(flowMaxAge/time.Second)))
	http.Redirect(w, r, p.config.AuthCodeURL(f.State, opts...), http.StatusSeeOther)
	return nil
}

// Finish checks the provider's answer and returns the identity and where to go next. The flow
// cookie is cleared either way, so a callback can't be replayed.
func (p *Provider) Finish(w http.ResponseWriter, r *http.Request, secure bool) (Identity, string, error) {
	c, err := r.Cookie(flowCookieName(secure))
	http.SetCookie(w, p.flowCookie(secure, "", -1))
	if err != nil {
		return Identity{}, "", fmt.Errorf("%w: no flow cookie", ErrSignInFailed)
	}
	var f flow
	raw, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil || json.Unmarshal(raw, &f) != nil || f.Provider != p.Name || f.State == "" {
		return Identity{}, "", fmt.Errorf("%w: bad flow cookie", ErrSignInFailed)
	}

	params := r.URL.Query()
	if p.FormPost && r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			return Identity{}, "", fmt.Errorf("%w: %v", ErrSignInFailed, err)
		}
		params = r.PostForm
	}
	if subtle.ConstantTimeCompare([]byte(params.Get("state")), []byte(f.State)) != 1 {
		return Identity{}, "", fmt.Errorf("%w: state mismatch", ErrSignInFailed)
	}
	if e := params.Get("error"); e != "" {
		if e == "access_denied" || e == "user_cancelled_authorize" {
			return Identity{}, "", ErrSignInCancelled
		}
		return Identity{}, "", fmt.Errorf("%w: provider error %q", ErrSignInFailed, e)
	}
	code := params.Get("code")
	if code == "" {
		return Identity{}, "", fmt.Errorf("%w: no code", ErrSignInFailed)
	}

	ctx := context.WithValue(r.Context(), oauth2.HTTPClient, p.client)
	config := *p.config
	if p.clientSecret != nil {
		if config.ClientSecret, err = p.clientSecret(); err != nil {
			return Identity{}, "", err
		}
	}
	var opts []oauth2.AuthCodeOption
	if f.Verifier != "" {
		opts = append(opts, oauth2.VerifierOption(f.Verifier))
	}
	token, err := config.Exchange(ctx, code, opts...)
	if err != nil {
		return Identity{}, "", fmt.Errorf("%w: exchange code: %v", ErrSignInFailed, err)
	}
	id, err := p.identify(oidc.ClientContext(ctx, p.client), p, token, f.Nonce)
	if err != nil {
		return Identity{}, "", fmt.Errorf("%w: %v", ErrSignInFailed, err)
	}
	return id, f.Next, nil
}

// identifyOIDC reads the identity from a verified ID token whose nonce is the flow's.
func identifyOIDC(ctx context.Context, p *Provider, token *oauth2.Token, nonce string) (Identity, error) {
	raw, _ := token.Extra("id_token").(string)
	if raw == "" {
		return Identity{}, errors.New("no id_token")
	}
	idToken, err := p.verifier.Verify(ctx, raw)
	if err != nil {
		return Identity{}, err
	}
	issuerOK := false
	for _, iss := range p.issuers {
		issuerOK = issuerOK || idToken.Issuer == iss
	}
	if !issuerOK {
		return Identity{}, fmt.Errorf("unexpected issuer %q", idToken.Issuer)
	}
	if subtle.ConstantTimeCompare([]byte(idToken.Nonce), []byte(nonce)) != 1 {
		return Identity{}, errors.New("nonce mismatch")
	}
	var claims struct {
		Email string `json:"email"`
		// a boolean, except at Apple, where it can be the string "true"
		EmailVerified any `json:"email_verified"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return Identity{}, err
	}
	verified := claims.EmailVerified == true || claims.EmailVerified == "true"
	return Identity{Provider: p.Name, Subject: idToken.Subject, Email: normalize(claims.Email), EmailVerified: verified && claims.Email != ""}, nil
}

// newOIDCProvider is the common part of the OpenID Connect providers (Google, Apple). Discovery is
// not used, so the app starts without reaching the provider; the signing keys are fetched on first use.
func newOIDCProvider(name, label, clientID, clientSecret, redirectURL string, scopes []string, e Endpoints, issuers []string) *Provider {
	client := &http.Client{Timeout: 15 * time.Second}
	keys := oidc.NewRemoteKeySet(oidc.ClientContext(context.Background(), client), e.JWKSURL)
	return &Provider{
		Name:  name,
		Label: label,
		PKCE:  true,
		config: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURL,
			Scopes:       scopes,
			Endpoint:     oauth2.Endpoint{AuthURL: e.AuthURL, TokenURL: e.TokenURL, AuthStyle: oauth2.AuthStyleInParams},
		},
		// the issuer is checked against issuers below, since Google uses two spellings
		verifier: oidc.NewVerifier(e.Issuer, keys, &oidc.Config{ClientID: clientID, SkipIssuerCheck: true}),
		issuers:  issuers,
		identify: identifyOIDC,
		client:   client,
	}
}
