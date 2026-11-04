package factory

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/alexedwards/scs/v2"
	"github.com/go-playground/validator/v10"

	"github.com/owenwexler/gotth-plus/internal/auth"
	"github.com/owenwexler/gotth-plus/internal/csrf"
	"github.com/owenwexler/gotth-plus/internal/dto"
	"github.com/owenwexler/gotth-plus/internal/repository"
	"github.com/owenwexler/gotth-plus/internal/service"
	"github.com/owenwexler/gotth-plus/internal/view"
)

// Auth holds what the sign-in handlers need.
type Auth struct {
	Sessions *scs.SessionManager
	Service  *service.AuthService
	Users    repository.UserRepository
	Validate *validator.Validate
	// Secure is true in production (HTTPS): cookies get the Secure attribute.
	Secure bool

	// Per-account limits, on top of the per-IP ones in the middleware.
	LoginLimit  *auth.KeyLimiter
	SignupLimit *auth.KeyLimiter
	ResetLimit  *auth.KeyLimiter
}

// NewAuth returns the sign-in handlers with the default per-account limits: 5 sign-in attempts per
// email, then one every 3 minutes; 3 sign-ups or reset emails per address, then one every 20 minutes.
func NewAuth(sessions *scs.SessionManager, svc *service.AuthService, users repository.UserRepository, validate *validator.Validate, secure bool) *Auth {
	return &Auth{
		Sessions: sessions, Service: svc, Users: users, Validate: validate, Secure: secure,
		LoginLimit:  auth.NewKeyLimiter(3*time.Minute, 5),
		SignupLimit: auth.NewKeyLimiter(20*time.Minute, 3),
		ResetLimit:  auth.NewKeyLimiter(20*time.Minute, 3),
	}
}

func normalizeEmail(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// renderPage writes a full page. Auth pages are never cached, since they depend on who is signed in.
func renderPage(w http.ResponseWriter, r *http.Request, page templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := page.Render(r.Context(), w); err != nil {
		logError(r, "render page", err)
	}
}

// renderTokenPage is renderPage for pages opened from an emailed link: the token is in the URL, so
// no Referer may carry it to another site.
func renderTokenPage(w http.ResponseWriter, r *http.Request, page templ.Component) {
	w.Header().Set("Referrer-Policy", "no-referrer")
	renderPage(w, r, page)
}

// redirect navigates the whole page: HX-Redirect for htmx requests, a 303 otherwise.
func redirect(w http.ResponseWriter, r *http.Request, to string) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", to)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// rotateCSRF replaces the CSRF token when someone signs in or out, so one planted or seen before
// stops working. The next full page load renders the new token.
func (a *Auth) rotateCSRF(w http.ResponseWriter, r *http.Request) bool {
	if err := csrf.Rotate(w, a.Secure); err != nil {
		respondWithToast(w, r, http.StatusInternalServerError, view.ErrorToast("Something went wrong"))
		logError(r, "rotate csrf token", err)
		return false
	}
	return true
}

func tooManyAttempts(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Retry-After", "180")
	respondWithToast(w, r, http.StatusTooManyRequests, view.ToastArgs{Kind: view.ToastError, Header: "Too many attempts", Body: "Wait a few minutes and try again."})
}

func (a *Auth) internalError(w http.ResponseWriter, r *http.Request, msg string, err error) {
	logError(r, msg, err)
	respondWithToast(w, r, http.StatusInternalServerError, view.ErrorToast("Something went wrong"))
}

// LoginPage shows the sign-in form, or sends a signed-in user to their account.
func (a *Auth) LoginPage() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := auth.CurrentUser(r.Context()); ok {
			http.Redirect(w, r, "/account", http.StatusSeeOther)
			return
		}
		renderPage(w, r, view.LoginPage(auth.SafeNext(r.URL.Query().Get("next"), "")))
	}
}

// Login checks the email and password and starts a session.
func (a *Auth) Login() http.HandlerFunc {
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

		user, err := a.Service.Login(r.Context(), req.Email, req.Password)
		switch {
		case errors.Is(err, service.ErrInvalidCredentials):
			respondWithToast(w, r, http.StatusUnauthorized, view.ToastArgs{Kind: view.ToastError, Header: "Sign-in failed", Body: "The email or password is incorrect."})
			return
		case errors.Is(err, service.ErrEmailNotVerified):
			respondWithToast(w, r, http.StatusForbidden, view.ToastArgs{Kind: view.ToastError, Header: "Confirm your email first", Body: "We've sent you a new link."})
			return
		case err != nil:
			a.internalError(w, r, "login", err)
			return
		}

		if err := auth.Login(r.Context(), a.Sessions, user); err != nil {
			a.internalError(w, r, "start session", err)
			return
		}
		if !a.rotateCSRF(w, r) {
			return
		}
		redirect(w, r, auth.SafeNext(req.Next, "/account"))
	}
}

// Logout ends this browser's session.
func (a *Auth) Logout() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := auth.Logout(r.Context(), a.Sessions); err != nil {
			a.internalError(w, r, "end session", err)
			return
		}
		if !a.rotateCSRF(w, r) {
			return
		}
		redirect(w, r, "/")
	}
}

// LogoutEverywhere ends every session of the signed-in user, on every device.
func (a *Auth) LogoutEverywhere() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := auth.CurrentUser(r.Context()) // behind RequireUser
		if err := a.Users.SignOutEverywhere(r.Context(), user.ID); err != nil {
			a.internalError(w, r, "sign out everywhere", err)
			return
		}
		a.Logout()(w, r)
	}
}

