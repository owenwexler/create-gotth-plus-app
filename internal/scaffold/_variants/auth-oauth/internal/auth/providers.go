package auth

import (
	// cgpa:if apple
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	// cgpa:end
	// cgpa:if github apple
	"encoding/json"
	"errors"
	"fmt"
	// cgpa:end
	// cgpa:if github apple oidc
	"time"
	// cgpa:end
	// cgpa:if github oidc
	"context"
	"net/http"
	// cgpa:end
	// cgpa:if github
	"io"
	"strconv"
	// cgpa:end
	// cgpa:if google github
	"strings"
	// cgpa:end

	// cgpa:if oidc
	"github.com/coreos/go-oidc/v3/oidc"
	// cgpa:end
	// cgpa:if github oidc
	"golang.org/x/oauth2"
	// cgpa:end
)

// cgpa:if google
// GoogleEndpoints are Google's OpenID Connect URLs.
var GoogleEndpoints = Endpoints{
	AuthURL:  "https://accounts.google.com/o/oauth2/v2/auth",
	TokenURL: "https://oauth2.googleapis.com/token",
	Issuer:   "https://accounts.google.com",
	JWKSURL:  "https://www.googleapis.com/oauth2/v3/certs",
}

// Google signs in with a Google account (OpenID Connect). Create the client at
// https://console.cloud.google.com/apis/credentials with redirectURL as an authorized redirect URI.
func Google(clientID, clientSecret, redirectURL string, e Endpoints) *Provider {
	// Google's ID tokens use either spelling of its issuer
	issuers := []string{e.Issuer, strings.TrimPrefix(e.Issuer, "https://")}
	return newOIDCProvider("google", "Google", clientID, clientSecret, redirectURL, []string{"openid", "email"}, e, issuers)
}

// cgpa:end
// cgpa:if github
// GitHubEndpoints are GitHub's OAuth and API URLs.
var GitHubEndpoints = Endpoints{
	AuthURL:  "https://github.com/login/oauth/authorize",
	TokenURL: "https://github.com/login/oauth/access_token",
	APIURL:   "https://api.github.com",
}

// GitHub signs in with a GitHub account. GitHub has no ID tokens (it is OAuth, not OpenID Connect),
// so the identity comes from its API: the numeric user ID, and the primary email if it is verified.
// Create the OAuth app at https://github.com/settings/developers with redirectURL as its callback URL.
func GitHub(clientID, clientSecret, redirectURL string, e Endpoints) *Provider {
	return &Provider{
		Name:  "github",
		Label: "GitHub",
		PKCE:  true,
		config: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURL,
			Scopes:       []string{"read:user", "user:email"},
			Endpoint:     oauth2.Endpoint{AuthURL: e.AuthURL, TokenURL: e.TokenURL, AuthStyle: oauth2.AuthStyleInParams},
		},
		identify: identifyGitHub,
		apiURL:   strings.TrimSuffix(e.APIURL, "/"),
		client:   &http.Client{Timeout: 15 * time.Second},
	}
}

func identifyGitHub(ctx context.Context, p *Provider, token *oauth2.Token, _ string) (Identity, error) {
	var user struct {
		ID int64 `json:"id"`
	}
	if err := p.githubGet(ctx, token, "/user", &user); err != nil {
		return Identity{}, err
	}
	if user.ID == 0 {
		return Identity{}, errors.New("github: no user id")
	}
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := p.githubGet(ctx, token, "/user/emails", &emails); err != nil {
		return Identity{}, err
	}
	id := Identity{Provider: p.Name, Subject: strconv.FormatInt(user.ID, 10)}
	for _, e := range emails {
		if e.Primary && e.Verified {
			id.Email, id.EmailVerified = normalize(e.Email), true
		}
	}
	return id, nil
}

