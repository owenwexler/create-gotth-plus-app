package factory

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-playground/validator/v10"

	"github.com/owenwexler/gotth-plus/internal/auth"
	"github.com/owenwexler/gotth-plus/internal/dto"
	"github.com/owenwexler/gotth-plus/internal/service"
	"github.com/owenwexler/gotth-plus/internal/view"
)

// keyRecoveryToken holds Supabase's access token from a password reset link, server-side in the
// session, only until the new password is set.
const keyRecoveryToken = "supabase_recovery_token"

// Supabase signs people up and in through Supabase Auth: with an email and a password, or with
// the providers enabled in the Supabase dashboard. Users and passwords live in Supabase.
type Supabase struct {
	*Auth
	Client     *auth.Supabase
	Identities *service.IdentityService
	Validate   *validator.Validate
	// BaseURL builds the links Supabase comes back to (allow them in Supabase's redirect URLs).
	BaseURL string
	// Providers maps a button's name to the provider name Supabase uses ("google", "github", ...).
	Providers map[string]string

	LoginLimit  *auth.KeyLimiter
	SignupLimit *auth.KeyLimiter
	ResetLimit  *auth.KeyLimiter
}

// NewSupabase returns the Supabase handlers with the same per-account limits as built-in sign-in.
func NewSupabase(a *Auth, client *auth.Supabase, identities *service.IdentityService, validate *validator.Validate, baseURL string, providers map[string]string) *Supabase {
	return &Supabase{
		Auth: a, Client: client, Identities: identities, Validate: validate, BaseURL: baseURL, Providers: providers,
		LoginLimit:  auth.NewKeyLimiter(3*time.Minute, 5),
		SignupLimit: auth.NewKeyLimiter(20*time.Minute, 3),
		ResetLimit:  auth.NewKeyLimiter(20*time.Minute, 3),
	}
}

func tooManyAttempts(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Retry-After", "180")
	respondWithToast(w, r, http.StatusTooManyRequests, view.ToastArgs{Kind: view.ToastError, Header: "Too many attempts", Body: "Wait a few minutes and try again."})
}

func passwordLengthToast(w http.ResponseWriter, r *http.Request) {
	respondWithToast(w, r, http.StatusUnprocessableEntity, view.ToastArgs{Kind: view.ToastError, Header: "Choose another password", Body: "Use 15 to 72 characters."})
}

// failedPage shows what went wrong on a page: callbacks are full navigations, not htmx.
func failedPage(w http.ResponseWriter, r *http.Request, status int, title, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := view.SignInFailedPage(title, body).Render(r.Context(), w); err != nil {
		logError(r, "render sign-in failed page", err)
	}
}

// signInSupabaseUser finds or creates the app's user for a Supabase user and starts a session.
func (a *Supabase) signInSupabaseUser(w http.ResponseWriter, r *http.Request, user auth.SupabaseUser, next string) {
	u, err := a.Identities.SignIn(r.Context(), user.Identity())
	switch {
	case errors.Is(err, service.ErrNoVerifiedEmail):
		failedPage(w, r, http.StatusForbidden, "Confirm your email first", "Open the link Supabase emailed you, then sign in.")
		return
	case errors.Is(err, service.ErrEmailTaken):
		failedPage(w, r, http.StatusConflict, "You already have an account", "There's an account with this email that can't be linked automatically. Sign in the way you did before.")
		return
	case err != nil:
		a.internalError(w, r, "sign in supabase user", err)
		return
	}
	a.signIn(w, r, u, next)
}

// Login checks the email and password with Supabase and starts a session.
func (a *Supabase) Login() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			logRejected(r, "parse login form", err)
			respondWithToast(w, r, http.StatusBadRequest, view.ErrorToast("Sign-in failed"))
			return
		}
		req := dto.LoginRequest{Email: normalizeEmail(r.FormValue("email")), Password: r.FormValue("password"), Next: r.FormValue("next")}
		if err := a.Validate.Struct(&req); err != nil {
			logRejected(r, "login validation failed", err)
			respondWithToast(w, r, http.StatusUnprocessableEntity, view.ToastArgs{Kind: view.ToastError, Header: "Sign-in failed", Body: "Enter your email address and password."})
			return
		}
		if !a.LoginLimit.Allow(req.Email) {
			tooManyAttempts(w, r)
			return
		}
		user, err := a.Client.SignInWithPassword(r.Context(), req.Email, req.Password)
		switch {
		case errors.Is(err, auth.ErrSupabaseCredentials):
			respondWithToast(w, r, http.StatusUnauthorized, view.ToastArgs{Kind: view.ToastError, Header: "Sign-in failed", Body: "The email or password is incorrect."})
			return
		case errors.Is(err, auth.ErrSupabaseNotConfirmed):
			respondWithToast(w, r, http.StatusForbidden, view.ToastArgs{Kind: view.ToastError, Header: "Confirm your email first", Body: "Open the link we emailed you when you signed up."})
			return
		case err != nil:
			a.internalError(w, r, "supabase sign-in", err)
			return
		}
		u, err := a.Identities.SignIn(r.Context(), user.Identity())
		if err != nil {
			a.internalError(w, r, "sign in supabase user", err)
			return
		}
		a.signIn(w, r, u, req.Next)
	}
}

