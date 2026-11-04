package factory

import (
	"errors"
	"net/http"

	"github.com/owenwexler/gotth-plus/internal/auth"
	"github.com/owenwexler/gotth-plus/internal/service"
	"github.com/owenwexler/gotth-plus/internal/view"
)

// OAuth adds "Continue with ..." sign-in (Google, GitHub, Apple) to Auth.
type OAuth struct {
	*Auth
	Providers  *auth.Providers
	Identities *service.IdentityService
}

// NewOAuth returns the provider sign-in handlers.
func NewOAuth(a *Auth, providers *auth.Providers, identities *service.IdentityService) *OAuth {
	return &OAuth{Auth: a, Providers: providers, Identities: identities}
}

// signInFailed shows what went wrong as a page: the callback is a full navigation, not htmx.
func signInFailed(w http.ResponseWriter, r *http.Request, status int, title, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := view.SignInFailedPage(title, body).Render(r.Context(), w); err != nil {
		logError(r, "render sign-in failed page", err)
	}
}

// Begin sends the browser to the provider. It is a GET link (not a form), so the redirect to the
// provider isn't subject to the CSP's form-action 'self'.
func (a *OAuth) Begin() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := a.Providers.Get(r.PathValue("provider"))
		if p == nil {
			http.NotFound(w, r)
			return
		}
		if err := p.Begin(w, r, a.Secure, r.URL.Query().Get("next")); err != nil {
			logError(r, "begin provider sign-in", err)
			signInFailed(w, r, http.StatusInternalServerError, "Something went wrong", "Please try again.")
		}
	}
}

// Callback is where the provider sends the browser back.
func (a *OAuth) Callback() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := a.Providers.Get(r.PathValue("provider"))
		if p == nil || (r.Method == http.MethodPost && !p.FormPost) {
			http.NotFound(w, r)
			return
		}

		id, next, err := p.Finish(w, r, a.Secure)
		switch {
		case errors.Is(err, auth.ErrSignInCancelled):
			signInFailed(w, r, http.StatusOK, "Sign-in cancelled", "You didn't finish signing in with "+p.Label+".")
			return
		case err != nil:
			logRejected(r, "provider sign-in", err)
			signInFailed(w, r, http.StatusBadRequest, "Sign-in failed", "Signing in with "+p.Label+" didn't work. Please try again.")
			return
		}

		user, err := a.Identities.SignIn(r.Context(), id)
		switch {
		case errors.Is(err, service.ErrNoVerifiedEmail):
			signInFailed(w, r, http.StatusForbidden, "We need a verified email", p.Label+" didn't give us a verified email address. Verify one there, then try again.")
			return
		case errors.Is(err, service.ErrEmailTaken):
			signInFailed(w, r, http.StatusConflict, "You already have an account",
				"There's an account with this email that we can't link to "+p.Label+" automatically. Sign in the way you did before.")
			return
		case err != nil:
			logError(r, "provider sign-in", err)
			signInFailed(w, r, http.StatusInternalServerError, "Something went wrong", "Please try again.")
			return
		}
		a.signIn(w, r, user, next)
	}
}
