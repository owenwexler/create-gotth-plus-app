package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/google/uuid"

	"github.com/owenwexler/gotth-plus/internal/auth"
	"github.com/owenwexler/gotth-plus/internal/mail"
	"github.com/owenwexler/gotth-plus/internal/model"
	"github.com/owenwexler/gotth-plus/internal/repository"
)

// How long emailed links work.
const (
	VerifyEmailTTL   = 24 * time.Hour
	ResetPasswordTTL = 30 * time.Minute
)

var (
	// ErrInvalidCredentials is the only answer to a failed sign-in, whatever the reason, so it
	// doesn't reveal which emails have accounts.
	ErrInvalidCredentials = errors.New("invalid email or password")
	// ErrEmailNotVerified means the password was right but the email isn't confirmed yet; a new
	// confirmation link has been sent.
	ErrEmailNotVerified = errors.New("email not confirmed")
	// ErrTokenInvalid means an emailed link is unknown, used, or expired.
	ErrTokenInvalid = errors.New("link is invalid or has expired")
)

// AuthService signs people up and in with an email and a password.
type AuthService struct {
	users   repository.UserRepository
	mailer  mail.Mailer
	baseURL string
	now     func() time.Time
}

// NewAuthService builds links in emails from baseURL (config BaseURL), never from the request.
func NewAuthService(users repository.UserRepository, mailer mail.Mailer, baseURL string) *AuthService {
	return &AuthService{users: users, mailer: mailer, baseURL: baseURL, now: time.Now}
}

// SignUp creates an unconfirmed account and emails a confirmation link. If the email already has an
// account, that address gets a note instead, and the caller sees the same result either way, so
// sign-up can't be used to find out who has an account.
func (s *AuthService) SignUp(ctx context.Context, email, password string) error {
	if err := auth.CheckPasswordLength(password); err != nil {
		return err
	}
	// hash first, so both paths below take the same time
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}

	now := s.now()
	user := model.User{ID: uuid.New(), Email: email, PasswordHash: hash, SessionVersion: 1, CreatedAt: now, UpdatedAt: now}
	err = s.users.Create(ctx, &user)
	if errors.Is(err, repository.ErrConflict) {
		return s.mailer.Send(ctx, mail.Message{
			To:      email,
			Subject: "Sign-up attempt",
			Text: "Someone tried to create an account with this email address, which already has one.\n\n" +
				"If that was you, sign in, or reset your password:\n" + s.link("/forgot-password", "") + "\n\n" +
				"If it wasn't you, you can ignore this email.\n",
		})
	}
	if err != nil {
		return fmt.Errorf("create user: %w", err)
	}
	return s.sendVerification(ctx, user)
}

// Login checks the password and returns the user. A hash made with weaker parameters is replaced.
func (s *AuthService) Login(ctx context.Context, email, password string) (model.User, error) {
	user, err := s.users.GetByEmail(ctx, email)
	if errors.Is(err, repository.ErrNotFound) || (err == nil && user.PasswordHash == "") {
		auth.VerifyDummy(password)
		return model.User{}, ErrInvalidCredentials
	}
	if err != nil {
		return model.User{}, err
	}
	ok, err := auth.VerifyPassword(password, user.PasswordHash)
	if err != nil {
		return model.User{}, fmt.Errorf("verify password: %w", err)
	}
	if !ok {
		return model.User{}, ErrInvalidCredentials
	}
	if user.EmailVerifiedAt == nil {
		if err := s.sendVerification(ctx, user); err != nil {
			return model.User{}, err
		}
		return model.User{}, ErrEmailNotVerified
	}
	if auth.NeedsRehash(user.PasswordHash) {
		if hash, err := auth.HashPassword(password); err == nil {
			_ = s.users.SetPassword(ctx, user.ID, hash, false)
		}
	}
	return user, nil
}

// VerifyEmail confirms the email of the token's user.
func (s *AuthService) VerifyEmail(ctx context.Context, token string) error {
	t, err := s.users.UseToken(ctx, model.TokenVerifyEmail, hashToken(token), s.now())
	if errors.Is(err, repository.ErrNotFound) {
		return ErrTokenInvalid
	}
	if err != nil {
		return err
	}
	return s.users.MarkEmailVerified(ctx, t.UserID, s.now())
}

// RequestPasswordReset emails a reset link if the email has an account. It returns nil either way.
func (s *AuthService) RequestPasswordReset(ctx context.Context, email string) error {
	user, err := s.users.GetByEmail(ctx, email)
	if errors.Is(err, repository.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	token, err := s.newToken(ctx, user.ID, model.TokenResetPassword, ResetPasswordTTL)
	if err != nil {
		return err
	}
	return s.mailer.Send(ctx, mail.Message{
		To:      user.Email,
		Subject: "Reset your password",
		Text: "Use this link to choose a new password. It works once, for 30 minutes:\n" +
			s.link("/reset-password", token) + "\n\n" +
			"If you didn't ask for this, you can ignore this email; your password stays the same.\n",
	})
}

// ResetPassword sets a new password and signs the user out everywhere. Using the emailed link also
// proves the email works, so it counts as confirmed.
func (s *AuthService) ResetPassword(ctx context.Context, token, password string) (model.User, error) {
	if err := auth.CheckPasswordLength(password); err != nil {
		return model.User{}, err
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return model.User{}, err
	}
	t, err := s.users.UseToken(ctx, model.TokenResetPassword, hashToken(token), s.now())
	if errors.Is(err, repository.ErrNotFound) {
		return model.User{}, ErrTokenInvalid
	}
	if err != nil {
		return model.User{}, err
	}
	if err := s.users.SetPassword(ctx, t.UserID, hash, true); err != nil {
		return model.User{}, err
	}
	if err := s.users.MarkEmailVerified(ctx, t.UserID, s.now()); err != nil {
		return model.User{}, err
	}
	return s.users.GetByID(ctx, t.UserID)
}

func (s *AuthService) sendVerification(ctx context.Context, user model.User) error {
	token, err := s.newToken(ctx, user.ID, model.TokenVerifyEmail, VerifyEmailTTL)
	if err != nil {
		return err
	}
	return s.mailer.Send(ctx, mail.Message{
		To:      user.Email,
		Subject: "Confirm your email",
		Text: "Confirm your email address to finish creating your account. The link works once, for 24 hours:\n" +
			s.link("/verify-email", token) + "\n\n" +
			"If you didn't sign up, you can ignore this email.\n",
	})
}

// newToken stores the hash of a new random token and returns the token.
func (s *AuthService) newToken(ctx context.Context, userID uuid.UUID, purpose string, ttl time.Duration) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	now := s.now()
	err := s.users.CreateToken(ctx, model.AuthToken{
		ID: uuid.New(), UserID: userID, Purpose: purpose, TokenHash: hashToken(token),
		ExpiresAt: now.Add(ttl), CreatedAt: now,
	})
	return token, err
}

func (s *AuthService) link(path, token string) string {
	if token == "" {
		return s.baseURL + path
	}
	return s.baseURL + path + "?token=" + url.QueryEscape(token)
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
