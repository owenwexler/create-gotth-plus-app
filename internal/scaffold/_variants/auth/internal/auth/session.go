// Package auth holds sign-in sessions, the middleware that loads the signed-in user, password
// hashing, and per-account rate limits.
package auth

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/owenwexler/gotth-plus/internal/model"
)

const (
	// SessionLifetime is the longest a session lasts, active or not.
	SessionLifetime = 7 * 24 * time.Hour
	// SessionIdleTimeout ends a session nobody has used for this long.
	SessionIdleTimeout = 24 * time.Hour

	keyUserID  = "user_id"
	keyVersion = "session_version"
)

// NewSessions returns the session manager. In production the cookie is __Host-session: Secure,
// Path=/ and no Domain, so a subdomain can't set or overwrite it (__Host- cookies need HTTPS, so
// development uses a plain name).
func NewSessions(db *gorm.DB, production bool) *scs.SessionManager {
	s := scs.New()
	s.Store = NewStore(db)
	s.Lifetime = SessionLifetime
	s.IdleTimeout = SessionIdleTimeout
	s.Cookie.Name = "session"
	if production {
		s.Cookie.Name = "__Host-session"
	}
	s.Cookie.Path = "/"
	s.Cookie.HttpOnly = true
	s.Cookie.Secure = production
	s.Cookie.SameSite = http.SameSiteLaxMode
	s.Cookie.Persist = true
	s.ErrorFunc = func(w http.ResponseWriter, r *http.Request, err error) {
		slog.ErrorContext(r.Context(), "session", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
	return s
}

// Login signs user in. It issues a new session token first, so a token someone planted or saw
// before the sign-in (session fixation) is worthless afterwards.
func Login(ctx context.Context, s *scs.SessionManager, user model.User) error {
	if err := s.RenewToken(ctx); err != nil {
		return err
	}
	s.Put(ctx, keyUserID, user.ID.String())
	s.Put(ctx, keyVersion, user.SessionVersion)
	return nil
}

// Logout ends the session: it is deleted from the store and the cookie is expired.
func Logout(ctx context.Context, s *scs.SessionManager) error {
	return s.Destroy(ctx)
}

// sessionUser is the user ID and session version stored in the session, if any.
func sessionUser(ctx context.Context, s *scs.SessionManager) (uuid.UUID, int, bool) {
	id, err := uuid.Parse(s.GetString(ctx, keyUserID))
	if err != nil {
		return uuid.Nil, 0, false
	}
	return id, s.GetInt(ctx, keyVersion), true
}