// SignupPage shows the sign-up form.
func (a *Auth) SignupPage() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		renderPage(w, r, view.SignupPage())
	}
}

// Signup creates an account and emails a confirmation link. The answer is the same whether or not
// the email already has an account.
func (a *Auth) Signup() http.HandlerFunc {
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
			respondWithToast(w, r, http.StatusUnprocessableEntity, view.ToastArgs{Kind: view.ToastError, Header: "Choose a longer password", Body: "Use at least 15 characters."})
			return
		}
		if !a.SignupLimit.Allow(req.Email) {
			tooManyAttempts(w, r)
			return
		}
		if err := a.Service.SignUp(r.Context(), req.Email, req.Password); err != nil {
			a.internalError(w, r, "signup", err)
			return
		}
		renderPage(w, r, view.AuthNotice("Check your email",
			"We've sent a link to "+req.Email+". Open it to confirm your address, then sign in. It can take a minute to arrive.",
			"/login", "Sign in"))
	}
}

// ForgotPage shows the form that asks for a reset link.
func (a *Auth) ForgotPage() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		renderPage(w, r, view.ForgotPasswordPage())
	}
}

// Forgot emails a reset link if the email has an account; the answer is the same either way.
func (a *Auth) Forgot() http.HandlerFunc {
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
		if err := a.Service.RequestPasswordReset(r.Context(), req.Email); err != nil {
			a.internalError(w, r, "request password reset", err)
			return
		}
		renderPage(w, r, view.AuthNotice("Check your email",
			"If there's an account for "+req.Email+", we've sent it a link to choose a new password. The link works for 30 minutes.",
			"", ""))
	}
}

// tokenFromQuery reads the token of an emailed link, or "" when it can't be one of ours.
func tokenFromQuery(r *http.Request) string {
	token := r.URL.Query().Get("token")
	if token == "" || len(token) > 128 {
		return ""
	}
	return token
}

// ResetPage shows the new-password form for an emailed link.
func (a *Auth) ResetPage() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := tokenFromQuery(r)
		if token == "" {
			renderTokenPage(w, r, view.InvalidLinkPage())
			return
		}
		renderTokenPage(w, r, view.ResetPasswordPage(token))
	}
}

// Reset sets the new password and signs the user out everywhere.
func (a *Auth) Reset() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			logRejected(r, "parse reset form", err)
			respondWithToast(w, r, http.StatusBadRequest, view.ErrorToast("Couldn't change the password"))
			return
		}
		req := dto.ResetPasswordRequest{Token: r.FormValue("token"), Password: r.FormValue("password")}
		if err := a.Validate.Struct(&req); err != nil {
			logRejected(r, "reset validation failed", err)
			respondWithToast(w, r, http.StatusUnprocessableEntity, view.ToastArgs{Kind: view.ToastError, Header: "Couldn't change the password", Body: "Enter a new password."})
			return
		}
		_, err := a.Service.ResetPassword(r.Context(), req.Token, req.Password)
		switch {
		case errors.Is(err, auth.ErrPasswordLength):
			respondWithToast(w, r, http.StatusUnprocessableEntity, view.ToastArgs{Kind: view.ToastError, Header: "Choose a longer password", Body: "Use at least 15 characters."})
			return
		case errors.Is(err, service.ErrTokenInvalid):
			respondWithToast(w, r, http.StatusBadRequest, view.ToastArgs{Kind: view.ToastError, Header: "This link has expired", Body: "Ask for a new one from the sign-in page."})
			return
		case err != nil:
			a.internalError(w, r, "reset password", err)
			return
		}
		renderPage(w, r, view.AuthNotice("Password changed", "You've been signed out everywhere. Sign in with your new password.", "/login", "Sign in"))
	}
}

// VerifyPage shows the confirm button for an emailed link.
func (a *Auth) VerifyPage() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := tokenFromQuery(r)
		if token == "" {
			renderTokenPage(w, r, view.InvalidLinkPage())
			return
		}
		renderTokenPage(w, r, view.VerifyEmailPage(token))
	}
}

// Verify confirms the email.
func (a *Auth) Verify() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			logRejected(r, "parse verify form", err)
			respondWithToast(w, r, http.StatusBadRequest, view.ErrorToast("Couldn't confirm the email"))
			return
		}
		req := dto.TokenRequest{Token: r.FormValue("token")}
		if err := a.Validate.Struct(&req); err != nil {
			logRejected(r, "verify validation failed", err)
			respondWithToast(w, r, http.StatusUnprocessableEntity, view.ErrorToast("Couldn't confirm the email"))
			return
		}
		err := a.Service.VerifyEmail(r.Context(), req.Token)
		switch {
		case errors.Is(err, service.ErrTokenInvalid):
			respondWithToast(w, r, http.StatusBadRequest, view.ToastArgs{Kind: view.ToastError, Header: "This link has expired", Body: "Sign in to get a new one."})
			return
		case err != nil:
			a.internalError(w, r, "verify email", err)
			return
		}
		renderPage(w, r, view.AuthNotice("Email confirmed", "Your account is ready.", "/login", "Sign in"))
	}
}

// Account shows the signed-in user's account. It must be behind auth.RequireUser.
func (a *Auth) Account() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := auth.CurrentUser(r.Context())
		renderPage(w, r, view.AccountPage(user))
	}
}