// SignupPage shows the sign-up form.
func (a *Supabase) SignupPage() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		renderPage(w, r, view.SignupPage())
	}
}

// Signup asks Supabase to create the account and email a confirmation link. Opened in this browser,
// the link signs the user in too (the PKCE verifier is in this browser's flow cookie).
func (a *Supabase) Signup() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			logRejected(r, "parse signup form", err)
			respondWithToast(w, r, http.StatusBadRequest, view.ErrorToast("Sign-up failed"))
			return
		}
		req := dto.SignupRequest{Email: normalizeEmail(r.FormValue("email")), Password: r.FormValue("password")}
		if err := a.Validate.Struct(&req); err != nil {
			logRejected(r, "signup validation failed", err)
			respondWithToast(w, r, http.StatusUnprocessableEntity, view.ToastArgs{Kind: view.ToastError, Header: "Sign-up failed", Body: "Enter a valid email address and a password."})
			return
		}
		if err := auth.CheckPasswordLength(req.Password); err != nil {
			passwordLengthToast(w, r)
			return
		}
		if !a.SignupLimit.Allow(req.Email) {
			tooManyAttempts(w, r)
			return
		}
		challenge, err := auth.StartFlow(w, a.Secure, "", auth.SignUpFlowMaxAge)
		if err != nil {
			a.internalError(w, r, "start signup flow", err)
			return
		}
		if err := a.Client.SignUp(r.Context(), req.Email, req.Password, challenge, a.BaseURL+"/auth/supabase/callback"); err != nil {
			a.internalError(w, r, "supabase signup", err)
			return
		}
		renderPage(w, r, view.AuthNotice("Check your email",
			"We've sent a link to "+req.Email+". Open it to confirm your address. It can take a minute to arrive.",
			"/login", "Sign in"))
	}
}

// ForgotPage shows the form that asks for a reset link.
func (a *Supabase) ForgotPage() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		renderPage(w, r, view.ForgotPasswordPage())
	}
}

// Forgot asks Supabase to email a reset link; the answer is the same whether or not the email has
// an account. The link only works in this browser, which keeps the PKCE verifier.
func (a *Supabase) Forgot() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			logRejected(r, "parse forgot-password form", err)
			respondWithToast(w, r, http.StatusBadRequest, view.ErrorToast("Couldn't send the link"))
			return
		}
		req := dto.EmailRequest{Email: normalizeEmail(r.FormValue("email"))}
		if err := a.Validate.Struct(&req); err != nil {
			logRejected(r, "forgot-password validation failed", err)
			respondWithToast(w, r, http.StatusUnprocessableEntity, view.ToastArgs{Kind: view.ToastError, Header: "Couldn't send the link", Body: "Enter a valid email address."})
			return
		}
		if !a.ResetLimit.Allow(req.Email) {
			tooManyAttempts(w, r)
			return
		}
		challenge, err := auth.StartFlow(w, a.Secure, "", auth.RecoveryFlowMaxAge)
		if err != nil {
			a.internalError(w, r, "start recovery flow", err)
			return
		}
		if err := a.Client.Recover(r.Context(), req.Email, challenge, a.BaseURL+"/reset-password"); err != nil {
			a.internalError(w, r, "supabase recover", err)
			return
		}
		renderPage(w, r, view.AuthNotice("Check your email",
			"If there's an account for "+req.Email+", we've sent it a link to choose a new password. Open it in this browser, within an hour.",
			"", ""))
	}
}

