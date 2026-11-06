package model

import (
	"time"

	"github.com/google/uuid"
)

// User is someone who can sign in.
type User struct {
	ID uuid.UUID `gorm:"type:__UUID_TYPE__;primaryKey"`
	// Email is stored lowercased, so it is unique without regard to case.
	Email           string `gorm:"type:varchar(320);uniqueIndex;not null"`
	EmailVerifiedAt *time.Time
	// PasswordHash is an argon2id hash, or "" for an account that only signs in with a provider.
	PasswordHash string `gorm:"type:varchar(255);not null"`
	// SessionVersion is copied into each session. Raising it signs the user out everywhere
	// (after a password reset, or with "Sign out everywhere").
	SessionVersion int `gorm:"not null"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Identity links a User to an account at a sign-in provider (Google, GitHub, an OIDC issuer).
// Users are matched by (Provider, Subject), the provider's stable ID, never by email.
type Identity struct {
	ID        uuid.UUID `gorm:"type:__UUID_TYPE__;primaryKey"`
	UserID    uuid.UUID `gorm:"type:__UUID_TYPE__;index;not null"`
	Provider  string    `gorm:"type:varchar(64);uniqueIndex:idx_identity_provider_subject;not null"`
	Subject   string    `gorm:"type:varchar(255);uniqueIndex:idx_identity_provider_subject;not null"`
	Email     string    `gorm:"type:varchar(320);not null"`
	CreatedAt time.Time
}

// Token purposes.
const (
	TokenVerifyEmail   = "verify_email"
	TokenResetPassword = "reset_password"
)

// AuthToken is a single-use token sent by email (email confirmation, password reset). Only its
// SHA-256 is stored, so a database leak doesn't hand out working links.
type AuthToken struct {
	ID        uuid.UUID `gorm:"type:__UUID_TYPE__;primaryKey"`
	UserID    uuid.UUID `gorm:"type:__UUID_TYPE__;index;not null"`
	Purpose   string    `gorm:"type:varchar(32);not null"`
	TokenHash string    `gorm:"type:varchar(64);uniqueIndex;not null"`
	ExpiresAt time.Time `gorm:"not null"`
	UsedAt    *time.Time
	CreatedAt time.Time
}

// Session is one row of the server-side session store (see internal/auth).
type Session struct {
	Token  string    `gorm:"type:varchar(64);primaryKey"`
	Data   []byte    `gorm:"not null"`
	Expiry time.Time `gorm:"index;not null"`
}

// All lists every model, for AutoMigrate in the seed route and the tests. Add new models here.
func All() []any {
	return []any{&Example{}, &User{}, &Identity{}, &AuthToken{}, &Session{}}
}
