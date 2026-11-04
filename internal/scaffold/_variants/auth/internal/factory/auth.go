package factory

import (
	"net/http"
	"strings"

	"github.com/a-h/templ"
	"github.com/alexedwards/scs/v2"

	"github.com/owenwexler/gotth-plus/internal/auth"
	"github.com/owenwexler/gotth-plus/internal/csrf"
	"github.com/owenwexler/gotth-plus/internal/model"
	"github.com/owenwexler/gotth-plus/internal/repository"
	"github.com/owenwexler/gotth-plus/internal/view"
)

// Auth holds the sign-in handlers every sign-in method shares: the sign-in page, signing out, and
// the account page.
type Auth struct {
	Sessions *scs.SessionManager
	Users    repository.UserRepository
	// Secure is true in production (HTTPS): cookies get the Secure attribute.
	Secure bool
	// Login says what the sign-in page offers (the password form, provider buttons).
	Login view.LoginOptions
	// AfterLogout, when set, says where to send the browser after signing out, e.g. to the sign-in
	// provider's logout page so its session ends too. "" means the home page.
	AfterLogout func(r *http.Request) string
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

// redirect navigates the whole page: HX-Redirect for htmx requests, a 303 otherwise.
func redirect(w http.ResponseWriter, r *http.Request, to string) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", to)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

func (a *Auth) internalError(w http.ResponseWriter, r *http.Request, msg string, err error) {
	logError(r, msg, err)
	respondWithToast(w, r, http.StatusInternalServerError, view.ErrorToast("Something went wrong"))
}

// signIn starts a session for user and sends the browser on. The session token and the CSRF token
// are both replaced, so neither can be planted or reused from before the sign-in.
func (a *Auth) signIn(w http.ResponseWriter, r *http.Request, user model.User, next string) {
	if err := auth.Login(r.Context(), a.Sessions, user); err != nil {
		a.internalError(w, r, "start session", err)
		return
	}
	if err := csrf.Rotate(w, a.Secure); err != nil {
		a.internalError(w, r, "rotate csrf token", err)
		return
	}
	redirect(w, r, auth.SafeNext(next, "/account"))
}

// LoginPage shows the sign-in options, or sends a signed-in user to their account.
func (a *Auth) LoginPage() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := auth.CurrentUser(r.Context()); ok {
			http.Redirect(w, r, "/account", http.StatusSeeOther)
			return
		}
		opts := a.Login
		opts.Next = auth.SafeNext(r.URL.Query().Get("next"), "")
		renderPage(w, r, view.LoginPage(opts))
	}
}

// Logout ends this browser's session.
func (a *Auth) Logout() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := auth.Logout(r.Context(), a.Sessions); err != nil {
			a.internalError(w, r, "end session", err)
			return
		}
		if err := csrf.Rotate(w, a.Secure); err != nil {
			a.internalError(w, r, "rotate csrf token", err)
			return
		}
		to := "/"
		if a.AfterLogout != nil {
			if u := a.AfterLogout(r); u != "" {
				to = u
			}
		}
		redirect(w, r, to)
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

// Account shows the signed-in user's account. It must be behind auth.RequireUser.
func (a *Auth) Account() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := auth.CurrentUser(r.Context())
		renderPage(w, r, view.AccountPage(user))
	}
}