// ResetPage is where Supabase's reset link comes back. The code is exchanged with this browser's
// verifier, and Supabase's access token is kept server-side in the session until the new password
// is set.
func (a *Supabase) ResetPage() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Referrer-Policy", "no-referrer") // the code is in the URL
		code := r.URL.Query().Get("code")
		flow, ok := auth.EndFlow(w, r, a.Secure)
		if code == "" || !ok {
			failedPage(w, r, http.StatusBadRequest, "This link doesn't work here", "Open the reset link in the browser where you asked for it, or ask for a new one.")
			return
		}
		user, token, err := a.Client.Exchange(r.Context(), code, flow.Verifier)
		if err != nil {
			logRejected(r, "supabase recovery exchange", err)
			failedPage(w, r, http.StatusBadRequest, "This link has expired", "Ask for a new one from the sign-in page.")
			return
		}
		a.Sessions.Put(r.Context(), keyRecoveryToken, token)
		a.Sessions.Put(r.Context(), keyRecoveryToken+"_user", user.ID)
		renderPage(w, r, view.ResetPasswordPage(""))
	}
}

// Reset sets the new password at Supabase and signs the user out everywhere in the app.
func (a *Supabase) Reset() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			logRejected(r, "parse reset form", err)
			respondWithToast(w, r, http.StatusBadRequest, view.ErrorToast("Couldn't change the password"))
			return
		}
		password := r.FormValue("password")
		if err := auth.CheckPasswordLength(password); err != nil {
			passwordLengthToast(w, r)
			return
		}
		token := a.Sessions.GetString(r.Context(), keyRecoveryToken)
		supabaseUserID := a.Sessions.GetString(r.Context(), keyRecoveryToken+"_user")
		if token == "" {
			respondWithToast(w, r, http.StatusBadRequest, view.ToastArgs{Kind: view.ToastError, Header: "This link has expired", Body: "Ask for a new one from the sign-in page."})
			return
		}
		if err := a.Client.UpdatePassword(r.Context(), token, password); err != nil {
			a.internalError(w, r, "supabase update password", err)
			return
		}
		a.Sessions.Remove(r.Context(), keyRecoveryToken)
		a.Sessions.Remove(r.Context(), keyRecoveryToken+"_user")
		if u, err := a.Identities.UserFor(r.Context(), "supabase", supabaseUserID); err == nil {
			if err := a.Users.SignOutEverywhere(r.Context(), u.ID); err != nil {
				a.internalError(w, r, "sign out everywhere", err)
				return
			}
		}
		renderPage(w, r, view.AuthNotice("Password changed", "You've been signed out everywhere. Sign in with your new password.", "/login", "Sign in"))
	}
}

// Begin sends the browser to Supabase to sign in with a provider.
func (a *Supabase) Begin() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		provider, ok := a.Providers[r.PathValue("provider")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		challenge, err := auth.StartFlow(w, a.Secure, r.URL.Query().Get("next"), auth.ProviderFlowMaxAge)
		if err != nil {
			a.internalError(w, r, "start provider flow", err)
			return
		}
		http.Redirect(w, r, a.Client.AuthorizeURL(provider, challenge, a.BaseURL+"/auth/supabase/callback"), http.StatusSeeOther)
	}
}

// Callback is where Supabase comes back after a provider sign-in or a sign-up confirmation link.
func (a *Supabase) Callback() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Referrer-Policy", "no-referrer") // the code is in the URL
		q := r.URL.Query()
		flow, ok := auth.EndFlow(w, r, a.Secure)
		if q.Get("error") != "" {
			logRejected(r, "supabase callback", errors.New(q.Get("error")+": "+q.Get("error_description")))
			failedPage(w, r, http.StatusOK, "Sign-in didn't finish", "Please try again.")
			return
		}
		code := q.Get("code")
		if code == "" || !ok {
			// a confirmation link opened in another browser: Supabase has confirmed the email by now,
			// but without this browser's verifier the code can't sign anyone in
			failedPage(w, r, http.StatusOK, "Sign in to continue", "If you opened a confirmation link, your email is confirmed. Sign in with your email and password.")
			return
		}
		user, _, err := a.Client.Exchange(r.Context(), code, flow.Verifier)
		if err != nil {
			logRejected(r, "supabase code exchange", err)
			failedPage(w, r, http.StatusBadRequest, "Sign-in failed", "The link or sign-in has expired. Please try again.")
			return
		}
		a.signInSupabaseUser(w, r, user, flow.Next)
	}
}
