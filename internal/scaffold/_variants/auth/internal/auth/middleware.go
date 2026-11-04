package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/alexedwards/scs/v2"
	"github.com/google/uuid"

	"github.com/owenwexler/gotth-plus/internal/model"
	"github.com/owenwexler/gotth-plus/internal/repository"
)

// UserGetter loads a user by ID (repository.UserRepository satisfies it).
type UserGetter interface {
	GetByID(ctx context.Context, id uuid.UUID) (model.User, error)
}

type userKey struct{}

// CurrentUser returns the signed-in user, if there is one.
func CurrentUser(ctx context.Context) (model.User, bool) {
	u, ok := ctx.Value(userKey{}).(model.User)
	return u, ok
}

// WithUser returns ctx with user as the signed-in user (LoadUser does this; tests can too).
func WithUser(ctx context.Context, user model.User) context.Context {
	return context.WithValue(ctx, userKey{}, user)
}

// Middleware loads the session and the signed-in user for every request except static files (which
// never need them, so they cost no database query). It runs inside the CSRF middleware, so a forged
// request is rejected before any session lookup (SECURITY.md, "Middleware order").
func Middleware(s *scs.SessionManager, users UserGetter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		withUser := s.LoadAndSave(loadUser(s, users, next))
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/static/") {
				next.ServeHTTP(w, r)
				return
			}
			withUser.ServeHTTP(w, r)
		})
	}
}

// loadUser puts the session's user into the request context. A session whose user is gone, or
// whose version is older than the user's (signed out everywhere), is ended.
func loadUser(s *scs.SessionManager, users UserGetter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		id, version, ok := sessionUser(ctx, s)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}

		user, err := users.GetByID(ctx, id)
		switch {
		case errors.Is(err, repository.ErrNotFound) || (err == nil && user.SessionVersion != version):
			if err := Logout(ctx, s); err != nil {
				slog.ErrorContext(ctx, "end stale session", "err", err)
			}
		case err != nil:
			slog.ErrorContext(ctx, "load session user", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		default:
			ctx = WithUser(ctx, user)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireUser lets signed-in users through and sends everyone else to the sign-in page, which
// brings them back here afterwards. htmx requests get HX-Redirect, so the whole page navigates
// instead of the sign-in page being swapped into a fragment.
func RequireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := CurrentUser(r.Context()); ok {
			next.ServeHTTP(w, r)
			return
		}
		login := "/login"
		if r.Method == http.MethodGet {
			login += "?next=" + url.QueryEscape(r.URL.RequestURI())
		}
		if r.Header.Get("HX-Request") == "true" {
			w.Header().Set("HX-Redirect", login)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		http.Redirect(w, r, login, http.StatusSeeOther)
	})
}

// SafeNext returns next when it is a path on this site, and fallback otherwise, so ?next= can't
// send a user to another site after signing in (an open redirect).
func SafeNext(next, fallback string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.Contains(next, `\`) {
		return fallback
	}
	u, err := url.Parse(next)
	if err != nil || u.IsAbs() || u.Host != "" {
		return fallback
	}
	return next
}