func (p *Provider) githubGet(ctx context.Context, token *oauth2.Token, path string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.apiURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("github %s: %s", path, resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(v)
}

// cgpa:end
// cgpa:if apple
// AppleEndpoints are Sign in with Apple's URLs.
var AppleEndpoints = Endpoints{
	AuthURL:  "https://appleid.apple.com/auth/authorize",
	TokenURL: "https://appleid.apple.com/auth/token",
	Issuer:   "https://appleid.apple.com",
	JWKSURL:  "https://appleid.apple.com/auth/keys",
}

// AppleConfig is a Sign in with Apple client: a Services ID, and a key from the Apple developer
// account (Certificates, Identifiers & Profiles > Keys) with its .p8 file.
type AppleConfig struct {
	ClientID   string // the Services ID, e.g. com.example.web
	TeamID     string
	KeyID      string
	PrivateKey []byte // contents of the .p8 file (PEM, PKCS #8)
}

// Apple signs in with an Apple Account. Apple comes back with a cross-site POST (it asks for the
// email that way), and its client secret is a token signed with your key, made fresh for each sign-in.
// Apple only accepts https redirect URLs, so testing it locally needs an https tunnel.
func Apple(cfg AppleConfig, redirectURL string, e Endpoints) (*Provider, error) {
	key, err := parseAppleKey(cfg.PrivateKey)
	if err != nil {
		return nil, err
	}
	p := newOIDCProvider("apple", "Apple", cfg.ClientID, "", redirectURL, []string{"email"}, e, []string{e.Issuer})
	p.FormPost = true
	// Apple doesn't document PKCE for web sign-in; state and the ID token's nonce protect the flow
	p.PKCE = false
	p.clientSecret = func() (string, error) { return appleClientSecret(cfg, key, e.Issuer, time.Now()) }
	return p, nil
}

func parseAppleKey(pemBytes []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("apple: the private key is not PEM (use the .p8 file)")
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("apple: private key: %w", err)
	}
	key, ok := k.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("apple: the private key is not an EC key")
	}
	return key, nil
}

// appleClientSecret is the ES256-signed JWT Apple takes as the client secret. It is valid for five
// minutes, only for this exchange (Apple allows up to six months).
func appleClientSecret(cfg AppleConfig, key *ecdsa.PrivateKey, audience string, now time.Time) (string, error) {
	enc := base64.RawURLEncoding
	header, _ := json.Marshal(map[string]string{"alg": "ES256", "kid": cfg.KeyID, "typ": "JWT"})
	claims, _ := json.Marshal(map[string]any{
		"iss": cfg.TeamID,
		"iat": now.Unix(),
		"exp": now.Add(5 * time.Minute).Unix(),
		"aud": audience,
		"sub": cfg.ClientID,
	})
	signingInput := enc.EncodeToString(header) + "." + enc.EncodeToString(claims)
	digest := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		return "", err
	}
	// JWS ES256 signatures are r and s as two 32-byte big-endian numbers
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return signingInput + "." + enc.EncodeToString(sig), nil
}

// cgpa:end
// cgpa:if oidc

// OIDC signs in with any OpenID Connect provider (WorkOS, Logto, Kinde, Auth0, Zitadel, Keycloak,
// ...). Everything but the issuer comes from its discovery document, read at the first sign-in;
// the ID token's issuer must then match issuer exactly.
func OIDC(label, issuer, clientID, clientSecret, redirectURL string) *Provider {
	client := &http.Client{Timeout: 15 * time.Second}
	p := &Provider{
		Name:  "oidc",
		Label: label,
		PKCE:  true,
		config: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURL,
			Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
		},
		issuers:  []string{issuer},
		identify: identifyOIDC,
		client:   client,
	}
	p.discover = func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(oidc.ClientContext(ctx, client), 15*time.Second)
		defer cancel()
		provider, err := oidc.NewProvider(ctx, issuer) // checks that the document's issuer is issuer
		if err != nil {
			return err
		}
		var meta struct {
			EndSession string `json:"end_session_endpoint"`
		}
		if err := provider.Claims(&meta); err != nil {
			return err
		}
		p.config.Endpoint = provider.Endpoint()
		// the keys are fetched on first use, with this client, independent of this request
		p.verifier = provider.VerifierContext(oidc.ClientContext(context.Background(), client), &oidc.Config{ClientID: clientID})
		p.endSession = meta.EndSession
		return nil
	}
	return p
}

// cgpa:end
