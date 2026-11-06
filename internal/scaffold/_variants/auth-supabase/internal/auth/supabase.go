package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

// Password length limits, following NIST SP 800-63B-4 for passwords that are the only factor. Set
// Supabase's own minimum (Authentication > Providers > Email) to at most this.
const (
	MinPasswordLength = 15
	MaxPasswordLength = 72 // Supabase hashes with bcrypt, which only uses the first 72 bytes
)

// ErrPasswordLength means the password is too short or too long.
var ErrPasswordLength = fmt.Errorf("password must be %d to %d characters", MinPasswordLength, MaxPasswordLength)

// CheckPasswordLength enforces the length limits (characters for the minimum, bytes for bcrypt's maximum).
func CheckPasswordLength(password string) error {
	if utf8.RuneCountInString(password) < MinPasswordLength || len(password) > MaxPasswordLength {
		return ErrPasswordLength
	}
	return nil
}

var (
	// ErrSupabaseCredentials is the only answer to a failed password sign-in.
	ErrSupabaseCredentials = errors.New("invalid email or password")
	// ErrSupabaseNotConfirmed means the password was right but the email isn't confirmed yet.
	ErrSupabaseNotConfirmed = errors.New("email not confirmed")
)

// Supabase talks to Supabase Auth's REST API (https://<project>.supabase.co/auth/v1) with the
// project's publishable (anon) key. Users and passwords live in Supabase; the app keeps its own
// user row (linked by Supabase's user ID) and its own server-side session.
type Supabase struct {
	url    string // the project URL, without /auth/v1
	key    string
	client *http.Client
}

// NewSupabase returns a client for the project at projectURL.
func NewSupabase(projectURL, publishableKey string) *Supabase {
	return &Supabase{url: strings.TrimSuffix(projectURL, "/"), key: publishableKey, client: &http.Client{Timeout: 15 * time.Second}}
}

// SupabaseUser is the part of a Supabase user the app uses.
type SupabaseUser struct {
	ID               string     `json:"id"`
	Email            string     `json:"email"`
	EmailConfirmedAt *time.Time `json:"email_confirmed_at"`
}

// Identity is the user as a sign-in identity: matched on Supabase's user ID, with the email
// verified once Supabase has confirmed it.
func (u SupabaseUser) Identity() Identity {
	return Identity{Provider: "supabase", Subject: u.ID, Email: normalize(u.Email), EmailVerified: u.EmailConfirmedAt != nil && u.Email != ""}
}

// supabaseSession is Supabase's answer to a sign-in or a code exchange. Only the access token is
// used, briefly; the refresh token is never kept, since the app has its own session.
type supabaseSession struct {
	AccessToken string       `json:"access_token"`
	User        SupabaseUser `json:"user"`
}

// supabaseError is the error body Supabase Auth answers with.
type supabaseError struct {
	Status  int
	Code    string `json:"error_code"`
	Err     string `json:"error"`
	Message string `json:"msg"`
	Desc    string `json:"error_description"`
}

func (e *supabaseError) Error() string {
	return fmt.Sprintf("supabase: %d %s %s %s %s", e.Status, e.Code, e.Err, e.Message, e.Desc)
}

// call sends a request to /auth/v1/<path>. accessToken, when set, acts as the user.
func (s *Supabase) call(ctx context.Context, method, path string, query url.Values, body any, accessToken string, out any) error {
	u := s.url + "/auth/v1/" + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, reader)
	if err != nil {
		return err
	}
	req.Header.Set("apikey", s.key)
	req.Header.Set("Content-Type", "application/json")
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("supabase: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		e := &supabaseError{Status: resp.StatusCode}
		_ = json.Unmarshal(data, e)
		return e
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

// newVerifier returns a PKCE code verifier and its S256 challenge.
func newVerifier() (verifier, challenge string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:]), nil
}

// SignUp asks Supabase to create the user and email a confirmation link that comes back to
// redirectTo with a code for challenge. Supabase answers the same for an existing email.
func (s *Supabase) SignUp(ctx context.Context, email, password, challenge, redirectTo string) error {
	return s.call(ctx, http.MethodPost, "signup", url.Values{"redirect_to": {redirectTo}},
		map[string]string{"email": email, "password": password, "code_challenge": challenge, "code_challenge_method": "s256"}, "", nil)
}

// SignInWithPassword checks the email and password and returns the user.
func (s *Supabase) SignInWithPassword(ctx context.Context, email, password string) (SupabaseUser, error) {
	var session supabaseSession
	err := s.call(ctx, http.MethodPost, "token", url.Values{"grant_type": {"password"}},
		map[string]string{"email": email, "password": password}, "", &session)
	var e *supabaseError
	if errors.As(err, &e) && e.Status == http.StatusBadRequest {
		if e.Code == "email_not_confirmed" {
			return SupabaseUser{}, ErrSupabaseNotConfirmed
		}
		return SupabaseUser{}, ErrSupabaseCredentials
	}
	if err != nil {
		return SupabaseUser{}, err
	}
	return s.checkedUser(ctx, session)
}

