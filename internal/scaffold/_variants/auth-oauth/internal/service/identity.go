package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/owenwexler/gotth-plus/internal/auth"
	"github.com/owenwexler/gotth-plus/internal/model"
	"github.com/owenwexler/gotth-plus/internal/repository"
)

var (
	// ErrNoVerifiedEmail means the provider gave no email it has verified, so no account is made.
	ErrNoVerifiedEmail = errors.New("the provider gave no verified email")
	// ErrEmailTaken means an account with the email exists and can't be linked safely.
	ErrEmailTaken = errors.New("an account with this email exists")
)

// IdentityService turns a provider's identity into a user of the app.
type IdentityService struct {
	users      repository.UserRepository
	identities repository.IdentityRepository
	now        func() time.Time
}

// NewIdentityService returns an IdentityService.
func NewIdentityService(users repository.UserRepository, identities repository.IdentityRepository) *IdentityService {
	return &IdentityService{users: users, identities: identities, now: time.Now}
}

// SignIn returns the user for id:
//
//  1. the user already linked to this provider account, whatever its email is now;
//  2. else the user with the same email, linked now, but only when the provider has verified the
//     email and the user's email is confirmed too, so nobody can take over an account by signing in
//     elsewhere with someone else's address;
//  3. else a new user with that email, again only when the provider has verified it: otherwise
//     someone could create an account in another person's name and keep access after the real owner
//     resets the password.
func (s *IdentityService) SignIn(ctx context.Context, id auth.Identity) (model.User, error) {
	linked, err := s.identities.Get(ctx, id.Provider, id.Subject)
	if err == nil {
		return s.users.GetByID(ctx, linked.UserID)
	}
	if !errors.Is(err, repository.ErrNotFound) {
		return model.User{}, err
	}
	if id.Email == "" || !id.EmailVerified {
		return model.User{}, ErrNoVerifiedEmail
	}

	user, err := s.users.GetByEmail(ctx, id.Email)
	switch {
	case err == nil:
		if user.EmailVerifiedAt == nil {
			return model.User{}, ErrEmailTaken
		}
	case errors.Is(err, repository.ErrNotFound):
		now := s.now()
		user = model.User{ID: uuid.New(), Email: id.Email, EmailVerifiedAt: &now, SessionVersion: 1, CreatedAt: now, UpdatedAt: now}
		if err := s.users.Create(ctx, &user); errors.Is(err, repository.ErrConflict) {
			return model.User{}, ErrEmailTaken // created at the same moment by another sign-in
		} else if err != nil {
			return model.User{}, fmt.Errorf("create user: %w", err)
		}
	default:
		return model.User{}, err
	}

	err = s.identities.Create(ctx, model.Identity{
		ID: uuid.New(), UserID: user.ID, Provider: id.Provider, Subject: id.Subject, Email: id.Email, CreatedAt: s.now(),
	})
	if errors.Is(err, repository.ErrConflict) {
		// linked at the same moment by another sign-in: use that link
		return s.SignIn(ctx, id)
	}
	if err != nil {
		return model.User{}, fmt.Errorf("link identity: %w", err)
	}
	return user, nil
}