// Recover asks Supabase to email a password reset link that comes back to redirectTo with a code
// for challenge. It succeeds whether or not the email has an account.
func (s *Supabase) Recover(ctx context.Context, email, challenge, redirectTo string) error {
	return s.call(ctx, http.MethodPost, "recover", url.Values{"redirect_to": {redirectTo}},
		map[string]string{"email": email, "code_challenge": challenge, "code_challenge_method": "s256"}, "", nil)
}

// AuthorizeURL is where a "Continue with ..." button sends the browser: Supabase signs the user in
// with provider (as enabled in the Supabase dashboard) and comes back to redirectTo with a code.
func (s *Supabase) AuthorizeURL(provider, challenge, redirectTo string) string {
	q := url.Values{"provider": {provider}, "redirect_to": {redirectTo}, "code_challenge": {challenge}, "code_challenge_method": {"s256"}}
	return s.url + "/auth/v1/authorize?" + q.Encode()
}

// Exchange trades a code from a redirect, with the verifier this browser kept, for the user and an
// access token. Without the right verifier the code is worthless, which stops login CSRF.
func (s *Supabase) Exchange(ctx context.Context, code, verifier string) (SupabaseUser, string, error) {
	var session supabaseSession
	if err := s.call(ctx, http.MethodPost, "token", url.Values{"grant_type": {"pkce"}},
		map[string]string{"auth_code": code, "code_verifier": verifier}, "", &session); err != nil {
		return SupabaseUser{}, "", err
	}
	user, err := s.checkedUser(ctx, session)
	return user, session.AccessToken, err
}

// checkedUser asks Supabase who the session's access token belongs to, rather than trusting the
// token's contents: that works with any signing setup and catches revoked tokens.
func (s *Supabase) checkedUser(ctx context.Context, session supabaseSession) (SupabaseUser, error) {
	if session.AccessToken == "" {
		return SupabaseUser{}, errors.New("supabase: no access token")
	}
	var user SupabaseUser
	if err := s.call(ctx, http.MethodGet, "user", nil, nil, session.AccessToken, &user); err != nil {
		return SupabaseUser{}, err
	}
	if user.ID == "" || user.ID != session.User.ID {
		return SupabaseUser{}, errors.New("supabase: the token is for another user")
	}
	return user, nil
}

// UpdatePassword sets the user's password, acting as the user with accessToken.
func (s *Supabase) UpdatePassword(ctx context.Context, accessToken, password string) error {
	return s.call(ctx, http.MethodPut, "user", nil, map[string]string{"password": password}, accessToken, nil)
}

// SupabaseFlow is what the browser keeps between leaving for Supabase (or for an emailed link) and
// coming back: the PKCE verifier, and where to go next. It is a short-lived cookie.
type SupabaseFlow struct {
	Verifier string `json:"v"`
	Next     string `json:"x,omitempty"`
}

// How long a flow cookie lasts: Supabase's confirmation links work for 24 hours by default, reset
// links for an hour, and a provider sign-in takes minutes.
const (
	SignUpFlowMaxAge   = 24 * time.Hour
	RecoveryFlowMaxAge = time.Hour
	ProviderFlowMaxAge = 10 * time.Minute
)

// supabaseFlowCookie is __Host- prefixed in production, so a subdomain can't plant one.
func supabaseFlowCookie(secure bool) string {
	if secure {
		return "__Host-supabase-flow"
	}
	return "supabase-flow"
}

// StartFlow makes a PKCE verifier, keeps it (and next) in the flow cookie, and returns its challenge.
func StartFlow(w http.ResponseWriter, secure bool, next string, maxAge time.Duration) (string, error) {
	verifier, challenge, err := newVerifier()
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(SupabaseFlow{Verifier: verifier, Next: SafeNext(next, "")})
	if err != nil {
		return "", err
	}
	http.SetCookie(w, &http.Cookie{
		Name: supabaseFlowCookie(secure), Value: base64.RawURLEncoding.EncodeToString(b),
		Path: "/", MaxAge: int(maxAge / time.Second), HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
	})
	return challenge, nil
}

// EndFlow reads and clears the flow cookie; ok is false when this browser started no flow.
func EndFlow(w http.ResponseWriter, r *http.Request, secure bool) (SupabaseFlow, bool) {
	name := supabaseFlowCookie(secure)
	c, err := r.Cookie(name)
	http.SetCookie(w, &http.Cookie{Name: name, Path: "/", MaxAge: -1, HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode})
	if err != nil {
		return SupabaseFlow{}, false
	}
	var f SupabaseFlow
	b, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil || json.Unmarshal(b, &f) != nil || f.Verifier == "" {
		return SupabaseFlow{}, false
	}
	return f, true
}
